package pg_sqlc

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"grape/internal/biz"
	"grape/internal/conf"
	"grape/internal/data/pg_sqlc/deploy"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/trace"
)

// 模型调用留痕集成测试（真实 PostgreSQL，未设 GRAPE_MODEL_CALL_TEST_ADMIN_DSN /
// GRAPE_MODEL_CALL_TEST_DSN 时跳过）：覆盖 append-only 落库、租户 RLS 与
// 读侧兜底、过滤分页与无身份 fail-closed。测试从模板库克隆全新独立库，
// 不触碰其他数据库（与检索留痕集成测试同一套手法）。

// modelCallTenantA/B 是用例身份：tenant_id 列宽 varchar(16)，取值保持在
// 列宽内。
var (
	modelCallTenantA = biz.Identity{TenantID: "t-d7-a", UserID: 91}
	modelCallTenantB = biz.Identity{TenantID: "t-d7-b", UserID: 92}
)

// modelCallTestTraceID 是用例的固定链路 ID（合法非零 16 字节）。
var modelCallTestTraceID = [16]byte{0xd7, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}

// setupModelCallTestDB 从模板库克隆全新独立测试库（含
// schema/99_model_call_log.sql 的模板装载）并返回以 grape_u 连接的存储
// 门面；测试结束经 t.Cleanup 删库，测试可并行互不干扰。
func setupModelCallTestDB(t *testing.T) *Data {
	t.Helper()
	adminDSN := os.Getenv("GRAPE_MODEL_CALL_TEST_ADMIN_DSN")
	testDSN := os.Getenv("GRAPE_MODEL_CALL_TEST_DSN")
	if adminDSN == "" || testDSN == "" {
		t.Skip("GRAPE_MODEL_CALL_TEST_ADMIN_DSN / GRAPE_MODEL_CALL_TEST_DSN not set; skipping model call log integration tests")
	}
	ctx := context.Background()

	dbName, err := deploy.CloneTestDatabase(ctx, adminDSN, testGrapeUPassword, testTestUPassword, t.Name())
	if err != nil {
		t.Fatalf("clone test database: %v", err)
	}
	t.Cleanup(func() {
		if err := deploy.DropTestDatabase(ctx, adminDSN, dbName); err != nil {
			t.Logf("drop test database %s: %v", dbName, err)
		}
	})

	// 环境变量 DSN 指向固定库名，门面装配经改写指向本次克隆的库
	poolDSN, err := deploy.RewriteDSNDatabase(testDSN, dbName)
	if err != nil {
		t.Fatalf("rewrite test dsn: %v", err)
	}
	d, cleanup, err := NewData(&conf.Data{Database: &conf.Data_Database{
		Master: &conf.Data_Database_Slot{Source: poolDSN},
		System: &conf.Data_Database_Slot{Source: poolDSN},
	}})
	if err != nil {
		t.Fatalf("connect grape_u: %v", err)
	}
	t.Cleanup(cleanup)
	return d
}

// modelCallIdentityContext 注入认证身份；withTrace 为真时附加 SpanContext
// （模拟链路中间件注入，trace_id 从这里来）。
func modelCallIdentityContext(id biz.Identity, withTrace bool) context.Context {
	ctx := biz.WithIdentity(context.Background(), id)
	if !withTrace {
		return ctx
	}
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    modelCallTestTraceID,
		SpanID:     [8]byte{9, 9, 9, 9, 9, 9, 9, 9},
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	return trace.ContextWithSpanContext(ctx, sc)
}

// countModelCallRows 经身份事务直查留痕行数（表主非 grape_u 且 FORCE
// RLS，裸计数会被全拒；绕过仓储验证落库事实需要同源租户注入）。
func countModelCallRows(t *testing.T, d *Data, tenant biz.Identity) int64 {
	t.Helper()
	auth, err := NewIdentity(tenant)
	if err != nil {
		t.Fatalf("map identity: %v", err)
	}
	var total int64
	err = d.IdentityTx(context.Background(), auth, func(s *IdentityTx) error {
		return s.Tx().QueryRow(context.Background(),
			"SELECT count(*) FROM public.agent_model_call_log").Scan(&total)
	})
	if err != nil {
		t.Fatalf("count model call rows: %v", err)
	}
	return total
}

// appendModelCallOf 经观察面（biz.ModelCallLogUsecase.ObserveModelCall，
// 生产写入路径）写入一条场景化的留痕行：链路/会话事实由观察面从 ctx
// 补齐，行为断言因此覆盖完整的观察纪律。缺省 embed_document/success，
// 可覆盖字段由用例填充。
func appendModelCallOf(t *testing.T, uc *biz.ModelCallLogUsecase, ctx context.Context, mutate func(*biz.ModelCallEntry)) *biz.ModelCallEntry {
	t.Helper()
	entry := &biz.ModelCallEntry{
		Scenario:      biz.ModelCallScenarioEmbedDocument,
		ModelKey:      "bge-m1024",
		Endpoint:      "http://embed.example/v1/embeddings",
		InputSummary:  "chunk body",
		OutputSummary: "embedding vector of 1024 dimensions (vector not persisted)",
		InputTokens:   32,
		DurationMS:    18,
		Status:        biz.ModelCallStatusSuccess,
	}
	if mutate != nil {
		mutate(entry)
	}
	uc.ObserveModelCall(ctx, entry)
	return entry
}

func TestModelCallLogRepo_append_and_query_roundtrip(t *testing.T) {
	t.Parallel()
	d := setupModelCallTestDB(t)
	repo := NewModelCallLogRepo(d)
	ctx := modelCallIdentityContext(modelCallTenantA, true)
	ctx = biz.WithSessionID(ctx, "sess-d7")
	uc := biz.NewModelCallLogUsecase(repo, nil)

	appendModelCallOf(t, uc, ctx, func(e *biz.ModelCallEntry) {
		e.Scenario = biz.ModelCallScenarioIngestSummary
		e.InputSummary = "summarize prompt"
		e.OutputSummary = "document summary text"
		e.InputTokens = 128
		e.OutputTokens = 64
		e.DocumentID = 42
	})
	appendModelCallOf(t, uc, ctx, func(e *biz.ModelCallEntry) {
		e.Status = biz.ModelCallStatusError
		e.Error = "embeddings endpoint returned status 502: gateway down"
		e.InputTokens = 0
		e.OutputTokens = 0
		e.DurationMS = 0
	})
	// 空产出成功行：output 记空串（落库 NULL）。
	appendModelCallOf(t, uc, ctx, func(e *biz.ModelCallEntry) {
		e.OutputSummary = ""
	})

	list, err := uc.List(ctx, nil)
	if err != nil || list.Total != 3 {
		t.Fatalf("list = (%d total, %v), want 3", list.Total, err)
	}
	// 时间倒序：最新写入在前（同秒 id 倒序）。
	summaryRow := list.Items[2]
	switch {
	case summaryRow.Scenario != biz.ModelCallScenarioIngestSummary:
		t.Fatalf("first row scenario = %s, want ingest_summary", summaryRow.Scenario)
	case summaryRow.TenantID != modelCallTenantA.TenantID || summaryRow.UserID != modelCallTenantA.UserID:
		t.Fatalf("row owner = (%s, %d), want from authenticated identity", summaryRow.TenantID, summaryRow.UserID)
	case summaryRow.TraceID == "" || summaryRow.SessionID != "sess-d7":
		t.Fatalf("row facts = (trace %q, session %q), want trace id and session recorded", summaryRow.TraceID, summaryRow.SessionID)
	case summaryRow.ModelKey != "bge-m1024" || summaryRow.Endpoint == "":
		t.Fatalf("endpoint facts = (%q, %q), want model key and endpoint recorded", summaryRow.ModelKey, summaryRow.Endpoint)
	case summaryRow.InputTokens != 128 || summaryRow.OutputTokens != 64:
		t.Fatalf("tokens = (%d, %d), want endpoint usage", summaryRow.InputTokens, summaryRow.OutputTokens)
	case summaryRow.DocumentID != 42:
		t.Fatalf("document id = %d, want 42", summaryRow.DocumentID)
	case summaryRow.Status != biz.ModelCallStatusSuccess:
		t.Fatalf("status = %s, want success", summaryRow.Status)
	}
	errorRow := list.Items[1]
	switch {
	case errorRow.Status != biz.ModelCallStatusError:
		t.Fatalf("error row status = %s, want error", errorRow.Status)
	case errorRow.Error == "":
		t.Fatalf("error row error = %q, want failure summary", errorRow.Error)
	case errorRow.InputTokens != 0 || errorRow.OutputTokens != 0 || errorRow.DurationMS != 0:
		t.Fatalf("error row nullables = (%d, %d, %d), want zero→NULL semantics",
			errorRow.InputTokens, errorRow.OutputTokens, errorRow.DurationMS)
	}
	emptyRow := list.Items[0]
	if emptyRow.OutputSummary != "" {
		t.Fatalf("empty output row = %q, want empty (NULL)", emptyRow.OutputSummary)
	}

	// Count 与 List 同口径。
	total, err := repo.Count(ctx, &biz.ModelCallFilter{Scenario: biz.ModelCallScenarioEmbedDocument})
	if err != nil || total != 2 {
		t.Fatalf("count embed_document = (%d, %v), want 2", total, err)
	}
}

func TestModelCallLogRepo_filters_narrow_rows(t *testing.T) {
	t.Parallel()
	d := setupModelCallTestDB(t)
	repo := NewModelCallLogRepo(d)
	ctx := modelCallIdentityContext(modelCallTenantA, false)
	uc := biz.NewModelCallLogUsecase(repo, nil)

	appendModelCallOf(t, uc, ctx, nil) // embed_document @ doc 42
	appendModelCallOf(t, uc, ctx, func(e *biz.ModelCallEntry) {
		e.Scenario = biz.ModelCallScenarioParseDocument
		e.ModelKey = ""
		e.Endpoint = "http://ocr.example/parse"
		e.DocumentID = 7
	})
	appendModelCallOf(t, uc, ctx, func(e *biz.ModelCallEntry) {
		e.Scenario = biz.ModelCallScenarioEmbedQuery
		e.DocumentID = 0
		e.Status = biz.ModelCallStatusError
	})

	cases := []struct {
		name   string
		filter *biz.ModelCallFilter
		want   int64
	}{
		{"scenario", &biz.ModelCallFilter{Scenario: biz.ModelCallScenarioParseDocument}, 1},
		{"model_key", &biz.ModelCallFilter{ModelKey: "bge-m1024"}, 2},
		{"status", &biz.ModelCallFilter{Status: biz.ModelCallStatusError}, 1},
		{"document", &biz.ModelCallFilter{DocumentID: 7}, 1},
		{"document zero filters nothing", &biz.ModelCallFilter{DocumentID: 0}, 3},
		{"time from future", &biz.ModelCallFilter{CreatedFrom: time.Now().Add(time.Hour)}, 0},
		{"time to past", &biz.ModelCallFilter{CreatedTo: time.Now().Add(-time.Hour)}, 0},
	}
	for _, tc := range cases {
		got, err := uc.List(ctx, tc.filter)
		if err != nil || got.Total != tc.want {
			t.Fatalf("%s: list = (%d total, %v), want %d", tc.name, got.Total, err, tc.want)
		}
	}

	// 分页：limit 1 + offset 1 在过滤结果内翻页。
	page, err := uc.List(ctx, &biz.ModelCallFilter{ModelKey: "bge-m1024", Limit: 1, Offset: 1})
	if err != nil || page.Total != 2 || len(page.Items) != 1 {
		t.Fatalf("paged list = (%d items, %d total, %v), want 1/2", len(page.Items), page.Total, err)
	}
}

func TestModelCallLogRepo_cross_tenant_invisible_under_enabled_rls(t *testing.T) {
	t.Parallel()
	d := setupModelCallTestDB(t)
	repo := NewModelCallLogRepo(d)
	uc := biz.NewModelCallLogUsecase(repo, nil)
	appendModelCallOf(t, uc, modelCallIdentityContext(modelCallTenantA, false), nil)
	if total := countModelCallRows(t, d, modelCallTenantA); total != 1 {
		t.Fatalf("seeded rows = %d, want 1", total)
	}

	// 租户 B 只能写自己的行、看到自己的行：跨租户行不可见等同不存在。
	appendModelCallOf(t, uc, modelCallIdentityContext(modelCallTenantB, false), nil)
	foreign, err := uc.List(modelCallIdentityContext(modelCallTenantB, false), nil)
	if err != nil || foreign.Total != 1 {
		t.Fatalf("tenant B list = (%d total, %v), want only its own row", foreign.Total, err)
	}
	if foreign.Items[0].TenantID != modelCallTenantB.TenantID {
		t.Fatalf("tenant B row tenant = %s, want %s", foreign.Items[0].TenantID, modelCallTenantB.TenantID)
	}
	own, err := uc.List(modelCallIdentityContext(modelCallTenantA, false), nil)
	if err != nil || own.Total != 1 {
		t.Fatalf("tenant A list = (%d total, %v), want own row", own.Total, err)
	}
}

func TestModelCallLogRepo_cross_tenant_invisible_while_rls_disabled(t *testing.T) {
	t.Parallel()
	d := setupModelCallTestDB(t)
	repo := NewModelCallLogRepo(d)
	uc := biz.NewModelCallLogUsecase(repo, nil)
	appendModelCallOf(t, uc, modelCallIdentityContext(modelCallTenantA, false), nil)

	// 关闭 RLS（模拟误操作/降级部署）：读侧同源租户谓词必须独立兜住
	// 隔离，属主语义不变。
	adminCfg, err := pgx.ParseConfig(os.Getenv("GRAPE_MODEL_CALL_TEST_ADMIN_DSN"))
	if err != nil {
		t.Fatalf("parse admin dsn: %v", err)
	}
	adminCfg.Database = deploy.DatabaseNameFor(t.Name())
	admin, err := pgx.ConnectConfig(context.Background(), adminCfg)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	for _, stmt := range []string{
		"ALTER TABLE public.agent_model_call_log NO FORCE ROW LEVEL SECURITY",
		"ALTER TABLE public.agent_model_call_log DISABLE ROW LEVEL SECURITY",
	} {
		if _, err := admin.Exec(context.Background(), stmt); err != nil {
			t.Fatalf("disable rls (%s): %v", stmt, err)
		}
	}
	if err := admin.Close(context.Background()); err != nil {
		t.Fatalf("close admin: %v", err)
	}

	foreign, err := uc.List(modelCallIdentityContext(modelCallTenantB, false), nil)
	if err != nil || foreign.Total != 0 {
		t.Fatalf("tenant B list with RLS disabled = (%d total, %v), want 0 (app-layer predicate must hold)", foreign.Total, err)
	}
	own, err := uc.List(modelCallIdentityContext(modelCallTenantA, false), nil)
	if err != nil || own.Total != 1 {
		t.Fatalf("owner list with RLS disabled = (%d total, %v), want own rows", own.Total, err)
	}
}

func TestModelCallLogRepo_rejects_unknown_scenario_and_unauthenticated_calls(t *testing.T) {
	t.Parallel()
	d := setupModelCallTestDB(t)
	repo := NewModelCallLogRepo(d)

	// 未知场景在仓储侧复检拒绝（表无 CHECK，仓储是落库前的最后一道把门）。
	ctx := modelCallIdentityContext(modelCallTenantA, false)
	err := repo.Append(ctx, &biz.ModelCallEntry{Scenario: "unknown_scene", Status: biz.ModelCallStatusSuccess})
	if err == nil || !strings.Contains(err.Error(), "scenario") {
		t.Fatalf("append unknown scenario = %v, want rejection", err)
	}
	err = repo.Append(ctx, &biz.ModelCallEntry{Scenario: biz.ModelCallScenarioRerank, Status: biz.ModelCallStatusSuccess})
	if err != nil {
		t.Fatalf("append rerank scenario = %v, want accepted (in vocabulary)", err)
	}
	err = repo.Append(ctx, &biz.ModelCallEntry{Scenario: biz.ModelCallScenarioEmbedDocument, Status: "timeout"})
	if err == nil || !strings.Contains(err.Error(), "status") {
		t.Fatalf("append unknown status = %v, want rejection", err)
	}
	err = repo.Append(ctx, nil)
	if err == nil {
		t.Fatal("append nil entry = nil error, want rejection")
	}

	// 无认证 ctx 的写入与读取一律拒绝——写侧观察面直连仓储（不经查询
	// 用例），该门禁是留痕旁路面唯一的身份防线，与检索留痕同款显式断言。
	err = repo.Append(context.Background(), &biz.ModelCallEntry{Scenario: biz.ModelCallScenarioEmbedDocument, Status: biz.ModelCallStatusSuccess})
	if err == nil {
		t.Fatal("repo append without identity = nil error, want rejection")
	}
	if _, err := repo.List(context.Background(), &biz.ModelCallFilter{Limit: 1}); err == nil {
		t.Fatal("repo list without identity = nil error, want rejection")
	}
	if _, err := repo.Count(context.Background(), nil); err == nil {
		t.Fatal("repo count without identity = nil error, want rejection")
	}
}
