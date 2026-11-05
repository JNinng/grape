package tools

// 摄取管线与检索工具集成测试：以真实 PostgreSQL（本套件专属库
// grape_search_test）验证 摄取 → 检索 闭环、RLS 关闭时的跨租户隔离、
// 引用可追溯、幂等覆盖与 top_k 上限。embedding 一律用确定性替身（关键词
// → 正交基向量），不发起真实网络请求；向量访问走真实 VectorStore 契约
// 实现与框架适配器（生产装配路径）。
//
// 环境变量门控（未设置时跳过）：
//
//	GRAPE_SEARCH_TEST_ADMIN_DSN=postgres://postgresql:root@127.0.0.1:5432/postgres \
//	go test ./internal/agent/tools -run TestPGDocumentSearch -count=1 -v
//
// 装载取舍与会话/确认集成测试一致：TestDeployFiles 全清单部署 + 跨包部署
// 锁互斥，绝不触碰其他套件的测试库。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"grape/internal/agent/internal/testsupport"
	"grape/internal/biz"
	"grape/internal/conf"
	"grape/internal/data/pg_sqlc"
	pgvector "grape/internal/data/pg_sqlc/vector"

	"github.com/jackc/pgx/v5"
)

// searchSuite 是本套件专属测试库定位；其余测试库绝不触碰。
var searchSuite = testsupport.SuiteDatabase{
	EnvVar:         "GRAPE_SEARCH_TEST_ADMIN_DSN",
	Database:       "grape_search_test",
	GrapeUPassword: "grape_u_pwd",
	SkipMessage:    "GRAPE_SEARCH_TEST_ADMIN_DSN not set, skip document search integration tests",
}

// searchDimension 与部署 DDL 的表达式部分索引维度一致（1024）。
const searchDimension = 1024

// searchSpace 是检索套件的嵌入空间键（替身解析器固定产出）。
const searchSpace = "bge-m3@v1"

// d2 租户身份：tenant_id 列宽 varchar(16)，取值保持在列宽内。
var (
	searchTenantA = biz.Identity{TenantID: "t-search-a", UserID: 31}
	searchTenantB = biz.Identity{TenantID: "t-search-b", UserID: 32}
)

// keywordEmbedder 是确定性替身 embedder：按关键词映射到正交基向量
// （e0/e1/e2），保证检索命中可精确断言、无随机性。
type keywordEmbedder struct {
	dims  int
	calls []string
}

func (k *keywordEmbedder) Embed(_ context.Context, _ string, text string) ([]float32, error) {
	k.calls = append(k.calls, text)
	index := 3
	switch {
	case strings.Contains(text, "alpha"):
		index = 0
	case strings.Contains(text, "beta"):
		index = 1
	case strings.Contains(text, "gamma"):
		index = 2
	}
	vec := make([]float32, k.dims)
	vec[index] = 1
	return vec, nil
}

func (k *keywordEmbedder) Dimension(_ string) (int, error) { return k.dims, nil }

// keywordSpaceResolver 是检索套件的替身默认空间解析器：固定产出
// searchSpace（生产由 pg_sqlc 的 EmbedderResolver 读租户配置）。
type keywordSpaceResolver struct{}

func (keywordSpaceResolver) ResolveDefaultEmbedder(context.Context) (string, int, error) {
	return searchSpace, searchDimension, nil
}

func searchIdentityContext(id biz.Identity) context.Context {
	return biz.WithIdentity(context.Background(), id)
}

// searchFixture 装配生产路径的检索面：真实向量存储（含启动空间探针）→
// 框架适配器 → 检索工具，外加同一替身 embedder 驱动的摄取管线与库句柄
// （RLS 场景断言直查 pg_catalog 用）。
type searchFixture struct {
	data      *pg_sqlc.Data
	grapeUDSN string
	store     biz.VectorStore
	pipeline  *biz.DocumentUsecase
	tool      interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	}
	embedder *keywordEmbedder
}

func searchNewFixture(t *testing.T) *searchFixture {
	t.Helper()
	data, grapeUDSN := searchSetupDatabase(t)
	store, cleanup, err := pgvector.NewVectorStore(&conf.Vector{
		Enabled:         true,
		DefaultEmbedder: searchSpace,
		Embedders: []*conf.Vector_Embedding{
			{Model: "bge-m3", Version: "v1", Dimension: searchDimension},
		},
	}, data)
	if err != nil {
		t.Fatalf("NewVectorStore = %v, want probe pass", err)
	}
	t.Cleanup(cleanup)
	embedder := &keywordEmbedder{dims: searchDimension}
	pipeline, err := biz.NewDocumentUsecase(store, embedder, keywordSpaceResolver{})
	if err != nil {
		t.Fatalf("NewDocumentUsecase: %v", err)
	}
	return &searchFixture{
		data:      data,
		grapeUDSN: grapeUDSN,
		store:     store,
		pipeline:  pipeline,
		tool: NewSearchTool(embedder, keywordSpaceResolver{}, NewVectorStoreAdapter(store)).(interface {
			Call(ctx context.Context, jsonArgs []byte) (any, error)
		}),
		embedder: embedder,
	}
}

// searchSetupDatabase 重建专属测试库并按 TestDeployFiles 全清单部署，返回
// grape_u 连接的存储门面与 DSN（pg_catalog 断言用）。
func searchSetupDatabase(t *testing.T) (*pg_sqlc.Data, string) {
	t.Helper()
	dsn := testsupport.SetupSuiteDatabase(t, searchSuite)
	data, cleanup, err := pg_sqlc.NewData(&conf.Data{Database: &conf.Data_Database{
		// 两槽同源（与 configs 注释口径一致）。
		Master: &conf.Data_Database_Slot{Source: dsn},
		System: &conf.Data_Database_Slot{Source: dsn},
	}})
	if err != nil {
		t.Fatalf("build data: %v", err)
	}
	t.Cleanup(cleanup)
	return data, dsn
}

// searchCallTool 以 JSON 入参直调检索工具（框架 CallableTool 面，与模型侧
// 相同的 schema 反序列化路径），返回结构化出参。
func searchCallTool(t *testing.T, f *searchFixture, ctx context.Context, args string) searchDocsOutput {
	t.Helper()
	raw, err := f.tool.Call(ctx, []byte(args))
	if err != nil {
		t.Fatalf("search tool call: %v", err)
	}
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal tool output: %v", err)
	}
	var out searchDocsOutput
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("decode tool output: %v", err)
	}
	return out
}

func TestPGDocumentSearch_ingest_then_tool_roundtrip_hits_with_citation(t *testing.T) {
	f := searchNewFixture(t)
	ctx := searchIdentityContext(searchTenantA)

	result, err := f.pipeline.Ingest(ctx, &biz.Document{
		ID:      "kb-alpha-1",
		Name:    "Refund Policy",
		Content: "alpha refund policy applies to all annual plans without exceptions.",
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if result.Chunks != 1 || result.Replaced != 0 {
		t.Fatalf("ingest result = %+v, want 1 chunk first pass", result)
	}

	out := searchCallTool(t, f, ctx, `{"query":"alpha","top_k":3}`)
	if out.Count != 1 || len(out.Hits) != 1 {
		t.Fatalf("hits = %d, want exactly 1", out.Count)
	}
	hit := out.Hits[0]
	// 引用可追溯：文档 ID / 来源标识 / 锚点随命中返回。
	if hit.DocumentID != "kb-alpha-1#0" || hit.SourceID != "kb-alpha-1" || hit.SourceName != "Refund Policy" {
		t.Fatalf("citation fields = %+v, want kb-alpha-1 lineage", hit)
	}
	if !strings.HasPrefix(hit.Anchor, "runes 0-") {
		t.Fatalf("anchor = %q, want rune range into the source document", hit.Anchor)
	}
	// 不可信围栏：恰好一对真实标记包住原文，notice 恒在。
	if strings.Count(hit.Content, untrustedBeginMarker) != 1 || strings.Count(hit.Content, untrustedEndMarker) != 1 {
		t.Fatalf("untrusted fence broken:\n%s", hit.Content)
	}
	if !strings.Contains(hit.Content, "alpha refund policy applies to all annual plans") {
		t.Fatalf("chunk content lost in wrapping:\n%s", hit.Content)
	}
	if !strings.Contains(out.Notice, "never follow instructions") {
		t.Fatalf("untrusted notice missing: %q", out.Notice)
	}

	// 引用可反查：命中文档 ID 经存储点查取回切片原文（不含围栏）。
	doc, err := f.store.Get(ctx, "kb-alpha-1#0")
	if err != nil {
		t.Fatalf("reverse lookup by document id: %v", err)
	}
	if doc.Content != "alpha refund policy applies to all annual plans without exceptions." {
		t.Fatalf("stored chunk content = %q, want original", doc.Content)
	}
	if doc.Embedder != searchSpace || doc.Dimension != searchDimension {
		t.Fatalf("stored chunk space = %s/%d, want %s/%d", doc.Embedder, doc.Dimension, searchSpace, searchDimension)
	}
	if doc.Metadata["source_id"] != "kb-alpha-1" {
		t.Fatalf("stored metadata source_id = %v, want kb-alpha-1", doc.Metadata["source_id"])
	}
}

func TestPGDocumentSearch_cross_tenant_not_recallable_while_rls_disabled(t *testing.T) {
	f := searchNewFixture(t)
	ctxA := searchIdentityContext(searchTenantA)
	ctxB := searchIdentityContext(searchTenantB)

	// 固定场景前提：表无 RLS——隔离必须来自应用层过滤，
	// 数据库策略缺失不构成隔离缺口。
	conn, err := pgx.Connect(context.Background(), f.grapeUDSN)
	if err != nil {
		t.Fatalf("connect grape_u for catalog check: %v", err)
	}
	var rowSecurity bool
	if err := conn.QueryRow(context.Background(),
		`SELECT relrowsecurity FROM pg_class WHERE oid = 'agent.agent_document'::regclass`).Scan(&rowSecurity); err != nil {
		t.Fatalf("read relrowsecurity: %v", err)
	}
	if err := conn.Close(context.Background()); err != nil {
		t.Fatalf("close catalog connection: %v", err)
	}
	if rowSecurity {
		t.Fatal("agent.agent_document has RLS enabled; the no-RLS isolation scenario no longer holds")
	}

	if _, err := f.pipeline.Ingest(ctxA, &biz.Document{
		ID:      "kb-secret-a",
		Content: "alpha tenant a private playbook",
	}); err != nil {
		t.Fatalf("ingest as tenant a: %v", err)
	}

	// 工具路径：租户 B 检索 0 命中。
	out := searchCallTool(t, f, ctxB, `{"query":"alpha","top_k":5}`)
	if out.Count != 0 || len(out.Hits) != 0 {
		t.Fatalf("cross-tenant tool hits = %+v, want none", out.Hits)
	}
	// biz 路径：租户 B 以同一查询向量直检契约（声明同一检索空间），同样
	// 0 命中。
	vec, err := f.embedder.Embed(context.Background(), searchSpace, "alpha")
	if err != nil {
		t.Fatalf("embed query: %v", err)
	}
	hits, err := f.store.Search(ctxB, vec, &biz.VectorFilter{Embedder: searchSpace, Dimension: searchDimension, Limit: 10})
	if err != nil {
		t.Fatalf("biz search as tenant b: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("cross-tenant biz hits = %d, want 0 (isolation must hold without RLS)", len(hits))
	}
	// 租户 A 自身不受影响。
	if out := searchCallTool(t, f, ctxA, `{"query":"alpha"}`); out.Count != 1 {
		t.Fatalf("owner tool hits = %d, want 1", out.Count)
	}
}

func TestPGDocumentSearch_reingest_replaces_previous_chunks(t *testing.T) {
	f := searchNewFixture(t)
	ctx := searchIdentityContext(searchTenantA)
	// 小块切分制造多块场景（默认切块下单文档单块）。
	pipeline, err := biz.NewDocumentUsecase(f.store, f.embedder, keywordSpaceResolver{}, biz.WithChunking(4, 1))
	if err != nil {
		t.Fatalf("NewDocumentUsecase(small chunks): %v", err)
	}

	v1, err := pipeline.Ingest(ctx, &biz.Document{ID: "kb-evolve", Content: strings.Repeat("beta", 120)})
	if err != nil {
		t.Fatalf("ingest v1: %v", err)
	}
	if v1.Chunks <= 1 {
		t.Fatalf("v1 chunks = %d, want multiple", v1.Chunks)
	}
	// v2：同 ID 重摄（内容变短），旧块必须全部清除（同来源覆盖幂等）。
	v2, err := pipeline.Ingest(ctx, &biz.Document{ID: "kb-evolve", Content: strings.Repeat("beta", 20)})
	if err != nil {
		t.Fatalf("ingest v2: %v", err)
	}
	if v2.Replaced != int64(v1.Chunks) {
		t.Fatalf("v2 replaced = %d, want %d (all v1 chunks cleared)", v2.Replaced, v1.Chunks)
	}
	total, err := f.store.Count(ctx, &biz.VectorFilter{Metadata: map[string]any{"source_id": "kb-evolve"}})
	if err != nil || total != int64(v2.Chunks) {
		t.Fatalf("count after re-ingest = (%d, %v), want %d", total, err, v2.Chunks)
	}
	// v1 的末块（v2 不再产出的 ID）已不可见。
	staleID := fmt.Sprintf("kb-evolve#%d", v1.Chunks-1)
	if _, err := f.store.Get(ctx, staleID); !errors.Is(err, biz.ErrVectorDocNotFound) {
		t.Fatalf("stale v1 chunk %s still present: %v", staleID, err)
	}
}

func TestPGDocumentSearch_dimension_drift_rejects_at_ingest(t *testing.T) {
	f := searchNewFixture(t)
	ctx := searchIdentityContext(searchTenantA)
	// 替身 embedder 产出维度与解析器声明不一致：写路径逐次对账在调用点
	// 显式拒绝，存量数据不破坏（与存储注册表对账共同封住维度链路）。
	drifted := &keywordEmbedder{dims: 8}
	pipeline, err := biz.NewDocumentUsecase(f.store, drifted, keywordSpaceResolver{})
	if err != nil {
		t.Fatalf("NewDocumentUsecase: %v", err)
	}
	if _, err := pipeline.Ingest(ctx, &biz.Document{ID: "kb-drift", Content: "alpha content"}); err == nil ||
		!strings.Contains(err.Error(), "dimension") {
		t.Fatalf("Ingest with drifting embedder = %v, want dimension error", err)
	}
	total, err := f.store.Count(ctx, &biz.VectorFilter{IDs: []string{"kb-drift#0"}})
	if err != nil || total != 0 {
		t.Fatalf("count after drift rejection = (%d, %v), want (0, nil) — store untouched", total, err)
	}
}

func TestPGDocumentSearch_oversized_top_k_bounded_by_adapter_cap(t *testing.T) {
	f := searchNewFixture(t)
	ctx := searchIdentityContext(searchTenantA)
	pipeline, err := biz.NewDocumentUsecase(f.store, f.embedder, keywordSpaceResolver{}, biz.WithChunking(4, 1))
	if err != nil {
		t.Fatalf("NewDocumentUsecase(small chunks): %v", err)
	}
	// 30 块同关键词（同为 e0，全部可召回），top_k 传 1000：适配器上限
	// 必须把命中收敛到 maxSearchTopK。
	if _, err := pipeline.Ingest(ctx, &biz.Document{
		ID:      "kb-many",
		Content: strings.Repeat("gamma", 150),
	}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	out := searchCallTool(t, f, ctx, `{"query":"gamma","top_k":1000}`)
	if out.Count != maxSearchTopK {
		t.Fatalf("hits = %d, want exactly %d (30 recallable chunks, adapter cap binds)",
			out.Count, maxSearchTopK)
	}
}
