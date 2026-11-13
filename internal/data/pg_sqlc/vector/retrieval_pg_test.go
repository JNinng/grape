package vector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"grape/internal/biz"
	"grape/internal/conf"
	pgsqlc "grape/internal/data/pg_sqlc"
	"grape/internal/data/pg_sqlc/deploy"
	"grape/pkg/redact"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/trace"
)

// 检索留痕集成测试（真实 PostgreSQL，未设 GRAPE_RETRIEVAL_TEST_ADMIN_DSN /
// GRAPE_RETRIEVAL_TEST_DSN 时跳过）：覆盖留痕落库、租户 RLS 与读侧兜底、
// 留痕失败不中断检索、管线摄取来源与分页。测试从模板库克隆全新独立库，
// 不触碰其他数据库。

// retrievalTenantA/B 是用例身份：tenant_id 列宽 varchar(16)，取值保持在
// 列宽内。
var (
	retrievalTenantA = biz.Identity{TenantID: "t-d3-a", UserID: 81}
	retrievalTenantB = biz.Identity{TenantID: "t-d3-b", UserID: 82}
)

// retrievalTestTraceID 是用例的固定链路 ID（合法非零 16 字节）。
var retrievalTestTraceID = [16]byte{0xdd, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}

// setupRetrievalTestDB 从模板库克隆全新独立测试库（含
// schema/97_agent_retrieval_log.sql 的模板装载）并返回以 grape_u 连接的
// 存储门面；测试结束经 t.Cleanup 删库，测试可并行互不干扰。
func setupRetrievalTestDB(t *testing.T) *pgsqlc.Data {
	t.Helper()
	adminDSN := os.Getenv("GRAPE_RETRIEVAL_TEST_ADMIN_DSN")
	testDSN := os.Getenv("GRAPE_RETRIEVAL_TEST_DSN")
	if adminDSN == "" || testDSN == "" {
		t.Skip("GRAPE_RETRIEVAL_TEST_ADMIN_DSN / GRAPE_RETRIEVAL_TEST_DSN not set; skipping retrieval log integration tests")
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
	d, cleanup, err := pgsqlc.NewData(&conf.Data{Database: &conf.Data_Database{
		Master: &conf.Data_Database_Slot{Source: poolDSN},
		System: &conf.Data_Database_Slot{Source: poolDSN},
	}})
	if err != nil {
		t.Fatalf("connect grape_u: %v", err)
	}
	t.Cleanup(cleanup)
	return d
}

// newLoggedVectorStoreForTest 构造装饰器与留痕仓储：真实 pgvector 仓储
// （空间声明与部署索引对齐）为内层，registry 与生产装配同默认值。
func newLoggedVectorStoreForTest(t *testing.T, d *pgsqlc.Data, logs biz.RetrievalLogRepo) biz.VectorStore {
	t.Helper()
	store, cleanup, err := NewVectorStore(vectorStoreConf(), d, sharedTokenizerForTest(t))
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
func countRetrievalRows(t *testing.T, d *pgsqlc.Data, tenant biz.Identity) int64 {
	t.Helper()
	auth, err := pgsqlc.NewIdentity(tenant)
	if err != nil {
		t.Fatalf("map identity: %v", err)
	}
	var total int64
	err = d.IdentityTx(context.Background(), auth, func(s *pgsqlc.IdentityTx) error {
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
		spaceDoc(&biz.VectorDocument{ID: "doc-a", Content: "grape vector alpha", Embedding: unitVector(int(vectorDimension), 0),
			Metadata: map[string]any{"source_id": "doc", "anchor": "runes 0-16"}}),
		spaceDoc(&biz.VectorDocument{ID: "doc-b", Content: "grape vector beta", Embedding: unitVector(int(vectorDimension), 1),
			Metadata: map[string]any{"source_id": "doc", "anchor": "runes 0-16"}}),
	}
	for _, doc := range docs {
		if err := store.Add(ctx, doc); err != nil {
			t.Fatalf("seed document %s: %v", doc.ID, err)
		}
	}
	return unitVector(int(vectorDimension), 0)
}

// retrievalSpaceFilter 是指向测试空间的检索过滤（Search 必填项）。
func retrievalSpaceFilter() *biz.VectorFilter {
	return &biz.VectorFilter{Embedder: vectorTestSpace, Dimension: int(vectorDimension), Limit: 2}
}

func TestRetrievalLog_search_records_query_topk_and_hits_matching_actual(t *testing.T) {
	t.Parallel()
	d := setupRetrievalTestDB(t)
	logRepo := pgsqlc.NewRetrievalLogRepo(d)
	store := newLoggedVectorStoreForTest(t, d, logRepo)
	queryVector := seedVectorDocs(t, store, retrievalTenantA)

	uc := biz.NewRetrievalLogUsecase(logRepo)
	ctx := retrievalSearchContext(retrievalTenantA, "grape 检索")
	hits, err := store.Search(ctx, queryVector, retrievalSpaceFilter())
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
	case row.Embedder != vectorTestSpace:
		t.Fatalf("row embedder = %q, want %s (search space recorded)", row.Embedder, vectorTestSpace)
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

func TestRetrievalLog_console_search_records_console_source_without_session(t *testing.T) {
	t.Parallel()
	d := setupRetrievalTestDB(t)
	logRepo := pgsqlc.NewRetrievalLogRepo(d)
	store := newLoggedVectorStoreForTest(t, d, logRepo)
	queryVector := seedVectorDocs(t, store, retrievalTenantA)

	ctx := retrievalIdentityContext(retrievalTenantA, true)
	ctx = biz.WithRetrievalQuery(ctx, "console query")
	ctx = biz.WithRetrievalSource(ctx, biz.RetrievalSourceConsole)
	if _, err := store.Search(ctx, queryVector, retrievalSpaceFilter()); err != nil {
		t.Fatalf("search: %v", err)
	}

	uc := biz.NewRetrievalLogUsecase(logRepo)
	list, err := uc.List(ctx, &biz.RetrievalLogFilter{Source: biz.RetrievalSourceConsole})
	if err != nil || list.Total != 1 {
		t.Fatalf("list console rows = (%d total, %v), want 1", list.Total, err)
	}
	row := list.Items[0]
	switch {
	case row.SessionID != "":
		t.Fatalf("session id = %q, want empty (console path has no session)", row.SessionID)
	case row.QueryText != "console query":
		t.Fatalf("query text = %q, want console query", row.QueryText)
	case row.RequestedTopK != 2 || row.EffectiveTopK != 2:
		t.Fatalf("top_k = (%d, %d), want (2, 2)", row.RequestedTopK, row.EffectiveTopK)
	}
	tools, err := uc.List(ctx, &biz.RetrievalLogFilter{Source: biz.RetrievalSourceTool})
	if err != nil || tools.Total != 0 {
		t.Fatalf("list tool rows = (%d total, %v), want 0 (console search is not a tool row)", tools.Total, err)
	}
}

func TestRetrievalLog_cross_tenant_invisible_under_enabled_rls(t *testing.T) {
	t.Parallel()
	d := setupRetrievalTestDB(t)
	logRepo := pgsqlc.NewRetrievalLogRepo(d)
	store := newLoggedVectorStoreForTest(t, d, logRepo)
	seedVectorDocs(t, store, retrievalTenantA)
	// 租户 A 的检索留痕已落库。
	if total := countRetrievalRows(t, d, retrievalTenantA); total == 0 {
		t.Fatalf("seed produced no retrieval rows")
	}

	// 租户 B 检索本租户文档（自身留痕成功）后查询：跨租户行不可见，
	// 只见自己的行。
	store.Search(retrievalSearchContext(retrievalTenantB, "b query"), unitVector(int(vectorDimension), 2), retrievalSpaceFilter())
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
	t.Parallel()
	d := setupRetrievalTestDB(t)
	logRepo := pgsqlc.NewRetrievalLogRepo(d)
	store := newLoggedVectorStoreForTest(t, d, logRepo)
	seedVectorDocs(t, store, retrievalTenantA)
	uc := biz.NewRetrievalLogUsecase(logRepo)

	// 关闭 RLS（模拟误操作/降级部署）：读侧同源租户谓词必须独立兜住
	// 隔离，属主语义不变。
	adminCfg, err := pgx.ParseConfig(os.Getenv("GRAPE_RETRIEVAL_TEST_ADMIN_DSN"))
	if err != nil {
		t.Fatalf("parse admin dsn: %v", err)
	}
	adminCfg.Database = deploy.DatabaseNameFor(t.Name())
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
	t.Parallel()
	d := setupRetrievalTestDB(t)
	// 留痕仓储恒失败：检索必须照常返回，行数不得增加（失败只记日志）。
	store := newLoggedVectorStoreForTest(t, d, failingRetrievalRepo{})
	queryVector := seedVectorDocs(t, store, retrievalTenantA)

	hits, err := store.Search(retrievalSearchContext(retrievalTenantA, "grape 检索"), queryVector, retrievalSpaceFilter())
	if err != nil || len(hits) != 2 {
		t.Fatalf("search with failing log repo = (%d hits, %v), must succeed", len(hits), err)
	}
	if total := countRetrievalRows(t, d, retrievalTenantA); total != 0 {
		t.Fatalf("retrieval rows = %d, want 0 (nothing written, search unaffected)", total)
	}
}

// pipelineEmbedder 是摄取管线的替身 embedder：维度与部署索引一致，向量
// 由文本长度决定维度位（确定性，内容与留痕断言无关）。
type pipelineEmbedder struct{}

func (pipelineEmbedder) Dimension(string) (int, error) { return int(vectorDimension), nil }

func (pipelineEmbedder) Embed(_ context.Context, _ string, text string) ([]float32, error) {
	return unitVector(int(vectorDimension), len(text)%int(vectorDimension)), nil
}

// fixedSpaceResolver 是摄取管线的替身默认空间解析器：固定解析测试空间
// （生产由 pg_sqlc 的 EmbedderResolver 读租户配置）。
type fixedSpaceResolver struct{}

func (fixedSpaceResolver) ResolveDefaultEmbedder(context.Context) (string, int, error) {
	return vectorTestSpace, int(vectorDimension), nil
}

func (fixedSpaceResolver) ResolveEmbeddingSpace(_ context.Context, key string) (biz.EmbeddingSpace, error) {
	if key != vectorTestSpace {
		return biz.EmbeddingSpace{}, biz.ErrEmbedderUnknown
	}
	return biz.EmbeddingSpace{Key: vectorTestSpace, Dimension: int(vectorDimension)}, nil
}

func TestRetrievalLog_pipeline_ingest_records_source_pipeline(t *testing.T) {
	t.Parallel()
	d := setupRetrievalTestDB(t)
	logRepo := pgsqlc.NewRetrievalLogRepo(d)
	store := newLoggedVectorStoreForTest(t, d, logRepo)
	ucase, err := biz.NewDocumentUsecase(store, pipelineEmbedder{}, fixedSpaceResolver{}, nil, biz.WithChunking(8, 2))
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
	// 首次摄取：差量对账无现存行，两行 Add，无覆盖清除行。
	list, err := uc.List(ctx, &biz.RetrievalLogFilter{Source: biz.RetrievalSourcePipeline})
	if err != nil || list.Total != 2 {
		t.Fatalf("pipeline rows = (%d total, %v), want 2 adds", list.Total, err)
	}
	// 落库切片 ID 是留痕行的唯一事实来源（哈希按块正文派生，用例不重算）。
	existing, err := store.GetMetadata(ctx, &biz.VectorFilter{Metadata: map[string]any{"source_id": "handbook"}})
	if err != nil || len(existing) != 2 {
		t.Fatalf("stored chunks = (%d, %v), want 2", len(existing), err)
	}
	seen := map[string]bool{}
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
		if !hashedChunkID(id) {
			t.Fatalf("add row %d document = %q, want hashed chunk id", i, id)
		}
		if _, ok := existing[id]; !ok {
			t.Fatalf("add row %d document = %q not among stored chunks %v", i, id, existing)
		}
		seen[id] = true
		anchor, _ := hits[0]["anchor"].(string)
		if !strings.HasPrefix(anchor, "runes ") {
			t.Fatalf("add row %d anchor = %q, want chunk metadata anchor", i, anchor)
		}
		if row.QueryText != "" || row.RequestedTopK != 0 || row.EffectiveTopK != 0 {
			t.Fatalf("add row %d = (%q, %d, %d), want ingest-side nulls", i, row.QueryText, row.RequestedTopK, row.EffectiveTopK)
		}
		if row.Embedder != vectorTestSpace {
			t.Fatalf("add row %d embedder = %q, want %s (ingested space recorded)", i, row.Embedder, vectorTestSpace)
		}
	}
	if len(seen) != 2 {
		t.Fatalf("logged add ids = %v, want both stored chunks", seen)
	}

	// 同内容重摄：ID 未变，差量对账走 Update（两行），无标记退役行。
	result2, err := ucase.Ingest(ctx, &biz.Document{ID: "handbook", Name: "Grape Handbook", Content: "01234567890123"})
	if err != nil || result2.Replaced != 0 {
		t.Fatalf("same-content re-ingest = (%+v, %v), want replaced 0", result2, err)
	}
	list2, err := uc.List(ctx, &biz.RetrievalLogFilter{Source: biz.RetrievalSourcePipeline})
	if err != nil || list2.Total != 4 {
		t.Fatalf("pipeline rows after same-content re-ingest = (%d total, %v), want 4", list2.Total, err)
	}

	// 内容变化重摄：旧两行标记退役（一行 MarkSuperseded，条件记旧 ID），
	// 新两行 Add；Replaced=2。
	result3, err := ucase.Ingest(ctx, &biz.Document{ID: "handbook", Name: "Grape Handbook", Content: "abcdefghijklmn"})
	if err != nil || result3.Replaced != 2 {
		t.Fatalf("changed re-ingest = (%+v, %v), want replaced 2", result3, err)
	}
	list3, err := uc.List(ctx, &biz.RetrievalLogFilter{Source: biz.RetrievalSourcePipeline})
	if err != nil || list3.Total != 7 {
		t.Fatalf("pipeline rows after changed re-ingest = (%d total, %v), want 7", list3.Total, err)
	}
	var supersede *biz.RetrievalLog
	for _, row := range list3.Items {
		if row.Hits != nil || len(row.FilterSummary) == 0 {
			continue
		}
		summary := map[string]any{}
		if err := json.Unmarshal(row.FilterSummary, &summary); err != nil {
			t.Fatalf("unmarshal supersede row summary: %v", err)
		}
		if ids, _ := summary["ids"].([]any); len(ids) == 2 {
			supersede = row
			break
		}
	}
	if supersede == nil || supersede.Embedder != "" || supersede.Status != biz.RetrievalStatusSuccess {
		t.Fatalf("supersede row = %+v, want success row with two retired ids and no space", supersede)
	}
}

// hashedChunkID 判定切片 ID 为现行形态 "<source>#<index>#<8 hex>"。
func hashedChunkID(id string) bool {
	at := strings.LastIndex(id, "#")
	if at <= 0 {
		return false
	}
	suffix := id[at+1:]
	if len(suffix) != 8 {
		return false
	}
	for _, r := range suffix {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return strings.Contains(id[:at], "#")
}

func TestRetrievalLog_pagination_orders_by_time_desc_and_boundaries(t *testing.T) {
	t.Parallel()
	d := setupRetrievalTestDB(t)
	logRepo := pgsqlc.NewRetrievalLogRepo(d)
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
	t.Parallel()
	d := setupRetrievalTestDB(t)
	repo := pgsqlc.NewRetrievalLogRepo(d)
	ctx := context.Background()

	err := repo.Append(ctx, &biz.RetrievalLogRecord{Source: biz.RetrievalSourceTool})
	if err == nil {
		t.Fatal("repo append without identity = nil error, want rejection")
	}
	if _, err := repo.List(ctx, &biz.RetrievalLogFilter{Limit: 1}); err == nil {
		t.Fatal("repo list without identity = nil error, want rejection")
	}
}

// stageLibraryDirectory 是检索编排集成测试的可见库集合替身：固定返回库 7。
type stageLibraryDirectory struct{}

func (stageLibraryDirectory) VisibleLibraries(context.Context, biz.Identity) ([]*biz.KnowledgeLibrary, error) {
	return []*biz.KnowledgeLibrary{{ID: 7}}, nil
}

// fixedQueryEmbedder 是检索编排集成测试的替身 embedder：任意查询文本都
// 产出基向量 e0（与 seedKnowledgeDocs 的 doc-a 同向，命中可精确断言）。
type fixedQueryEmbedder struct{}

func (fixedQueryEmbedder) Dimension(string) (int, error) { return int(vectorDimension), nil }

func (fixedQueryEmbedder) Embed(context.Context, string, string) ([]float32, error) {
	return unitVector(int(vectorDimension), 0), nil
}

// seedKnowledgeDocs 以装饰器写入满足检索固定条件的两篇文档（kb_id=7、
// 参与态、有效期全覆盖），返回供检索的查询向量。
func seedKnowledgeDocs(t *testing.T, store biz.VectorStore, id biz.Identity) []float32 {
	t.Helper()
	ctx := retrievalIdentityContext(id, false)
	kbMeta := func(index int) map[string]any {
		return map[string]any{
			"source_id":       "doc",
			"source_name":     "seed",
			"anchor":          fmt.Sprintf("runes %d-%d", index*16, index*16+15),
			"chunk_index":     index,
			"kb_id":           "7",
			"document_id":     "1",
			"doc_status":      int(biz.KnowledgeStatusEnabled),
			"valid_from_unix": int64(0),
			"valid_to_unix":   int64(4102444800),
		}
	}
	docs := []*biz.VectorDocument{
		spaceDoc(&biz.VectorDocument{ID: "doc-a", Content: "grape vector alpha", Embedding: unitVector(int(vectorDimension), 0), Metadata: kbMeta(0)}),
		spaceDoc(&biz.VectorDocument{ID: "doc-b", Content: "grape vector beta", Embedding: unitVector(int(vectorDimension), 1), Metadata: kbMeta(1)}),
	}
	for _, doc := range docs {
		if err := store.Add(ctx, doc); err != nil {
			t.Fatalf("seed document %s: %v", doc.ID, err)
		}
	}
	return unitVector(int(vectorDimension), 0)
}

// newOrchestrationSearchUsecase 构造检索编排用例（真实装饰后向量仓储 +
// 真实留痕仓储；embedder/resolver/可见库集合为替身）。
func newOrchestrationSearchUsecase(t *testing.T, store biz.VectorStore, logs biz.RetrievalLogRepo, registry *redact.Registry) *biz.KnowledgeSearchUsecase {
	t.Helper()
	uc, err := biz.NewKnowledgeSearchUsecase(store, fixedQueryEmbedder{}, fixedSpaceResolver{}, nil, stageLibraryDirectory{}, logs, registry)
	if err != nil {
		t.Fatalf("build search usecase: %v", err)
	}
	return uc
}

// retrievalStageView 是留痕行 stages 数组的读侧最小投影（断言用）。
type retrievalStageView struct {
	Stage      string           `json:"stage"`
	QueryText  string           `json:"query_text"`
	Limit      int              `json:"limit"`
	Model      string           `json:"model"`
	Candidates []map[string]any `json:"candidates"`
	Units      []map[string]any `json:"units"`
	Error      string           `json:"error"`
	DurationMS int64            `json:"duration_ms"`
}

func TestRetrievalLog_orchestration_search_writes_single_stage_row(t *testing.T) {
	t.Parallel()
	d := setupRetrievalTestDB(t)
	logRepo := pgsqlc.NewRetrievalLogRepo(d)
	store := newLoggedVectorStoreForTest(t, d, logRepo)
	seedKnowledgeDocs(t, store, retrievalTenantA)
	uc := newOrchestrationSearchUsecase(t, store, logRepo, redact.NewStandardRegistry())

	ctx := retrievalIdentityContext(retrievalTenantA, true)
	ctx = biz.WithSessionID(ctx, "sess-d3-run")
	units, err := uc.Search(ctx, &biz.KnowledgeSearchQuery{Query: "grape 检索", TopK: 2})
	if err != nil || len(units) == 0 {
		t.Fatalf("orchestrated search = (%d units, %v), want hits", len(units), err)
	}

	// 一次操作一行：多路调用（2 召回路 + pack 的 List）只产生一行编排
	// 留痕，来源为缺省 console。
	reader := biz.NewRetrievalLogUsecase(logRepo)
	list, err := reader.List(ctx, &biz.RetrievalLogFilter{Source: biz.RetrievalSourceConsole})
	if err != nil || list.Total != 1 {
		t.Fatalf("console rows = (%d total, %v), want exactly one orchestration row", list.Total, err)
	}
	tools, err := reader.List(ctx, &biz.RetrievalLogFilter{Source: biz.RetrievalSourceTool})
	if err != nil || tools.Total != 0 {
		t.Fatalf("tool rows = (%d total, %v), want 0 (decorator suppressed)", tools.Total, err)
	}
	row := list.Items[0]
	switch {
	case row.Status != biz.RetrievalStatusSuccess:
		t.Fatalf("row status = %q, want success", row.Status)
	case row.QueryText != "grape 检索":
		t.Fatalf("query text = %q, want the redacted main query", row.QueryText)
	case row.RequestedTopK != 2 || row.EffectiveTopK != 2:
		t.Fatalf("top_k = (%d, %d), want the normalized top_k on both", row.RequestedTopK, row.EffectiveTopK)
	case row.Embedder != vectorTestSpace:
		t.Fatalf("embedder = %q, want %s", row.Embedder, vectorTestSpace)
	// 编排行与装饰器行同口径提取身份与链路事实：租户/用户来自认证 ctx
	// （仓储经 IdentityTx 注入），trace 取 SpanContext，会话取 ctx 事实。
	case row.TenantID != retrievalTenantA.TenantID || row.UserID != retrievalTenantA.UserID:
		t.Fatalf("row owner = (%s, %d), want from authenticated identity (%s, %d)",
			row.TenantID, row.UserID, retrievalTenantA.TenantID, retrievalTenantA.UserID)
	case row.TraceID != trace.TraceID(retrievalTestTraceID).String():
		t.Fatalf("trace id = %q, want %q from the span context", row.TraceID, trace.TraceID(retrievalTestTraceID).String())
	case row.SessionID != "sess-d3-run":
		t.Fatalf("session id = %q, want the ctx fact", row.SessionID)
	case len(row.Hits) == 0 || len(row.Stages) == 0:
		t.Fatalf("row = (hits %s, stages %s), want final hits and stage array", row.Hits, row.Stages)
	}
	var summary map[string]any
	if err := json.Unmarshal(row.FilterSummary, &summary); err != nil {
		t.Fatalf("unmarshal filter summary: %v", err)
	}
	if ids, ok := summary["kb_ids"].([]any); !ok || len(ids) != 1 || ids[0] != float64(7) {
		t.Fatalf("filter summary = %v, want kb_ids [7]", summary)
	}

	// 阶段数组按执行序：vector → lexical → fusion → pack（未装配重排层，
	// 不伪造 rerank 阶段）；召回阶段记变体文本与候选。
	var stages []retrievalStageView
	if err := json.Unmarshal(row.Stages, &stages); err != nil {
		t.Fatalf("unmarshal stages: %v", err)
	}
	wantOrder := []string{biz.RetrievalStageVector, biz.RetrievalStageLexical, biz.RetrievalStageFusion, biz.RetrievalStagePack}
	if len(stages) != len(wantOrder) {
		t.Fatalf("stages = %d elements, want %d (%v)", len(stages), len(wantOrder), stages)
	}
	for i, want := range wantOrder {
		if stages[i].Stage != want {
			t.Fatalf("stage %d = %s, want %s (execution order)", i, stages[i].Stage, want)
		}
	}
	if stages[0].QueryText != "grape 检索" || len(stages[0].Candidates) == 0 {
		t.Fatalf("vector stage = (query %q, candidates %d), want variant text and candidates",
			stages[0].QueryText, len(stages[0].Candidates))
	}
	if stages[0].Limit != 4 {
		t.Fatalf("vector stage limit = %d, want 2*top_k", stages[0].Limit)
	}
	if stages[3].Limit != biz.KnowledgeSearchPackBudgetTokens || len(stages[3].Units) == 0 {
		t.Fatalf("pack stage = (limit %d, units %d), want token budget and reply units", stages[3].Limit, len(stages[3].Units))
	}
}

func TestRetrievalLog_orchestration_redacts_variant_queries_in_stages(t *testing.T) {
	t.Parallel()
	// 变体查询文本与主查询同口径脱敏：掩码注册表下行级与阶段级查询文本
	// 一律掩码，多路调用仍只落一行。
	d := setupRetrievalTestDB(t)
	logRepo := pgsqlc.NewRetrievalLogRepo(d)
	store := newLoggedVectorStoreForTest(t, d, logRepo)
	seedKnowledgeDocs(t, store, retrievalTenantA)

	registry := redact.NewRegistry()
	if err := registry.Register(redact.CategoryRetrieval, redact.NewMask("[masked]")); err != nil {
		t.Fatalf("register: %v", err)
	}
	uc := newOrchestrationSearchUsecase(t, store, logRepo, registry)

	ctx := retrievalIdentityContext(retrievalTenantA, false)
	units, err := uc.Search(ctx, &biz.KnowledgeSearchQuery{Query: "grape 检索", Variants: []string{"变体查询"}})
	if err != nil || len(units) == 0 {
		t.Fatalf("orchestrated search = (%d units, %v), want hits", len(units), err)
	}

	reader := biz.NewRetrievalLogUsecase(logRepo)
	list, err := reader.List(ctx, &biz.RetrievalLogFilter{Source: biz.RetrievalSourceConsole})
	if err != nil || list.Total != 1 {
		t.Fatalf("rows = (%d total, %v), want exactly one orchestration row", list.Total, err)
	}
	row := list.Items[0]
	if row.QueryText != "[masked]" {
		t.Fatalf("row query text = %q, want masked", row.QueryText)
	}
	var stages []retrievalStageView
	if err := json.Unmarshal(row.Stages, &stages); err != nil {
		t.Fatalf("unmarshal stages: %v", err)
	}
	// 主查询 + 1 变体 = 两对召回阶段，查询文本都按 retrieval 类别脱敏。
	recallQueries := make([]string, 0, 4)
	for _, s := range stages {
		if s.Stage == biz.RetrievalStageVector || s.Stage == biz.RetrievalStageLexical {
			recallQueries = append(recallQueries, s.QueryText)
		}
	}
	if len(recallQueries) != 4 {
		t.Fatalf("recall stages = %d, want 4 (two queries, vector+lexical each)", len(recallQueries))
	}
	for _, q := range recallQueries {
		if q != "[masked]" {
			t.Fatalf("recall stage query = %q, want masked variant text", q)
		}
	}
}
