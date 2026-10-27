package pg_sqlc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"grape/internal/biz"
	"grape/internal/conf"
	"grape/pkg/redact"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/trace"
)

// 检索留痕集成测试（TODO 流 D 的 D3）：以真实 PostgreSQL 验证检索留痕
// 行落库（query/top_k/命中与实际一致）、租户 RLS、RLS 关闭时读侧兜底、
// 留痕失败不中断检索、管线摄取留痕来源与分页边界。测试自建专属库并
// 重建（DROP+CREATE+按 DeployFiles 部署清单全量装载，含
// schema/97_agent_retrieval_log.sql 与 agent_vector.sql），不触碰其他
// 数据库。设置以下环境变量后运行，未设置时跳过：
//
//	GRAPE_D3_TEST_ADMIN_DSN=postgres://postgresql:root@127.0.0.1:5432/postgres \
//	GRAPE_D3_TEST_DSN=postgres://grape_u:grape_u_pwd@127.0.0.1:5432/grape_retrieval_d3_test \
//	go test ./internal/data/pg_sqlc -run "TestRetrievalLog" -count=1 -v
//
// 隔离口径：本表启用租户 RLS（部署默认），租户边界由 RLS 强制、读侧
// WHERE 的同源租户谓词作应用层兜底——RLS 被关闭时跨租户仍不可见（对齐
// D1「隔离正确性不押在 RLS 是否存在上」的验收口径）。

const retrievalTestDatabase = "grape_retrieval_d3_test"

// retrievalTenantA/B 是用例身份：tenant_id 列宽 varchar(16)，取值保持在
// 列宽内。
var (
	retrievalTenantA = biz.Identity{TenantID: "t-d3-a", UserID: 81}
	retrievalTenantB = biz.Identity{TenantID: "t-d3-b", UserID: 82}
)

// retrievalTestTraceID 是用例的固定链路 ID（合法非零 16 字节）。
var retrievalTestTraceID = [16]byte{0xdd, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}

// setupRetrievalTestDB 重建专属测试库、按序装载部署清单（含
// schema/97_agent_retrieval_log.sql）并返回以 grape_u 连接的存储门面。
func setupRetrievalTestDB(t *testing.T) *Data {
	t.Helper()
	adminDSN := os.Getenv("GRAPE_D3_TEST_ADMIN_DSN")
	testDSN := os.Getenv("GRAPE_D3_TEST_DSN")
	if adminDSN == "" || testDSN == "" {
		t.Skip("GRAPE_D3_TEST_ADMIN_DSN / GRAPE_D3_TEST_DSN 未设置，跳过检索留痕集成测试")
	}
	ctx := context.Background()

	cfg, err := pgx.ParseConfig(adminDSN)
	if err != nil {
		t.Fatalf("parse admin dsn: %v", err)
	}
	cfg.Database = "postgres"
	admin, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+retrievalTestDatabase+" WITH (FORCE)"); err != nil {
		t.Fatalf("drop test database: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+retrievalTestDatabase); err != nil {
		t.Fatalf("create test database: %v", err)
	}
	if err := admin.Close(ctx); err != nil {
		t.Fatalf("close admin: %v", err)
	}

	deployCfg, err := pgx.ParseConfig(adminDSN)
	if err != nil {
		t.Fatalf("parse admin dsn: %v", err)
	}
	deployDir, err := DeployDir()
	if err != nil {
		t.Fatalf("locate deploy dir: %v", err)
	}
	if err := ValidateDeployFiles(deployDir); err != nil {
		t.Fatalf("validate deploy files: %v", err)
	}
	deployCfg.Database = retrievalTestDatabase
	deployCfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	deployConn, err := pgx.ConnectConfig(ctx, deployCfg)
	if err != nil {
		t.Fatalf("connect deploy: %v", err)
	}
	defer deployConn.Close(ctx)
	// role.sql 的角色目录操作是集群级的，跨测试包并发部署必须互斥。
	release, err := AcquireDeployLock(ctx)
	if err != nil {
		t.Fatalf("acquire deploy lock: %v", err)
	}
	defer release()
	for _, f := range DeployFiles {
		sql, err := os.ReadFile(filepath.Join(deployDir, f))
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		deployed := ResolveDeployPasswords(string(sql), testGrapeUPassword, testTestUPassword)
		if _, err := deployConn.Exec(ctx, deployed); err != nil {
			t.Fatalf("deploy %s: %v", f, err)
		}
	}
	if _, err := deployConn.Exec(ctx, "GRANT CONNECT ON DATABASE "+retrievalTestDatabase+" TO grape_u, test_u"); err != nil {
		t.Fatalf("grant connect: %v", err)
	}

	pool, err := pgxpool.New(ctx, testDSN)
	if err != nil {
		t.Fatalf("connect grape_u: %v", err)
	}
	t.Cleanup(pool.Close)
	return &Data{db: pool}
}

// newLoggedVectorStoreForTest 构造装饰器与留痕仓储：真实 pgvector 仓储
// （维度与部署列对齐）为内层，registry 与生产装配同默认值。
func newLoggedVectorStoreForTest(t *testing.T, d *Data, logs biz.RetrievalLogRepo) biz.VectorStore {
	t.Helper()
	c := &conf.Vector{Enabled: true, Dimension: vectorDimension, TableName: "agent_document"}
	store, cleanup, err := NewVectorStore(c, d)
	if err != nil {
		t.Fatalf("build vector store: %v", err)
	}
	t.Cleanup(cleanup)
	logged, _, err := NewLoggedVectorStore(VectorStoreSupply{VectorStore: store}, logs, redact.NewStandardRegistry())
	if err != nil {
		t.Fatalf("build logged vector store: %v", err)
	}
	return logged
}

// retrievalIdentityContext 注入认证身份；withTrace 为真时附加 SpanContext
// （模拟链路中间件注入，trace_id 从这里来）。
func retrievalIdentityContext(id biz.Identity, withTrace bool) context.Context {
	ctx := biz.WithIdentity(context.Background(), id)
	if !withTrace {
		return ctx
	}
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    retrievalTestTraceID,
		SpanID:     [8]byte{8, 8, 8, 8, 8, 8, 8, 8},
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	return trace.ContextWithSpanContext(ctx, sc)
}

// retrievalSearchContext 在身份与链路之上补齐检索事实（会话 ID 与查询
// 文本——生产由 agent 层的 withRetrievalLogFacts 注入，同一载体）。
func retrievalSearchContext(id biz.Identity, query string) context.Context {
	ctx := retrievalIdentityContext(id, true)
	ctx = biz.WithSessionID(ctx, "sess-d3-run")
	return biz.WithRetrievalQuery(ctx, query)
}

// failingRetrievalRepo 是恒失败的留痕仓储：验证留痕失败不影响检索。
type failingRetrievalRepo struct{}

func (failingRetrievalRepo) Append(context.Context, *biz.RetrievalLogRecord) error {
	return errors.New("retrieval log storage down")
}

func (failingRetrievalRepo) List(context.Context, *biz.RetrievalLogFilter) (*biz.RetrievalLogList, error) {
	return nil, errors.New("retrieval log storage down")
}

// countRetrievalRows 经身份事务直查留痕行数（表主非 grape_u 且 FORCE
// RLS，裸计数会被全拒；绕过装饰器验证落库事实需要同源租户注入）。
func countRetrievalRows(t *testing.T, d *Data, tenant biz.Identity) int64 {
	t.Helper()
	var total int64
	err := d.IdentityTx(context.Background(), pgAuth(tenant), func(s *IdentityTx) error {
		return s.Tx().QueryRow(context.Background(),
			"SELECT count(*) FROM public.agent_retrieval_log").Scan(&total)
	})
	if err != nil {
		t.Fatalf("count retrieval rows: %v", err)
	}
	return total
}

// seedVectorDocs 以装饰器写入两篇文档：embedding 取正交单位向量（检索
// 分数可精确断言），返回供检索的查询向量与文档 ID。
func seedVectorDocs(t *testing.T, store biz.VectorStore, id biz.Identity) []float32 {
	t.Helper()
	ctx := retrievalIdentityContext(id, false)
	docs := []*biz.VectorDocument{
		{ID: "doc-a", Content: "grape vector alpha", Embedding: unitVector(int(vectorDimension), 0),
			Metadata: map[string]any{"source_id": "doc", "anchor": "runes 0-16"}},
		{ID: "doc-b", Content: "grape vector beta", Embedding: unitVector(int(vectorDimension), 1),
			Metadata: map[string]any{"source_id": "doc", "anchor": "runes 0-16"}},
	}
	for _, doc := range docs {
		if err := store.Add(ctx, doc); err != nil {
			t.Fatalf("seed document %s: %v", doc.ID, err)
		}
	}
	return unitVector(int(vectorDimension), 0)
}

func TestRetrievalLog_search_records_query_topk_and_hits_matching_actual(t *testing.T) {
	d := setupRetrievalTestDB(t)
	logRepo := NewRetrievalLogRepo(d)
	store := newLoggedVectorStoreForTest(t, d, logRepo)
	queryVector := seedVectorDocs(t, store, retrievalTenantA)

	uc := biz.NewRetrievalLogUsecase(logRepo)
	ctx := retrievalSearchContext(retrievalTenantA, "grape 检索")
	filter := &biz.VectorFilter{Limit: 2}
	hits, err := store.Search(ctx, queryVector, filter)
	if err != nil || len(hits) != 2 {
		t.Fatalf("search = (%d hits, %v), want 2", len(hits), err)
	}

	list, err := uc.List(ctx, &biz.RetrievalLogFilter{Source: biz.RetrievalSourceTool})
	if err != nil || list.Total != 1 {
		t.Fatalf("list tool rows = (%d total, %v), want 1", list.Total, err)
	}
	row := list.Items[0]
	switch {
	case row.Source != biz.RetrievalSourceTool || row.Status != biz.RetrievalStatusSuccess:
		t.Fatalf("row = (%s/%s), want tool/success", row.Source, row.Status)
	case row.TenantID != retrievalTenantA.TenantID || row.UserID != retrievalTenantA.UserID:
		t.Fatalf("row owner = (%s, %d), want from authenticated identity", row.TenantID, row.UserID)
	case row.TraceID == "" || row.SessionID != "sess-d3-run":
		t.Fatalf("row facts = (trace %q, session %q), want trace id and session id recorded", row.TraceID, row.SessionID)
	case row.QueryText != "grape 检索":
		t.Fatalf("query text = %q, want original (standard registry keeps short text)", row.QueryText)
	case row.RequestedTopK != 2 || row.EffectiveTopK != 2:
		t.Fatalf("top_k = (%d, %d), want (2, 2)", row.RequestedTopK, row.EffectiveTopK)
	}

	// 命中与实际检索结果一致（document_id 与 score 逐一对应，反查链路
	// 成立：留痕行 → document 反查）。
	var logged []struct {
		DocumentID string  `json:"document_id"`
		Score      float64 `json:"score"`
		Anchor     string  `json:"anchor"`
	}
	if err := json.Unmarshal(row.Hits, &logged); err != nil {
		t.Fatalf("unmarshal hits: %v", err)
	}
	if len(logged) != len(hits) {
		t.Fatalf("logged hits = %d, want %d", len(logged), len(hits))
	}
	for i, hit := range logged {
		if hit.DocumentID != hits[i].Document.ID {
			t.Fatalf("hit %d document = %q, want %q", i, hit.DocumentID, hits[i].Document.ID)
		}
		if diff := hit.Score - hits[i].Score; diff < -scoreEpsilon || diff > scoreEpsilon {
			t.Fatalf("hit %d score = %v, want %v", i, hit.Score, hits[i].Score)
		}
		if hit.Anchor != "runes 0-16" {
			t.Fatalf("hit %d anchor = %q, want source metadata anchor", i, hit.Anchor)
		}
	}
	// 命中 ID 可反查文档原文（验收链路的最后一跳）。
	doc, err := store.Get(retrievalIdentityContext(retrievalTenantA, false), logged[0].DocumentID)
	if err != nil || doc.Content != "grape vector alpha" {
		t.Fatalf("reverse lookup = (%+v, %v), want seeded content", doc, err)
	}
}

func TestRetrievalLog_cross_tenant_invisible_under_enabled_rls(t *testing.T) {
	d := setupRetrievalTestDB(t)
	logRepo := NewRetrievalLogRepo(d)
	store := newLoggedVectorStoreForTest(t, d, logRepo)
	seedVectorDocs(t, store, retrievalTenantA)
	// 租户 A 的检索留痕已落库。
	if total := countRetrievalRows(t, d, retrievalTenantA); total == 0 {
		t.Fatalf("seed produced no retrieval rows")
	}

	// 租户 B 检索本租户文档（自身留痕成功）后查询：跨租户行不可见，
	// 只见自己的行。
	store.Search(retrievalSearchContext(retrievalTenantB, "b query"), unitVector(int(vectorDimension), 2), nil)
	uc := biz.NewRetrievalLogUsecase(logRepo)
	foreign, err := uc.List(retrievalIdentityContext(retrievalTenantB, false), nil)
	if err != nil || foreign.Total != 1 {
		t.Fatalf("tenant B list = (%d total, %v), want only its own row", foreign.Total, err)
	}
	if foreign.Items[0].TenantID != retrievalTenantB.TenantID {
		t.Fatalf("tenant B row tenant = %s, want %s", foreign.Items[0].TenantID, retrievalTenantB.TenantID)
	}
	own, err := uc.List(retrievalIdentityContext(retrievalTenantA, false), nil)
	if err != nil || own.Total == 0 {
		t.Fatalf("tenant A list = (%d total, %v), want own rows", own.Total, err)
	}
}

func TestRetrievalLog_cross_tenant_invisible_while_rls_disabled(t *testing.T) {
	d := setupRetrievalTestDB(t)
	logRepo := NewRetrievalLogRepo(d)
	store := newLoggedVectorStoreForTest(t, d, logRepo)
	seedVectorDocs(t, store, retrievalTenantA)
	uc := biz.NewRetrievalLogUsecase(logRepo)

	// 关闭 RLS（模拟误操作/降级部署）：读侧同源租户谓词必须独立兜住
	// 隔离，属主语义不变。
	adminCfg, err := pgx.ParseConfig(os.Getenv("GRAPE_D3_TEST_ADMIN_DSN"))
	if err != nil {
		t.Fatalf("parse admin dsn: %v", err)
	}
	adminCfg.Database = retrievalTestDatabase
	admin, err := pgx.ConnectConfig(context.Background(), adminCfg)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	for _, stmt := range []string{
		"ALTER TABLE public.agent_retrieval_log NO FORCE ROW LEVEL SECURITY",
		"ALTER TABLE public.agent_retrieval_log DISABLE ROW LEVEL SECURITY",
	} {
		if _, err := admin.Exec(context.Background(), stmt); err != nil {
			t.Fatalf("disable rls (%s): %v", stmt, err)
		}
	}
	if err := admin.Close(context.Background()); err != nil {
		t.Fatalf("close admin: %v", err)
	}

	foreign, err := uc.List(retrievalIdentityContext(retrievalTenantB, false), nil)
	if err != nil || foreign.Total != 0 {
		t.Fatalf("tenant B list with RLS disabled = (%d total, %v), want 0 (app-layer predicate must hold)", foreign.Total, err)
	}
	own, err := uc.List(retrievalIdentityContext(retrievalTenantA, false), nil)
	if err != nil || own.Total == 0 {
		t.Fatalf("owner list with RLS disabled = (%d total, %v), want own rows", own.Total, err)
	}
}

func TestRetrievalLog_write_failure_does_not_break_search(t *testing.T) {
	d := setupRetrievalTestDB(t)
	// 留痕仓储恒失败：检索必须照常返回，行数不得增加（失败只记日志）。
	store := newLoggedVectorStoreForTest(t, d, failingRetrievalRepo{})
	queryVector := seedVectorDocs(t, store, retrievalTenantA)

	hits, err := store.Search(retrievalSearchContext(retrievalTenantA, "grape 检索"), queryVector, &biz.VectorFilter{Limit: 2})
	if err != nil || len(hits) != 2 {
		t.Fatalf("search with failing log repo = (%d hits, %v), must succeed", len(hits), err)
	}
	if total := countRetrievalRows(t, d, retrievalTenantA); total != 0 {
		t.Fatalf("retrieval rows = %d, want 0 (nothing written, search unaffected)", total)
	}
}

// pipelineEmbedder 是摄取管线的替身 embedder：维度与部署列一致，向量
// 由文本长度决定维度位（确定性，内容与留痕断言无关）。
type pipelineEmbedder struct{}

func (pipelineEmbedder) Dimensions() int { return int(vectorDimension) }

func (pipelineEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	return unitVector(int(vectorDimension), len(text)%int(vectorDimension)), nil
}

func TestRetrievalLog_pipeline_ingest_records_source_pipeline(t *testing.T) {
	d := setupRetrievalTestDB(t)
	logRepo := NewRetrievalLogRepo(d)
	store := newLoggedVectorStoreForTest(t, d, logRepo)
	ucase, err := biz.NewDocumentUsecase(store, pipelineEmbedder{}, int(vectorDimension), biz.WithChunking(8, 2))
	if err != nil {
		t.Fatalf("build document usecase: %v", err)
	}

	ctx := retrievalIdentityContext(retrievalTenantA, false)
	result, err := ucase.Ingest(ctx, &biz.Document{ID: "handbook", Name: "Grape Handbook", Content: "01234567890123"})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if result.Chunks != 2 {
		t.Fatalf("chunks = %d, want 2 (content splits under chunking 8/2)", result.Chunks)
	}

	uc := biz.NewRetrievalLogUsecase(logRepo)
	list, err := uc.List(ctx, &biz.RetrievalLogFilter{Source: biz.RetrievalSourcePipeline})
	if err != nil || list.Total != 3 {
		t.Fatalf("pipeline rows = (%d total, %v), want 3 (one clear + two adds)", list.Total, err)
	}
	// 时间倒序：最先落库的覆盖清除行在末尾；两行 Add 在前。
	clear := list.Items[list.Total-1]
	if clear.Status != biz.RetrievalStatusSuccess || clear.Hits != nil {
		t.Fatalf("clear row = (%s, hits %v), want success without hits", clear.Status, clear.Hits)
	}
	summary := map[string]any{}
	if err := json.Unmarshal(clear.FilterSummary, &summary); err != nil {
		t.Fatalf("unmarshal clear filter summary: %v", err)
	}
	meta, _ := summary["metadata"].(map[string]any)
	if meta["source_id"] != "handbook" {
		t.Fatalf("clear condition = %v, want source_id=handbook", summary)
	}
	for i := 0; i < 2; i++ {
		row := list.Items[i]
		var hits []map[string]any
		if err := json.Unmarshal(row.Hits, &hits); err != nil {
			t.Fatalf("unmarshal add row %d hits: %v", i, err)
		}
		if len(hits) != 1 {
			t.Fatalf("add row %d hits = %v, want single affected chunk", i, hits)
		}
		id, _ := hits[0]["document_id"].(string)
		anchor, _ := hits[0]["anchor"].(string)
		want := fmt.Sprintf("handbook#%d", 1-i)
		if id != want {
			t.Fatalf("add row %d document = %q, want %q (time desc order)", i, id, want)
		}
		if !strings.HasPrefix(anchor, "runes ") {
			t.Fatalf("add row %d anchor = %q, want chunk metadata anchor", i, anchor)
		}
		if row.QueryText != "" || row.RequestedTopK != 0 || row.EffectiveTopK != 0 {
			t.Fatalf("add row %d = (%q, %d, %d), want ingest-side nulls", i, row.QueryText, row.RequestedTopK, row.EffectiveTopK)
		}
	}

	// 同来源重摄：覆盖清除行删旧写新，摄取行继续追加且 Replaced=2。
	result2, err := ucase.Ingest(ctx, &biz.Document{ID: "handbook", Name: "Grape Handbook", Content: "01234567890123"})
	if err != nil || result2.Replaced != 2 {
		t.Fatalf("re-ingest = (%+v, %v), want replaced 2", result2, err)
	}
	list2, err := uc.List(ctx, &biz.RetrievalLogFilter{Source: biz.RetrievalSourcePipeline})
	if err != nil || list2.Total != 6 {
		t.Fatalf("pipeline rows after re-ingest = (%d total, %v), want 6", list2.Total, err)
	}
}

func TestRetrievalLog_pagination_orders_by_time_desc_and_boundaries(t *testing.T) {
	d := setupRetrievalTestDB(t)
	logRepo := NewRetrievalLogRepo(d)
	ctx := retrievalIdentityContext(retrievalTenantA, false)

	// 同秒写入 25 行（created_at 秒级精度，id 为 tie-break）。
	for i := 0; i < 25; i++ {
		rec := &biz.RetrievalLogRecord{
			TraceID:       "0123456789abcdef0123456789abcdef",
			SessionID:     "sess-page",
			Source:        biz.RetrievalSourceTool,
			QueryText:     fmt.Sprintf("query %02d", i),
			RequestedTopK: 5,
			EffectiveTopK: 5,
			Status:        biz.RetrievalStatusSuccess,
		}
		if err := logRepo.Append(ctx, rec); err != nil {
			t.Fatalf("append row %d: %v", i, err)
		}
	}
	uc := biz.NewRetrievalLogUsecase(logRepo)

	// 缺省页大小 20：第一页 20 行、总数 25，时间倒序（同秒 id 倒序，
	// 最新写入在前）。
	page1, err := uc.List(ctx, &biz.RetrievalLogFilter{SessionID: "sess-page"})
	if err != nil || page1.Total != 25 || len(page1.Items) != 20 {
		t.Fatalf("page1 = (%d total, %d items, %v), want 25/20", page1.Total, len(page1.Items), err)
	}
	if page1.Items[0].QueryText != "query 24" || page1.Items[19].QueryText != "query 05" {
		t.Fatalf("page1 order = [%s .. %s], want time desc id desc", page1.Items[0].QueryText, page1.Items[19].QueryText)
	}

	// 第二页（offset 20）拿剩余 5 行，翻出界得空页。
	page2, err := uc.List(ctx, &biz.RetrievalLogFilter{SessionID: "sess-page", Offset: 20})
	if err != nil || len(page2.Items) != 5 || page2.Total != 25 {
		t.Fatalf("page2 = (%d items, %d total, %v), want 5/25", len(page2.Items), page2.Total, err)
	}
	if page2.Items[0].QueryText != "query 04" || page2.Items[4].QueryText != "query 00" {
		t.Fatalf("page2 order = [%s .. %s], want tail in desc order", page2.Items[0].QueryText, page2.Items[4].QueryText)
	}
	empty, err := uc.List(ctx, &biz.RetrievalLogFilter{SessionID: "sess-page", Offset: 25})
	if err != nil || len(empty.Items) != 0 || empty.Total != 25 {
		t.Fatalf("beyond range = (%d items, %d total, %v), want 0/25", len(empty.Items), empty.Total, err)
	}

	// 来源过滤与会话过滤组合：跨会话/来源互不可见。
	if err := logRepo.Append(ctx, &biz.RetrievalLogRecord{Source: biz.RetrievalSourcePipeline, Status: biz.RetrievalStatusSuccess}); err != nil {
		t.Fatalf("append pipeline row: %v", err)
	}
	tools, err := uc.List(ctx, &biz.RetrievalLogFilter{SessionID: "sess-page", Source: biz.RetrievalSourceTool})
	if err != nil || tools.Total != 25 {
		t.Fatalf("filtered list = (%d total, %v), want 25 (session+source combo)", tools.Total, err)
	}
	pipelines, err := uc.List(ctx, &biz.RetrievalLogFilter{Source: biz.RetrievalSourcePipeline})
	if err != nil || pipelines.Total != 1 {
		t.Fatalf("pipeline list = (%d total, %v), want 1", pipelines.Total, err)
	}
	// 单页上限 200 以内放行，超出在用例侧拒绝（400 语义）。
	if _, err := uc.List(ctx, &biz.RetrievalLogFilter{Limit: 200}); err != nil {
		t.Fatalf("list with max page size: %v", err)
	}
}

// TestRetrievalLogRepo_unauthenticated_calls_fail_closed 守卫仓储身份门：
// 无认证 ctx 的写入与读取一律拒绝——写侧装饰器直连仓储（不经用例），
// 该门禁是留痕旁路面唯一的身份防线，与 audit/vector 同款显式断言。
func TestRetrievalLogRepo_unauthenticated_calls_fail_closed(t *testing.T) {
	d := setupRetrievalTestDB(t)
	repo := NewRetrievalLogRepo(d)
	ctx := context.Background()

	err := repo.Append(ctx, &biz.RetrievalLogRecord{Source: biz.RetrievalSourceTool})
	if err == nil {
		t.Fatal("repo append without identity = nil error, want rejection")
	}
	if _, err := repo.List(ctx, &biz.RetrievalLogFilter{Limit: 1}); err == nil {
		t.Fatal("repo list without identity = nil error, want rejection")
	}
}
