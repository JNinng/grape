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

	"github.com/jninng/sqlc_gen_dynamic/dynq"
	"go.opentelemetry.io/otel/trace"
)

// 模型调用留痕集成测试（真实 PostgreSQL，未设 GRAPE_MODEL_CALL_TEST_ADMIN_DSN /
// GRAPE_MODEL_CALL_TEST_DSN 时跳过）：覆盖 append-only 落库、动态查询协议
// （过滤/分页/协议拒绝）与租户 RLS、无身份 fail-closed。测试从模板库克隆
// 全新独立库，不触碰其他数据库（与检索留痕集成测试同一套手法）。

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

// assertModelCallSessionRecorded 经身份事务直查 @internal 列 session_id：
// 该列不进动态投影，写入事实只能以直查断言。
func assertModelCallSessionRecorded(t *testing.T, d *Data, tenant biz.Identity, want string) {
	t.Helper()
	auth, err := NewIdentity(tenant)
	if err != nil {
		t.Fatalf("map identity: %v", err)
	}
	var sessionID *string
	err = d.IdentityTx(context.Background(), auth, func(s *IdentityTx) error {
		return s.Tx().QueryRow(context.Background(),
			"SELECT session_id FROM public.agent_model_call_log WHERE scenario = $1",
			biz.ModelCallScenarioIngestSummary).Scan(&sessionID)
	})
	if err != nil {
		t.Fatalf("query session_id: %v", err)
	}
	if sessionID == nil || *sessionID != want {
		t.Fatalf("session_id = %v, want %q (write path must persist the ctx fact)", sessionID, want)
	}
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

	list, err := repo.List(ctx, modelCallTenantA, &dynq.Query{})
	if err != nil || list.Total == nil || *list.Total != 3 {
		t.Fatalf("list = (%+v total, %v), want 3", list.Total, err)
	}
	if len(list.Items) != 3 {
		t.Fatalf("items = %d, want 3", len(list.Items))
	}
	// 默认排序是 created_at DESC + id ASC 兜底（同一秒内按 id 升序），
	// 故不按位置取行，按事实定位。
	var summaryRow, errorRow, emptyRow *biz.ModelCallLog
	for _, row := range list.Items {
		switch {
		case row.Scenario == biz.ModelCallScenarioIngestSummary:
			summaryRow = row
		case row.Status == biz.ModelCallStatusError:
			errorRow = row
		default:
			emptyRow = row
		}
	}
	if summaryRow == nil || errorRow == nil || emptyRow == nil {
		t.Fatalf("rows = %+v, want one ingest_summary, one error and one empty-output row", list.Items)
	}
	switch {
	case summaryRow.UserID != modelCallTenantA.UserID:
		t.Fatalf("row owner user = %d, want from authenticated identity", summaryRow.UserID)
	case summaryRow.TraceID == "":
		t.Fatalf("row trace = %q, want trace id recorded", summaryRow.TraceID)
	case summaryRow.ModelKey != "bge-m1024" || summaryRow.Endpoint == "":
		t.Fatalf("endpoint facts = (%q, %q), want model key and endpoint recorded", summaryRow.ModelKey, summaryRow.Endpoint)
	case summaryRow.InputTokens != 128 || summaryRow.OutputTokens != 64:
		t.Fatalf("tokens = (%d, %d), want endpoint usage", summaryRow.InputTokens, summaryRow.OutputTokens)
	case summaryRow.DocumentID != 42:
		t.Fatalf("document id = %d, want 42", summaryRow.DocumentID)
	case summaryRow.Status != biz.ModelCallStatusSuccess:
		t.Fatalf("status = %s, want success", summaryRow.Status)
	}
	// session_id 是 @internal 列（不进动态投影），写入事实经身份事务直查
	// 断言：观察面从 ctx 载体补齐会话事实。
	assertModelCallSessionRecorded(t, d, modelCallTenantA, "sess-d7")
	switch {
	case errorRow.Status != biz.ModelCallStatusError:
		t.Fatalf("error row status = %s, want error", errorRow.Status)
	case errorRow.Error == "":
		t.Fatalf("error row error = %q, want failure summary", errorRow.Error)
	case errorRow.InputTokens != 0 || errorRow.OutputTokens != 0 || errorRow.DurationMS != 0:
		t.Fatalf("error row nullables = (%d, %d, %d), want zero→NULL semantics",
			errorRow.InputTokens, errorRow.OutputTokens, errorRow.DurationMS)
	}
	if emptyRow.OutputSummary != "" {
		t.Fatalf("empty output row = %q, want empty (NULL)", emptyRow.OutputSummary)
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

	// 时间边界按本地墙钟格式化：created_at 是 tz-naive 列（timestamp(0)，
	// 存库时钟的本地墙钟），前端日期筛选同样以本地偏移输出 RFC3339。
	future := time.Now().Add(time.Hour).Format(time.RFC3339)
	past := time.Now().Add(-time.Hour).Format(time.RFC3339)
	cases := []struct {
		name string
		q    *dynq.Query
		want int64
	}{
		{"scenario", &dynq.Query{Where: []dynq.Condition{{Field: "scenario", Op: "eq", Value: biz.ModelCallScenarioParseDocument}}}, 1},
		{"model_key", &dynq.Query{Where: []dynq.Condition{{Field: "modelKey", Op: "eq", Value: "bge-m1024"}}}, 2},
		{"status", &dynq.Query{Where: []dynq.Condition{{Field: "status", Op: "eq", Value: biz.ModelCallStatusError}}}, 1},
		{"document", &dynq.Query{Where: []dynq.Condition{{Field: "documentId", Op: "eq", Value: float64(7)}}}, 1},
		{"membership", &dynq.Query{Where: []dynq.Condition{{Field: "scenario", Op: "in", Value: []any{biz.ModelCallScenarioEmbedDocument, biz.ModelCallScenarioEmbedQuery}}}}, 2},
		{"time from future", &dynq.Query{Where: []dynq.Condition{{Field: "createdAt", Op: "gte", Value: future}}}, 0},
		{"time to past", &dynq.Query{Where: []dynq.Condition{{Field: "createdAt", Op: "lte", Value: past}}}, 0},
	}
	for _, tc := range cases {
		got, err := repo.List(ctx, modelCallTenantA, tc.q)
		if err != nil || got.Total == nil || *got.Total != tc.want {
			gotTotal := int64(-1)
			if got != nil && got.Total != nil {
				gotTotal = *got.Total
			}
			t.Fatalf("%s: list = (%d items, %d total, %v), want %d", tc.name, len(got.Items), gotTotal, err, tc.want)
		}
	}

	// 分页：过滤结果内第 2 页（页大小 1）翻页，total 不随页码递减。
	page := 2
	pageSize := 1
	paged, err := repo.List(ctx, modelCallTenantA, &dynq.Query{
		Where:      []dynq.Condition{{Field: "modelKey", Op: "eq", Value: "bge-m1024"}},
		Pagination: &dynq.Pagination{Page: &page, PageSize: &pageSize},
	})
	if err != nil || paged.Total == nil || *paged.Total != 2 || len(paged.Items) != 1 {
		t.Fatalf("paged list = (%d items, %+v total, %v), want 1/2", len(paged.Items), paged.Total, err)
	}
}

func TestModelCallLogRepo_total_independent_of_cursor_page(t *testing.T) {
	t.Parallel()
	d := setupModelCallTestDB(t)
	repo := NewModelCallLogRepo(d)
	ctx := modelCallIdentityContext(modelCallTenantA, false)
	uc := biz.NewModelCallLogUsecase(repo, nil)
	appendModelCallOf(t, uc, ctx, nil)
	appendModelCallOf(t, uc, ctx, func(e *biz.ModelCallEntry) { e.Scenario = biz.ModelCallScenarioRerank })
	appendModelCallOf(t, uc, ctx, func(e *biz.ModelCallEntry) { e.Scenario = biz.ModelCallScenarioEmbedQuery })

	// 游标模式下 total 与翻页位置无关：第二页的 total 仍为全集 3。
	pageSize := 2
	includeTotal := true
	first, err := repo.List(ctx, modelCallTenantA, &dynq.Query{
		Pagination: &dynq.Pagination{PageSize: &pageSize, IncludeTotal: &includeTotal},
	})
	if err != nil || len(first.Items) != 2 || first.NextCursor == "" {
		t.Fatalf("first page = (%d items, cursor %q, %v), want 2 items with a cursor", len(first.Items), first.NextCursor, err)
	}
	after := first.NextCursor
	second, err := repo.List(ctx, modelCallTenantA, &dynq.Query{
		Pagination: &dynq.Pagination{After: &after, PageSize: &pageSize, IncludeTotal: &includeTotal},
	})
	if err != nil || len(second.Items) != 1 {
		t.Fatalf("second page items = %d (%v), want 1", len(second.Items), err)
	}
	if second.Total == nil || *second.Total != 3 {
		t.Fatalf("second page total = %+v, want 3", second.Total)
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
	foreign, err := repo.List(modelCallIdentityContext(modelCallTenantB, false), modelCallTenantB, &dynq.Query{})
	if err != nil || foreign.Total == nil || *foreign.Total != 1 {
		t.Fatalf("tenant B list = (%+v total, %v), want only its own row", foreign.Total, err)
	}
	if foreign.Items[0].UserID != modelCallTenantB.UserID {
		t.Fatalf("tenant B row user = %d, want %d", foreign.Items[0].UserID, modelCallTenantB.UserID)
	}
	own, err := repo.List(modelCallIdentityContext(modelCallTenantA, false), modelCallTenantA, &dynq.Query{})
	if err != nil || own.Total == nil || *own.Total != 1 {
		t.Fatalf("tenant A list = (%+v total, %v), want own row", own.Total, err)
	}
}

func TestModelCallLogRepo_rejects_unknown_field_and_unauthenticated_calls(t *testing.T) {
	t.Parallel()
	d := setupModelCallTestDB(t)
	repo := NewModelCallLogRepo(d)
	ctx := modelCallIdentityContext(modelCallTenantA, false)

	// 未知场景在仓储侧复检拒绝（表无 CHECK，仓储是落库前的最后一道把门）。
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

	// 协议面：未知字段拒绝并映射为 ErrModelCallInvalidArgument（词表外
	// 场景过滤值同理——过滤表达式由生成代码校验，不静默空结果）。
	if _, err := repo.List(ctx, modelCallTenantA, &dynq.Query{Where: []dynq.Condition{{Field: "noSuchField", Op: "eq", Value: "x"}}}); err != nil {
		if !strings.Contains(err.Error(), "AGENT_MODEL_CALL_INVALID_ARGUMENT") {
			t.Fatalf("list unknown field = %v, want AGENT_MODEL_CALL_INVALID_ARGUMENT", err)
		}
	} else {
		t.Fatal("list unknown field = nil error, want rejection")
	}
	if _, err := repo.List(ctx, modelCallTenantA, &dynq.Query{Where: []dynq.Condition{{Field: "scenario", Op: "eq", Value: "unknown_scene"}}}); err != nil {
		t.Fatalf("list unknown scenario filter value = %v, want accepted (scenario has no enum narrowing)", err)
	}

	// 无认证身份的写入与读取一律拒绝——写侧观察面直连仓储（不经查询
	// 用例），该门禁是留痕旁路面唯一的身份防线；读侧身份由显式参数传入，
	// 空身份与 ctx 缺身份同属拒绝。
	err = repo.Append(context.Background(), &biz.ModelCallEntry{Scenario: biz.ModelCallScenarioEmbedDocument, Status: biz.ModelCallStatusSuccess})
	if err == nil {
		t.Fatal("repo append without identity = nil error, want rejection")
	}
	if _, err := repo.List(context.Background(), biz.Identity{}, &dynq.Query{}); err == nil {
		t.Fatal("repo list with empty identity = nil error, want rejection")
	}
	if _, err := repo.List(context.Background(), biz.Identity{TenantID: "t-d7-a"}, &dynq.Query{}); err == nil {
		t.Fatal("repo list with non-positive user id = nil error, want rejection")
	}
}
