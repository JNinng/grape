package vector

import (
	"context"
	"errors"
	"strings"
	"testing"

	"grape/internal/biz"
	"grape/internal/conf"
	pgsqlc "grape/internal/data/pg_sqlc"
)

// 向量存储单测（纯内存，不触数据库）：配置校验与空间注册表解析、身份
// 注入与缺失拒绝、空间声明的注册表对账、过滤条件的参数化翻译、禁用
// 占位实现。介质行为（RLS 关闭下的租户隔离、空间隔离、检索命中）由
// vector_test.go 的集成测试覆盖。

// vectorTestConf 构造启用态配置：单空间 bge-m3@v1（dimension 维）。
func vectorTestConf(enabled bool, dimension int32, table string) *conf.Vector {
	return &conf.Vector{
		Enabled:         enabled,
		TableName:       table,
		DefaultEmbedder: "bge-m3@v1",
		Embedders: []*conf.Vector_Embedding{
			{Model: "bge-m3", Version: "v1", Dimension: dimension},
		},
	}
}

func vectorTestStore() *vectorStore {
	return &vectorStore{
		table:  vectorDefaultTable,
		spaces: map[string]int32{"bge-m3@v1": 4},
		dims:   map[int32]struct{}{4: {}},
	}
}

func TestNewVectorStore_disabled_conf_returns_disabled_store(t *testing.T) {
	store, cleanup, err := NewVectorStore(vectorTestConf(false, 0, ""), &pgsqlc.Data{})
	if err != nil {
		t.Fatalf("NewVectorStore(disabled) = %v, want nil error", err)
	}
	defer cleanup()
	if _, err := store.Get(context.Background(), "doc-1"); !errors.Is(err, biz.ErrVectorStoreDisabled) {
		t.Fatalf("Get on disabled store = %v, want ErrVectorStoreDisabled", err)
	}
}

func TestNewVectorStore_invalid_spaces_fails(t *testing.T) {
	// 空间注册表校验：维度越界、清单为空、默认键未声明，装配期拒绝。
	cases := []struct {
		name string
		conf *conf.Vector
	}{
		{"zero dimension", vectorTestConf(true, 0, "")},
		{"negative dimension", func() *conf.Vector {
			c := vectorTestConf(true, 4, "")
			c.Embedders[0].Dimension = -1
			return c
		}()},
		{"oversize dimension", func() *conf.Vector {
			c := vectorTestConf(true, 4, "")
			c.Embedders[0].Dimension = conf.VectorMaxDimension + 1
			return c
		}()},
		{"no embedders", func() *conf.Vector {
			c := vectorTestConf(true, 4, "")
			c.Embedders = nil
			return c
		}()},
		{"default undeclared", func() *conf.Vector {
			c := vectorTestConf(true, 4, "")
			c.DefaultEmbedder = "other-space"
			return c
		}()},
		{"duplicate derived keys", func() *conf.Vector {
			c := vectorTestConf(true, 4, "")
			c.Embedders = append(c.Embedders, &conf.Vector_Embedding{Model: "bge-m3", Version: "v1", Dimension: 8})
			return c
		}()},
	}
	for _, tc := range cases {
		if _, _, err := NewVectorStore(tc.conf, &pgsqlc.Data{}); err == nil {
			t.Fatalf("NewVectorStore(%s) = nil error, want failure", tc.name)
		}
	}
}

func TestNewVectorStore_invalid_table_name_fails(t *testing.T) {
	// 表名是拼进 SQL 文本的配置值，注入形态必须在探针前拒绝
	//（本用例传 nil 池也能安全返回错误，证明拒绝发生在触库之前）。
	for _, name := range []string{
		"agent_document; DROP SCHEMA agent CASCADE",
		"AgentDocument",
		"1_document",
		"doc-name",
		"doc name",
		strings.Repeat("d", 64),
	} {
		if _, _, err := NewVectorStore(vectorTestConf(true, 4, name), nil); err == nil {
			t.Fatalf("NewVectorStore(table=%q) = nil error, want failure", name)
		}
	}
}

func TestNewVectorStore_valid_table_name_defaults_and_passes_pattern(t *testing.T) {
	// 合法表名应通过白名单与空间注册表校验，进入启动探针；本用例无
	// 数据库句柄，断言只到探针报"句柄未初始化"为止——错误文案不含
	// 白名单/空间字样，即前两道校验已放行。
	_, _, err := NewVectorStore(vectorTestConf(true, 4, "agent_document"), &pgsqlc.Data{})
	if err == nil {
		t.Fatal("NewVectorStore without a database = nil error, want probe failure")
	}
	if strings.Contains(err.Error(), "must match") || strings.Contains(err.Error(), "must declare at least one") {
		t.Fatalf("probe error %q passed through earlier validation stages", err)
	}
	if !strings.Contains(err.Error(), "initialized database handle") {
		t.Fatalf("probe error %q, want uninitialized-handle failure", err)
	}
}

func TestNewVectorStore_table_name_without_agent_prefix_fails(t *testing.T) {
	// agent_ 前缀强制：合法标识符但缺前缀同样在构造期
	// 拒绝，且发生在启动探针之前（本用例传空 Data 即可安全返回错误）。
	for _, name := range []string{"document", "vec_documents", "agentdocument"} {
		_, _, err := NewVectorStore(vectorTestConf(true, 4, name), &pgsqlc.Data{})
		if err == nil || !strings.Contains(err.Error(), "must have the agent_ prefix") {
			t.Fatalf("NewVectorStore(table=%q) = %v, want agent_ prefix rejection", name, err)
		}
	}
}

func TestVectorMetadata_overrides_forged_identity_and_keeps_custom_keys(t *testing.T) {
	auth := pgsqlc.Identity{TenantID: "t-a", UserID: 7}
	metadata := vectorMetadata(map[string]any{
		"tenant_id": "t-forged",
		"user_id":   999,
		"embedder":  "forged-space",
		"embed_dim": 9999,
		"category":  "policy",
	}, auth, "bge-m3@v1", 4)
	if metadata["tenant_id"] != "t-a" {
		t.Fatalf("metadata tenant_id = %v, want server identity t-a", metadata["tenant_id"])
	}
	if metadata["user_id"] != int64(7) {
		t.Fatalf("metadata user_id = %v (%T), want int64(7)", metadata["user_id"], metadata["user_id"])
	}
	// 嵌入空间身份同为服务端产出：伪造同名键无效。
	if metadata["embedder"] != "bge-m3@v1" {
		t.Fatalf("metadata embedder = %v, want server-declared space", metadata["embedder"])
	}
	if metadata["embed_dim"] != 4 {
		t.Fatalf("metadata embed_dim = %v, want 4", metadata["embed_dim"])
	}
	if metadata["category"] != "policy" {
		t.Fatalf("custom metadata key lost: %v", metadata["category"])
	}
}

func TestVectorIdentity_missing_or_invalid_rejected(t *testing.T) {
	if _, err := vectorIdentity(context.Background()); err == nil {
		t.Fatal("vectorIdentity(ctx without identity) = nil error, want rejection")
	}
	// 身份非法（空租户/非正用户）同样拒绝：fail-closed，不回落默认租户。
	for _, id := range []biz.Identity{{TenantID: "", UserID: 1}, {TenantID: "t-a", UserID: 0}, {TenantID: "t-a", UserID: -3}} {
		ctx := biz.WithIdentity(context.Background(), id)
		if _, err := vectorIdentity(ctx); err == nil {
			t.Fatalf("vectorIdentity(%+v) = nil error, want rejection", id)
		}
	}
}

func TestVectorStore_methods_reject_without_identity(t *testing.T) {
	// Data 为 nil 池：身份检查先于一切触库动作，未认证调用必须在此
	// 拒绝，绝不落到驱动层。
	store := vectorTestStore()
	store.data = &pgsqlc.Data{}
	ctx := context.Background()
	doc := &biz.VectorDocument{ID: "doc-1", Embedder: "bge-m3@v1", Dimension: 4, Content: "c", Embedding: make([]float32, 4)}
	searchFilter := &biz.VectorFilter{Embedder: "bge-m3@v1", Dimension: 4}

	if err := store.Add(ctx, doc); err == nil {
		t.Fatal("Add without identity = nil error, want rejection")
	}
	if _, err := store.Get(ctx, "doc-1"); err == nil {
		t.Fatal("Get without identity = nil error, want rejection")
	}
	if err := store.Update(ctx, doc); err == nil {
		t.Fatal("Update without identity = nil error, want rejection")
	}
	if err := store.Delete(ctx, "doc-1"); err == nil {
		t.Fatal("Delete without identity = nil error, want rejection")
	}
	if _, err := store.Search(ctx, make([]float32, 4), searchFilter); err == nil {
		t.Fatal("Search without identity = nil error, want rejection")
	}
	if _, err := store.DeleteByFilter(ctx, &biz.VectorFilter{IDs: []string{"doc-1"}}); err == nil {
		t.Fatal("DeleteByFilter without identity = nil error, want rejection")
	}
	if _, err := store.Count(ctx, nil); err == nil {
		t.Fatal("Count without identity = nil error, want rejection")
	}
	if _, err := store.GetMetadata(ctx, nil); err == nil {
		t.Fatal("GetMetadata without identity = nil error, want rejection")
	}
}

func TestVectorStore_search_requires_declared_space(t *testing.T) {
	// 检索空间声明三方对账（键已声明 / 维度等于注册表 / 长度等于维度），
	// 任一缺失在身份检查之后、触库之前拒绝。
	store := vectorTestStore()
	ctx := biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-a", UserID: 1})
	cases := []struct {
		name   string
		filter *biz.VectorFilter
		vector []float32
		want   string
	}{
		{"nil filter", nil, make([]float32, 4), "filter is required"},
		{"empty space", &biz.VectorFilter{Dimension: 4}, make([]float32, 4), "must declare an embedder space"},
		{"unknown space", &biz.VectorFilter{Embedder: "other@v9", Dimension: 4}, make([]float32, 4), "unknown embedder space"},
		{"missing dimension", &biz.VectorFilter{Embedder: "bge-m3@v1"}, make([]float32, 4), "positive dimension"},
		{"registry dimension mismatch", &biz.VectorFilter{Embedder: "bge-m3@v1", Dimension: 8}, make([]float32, 8), "registry declares 4"},
		{"length mismatch", &biz.VectorFilter{Embedder: "bge-m3@v1", Dimension: 4}, make([]float32, 3), "embedding length 3"},
	}
	for _, tc := range cases {
		if _, err := store.Search(ctx, tc.vector, tc.filter); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: Search = %v, want containing %q", tc.name, err, tc.want)
		}
	}
}

func TestVectorStore_check_document_errors(t *testing.T) {
	store := vectorTestStore()

	cases := []struct {
		name string
		doc  *biz.VectorDocument
		want string
	}{
		{"nil document", nil, "required"},
		{"empty id", &biz.VectorDocument{Embedder: "bge-m3@v1", Dimension: 4, Embedding: make([]float32, 4)}, "id is required"},
		{"blank id", &biz.VectorDocument{ID: "  ", Embedder: "bge-m3@v1", Dimension: 4, Embedding: make([]float32, 4)}, "id is required"},
		{"missing space", &biz.VectorDocument{ID: "doc-1", Dimension: 4, Embedding: make([]float32, 4)}, "must declare an embedder space"},
		{"unknown space", &biz.VectorDocument{ID: "doc-1", Embedder: "other@v9", Dimension: 4, Embedding: make([]float32, 4)}, "unknown embedder space"},
		{"missing dimension", &biz.VectorDocument{ID: "doc-1", Embedder: "bge-m3@v1", Embedding: make([]float32, 4)}, "positive dimension"},
		{"dimension mismatch", &biz.VectorDocument{ID: "doc-1", Embedder: "bge-m3@v1", Dimension: 8, Embedding: make([]float32, 8)}, "registry declares 4"},
		{"length mismatch", &biz.VectorDocument{ID: "doc-1", Embedder: "bge-m3@v1", Dimension: 4, Embedding: make([]float32, 3)}, "embedding length 3"},
	}
	for _, tc := range cases {
		if _, err := store.checkDocument(tc.doc); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: checkDocument = %v, want containing %q", tc.name, err, tc.want)
		}
	}
	// 合法声明返回注册表维度（落库 embed_dim 列的取值来源）。
	dim, err := store.checkDocument(&biz.VectorDocument{ID: "doc-1", Embedder: "bge-m3@v1", Dimension: 4, Embedding: make([]float32, 4)})
	if err != nil || dim != 4 {
		t.Fatalf("checkDocument(valid) = (%d, %v), want (4, nil)", dim, err)
	}
}

func TestPlanVectorFilter_parameterizes_values_never_interpolates(t *testing.T) {
	malicious := "x'; DROP TABLE agent_document; --"
	plan, err := planVectorFilter(2, &biz.VectorFilter{
		IDs:      []string{"doc-1", malicious},
		Embedder: malicious,
		Metadata: map[string]any{"category": malicious},
	})
	if err != nil {
		t.Fatalf("planVectorFilter = %v", err)
	}
	if len(plan.clauses) != 3 {
		t.Fatalf("clauses = %v, want id + embedder + metadata clauses", plan.clauses)
	}
	if plan.clauses[0] != "id = ANY($2)" {
		t.Fatalf("id clause = %q, want parameterized ANY($2)", plan.clauses[0])
	}
	if plan.clauses[1] != "embedder = $3" {
		t.Fatalf("embedder clause = %q, want parameterized equality $3", plan.clauses[1])
	}
	if plan.clauses[2] != "metadata @> $4::jsonb" {
		t.Fatalf("metadata clause = %q, want parameterized @> $4::jsonb", plan.clauses[2])
	}
	// 恶意值只能作为参数值出现，绝不进入 SQL 文本。
	joined := strings.Join(plan.clauses, " ")
	if strings.Contains(joined, malicious) {
		t.Fatalf("filter value leaked into SQL text: %q", joined)
	}
	if len(plan.args) != 3 {
		t.Fatalf("args = %v, want 3 parameter values", plan.args)
	}
}

func TestPlanVectorFilter_nil_filter_yields_no_clauses(t *testing.T) {
	plan, err := planVectorFilter(1, nil)
	if err != nil {
		t.Fatalf("planVectorFilter(nil) = %v", err)
	}
	if plan.where() != "" {
		t.Fatalf("where = %q, want empty", plan.where())
	}
}

func TestVectorSearchSQL_expressions_match_index_shape(t *testing.T) {
	// 检索 SQL 的 score/order 表达式必须与 DDL 表达式部分索引逐字一致
	//（查询侧 cast 与索引表达式匹配才命中部分索引），维度以内联常量
	// 进 cast（typmod 不可参数化）。
	if got, want := vectorScoreExpr(1024), "1.0 - ((embedding::vector(1024)) <=> $1::vector(1024)) / 2.0"; got != want {
		t.Fatalf("vectorScoreExpr = %q, want %q", got, want)
	}
	if got, want := vectorOrderExpr(4), "(embedding::vector(4)) <=> $1::vector(4)"; got != want {
		t.Fatalf("vectorOrderExpr = %q, want %q", got, want)
	}
}

func TestContainsAny_requires_both_fragments(t *testing.T) {
	// indexdef 形态取自 pg_get_indexdef 实际产出（(列)::vector(N) 显式
	// 括号 cast + WHERE (谓词)）。
	defs := []string{
		"CREATE INDEX ... USING hnsw (((embedding)::vector(4)) vector_cosine_ops) WITH (m='16') WHERE (embed_dim = 4)",
		"CREATE INDEX ... USING hnsw (((embedding)::vector(40)) vector_cosine_ops) WITH (m='16') WHERE (embed_dim = 40)",
	}
	if !containsAny(defs, "::vector(4)) vector_cosine_ops", "embed_dim = 4)") {
		t.Fatal("containsAny(defs, dim 4) = false, want true")
	}
	// 闭括号锚定数字边界：维度 4 不得被维度 40 的定义误配。
	if containsAny(defs, "::vector(4)) vector_cosine_ops", "embed_dim = 40)") {
		t.Fatal("containsAny matched dimension 40 fragments for dim-4 probe")
	}
	if containsAny(defs, "::vector(8)) vector_cosine_ops", "embed_dim = 8)") {
		t.Fatal("containsAny(dim 8) = true, want false (no such index)")
	}
}

func TestDisabledVectorStore_all_methods_rejected(t *testing.T) {
	store := &disabledVectorStore{}
	ctx := context.Background()
	if err := store.Add(ctx, &biz.VectorDocument{}); !errors.Is(err, biz.ErrVectorStoreDisabled) {
		t.Fatalf("Add = %v, want ErrVectorStoreDisabled", err)
	}
	if _, err := store.Get(ctx, "doc-1"); !errors.Is(err, biz.ErrVectorStoreDisabled) {
		t.Fatalf("Get = %v, want ErrVectorStoreDisabled", err)
	}
	if err := store.Update(ctx, &biz.VectorDocument{}); !errors.Is(err, biz.ErrVectorStoreDisabled) {
		t.Fatalf("Update = %v, want ErrVectorStoreDisabled", err)
	}
	if err := store.Delete(ctx, "doc-1"); !errors.Is(err, biz.ErrVectorStoreDisabled) {
		t.Fatalf("Delete = %v, want ErrVectorStoreDisabled", err)
	}
	if _, err := store.Search(ctx, []float32{1}, nil); !errors.Is(err, biz.ErrVectorStoreDisabled) {
		t.Fatalf("Search = %v, want ErrVectorStoreDisabled", err)
	}
	if _, err := store.DeleteByFilter(ctx, nil); !errors.Is(err, biz.ErrVectorStoreDisabled) {
		t.Fatalf("DeleteByFilter = %v, want ErrVectorStoreDisabled", err)
	}
	if _, err := store.Count(ctx, nil); !errors.Is(err, biz.ErrVectorStoreDisabled) {
		t.Fatalf("Count = %v, want ErrVectorStoreDisabled", err)
	}
	if _, err := store.GetMetadata(ctx, nil); !errors.Is(err, biz.ErrVectorStoreDisabled) {
		t.Fatalf("GetMetadata = %v, want ErrVectorStoreDisabled", err)
	}
}
