package vector

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"grape/internal/biz"
	"grape/internal/conf"
	pgsqlc "grape/internal/data/pg_sqlc"
	"grape/internal/data/pg_sqlc/deploy"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pgvector/pgvector-go"
)

// 向量存储介质集成测试：以真实 PostgreSQL 验证应用层租户隔离、嵌入空间
// 隔离与向量读写语义。测试自建专属库并重建（DROP+CREATE+按 deploy.DeployFiles
// 部署清单全量装载，含 agent_vector.sql），不触碰其他数据库。设置以下环境
// 变量后运行，未设置时跳过：
//
//	GRAPE_VECTOR_TEST_ADMIN_DSN=postgres://postgresql:root@127.0.0.1:5432/postgres \
//	GRAPE_VECTOR_TEST_DSN=postgres://grape_u:grape_u_pwd@127.0.0.1:5432/grape_vector_test \
//	go test ./internal/data/pg_sqlc/vector -run "TestVectorStore|TestNewVectorStore" -count=1 -v
//
// 隔离口径：agent_document 不启用 RLS，租户边界由 vector_repo 的应用层
// 过滤强制——用例显式断言 RLS 关闭（relrowsecurity=false）时跨租户仍
// 不可召回，证明隔离不依赖数据库策略存在。空间隔离同表验证：同维度
// 双空间共存，检索只落声明空间。

const (
	vectorTestDatabase = "grape_vector_test"
	vectorDimension    = int32(1024)
	scoreEpsilon       = 1e-6

	// vectorTestSpace / vectorTestSpaceV2 是用例的嵌入空间键：同维度
	// 双空间（版本迭代形态，v2 与 v1 键不同、维度相同），V2 供空间隔离
	// 用例使用。
	vectorTestSpace   = "bge-m3@v1"
	vectorTestSpaceV2 = "bge-m3@v2"

	// testGrapeUPassword / testTestUPassword 与 role.sql 占位符替换值：两个
	// 运行角色的既定测试口令（与根包关系面套件同值）。
	testGrapeUPassword = "grape_u_pwd"
	testTestUPassword  = "test_u_pwd"
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

// spaceDoc 声明文档的嵌入空间（键 + 维度成对）：用例内统一走本包装，
// 与契约的「身份与维度必填」一致。
func spaceDoc(doc *biz.VectorDocument) *biz.VectorDocument {
	doc.Embedder = vectorTestSpace
	doc.Dimension = int(vectorDimension)
	return doc
}

// spaceFilter 是指向 vectorTestSpace 的检索过滤（Search 必填项）；Limit
// 由用例按断言需要设置（0 = 存储默认页大小）。
func spaceFilter() *biz.VectorFilter {
	return &biz.VectorFilter{Embedder: vectorTestSpace, Dimension: int(vectorDimension)}
}

func vectorIdentityContext(id biz.Identity) context.Context {
	return biz.WithIdentity(context.Background(), id)
}

// setupVectorTestDB 重建专属测试库、按序装载部署清单（含
// agent_vector.sql）并返回以 grape_u 连接的存储门面。
func setupVectorTestDB(t *testing.T) *pgsqlc.Data {
	t.Helper()
	adminDSN := os.Getenv("GRAPE_VECTOR_TEST_ADMIN_DSN")
	testDSN := os.Getenv("GRAPE_VECTOR_TEST_DSN")
	if adminDSN == "" || testDSN == "" {
		t.Skip("GRAPE_VECTOR_TEST_ADMIN_DSN / GRAPE_VECTOR_TEST_DSN not set; skipping vector store integration tests")
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
	deployDir, err := deploy.DeployDir()
	if err != nil {
		t.Fatalf("locate deploy dir: %v", err)
	}
	if err := deploy.ValidateDeployFiles(deployDir); err != nil {
		t.Fatalf("validate deploy files: %v", err)
	}
	deployCfg.Database = vectorTestDatabase
	deployCfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	deployConn, err := pgx.ConnectConfig(ctx, deployCfg)
	if err != nil {
		t.Fatalf("connect deploy: %v", err)
	}
	defer deployConn.Close(ctx)
	// role.sql 的角色目录操作是集群级的，跨测试包并发部署必须互斥。
	release, err := deploy.AcquireDeployLock(ctx)
	if err != nil {
		t.Fatalf("acquire deploy lock: %v", err)
	}
	defer release()
	for _, f := range deploy.DeployFiles {
		sql, err := os.ReadFile(filepath.Join(deployDir, f))
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		deployed := deploy.ResolveDeployPasswords(string(sql), testGrapeUPassword, testTestUPassword)
		if _, err := deployConn.Exec(ctx, deployed); err != nil {
			t.Fatalf("deploy %s: %v", f, err)
		}
	}
	if _, err := deployConn.Exec(ctx, "GRANT CONNECT ON DATABASE "+vectorTestDatabase+" TO grape_u, test_u"); err != nil {
		t.Fatalf("grant connect: %v", err)
	}

	d, cleanup, err := pgsqlc.NewData(&conf.Data{Database: &conf.Data_Database{
		Master: &conf.Data_Database_Slot{Source: testDSN},
		System: &conf.Data_Database_Slot{Source: testDSN},
	}})
	if err != nil {
		t.Fatalf("connect grape_u: %v", err)
	}
	t.Cleanup(cleanup)
	return d
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

// vectorStoreConf 构造与部署一致的配置：单空间（bge-m3@v1，1024 维）。
func vectorStoreConf() *conf.Vector {
	return &conf.Vector{
		Enabled:         true,
		DefaultEmbedder: vectorTestSpace,
		Embedders: []*conf.Vector_Embedding{
			{Model: "bge-m3", Version: "v1", Dimension: vectorDimension},
		},
	}
}

// newVectorStoreForTest 以与部署一致的配置构造向量存储（探针应通过）。
func newVectorStoreForTest(t *testing.T, d *pgsqlc.Data) biz.VectorStore {
	t.Helper()
	store, cleanup, err := NewVectorStore(vectorStoreConf(), d)
	if err != nil {
		t.Fatalf("NewVectorStore = %v, want probe pass", err)
	}
	t.Cleanup(cleanup)
	return store
}

// newMultiSpaceStoreForTest 构造双空间（同维度 1024，v1/v2 键不同）的
// 向量存储：空间隔离与跨空间清理用例使用。
func newMultiSpaceStoreForTest(t *testing.T, d *pgsqlc.Data) biz.VectorStore {
	t.Helper()
	c := vectorStoreConf()
	c.Embedders = append(c.Embedders, &conf.Vector_Embedding{Model: "bge-m3", Version: "v2", Dimension: vectorDimension})
	store, cleanup, err := NewVectorStore(c, d)
	if err != nil {
		t.Fatalf("NewVectorStore(multi-space) = %v, want probe pass", err)
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
		addVectorDocument(t, store, vectorTenantA, spaceDoc(&biz.VectorDocument{
			ID:        doc.id,
			Name:      doc.id + "-name",
			Content:   "content of " + doc.id,
			Embedding: doc.vec,
			Metadata:  map[string]any{"category": doc.id},
		}))
	}

	filter := spaceFilter()
	filter.Limit = 2
	hits, err := store.Search(ctx, unitVector(int(vectorDimension), 0), filter)
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
	// 命中文档携带 metadata（含仓储注入的租户身份）、原文与空间身份。
	if hits[0].Document.Metadata["tenant_id"] != vectorTenantA.TenantID {
		t.Fatalf("metadata tenant_id = %v, want %s", hits[0].Document.Metadata["tenant_id"], vectorTenantA.TenantID)
	}
	if hits[0].Document.Embedder != vectorTestSpace || hits[0].Document.Dimension != int(vectorDimension) {
		t.Fatalf("hit space = %s/%d, want %s/%d",
			hits[0].Document.Embedder, hits[0].Document.Dimension, vectorTestSpace, vectorDimension)
	}
	if hits[0].Document.Metadata["embedder"] != vectorTestSpace {
		t.Fatalf("metadata embedder = %v, want %s", hits[0].Document.Metadata["embedder"], vectorTestSpace)
	}
	if hits[0].Document.Content != "content of doc-e0" {
		t.Fatalf("content = %q, want original", hits[0].Document.Content)
	}
}

func TestVectorStore_search_only_hits_declared_space(t *testing.T) {
	// 空间隔离：同租户同表双空间（v1/v2 同维度），检索只召回声明空间
	// 的行；跨空间相似度语义不成立，混排不存在。
	d := setupVectorTestDB(t)
	store := newMultiSpaceStoreForTest(t, d)
	ctx := vectorIdentityContext(vectorTenantA)

	addVectorDocument(t, store, vectorTenantA, spaceDoc(&biz.VectorDocument{
		ID: "doc-v1", Content: "in v1", Embedding: unitVector(int(vectorDimension), 0),
	}))
	v2Doc := &biz.VectorDocument{
		ID: "doc-v2", Content: "in v2", Embedding: unitVector(int(vectorDimension), 0),
	}
	v2Doc.Embedder, v2Doc.Dimension = vectorTestSpaceV2, int(vectorDimension)
	addVectorDocument(t, store, vectorTenantA, v2Doc)

	// v1 空间检索只命中 v1 行，v2 空间检索只命中 v2 行。
	hitsV1, err := store.Search(ctx, unitVector(int(vectorDimension), 0), spaceFilter())
	if err != nil {
		t.Fatalf("search v1: %v", err)
	}
	if len(hitsV1) != 1 || hitsV1[0].Document.ID != "doc-v1" || hitsV1[0].Document.Embedder != vectorTestSpace {
		t.Fatalf("v1 hits = %v, want exactly doc-v1", searchIDs(hitsV1))
	}
	v2Filter := &biz.VectorFilter{Embedder: vectorTestSpaceV2, Dimension: int(vectorDimension)}
	hitsV2, err := store.Search(ctx, unitVector(int(vectorDimension), 0), v2Filter)
	if err != nil {
		t.Fatalf("search v2: %v", err)
	}
	if len(hitsV2) != 1 || hitsV2[0].Document.ID != "doc-v2" || hitsV2[0].Document.Embedder != vectorTestSpaceV2 {
		t.Fatalf("v2 hits = %v, want exactly doc-v2", searchIDs(hitsV2))
	}

	// 空间限定的计数与清理：Embedder 过滤收敛到单空间；不带 Embedder
	// 的 DeleteByFilter 跨空间（重摄取迁移的覆盖清除路径）。
	v1Only, err := store.Count(ctx, &biz.VectorFilter{Embedder: vectorTestSpace})
	if err != nil || v1Only != 1 {
		t.Fatalf("Count(v1) = (%d, %v), want (1, nil)", v1Only, err)
	}
	all, err := store.Count(ctx, nil)
	if err != nil || all != 2 {
		t.Fatalf("Count(cross-space) = (%d, %v), want (2, nil)", all, err)
	}
	deleted, err := store.DeleteByFilter(ctx, &biz.VectorFilter{Metadata: map[string]any{"tenant_id": vectorTenantA.TenantID}})
	if err != nil {
		t.Fatalf("cross-space delete: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("cross-space delete = %d, want 2 (v1 + v2)", deleted)
	}
	if remaining, err := store.Count(ctx, nil); err != nil || remaining != 0 {
		t.Fatalf("Count(after purge) = (%d, %v), want (0, nil)", remaining, err)
	}
}

func TestVectorStore_search_rejects_undeclared_space(t *testing.T) {
	// 未声明空间（conf 之外）检索：调用点显式拒绝，不静默换空间。
	d := setupVectorTestDB(t)
	store := newVectorStoreForTest(t, d)
	ctx := vectorIdentityContext(vectorTenantA)
	undeclared := &biz.VectorFilter{Embedder: "ghost-model@v1", Dimension: int(vectorDimension)}
	if _, err := store.Search(ctx, unitVector(int(vectorDimension), 0), undeclared); err == nil ||
		!errors.Is(err, biz.ErrEmbedderUnknown) {
		t.Fatalf("Search(undeclared space) = %v, want ErrEmbedderUnknown", err)
	}
}

func TestVectorStore_check_constraint_binds_dimension_column(t *testing.T) {
	// DB 层兜底：绕过应用侧对账直插维度不符的行必须被 CHECK 拒绝
	//（应用侧注册表对账在先，CHECK 是第二道防线）。
	d := setupVectorTestDB(t)
	newVectorStoreForTest(t, d)
	ctx := context.Background()
	var constraint string
	err := d.Pool().QueryRow(ctx,
		`SELECT conname FROM pg_constraint WHERE conrelid = 'agent.agent_document'::regclass AND contype = 'c'`).Scan(&constraint)
	if err != nil {
		t.Fatalf("read check constraint: %v", err)
	}
	_, err = d.Pool().Exec(ctx, `INSERT INTO agent.agent_document
(id, tenant_id, user_id, embedder, embed_dim, content, embedding, created_by, updated_by)
VALUES ('bad-dim', 't-vec-a', 11, 'bge-m3@v1', 4, 'c', $1, 11, 11)`,
		"["+strings.TrimSuffix(strings.Repeat("0.1,", 1024), ",")+"]")
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("insert violating dim check = %v, want check_violation (23514)", err)
	}
}

func TestVectorStore_search_plan_uses_expression_partial_index(t *testing.T) {
	// 检索计划断言：与仓储同形状的 SQL 必须命中表达式部分 HNSW 索引
	//（查询侧 cast 与索引表达式逐字一致才可命中）。
	d := setupVectorTestDB(t)
	newVectorStoreForTest(t, d)
	ctx := context.Background()
	planSQL := fmt.Sprintf(`EXPLAIN (COSTS OFF) SELECT id FROM agent.agent_document
WHERE tenant_id = $2 AND embedder = $3 AND embed_dim = %d
ORDER BY (embedding::vector(%d)) <=> $1::vector(%d) LIMIT 5`, vectorDimension, vectorDimension, vectorDimension)
	rows, err := d.Pool().Query(ctx, planSQL, pgvector.NewVector(make([]float32, vectorDimension)), "t-vec-a", vectorTestSpace)
	if err != nil {
		t.Fatalf("explain search: %v", err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan plan line: %v", err)
		}
		plan.WriteString(line)
		plan.WriteString("\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("explain rows: %v", err)
	}
	if !strings.Contains(plan.String(), "agent_document_embedding_1024_hnsw") {
		t.Fatalf("search plan does not use the expression partial hnsw index:\n%s", plan.String())
	}
}

func TestVectorStore_cross_tenant_search_returns_empty_while_rls_disabled(t *testing.T) {
	d := setupVectorTestDB(t)
	store := newVectorStoreForTest(t, d)

	// 固定场景前提：表无 RLS——本用例证明隔离来自应用层过滤而非数据库
	// 策略（RLS 可作为可选加强，见 agent_vector.sql 注释）。
	var rowSecurity bool
	err := d.Pool().QueryRow(context.Background(),
		`SELECT relrowsecurity FROM pg_class WHERE oid = 'agent.agent_document'::regclass`).Scan(&rowSecurity)
	if err != nil {
		t.Fatalf("read relrowsecurity: %v", err)
	}
	if rowSecurity {
		t.Fatal("agent.agent_document has RLS enabled; the no-RLS isolation scenario no longer holds")
	}

	addVectorDocument(t, store, vectorTenantA, spaceDoc(&biz.VectorDocument{
		ID:        "doc-a-1",
		Content:   "tenant a secret",
		Embedding: unitVector(int(vectorDimension), 0),
	}))

	// 租户 B 同库同表：检索、点查、计数、枚举全部不可见（无行可召回）。
	ctxB := vectorIdentityContext(vectorTenantB)
	bFilter := spaceFilter()
	bFilter.Limit = 10
	hits, err := store.Search(ctxB, unitVector(int(vectorDimension), 0), bFilter)
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
	forged := spaceDoc(&biz.VectorDocument{
		ID:        "doc-forged",
		Content:   "c",
		Embedding: unitVector(int(vectorDimension), 0),
		Metadata:  map[string]any{"tenant_id": vectorTenantB.TenantID},
	})
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

	addVectorDocument(t, store, vectorTenantA, spaceDoc(&biz.VectorDocument{
		ID:        "doc-u",
		Name:      "before",
		Content:   "before content",
		Embedding: unitVector(int(vectorDimension), 0),
		Metadata:  map[string]any{"category": "before"},
	}))

	if err := store.Update(ctx, spaceDoc(&biz.VectorDocument{
		ID:        "doc-u",
		Name:      "after",
		Content:   "after content",
		Embedding: unitVector(int(vectorDimension), 1),
		Metadata:  map[string]any{"category": "after"},
	})); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := store.Get(ctx, "doc-u")
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got.Name != "after" || got.Content != "after content" || got.Metadata["category"] != "after" {
		t.Fatalf("update not applied: %+v", got)
	}
	// 空间身份回填：读侧行携带声明的空间键与维度。
	if got.Embedder != vectorTestSpace || got.Dimension != int(vectorDimension) {
		t.Fatalf("row space = %s/%d, want %s/%d", got.Embedder, got.Dimension, vectorTestSpace, vectorDimension)
	}
	// 向量已换：按旧向量检索不再给满分，按新向量检索同向命中。
	uFilter := spaceFilter()
	uFilter.Limit = 1
	hits, err := store.Search(ctx, unitVector(int(vectorDimension), 1), uFilter)
	if err != nil || len(hits) != 1 || hits[0].Document.ID != "doc-u" {
		t.Fatalf("search after update = (%v, %v), want doc-u top1", hits, err)
	}
	if math.Abs(hits[0].Score-1.0) > scoreEpsilon {
		t.Fatalf("score after update = %f, want 1.0", hits[0].Score)
	}

	if err := store.Update(ctx, spaceDoc(&biz.VectorDocument{ID: "missing", Content: "x", Embedding: unitVector(int(vectorDimension), 0)})); !errors.Is(err, biz.ErrVectorDocNotFound) {
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
		addVectorDocument(t, store, vectorTenantA, spaceDoc(&biz.VectorDocument{
			ID:        fmt.Sprintf("doc-%02d", i),
			Content:   "content",
			Embedding: unitVector(int(vectorDimension), i),
			Metadata:  map[string]any{"category": category, "seq": i},
		}))
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
	bySpace, err := store.Count(ctx, &biz.VectorFilter{Embedder: vectorTestSpace})
	if err != nil || bySpace != 5 {
		t.Fatalf("Count(space) = (%d, %v), want (5, nil)", bySpace, err)
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

	// GetMetadata 分页：created_at 为秒级精度、两行同秒写入，排序由 id
	// tie-break（doc-01 < doc-03），翻页结果必须确定唯一；向量与正文不随行。
	firstPage, err := store.GetMetadata(ctx, &biz.VectorFilter{Limit: 1, Offset: 0})
	if err != nil {
		t.Fatalf("GetMetadata(page 0): %v", err)
	}
	if len(firstPage) != 1 {
		t.Fatalf("GetMetadata page 0 rows = %d, want 1", len(firstPage))
	}
	if _, ok := firstPage["doc-01"]; !ok {
		t.Fatalf("GetMetadata page 0 = %v, want exactly doc-01 (id tie-break)", pageIDs(firstPage))
	}
	secondPage, err := store.GetMetadata(ctx, &biz.VectorFilter{Limit: 1, Offset: 1})
	if err != nil {
		t.Fatalf("GetMetadata(page 1): %v", err)
	}
	if len(secondPage) != 1 {
		t.Fatalf("GetMetadata page 1 rows = %d, want 1", len(secondPage))
	}
	if _, ok := secondPage["doc-03"]; !ok {
		t.Fatalf("GetMetadata page 1 = %v, want exactly doc-03 (id tie-break)", pageIDs(secondPage))
	}
	for _, m := range secondPage {
		if _, hasContent := m["tenant_id"]; !hasContent {
			t.Fatalf("metadata row lacks injected tenant_id: %+v", m)
		}
	}
	if _, err := store.GetMetadata(ctx, &biz.VectorFilter{Offset: 2}); err == nil {
		t.Fatal("GetMetadata(offset without limit) = nil error, want rejection")
	}
}

// pageIDs 是 metadata 分页结果的 ID 集合，用于失败信息定位。
func pageIDs(metadata map[string]map[string]any) []string {
	ids := make([]string, 0, len(metadata))
	for id := range metadata {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func TestNewVectorStore_startup_probe_rejects_deployment_mismatch(t *testing.T) {
	d := setupVectorTestDB(t)

	// 声明维度无对应表达式部分索引：启动即失败（验收项——缺索引的
	// 检索会退化为顺序扫描）。
	undeclared := vectorStoreConf()
	undeclared.Embedders[0].Dimension = 999
	undeclared.Embedders[0].Version = "v999"
	undeclared.DefaultEmbedder = "bge-m3@v999"
	_, _, err := NewVectorStore(undeclared, d)
	if err == nil || !containsAll(err.Error(), "no expression partial hnsw index for dimension 999") {
		t.Fatalf("NewVectorStore(dimension=999) = %v, want missing-index failure", err)
	}
	// 表未部署（配置表名与 DDL 不符）：同样启动失败并指明处置。
	missing := vectorStoreConf()
	missing.TableName = "agent_document_missing"
	_, _, err = NewVectorStore(missing, d)
	if err == nil || !containsAll(err.Error(), "not deployed") {
		t.Fatalf("NewVectorStore(missing table) = %v, want not-deployed failure", err)
	}
	// 一致配置：探针通过，设施可用。
	store := newVectorStoreForTest(t, d)
	if err := store.Add(vectorIdentityContext(vectorTenantA), spaceDoc(&biz.VectorDocument{
		ID:        "doc-probe",
		Content:   "c",
		Embedding: unitVector(int(vectorDimension), 0),
	})); err != nil {
		t.Fatalf("Add after probe: %v", err)
	}
}

func TestVectorStore_search_minscore_filters_in_sql(t *testing.T) {
	d := setupVectorTestDB(t)
	store := newVectorStoreForTest(t, d)
	ctx := vectorIdentityContext(vectorTenantA)

	// 以 e0 检索的理论分数：doc-e0 为 1.0，doc-e1/doc-e2 为 0.5。阈值
	// 取 0.25 / 0.75，避开 0.5 边界（float4 距离计算有舍入），使断言只
	// 依赖区间归属而非浮点边界。
	for _, id := range []string{"doc-e0", "doc-e1", "doc-e2"} {
		index := int(id[len(id)-1] - '0')
		addVectorDocument(t, store, vectorTenantA, spaceDoc(&biz.VectorDocument{
			ID:        id,
			Content:   "content",
			Embedding: unitVector(int(vectorDimension), index),
		}))
	}

	// MinScore 0.75：0.5 档被 SQL 阈值（score >= $N）滤除，只剩同向行。
	hits, err := store.Search(ctx, unitVector(int(vectorDimension), 0), &biz.VectorFilter{
		Embedder: vectorTestSpace, Dimension: int(vectorDimension), Limit: 10, MinScore: 0.75,
	})
	if err != nil {
		t.Fatalf("search with min_score 0.75: %v", err)
	}
	if len(hits) != 1 || hits[0].Document.ID != "doc-e0" {
		t.Fatalf("min_score 0.75 hits = %s, want exactly [doc-e0]", searchIDs(hits))
	}
	// MinScore 0.25：三行全部越过阈值。
	hits, err = store.Search(ctx, unitVector(int(vectorDimension), 0), &biz.VectorFilter{
		Embedder: vectorTestSpace, Dimension: int(vectorDimension), Limit: 10, MinScore: 0.25,
	})
	if err != nil {
		t.Fatalf("search with min_score 0.25: %v", err)
	}
	if len(hits) != 3 {
		t.Fatalf("min_score 0.25 hits = %d, want 3", len(hits))
	}
	// 阈值与租户过滤叠加：租户 B 同阈值检索为空。
	foreign, err := store.Search(vectorIdentityContext(vectorTenantB), unitVector(int(vectorDimension), 0), &biz.VectorFilter{
		Embedder: vectorTestSpace, Dimension: int(vectorDimension), Limit: 10, MinScore: 0.25,
	})
	if err != nil || len(foreign) != 0 {
		t.Fatalf("cross-tenant search with min_score = (%d, %v), want (0, nil)", len(foreign), err)
	}
}

func TestVectorStore_add_duplicate_id_rejected_by_unique_constraint(t *testing.T) {
	d := setupVectorTestDB(t)
	store := newVectorStoreForTest(t, d)
	ctx := vectorIdentityContext(vectorTenantA)

	doc := spaceDoc(&biz.VectorDocument{
		ID:        "doc-dup",
		Content:   "first",
		Embedding: unitVector(int(vectorDimension), 0),
	})
	addVectorDocument(t, store, vectorTenantA, doc)
	doc.Content = "second"
	err := store.Add(ctx, doc)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("duplicate Add = %v, want unique violation (23505)", err)
	}
	got, err := store.Get(ctx, "doc-dup")
	if err != nil || got.Content != "first" {
		t.Fatalf("row after duplicate Add = (%+v, %v), want original untouched", got, err)
	}
}

func TestVectorStore_get_metadata_offset_beyond_total_returns_empty(t *testing.T) {
	d := setupVectorTestDB(t)
	store := newVectorStoreForTest(t, d)
	ctx := vectorIdentityContext(vectorTenantA)

	for i := 0; i < 2; i++ {
		addVectorDocument(t, store, vectorTenantA, spaceDoc(&biz.VectorDocument{
			ID:        fmt.Sprintf("doc-%02d", i),
			Content:   "content",
			Embedding: unitVector(int(vectorDimension), i),
		}))
	}
	metadata, err := store.GetMetadata(ctx, &biz.VectorFilter{Limit: 10, Offset: 100})
	if err != nil {
		t.Fatalf("GetMetadata(offset beyond total): %v", err)
	}
	if len(metadata) != 0 {
		t.Fatalf("GetMetadata(offset beyond total) = %v, want empty", pageIDs(metadata))
	}
}

// searchIDs 是检索命中结果的 ID 列表，用于失败信息定位。
func searchIDs(hits []*biz.ScoredVectorDocument) []string {
	ids := make([]string, 0, len(hits))
	for _, hit := range hits {
		ids = append(ids, hit.Document.ID)
	}
	return ids
}
