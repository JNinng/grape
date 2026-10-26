package pg_sqlc

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"grape/internal/biz"
	"grape/internal/conf"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// 向量存储介质集成测试：以真实 PostgreSQL 验证应用层租户隔离（决策 6）
// 与向量读写语义。测试自建专属库并重建（DROP+CREATE+按 DeployFiles 部署
// 清单全量装载，含 agent_vector.sql），不触碰其他数据库。设置以下环境
// 变量后运行，未设置时跳过：
//
//	GRAPE_VECTOR_TEST_ADMIN_DSN=postgres://postgresql:root@127.0.0.1:5432/postgres \
//	GRAPE_VECTOR_TEST_DSN=postgres://grape_u:grape_u_pwd@127.0.0.1:5432/grape_vector_d1_test \
//	go test ./internal/data/pg_sqlc -run "TestVectorStore|TestNewVectorStore" -count=1 -v
//
// 隔离口径：agent_document 不启用 RLS，租户边界由 vector_repo 的应用层
// 过滤强制——用例显式断言 RLS 关闭（relrowsecurity=false）时跨租户仍
// 不可召回，证明隔离不依赖数据库策略存在。

const (
	vectorTestDatabase = "grape_vector_d1_test"
	vectorDimension    = int32(1024)
	scoreEpsilon       = 1e-6
)

// vectorTestTenant/User 是用例身份：tenant_id 列宽 varchar(16)，取值
// 保持在列宽内。
var (
	vectorTenantA = biz.Identity{TenantID: "t-vec-a", UserID: 11}
	vectorTenantB = biz.Identity{TenantID: "t-vec-b", UserID: 22}
)

// unitVector 生成第 index 维为 1 的单位向量：正交基向量两两余弦距离
// 恰为 1，检索分数可精确断言（同向 1.0、正交 0.5）。
func unitVector(dim, index int) []float32 {
	vec := make([]float32, dim)
	vec[index] = 1
	return vec
}

func vectorIdentityContext(id biz.Identity) context.Context {
	return biz.WithIdentity(context.Background(), id)
}

// setupVectorTestDB 重建专属测试库、按序装载部署清单（含
// agent_vector.sql）并返回以 grape_u 连接的存储门面。
func setupVectorTestDB(t *testing.T) *Data {
	t.Helper()
	adminDSN := os.Getenv("GRAPE_VECTOR_TEST_ADMIN_DSN")
	testDSN := os.Getenv("GRAPE_VECTOR_TEST_DSN")
	if adminDSN == "" || testDSN == "" {
		t.Skip("GRAPE_VECTOR_TEST_ADMIN_DSN / GRAPE_VECTOR_TEST_DSN 未设置，跳过向量存储集成测试")
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
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+vectorTestDatabase+" WITH (FORCE)"); err != nil {
		t.Fatalf("drop test database: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+vectorTestDatabase); err != nil {
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
	deployCfg.Database = vectorTestDatabase
	deployCfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	deployConn, err := pgx.ConnectConfig(ctx, deployCfg)
	if err != nil {
		t.Fatalf("connect deploy: %v", err)
	}
	defer deployConn.Close(ctx)
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
	if _, err := deployConn.Exec(ctx, "GRANT CONNECT ON DATABASE "+vectorTestDatabase+" TO grape_u, test_u"); err != nil {
		t.Fatalf("grant connect: %v", err)
	}

	pool, err := pgxpool.New(ctx, testDSN)
	if err != nil {
		t.Fatalf("connect grape_u: %v", err)
	}
	t.Cleanup(pool.Close)
	return &Data{db: pool}
}

// containsAll 报告 s 是否包含 subs 中全部子串。
func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

// newVectorStoreForTest 以与部署一致的配置构造向量存储（探针应通过）。
func newVectorStoreForTest(t *testing.T, d *Data) biz.VectorStore {
	t.Helper()
	store, cleanup, err := NewVectorStore(&conf.Vector{
		Enabled:   true,
		Dimension: vectorDimension,
	}, d)
	if err != nil {
		t.Fatalf("NewVectorStore = %v, want probe pass", err)
	}
	t.Cleanup(cleanup)
	return store
}

// addVectorDocument 是 Add 的用例包装：失败即终止测试。
func addVectorDocument(t *testing.T, store biz.VectorStore, id biz.Identity, doc *biz.VectorDocument) {
	t.Helper()
	if err := store.Add(vectorIdentityContext(id), doc); err != nil {
		t.Fatalf("add document %s: %v", doc.ID, err)
	}
}

func TestVectorStore_add_then_search_returns_topk_with_scores(t *testing.T) {
	d := setupVectorTestDB(t)
	store := newVectorStoreForTest(t, d)
	ctx := vectorIdentityContext(vectorTenantA)

	docs := []struct {
		id    string
		vec   []float32
		score float64 // 以正交基向量 e0 检索时的理论分数
	}{
		{"doc-e0", unitVector(int(vectorDimension), 0), 1.0},
		{"doc-e1", unitVector(int(vectorDimension), 1), 0.5},
		{"doc-e2", unitVector(int(vectorDimension), 2), 0.5},
	}
	for _, doc := range docs {
		addVectorDocument(t, store, vectorTenantA, &biz.VectorDocument{
			ID:        doc.id,
			Name:      doc.id + "-name",
			Content:   "content of " + doc.id,
			Embedding: doc.vec,
			Metadata:  map[string]any{"category": doc.id},
		})
	}

	hits, err := store.Search(ctx, unitVector(int(vectorDimension), 0), &biz.VectorFilter{Limit: 2})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits = %d, want 2", len(hits))
	}
	if hits[0].Document.ID != "doc-e0" {
		t.Fatalf("top hit = %s, want doc-e0", hits[0].Document.ID)
	}
	if math.Abs(hits[0].Score-1.0) > scoreEpsilon {
		t.Fatalf("top score = %f, want 1.0", hits[0].Score)
	}
	if hits[1].Document.ID == "doc-e0" {
		t.Fatalf("second hit duplicated top: %+v", hits[1])
	}
	if math.Abs(hits[1].Score-0.5) > scoreEpsilon {
		t.Fatalf("second score = %f, want 0.5", hits[1].Score)
	}
	// 命中文档携带 metadata（含仓储注入的租户身份）与原文。
	if hits[0].Document.Metadata["tenant_id"] != vectorTenantA.TenantID {
		t.Fatalf("metadata tenant_id = %v, want %s", hits[0].Document.Metadata["tenant_id"], vectorTenantA.TenantID)
	}
	if hits[0].Document.Content != "content of doc-e0" {
		t.Fatalf("content = %q, want original", hits[0].Document.Content)
	}
}

func TestVectorStore_cross_tenant_search_returns_empty_while_rls_disabled(t *testing.T) {
	d := setupVectorTestDB(t)
	store := newVectorStoreForTest(t, d)

	// 固定场景前提：表无 RLS——本用例证明隔离来自应用层过滤而非数据库
	// 策略（RLS 可作为可选加强，见 agent_vector.sql 注释）。
	var rowSecurity bool
	err := d.db.QueryRow(context.Background(),
		`SELECT relrowsecurity FROM pg_class WHERE oid = 'agent.agent_document'::regclass`).Scan(&rowSecurity)
	if err != nil {
		t.Fatalf("read relrowsecurity: %v", err)
	}
	if rowSecurity {
		t.Fatal("agent.agent_document has RLS enabled; the no-RLS isolation scenario no longer holds")
	}

	addVectorDocument(t, store, vectorTenantA, &biz.VectorDocument{
		ID:        "doc-a-1",
		Content:   "tenant a secret",
		Embedding: unitVector(int(vectorDimension), 0),
	})

	// 租户 B 同库同表：检索、点查、计数、枚举全部不可见（无行可召回）。
	ctxB := vectorIdentityContext(vectorTenantB)
	hits, err := store.Search(ctxB, unitVector(int(vectorDimension), 0), &biz.VectorFilter{Limit: 10})
	if err != nil {
		t.Fatalf("search as tenant b: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("cross-tenant hits = %d, want 0 (isolation must hold without RLS)", len(hits))
	}
	if _, err := store.Get(ctxB, "doc-a-1"); !errors.Is(err, biz.ErrVectorDocNotFound) {
		t.Fatalf("cross-tenant Get = %v, want ErrVectorDocNotFound", err)
	}
	total, err := store.Count(ctxB, nil)
	if err != nil || total != 0 {
		t.Fatalf("cross-tenant Count = (%d, %v), want (0, nil)", total, err)
	}
	metadata, err := store.GetMetadata(ctxB, nil)
	if err != nil || len(metadata) != 0 {
		t.Fatalf("cross-tenant GetMetadata = (%d rows, %v), want empty", len(metadata), err)
	}
	// 租户 B 也不能借 Delete/DeleteByFilter 触达租户 A 的行。
	if err := store.Delete(ctxB, "doc-a-1"); !errors.Is(err, biz.ErrVectorDocNotFound) {
		t.Fatalf("cross-tenant Delete = %v, want ErrVectorDocNotFound", err)
	}
	deleted, err := store.DeleteByFilter(ctxB, &biz.VectorFilter{Metadata: map[string]any{"category": "x"}})
	if err != nil || deleted != 0 {
		t.Fatalf("cross-tenant DeleteByFilter = (%d, %v), want (0, nil)", deleted, err)
	}
	// 租户 A 自身不受影响。
	if _, err := store.Get(vectorIdentityContext(vectorTenantA), "doc-a-1"); err != nil {
		t.Fatalf("owner Get after foreign attempts = %v, want nil", err)
	}
}

func TestVectorStore_unauthenticated_calls_fail_closed(t *testing.T) {
	d := setupVectorTestDB(t)
	store := newVectorStoreForTest(t, d)
	// 无认证 ctx：每个方法都必须拒绝，绝不以任何默认身份触库。
	ctx := context.Background()
	doc := &biz.VectorDocument{ID: "doc-x", Content: "c", Embedding: unitVector(int(vectorDimension), 0)}

	if err := store.Add(ctx, doc); err == nil {
		t.Fatal("Add without identity = nil error, want rejection")
	}
	if _, err := store.Get(ctx, "doc-x"); err == nil {
		t.Fatal("Get without identity = nil error, want rejection")
	}
	if err := store.Update(ctx, doc); err == nil {
		t.Fatal("Update without identity = nil error, want rejection")
	}
	if err := store.Delete(ctx, "doc-x"); err == nil {
		t.Fatal("Delete without identity = nil error, want rejection")
	}
	if _, err := store.Search(ctx, unitVector(int(vectorDimension), 0), nil); err == nil {
		t.Fatal("Search without identity = nil error, want rejection")
	}
	if _, err := store.DeleteByFilter(ctx, &biz.VectorFilter{IDs: []string{"doc-x"}}); err == nil {
		t.Fatal("DeleteByFilter without identity = nil error, want rejection")
	}
	if _, err := store.Count(ctx, nil); err == nil {
		t.Fatal("Count without identity = nil error, want rejection")
	}
	if _, err := store.GetMetadata(ctx, nil); err == nil {
		t.Fatal("GetMetadata without identity = nil error, want rejection")
	}
	// 伪造身份元数据不得越过身份提取：metadata 里的 tenant_id 以服务端
	// 身份为准覆盖，调用方无法声明他人租户。
	forged := &biz.VectorDocument{
		ID:        "doc-forged",
		Content:   "c",
		Embedding: unitVector(int(vectorDimension), 0),
		Metadata:  map[string]any{"tenant_id": vectorTenantB.TenantID},
	}
	addVectorDocument(t, store, vectorTenantA, forged)
	got, err := store.Get(vectorIdentityContext(vectorTenantA), "doc-forged")
	if err != nil {
		t.Fatalf("Get forged doc: %v", err)
	}
	if got.Metadata["tenant_id"] != vectorTenantA.TenantID {
		t.Fatalf("forged tenant_id persisted: %v, want %s", got.Metadata["tenant_id"], vectorTenantA.TenantID)
	}
	if _, err := store.Get(vectorIdentityContext(vectorTenantB), "doc-forged"); !errors.Is(err, biz.ErrVectorDocNotFound) {
		t.Fatal("forged metadata must not move the row into another tenant")
	}
}

func TestVectorStore_update_delete_take_effect_on_rows(t *testing.T) {
	d := setupVectorTestDB(t)
	store := newVectorStoreForTest(t, d)
	ctx := vectorIdentityContext(vectorTenantA)

	addVectorDocument(t, store, vectorTenantA, &biz.VectorDocument{
		ID:        "doc-u",
		Name:      "before",
		Content:   "before content",
		Embedding: unitVector(int(vectorDimension), 0),
		Metadata:  map[string]any{"category": "before"},
	})

	if err := store.Update(ctx, &biz.VectorDocument{
		ID:        "doc-u",
		Name:      "after",
		Content:   "after content",
		Embedding: unitVector(int(vectorDimension), 1),
		Metadata:  map[string]any{"category": "after"},
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := store.Get(ctx, "doc-u")
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got.Name != "after" || got.Content != "after content" || got.Metadata["category"] != "after" {
		t.Fatalf("update not applied: %+v", got)
	}
	// 向量已换：按旧向量检索不再给满分，按新向量检索同向命中。
	hits, err := store.Search(ctx, unitVector(int(vectorDimension), 1), &biz.VectorFilter{Limit: 1})
	if err != nil || len(hits) != 1 || hits[0].Document.ID != "doc-u" {
		t.Fatalf("search after update = (%v, %v), want doc-u top1", hits, err)
	}
	if math.Abs(hits[0].Score-1.0) > scoreEpsilon {
		t.Fatalf("score after update = %f, want 1.0", hits[0].Score)
	}

	if err := store.Update(ctx, &biz.VectorDocument{ID: "missing", Content: "x", Embedding: unitVector(int(vectorDimension), 0)}); !errors.Is(err, biz.ErrVectorDocNotFound) {
		t.Fatalf("update missing = %v, want ErrVectorDocNotFound", err)
	}

	if err := store.Delete(ctx, "doc-u"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.Get(ctx, "doc-u"); !errors.Is(err, biz.ErrVectorDocNotFound) {
		t.Fatalf("get after delete = %v, want ErrVectorDocNotFound", err)
	}
	if err := store.Delete(ctx, "doc-u"); !errors.Is(err, biz.ErrVectorDocNotFound) {
		t.Fatalf("delete twice = %v, want ErrVectorDocNotFound", err)
	}
}

func TestVectorStore_filters_count_delete_and_metadata_paging(t *testing.T) {
	d := setupVectorTestDB(t)
	store := newVectorStoreForTest(t, d)
	ctx := vectorIdentityContext(vectorTenantA)

	for i := 0; i < 5; i++ {
		category := "keep"
		if i%2 == 0 {
			category = "drop"
		}
		addVectorDocument(t, store, vectorTenantA, &biz.VectorDocument{
			ID:        fmt.Sprintf("doc-%02d", i),
			Content:   "content",
			Embedding: unitVector(int(vectorDimension), i),
			Metadata:  map[string]any{"category": category, "seq": i},
		})
	}

	total, err := store.Count(ctx, nil)
	if err != nil || total != 5 {
		t.Fatalf("Count(all) = (%d, %v), want (5, nil)", total, err)
	}
	kept, err := store.Count(ctx, &biz.VectorFilter{Metadata: map[string]any{"category": "keep"}})
	if err != nil || kept != 2 {
		t.Fatalf("Count(keep) = (%d, %v), want (2, nil)", kept, err)
	}
	byIDs, err := store.Count(ctx, &biz.VectorFilter{IDs: []string{"doc-00", "doc-01", "missing"}})
	if err != nil || byIDs != 2 {
		t.Fatalf("Count(ids) = (%d, %v), want (2, nil)", byIDs, err)
	}

	// DeleteByFilter 只删交集；空条件拒绝（租户级全量清理归 super 口径）。
	deleted, err := store.DeleteByFilter(ctx, &biz.VectorFilter{Metadata: map[string]any{"category": "drop"}})
	if err != nil || deleted != 3 {
		t.Fatalf("DeleteByFilter(drop) = (%d, %v), want (3, nil)", deleted, err)
	}
	if _, err := store.DeleteByFilter(ctx, nil); err == nil {
		t.Fatal("DeleteByFilter(no conditions) = nil error, want rejection")
	}
	if remaining, err := store.Count(ctx, nil); err != nil || remaining != 2 {
		t.Fatalf("Count(after purge) = (%d, %v), want (2, nil)", remaining, err)
	}

	// GetMetadata 分页：升序 + limit/offset；向量与正文不随行。
	metadata, err := store.GetMetadata(ctx, &biz.VectorFilter{Limit: 1, Offset: 1})
	if err != nil {
		t.Fatalf("GetMetadata(page): %v", err)
	}
	if len(metadata) != 1 {
		t.Fatalf("GetMetadata rows = %d, want 1", len(metadata))
	}
	for id, m := range metadata {
		if id != "doc-01" && id != "doc-03" {
			t.Fatalf("unexpected page row %q", id)
		}
		if _, hasContent := m["tenant_id"]; !hasContent {
			t.Fatalf("metadata row %q lacks injected tenant_id: %+v", id, m)
		}
	}
	if _, err := store.GetMetadata(ctx, &biz.VectorFilter{Offset: 2}); err == nil {
		t.Fatal("GetMetadata(offset without limit) = nil error, want rejection")
	}
}

func TestNewVectorStore_startup_probe_rejects_dimension_mismatch(t *testing.T) {
	d := setupVectorTestDB(t)

	// 维度与部署列不一致：启动即失败（验收项）。
	_, _, err := NewVectorStore(&conf.Vector{Enabled: true, Dimension: 999}, d)
	if err == nil || !containsAll(err.Error(), "dimension mismatch", "999") {
		t.Fatalf("NewVectorStore(dimension=999) = %v, want dimension mismatch failure", err)
	}
	// 表未部署（配置表名与 DDL 不符）：同样启动失败并指明处置。
	_, _, err = NewVectorStore(&conf.Vector{Enabled: true, Dimension: vectorDimension, TableName: "agent_document_missing"}, d)
	if err == nil || !containsAll(err.Error(), "not deployed") {
		t.Fatalf("NewVectorStore(missing table) = %v, want not-deployed failure", err)
	}
	// 一致配置：探针通过，设施可用。
	store := newVectorStoreForTest(t, d)
	if err := store.Add(vectorIdentityContext(vectorTenantA), &biz.VectorDocument{
		ID:        "doc-probe",
		Content:   "c",
		Embedding: unitVector(int(vectorDimension), 0),
	}); err != nil {
		t.Fatalf("Add after probe: %v", err)
	}
}
