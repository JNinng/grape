package pg_sqlc

import (
	"context"
	"errors"
	"strings"
	"testing"

	"grape/internal/biz"
	"grape/internal/conf"
)

// 向量存储单测（纯内存，不触数据库）：配置校验与维度解析、身份注入与
// 缺失拒绝、过滤条件的参数化翻译、禁用占位实现。介质行为（RLS 关闭下
// 的租户隔离、检索命中）由 vector_test.go 的集成测试覆盖。

func vectorTestConf(enabled bool, dimension int32, table string) *conf.Vector {
	return &conf.Vector{Enabled: enabled, Dimension: dimension, TableName: table}
}

func TestNewVectorStore_disabled_conf_returns_disabled_store(t *testing.T) {
	store, cleanup, err := NewVectorStore(vectorTestConf(false, 0, ""), &Data{})
	if err != nil {
		t.Fatalf("NewVectorStore(disabled) = %v, want nil error", err)
	}
	defer cleanup()
	if _, err := store.Get(context.Background(), "doc-1"); !errors.Is(err, biz.ErrVectorStoreDisabled) {
		t.Fatalf("Get on disabled store = %v, want ErrVectorStoreDisabled", err)
	}
}

func TestNewVectorStore_invalid_dimension_fails(t *testing.T) {
	for _, dimension := range []int32{0, -1, vectorMaxDimension + 1} {
		if _, _, err := NewVectorStore(vectorTestConf(true, dimension, ""), &Data{}); err == nil {
			t.Fatalf("NewVectorStore(dimension=%d) = nil error, want failure", dimension)
		}
	}
}

func TestNewVectorStore_invalid_table_name_fails(t *testing.T) {
	// 表名是唯一拼进 SQL 文本的配置值，注入形态必须在探针前拒绝
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
	// 合法表名应通过白名单与维度校验，进入启动探针；本用例无数据库
	// 句柄，断言只到探针报"句柄未初始化"为止——错误文案不含白名单/
	// 维度字样，即前两道校验已放行。
	_, _, err := NewVectorStore(vectorTestConf(true, 4, "agent_document"), &Data{})
	if err == nil {
		t.Fatal("NewVectorStore without a database = nil error, want probe failure")
	}
	if strings.Contains(err.Error(), "must match") || strings.Contains(err.Error(), "dimension must be") {
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
		_, _, err := NewVectorStore(vectorTestConf(true, 4, name), &Data{})
		if err == nil || !strings.Contains(err.Error(), "must have the agent_ prefix") {
			t.Fatalf("NewVectorStore(table=%q) = %v, want agent_ prefix rejection", name, err)
		}
	}
}

func TestVectorDeclaredDimension_parses_format_type_output(t *testing.T) {
	cases := []struct {
		declared string
		want     int32
		wantErr  bool
	}{
		{"vector(1024)", 1024, false},
		{"vector(4)", 4, false},
		{"integer", 0, true},
		{"vector", 0, true},
		{"vector()", 0, true},
		{"vector(abc)", 0, true},
		{"vector(0)", 0, true},
		{"vector(-3)", 0, true},
	}
	for _, tc := range cases {
		got, err := vectorDeclaredDimension(tc.declared)
		if tc.wantErr != (err != nil) {
			t.Fatalf("vectorDeclaredDimension(%q) = (%d, %v), want err=%v", tc.declared, got, err, tc.wantErr)
		}
		if !tc.wantErr && got != tc.want {
			t.Fatalf("vectorDeclaredDimension(%q) = %d, want %d", tc.declared, got, tc.want)
		}
	}
}

func TestVectorMetadata_overrides_forged_identity_and_keeps_custom_keys(t *testing.T) {
	auth := Identity{TenantID: "t-a", UserID: 7}
	metadata := vectorMetadata(map[string]any{
		"tenant_id": "t-forged",
		"user_id":   999,
		"category":  "policy",
	}, auth)
	if metadata["tenant_id"] != "t-a" {
		t.Fatalf("metadata tenant_id = %v, want server identity t-a", metadata["tenant_id"])
	}
	if metadata["user_id"] != int64(7) {
		t.Fatalf("metadata user_id = %v (%T), want int64(7)", metadata["user_id"], metadata["user_id"])
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
	store := &vectorStore{data: &Data{}, table: vectorDefaultTable, dimension: 4}
	ctx := context.Background()
	doc := &biz.VectorDocument{ID: "doc-1", Content: "c", Embedding: make([]float32, 4)}

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
	if _, err := store.Search(ctx, make([]float32, 4), nil); err == nil {
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

func TestVectorStore_check_document_errors(t *testing.T) {
	store := &vectorStore{data: &Data{}, table: vectorDefaultTable, dimension: 4}

	cases := []struct {
		name string
		doc  *biz.VectorDocument
		want string
	}{
		{"nil document", nil, "required"},
		{"empty id", &biz.VectorDocument{Embedding: make([]float32, 4)}, "id is required"},
		{"blank id", &biz.VectorDocument{ID: "  ", Embedding: make([]float32, 4)}, "id is required"},
		{"empty embedding", &biz.VectorDocument{ID: "doc-1"}, "empty embedding"},
		{"dimension mismatch", &biz.VectorDocument{ID: "doc-1", Embedding: make([]float32, 3)}, "does not match"},
	}
	for _, tc := range cases {
		if err := store.checkDocument(tc.doc); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: checkDocument = %v, want containing %q", tc.name, err, tc.want)
		}
	}
}

func TestPlanVectorFilter_parameterizes_values_never_interpolates(t *testing.T) {
	malicious := "x'; DROP TABLE agent_document; --"
	plan, err := planVectorFilter(2, &biz.VectorFilter{
		IDs:      []string{"doc-1", malicious},
		Metadata: map[string]any{"category": malicious},
	})
	if err != nil {
		t.Fatalf("planVectorFilter = %v", err)
	}
	if len(plan.clauses) != 2 {
		t.Fatalf("clauses = %v, want id + metadata clauses", plan.clauses)
	}
	if plan.clauses[0] != "id = ANY($2)" {
		t.Fatalf("id clause = %q, want parameterized ANY($2)", plan.clauses[0])
	}
	if plan.clauses[1] != "metadata @> $3::jsonb" {
		t.Fatalf("metadata clause = %q, want parameterized @> $3::jsonb", plan.clauses[1])
	}
	// 恶意值只能作为参数值出现，绝不进入 SQL 文本。
	joined := strings.Join(plan.clauses, " ")
	if strings.Contains(joined, malicious) {
		t.Fatalf("filter value leaked into SQL text: %q", joined)
	}
	if len(plan.args) != 2 {
		t.Fatalf("args = %v, want 2 parameter values", plan.args)
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
