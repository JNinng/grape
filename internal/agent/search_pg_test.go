package agent

// D2 摄取管线与检索工具集成测试：以真实 PostgreSQL（本套件专属库
// grape_vector_d2_test）验证 摄取 → 检索 闭环、RLS 关闭时的跨租户隔离、
// 引用可追溯、幂等覆盖与 top_k 上限。embedding 一律用确定性替身（关键词
// → 正交基向量），不发起真实网络请求；向量访问走真实 VectorStore 契约
// 实现与框架适配器（生产装配路径）。
//
// 环境变量门控（未设置时跳过）：
//
//	GRAPE_D2_TEST_ADMIN_DSN=postgres://postgresql:root@127.0.0.1:5432/postgres \
//	go test ./internal/agent -run TestPGDocumentSearch -count=1 -v
//
// 装载取舍与会话/确认集成测试一致：DeployFiles 全清单部署 + 跨包部署
// 锁互斥，绝不触碰其他套件的测试库。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"grape/internal/biz"
	"grape/internal/conf"
	"grape/internal/data/pg_sqlc"

	"github.com/jackc/pgx/v5"
)

// d2TestDatabase 是本套件专属测试库；其余测试库绝不触碰。
const d2TestDatabase = "grape_vector_d2_test"

// d2GrapeUPassword 与 role.sql 占位符替换值：grape_u 的既定测试口令。
const d2GrapeUPassword = "grape_u_pwd"

// d2Dimension 与部署 DDL 的 embedding 列维度一致（vector(1024)）。
const d2Dimension = 1024

// d2 租户身份：tenant_id 列宽 varchar(16)，取值保持在列宽内。
var (
	d2TenantA = biz.Identity{TenantID: "t-d2-a", UserID: 31}
	d2TenantB = biz.Identity{TenantID: "t-d2-b", UserID: 32}
)

// keywordEmbedder 是确定性替身 embedder：按关键词映射到正交基向量
// （e0/e1/e2），保证检索命中可精确断言、无随机性。
type keywordEmbedder struct {
	dims  int
	calls []string
}

func (k *keywordEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
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

func (k *keywordEmbedder) Dimensions() int { return k.dims }

func d2IdentityContext(id biz.Identity) context.Context {
	return biz.WithIdentity(context.Background(), id)
}

// d2Fixture 装配生产路径的检索面：真实向量存储（含启动维度探针）→
// 框架适配器 → 检索工具，外加同一替身 embedder 驱动的摄取管线与库句柄
// （RLS 场景断言直查 pg_catalog 用）。
type d2Fixture struct {
	data      *pg_sqlc.Data
	grapeUDSN string
	store     biz.VectorStore
	pipeline  *biz.DocumentUsecase
	tool      interface {
		Call(ctx context.Context, jsonArgs []byte) (any, error)
	}
	embedder *keywordEmbedder
}

func d2NewFixture(t *testing.T) *d2Fixture {
	t.Helper()
	data, grapeUDSN := d2SetupDatabase(t)
	store, cleanup, err := pg_sqlc.NewVectorStore(&conf.Vector{
		Enabled:   true,
		Dimension: d2Dimension,
	}, data)
	if err != nil {
		t.Fatalf("NewVectorStore = %v, want probe pass", err)
	}
	t.Cleanup(cleanup)
	embedder := &keywordEmbedder{dims: d2Dimension}
	pipeline, err := biz.NewDocumentUsecase(store, embedder, d2Dimension)
	if err != nil {
		t.Fatalf("NewDocumentUsecase: %v", err)
	}
	return &d2Fixture{
		data:      data,
		grapeUDSN: grapeUDSN,
		store:     store,
		pipeline:  pipeline,
		tool: newSearchTool(embedder, NewVectorStore(store)).(interface {
			Call(ctx context.Context, jsonArgs []byte) (any, error)
		}),
		embedder: embedder,
	}
}

// d2SetupDatabase 重建专属测试库并按 DeployFiles 全清单部署，返回
// grape_u 连接的存储门面与 DSN（pg_catalog 断言用）。
func d2SetupDatabase(t *testing.T) (*pg_sqlc.Data, string) {
	t.Helper()
	adminDSN := os.Getenv("GRAPE_D2_TEST_ADMIN_DSN")
	if adminDSN == "" {
		t.Skip("GRAPE_D2_TEST_ADMIN_DSN not set, skip document search integration tests")
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
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+d2TestDatabase+" WITH (FORCE)"); err != nil {
		t.Fatalf("drop test database: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+d2TestDatabase); err != nil {
		t.Fatalf("create test database: %v", err)
	}

	deployDir, err := pg_sqlc.DeployDir()
	if err != nil {
		t.Fatalf("locate deploy dir: %v", err)
	}
	if err := pg_sqlc.ValidateDeployFiles(deployDir); err != nil {
		t.Fatalf("validate deploy files: %v", err)
	}
	deployCfg := *cfg
	deployCfg.Database = d2TestDatabase
	deployCfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	deployConn, err := pgx.ConnectConfig(ctx, &deployCfg)
	if err != nil {
		t.Fatalf("connect deploy: %v", err)
	}
	defer deployConn.Close(ctx)
	// role.sql 的角色目录操作是集群级的，跨测试包并发部署必须互斥。
	release, err := pg_sqlc.AcquireDeployLock(ctx)
	if err != nil {
		t.Fatalf("acquire deploy lock: %v", err)
	}
	defer release()
	for _, name := range pg_sqlc.DeployFiles {
		sql, err := os.ReadFile(deployDir + "/" + name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		deployed := pg_sqlc.ResolveDeployPasswords(string(sql), d2GrapeUPassword, "test_u_pwd")
		if _, err := deployConn.Exec(ctx, deployed); err != nil {
			t.Fatalf("deploy %s: %v", name, err)
		}
	}
	if _, err := deployConn.Exec(ctx,
		"GRANT CONNECT ON DATABASE "+d2TestDatabase+" TO grape_u, test_u"); err != nil {
		t.Fatalf("grant connect: %v", err)
	}

	dsn, err := d2GrapeUDSN(adminDSN)
	if err != nil {
		t.Fatalf("build grape_u dsn: %v", err)
	}
	data, cleanup, err := pg_sqlc.NewData(&conf.Data{Database: &conf.Data_Database{
		// 过渡期两槽同源（与 configs 注释口径一致）。
		Master: &conf.Data_Database_Slot{Source: dsn},
		System: &conf.Data_Database_Slot{Source: dsn},
	}})
	if err != nil {
		t.Fatalf("build data: %v", err)
	}
	t.Cleanup(cleanup)
	return data, dsn
}

// d2GrapeUDSN 把管理员 DSN 改写为 grape_u 连接专属测试库的 DSN。
func d2GrapeUDSN(adminDSN string) (string, error) {
	u, err := url.Parse(adminDSN)
	if err != nil {
		return "", err
	}
	u.User = url.UserPassword("grape_u", d2GrapeUPassword)
	u.Path = "/" + d2TestDatabase
	return u.String(), nil
}

// d2CallTool 以 JSON 入参直调检索工具（框架 CallableTool 面，与模型侧
// 相同的 schema 反序列化路径），返回结构化出参。
func d2CallTool(t *testing.T, f *d2Fixture, ctx context.Context, args string) searchDocsOutput {
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
	f := d2NewFixture(t)
	ctx := d2IdentityContext(d2TenantA)

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

	out := d2CallTool(t, f, ctx, `{"query":"alpha","top_k":3}`)
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
	if doc.Metadata["source_id"] != "kb-alpha-1" {
		t.Fatalf("stored metadata source_id = %v, want kb-alpha-1", doc.Metadata["source_id"])
	}
}

func TestPGDocumentSearch_cross_tenant_not_recallable_while_rls_disabled(t *testing.T) {
	f := d2NewFixture(t)
	ctxA := d2IdentityContext(d2TenantA)
	ctxB := d2IdentityContext(d2TenantB)

	// 固定场景前提：表无 RLS——隔离必须来自应用层过滤（决策 6），
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
	out := d2CallTool(t, f, ctxB, `{"query":"alpha","top_k":5}`)
	if out.Count != 0 || len(out.Hits) != 0 {
		t.Fatalf("cross-tenant tool hits = %+v, want none", out.Hits)
	}
	// biz 路径：租户 B 以同一查询向量直检契约，同样 0 命中。
	vec, err := f.embedder.Embed(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("embed query: %v", err)
	}
	hits, err := f.store.Search(ctxB, vec, &biz.VectorFilter{Limit: 10})
	if err != nil {
		t.Fatalf("biz search as tenant b: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("cross-tenant biz hits = %d, want 0 (isolation must hold without RLS)", len(hits))
	}
	// 租户 A 自身不受影响。
	if out := d2CallTool(t, f, ctxA, `{"query":"alpha"}`); out.Count != 1 {
		t.Fatalf("owner tool hits = %d, want 1", out.Count)
	}
}

func TestPGDocumentSearch_reingest_replaces_previous_chunks(t *testing.T) {
	f := d2NewFixture(t)
	ctx := d2IdentityContext(d2TenantA)
	// 小块切分制造多块场景（默认切块下单文档单块）。
	pipeline, err := biz.NewDocumentUsecase(f.store, f.embedder, d2Dimension, biz.WithChunking(4, 1))
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

func TestPGDocumentSearch_dimension_mismatch_rejects_at_assembly(t *testing.T) {
	f := d2NewFixture(t)
	// 替身 embedder 声明维度与 conf（部署列）不一致：装配期失败（启动
	// 即失败，与 D1 conf vs DDL 探针共同封住维度链路两端）。
	_, err := biz.NewDocumentUsecase(f.store, &keywordEmbedder{dims: 8}, d2Dimension)
	if err == nil || !strings.Contains(err.Error(), "dimension") {
		t.Fatalf("NewDocumentUsecase(dimension mismatch) = %v, want rejection", err)
	}
}

func TestPGDocumentSearch_oversized_top_k_bounded_by_adapter_cap(t *testing.T) {
	f := d2NewFixture(t)
	ctx := d2IdentityContext(d2TenantA)
	pipeline, err := biz.NewDocumentUsecase(f.store, f.embedder, d2Dimension, biz.WithChunking(4, 1))
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
	out := d2CallTool(t, f, ctx, `{"query":"gamma","top_k":1000}`)
	if out.Count != maxSearchTopK {
		t.Fatalf("hits = %d, want exactly %d (30 recallable chunks, adapter cap binds)",
			out.Count, maxSearchTopK)
	}
}
