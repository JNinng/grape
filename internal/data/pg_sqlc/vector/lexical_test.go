package vector

import (
	"context"
	"os"
	"strings"
	"testing"

	"grape/internal/biz"
	"grape/internal/data/pg_sqlc/deploy"

	"github.com/jackc/pgx/v5"
)

// 词法检索集成测试（真实 PostgreSQL，未设 GRAPE_VECTOR_TEST_ADMIN_DSN /
// GRAPE_VECTOR_TEST_DSN 时跳过）：中文内容写入后的中文查询命中与排序、
// conditions 口径与向量路一致、特殊字符 round-trip、跨租户不可见、空
// 分词查询空集。分词口径（Tokenizer 同实例）由 gsetokenizer 自身单测
// 覆盖；本文件只验证存储边界（tsvector 落库、GIN 索引、条件过滤）。

// lexicalTestDoc 是词法用例的文档载体：正文进词法索引，metadata 带固定
// 条件字段（与知识检索的固定条件同形），向量用正交基占位（词法路不消费）。
type lexicalTestDoc struct {
	id       string
	content  string
	metadata map[string]any
}

func addLexicalDoc(t *testing.T, store biz.VectorStore, id biz.Identity, doc lexicalTestDoc) {
	t.Helper()
	metadata := doc.metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	addVectorDocument(t, store, id, spaceDoc(&biz.VectorDocument{
		ID:        doc.id,
		Content:   doc.content,
		Embedding: unitVector(int(vectorDimension), 0),
		Metadata:  metadata,
	}))
}

// lexicalFilter 是词法检索的声明过滤（Embedder/Dimension 必填，与向量
// 路同一空间）。
func lexicalFilter() *biz.VectorFilter {
	return &biz.VectorFilter{Embedder: vectorTestSpace, Dimension: int(vectorDimension)}
}

func lexicalHits(hits []*biz.ScoredVectorDocument) []string {
	ids := make([]string, 0, len(hits))
	for _, hit := range hits {
		ids = append(ids, hit.Document.ID)
	}
	return ids
}

// AdminDSNForVectorTest 返回本包集成测试的管理员 DSN（门控由 setupVectorTestDB
// 先行完成）：供需要表属主权限的用例（如刷新优化器统计）复用。
func AdminDSNForVectorTest(t *testing.T) string {
	t.Helper()
	adminDSN := os.Getenv("GRAPE_VECTOR_TEST_ADMIN_DSN")
	if adminDSN == "" {
		t.Skip("GRAPE_VECTOR_TEST_ADMIN_DSN not set; skipping vector store integration tests")
	}
	return adminDSN
}

func TestVectorStore_lexical_search_hits_chinese_content_with_rank_order(t *testing.T) {
	t.Parallel()
	d := setupVectorTestDB(t)
	store := newVectorStoreForTest(t, d)
	ctx := vectorIdentityContext(vectorTenantA)

	// 富文档重复查询词（部署/步骤各 3 次），贫文档各 1 次：
	// ts_rank_cd 的覆盖密度把富文档排前。
	addLexicalDoc(t, store, vectorTenantA, lexicalTestDoc{
		id:      "doc-rich",
		content: "部署步骤说明：部署步骤见文档，部署步骤必须按序执行。",
	})
	addLexicalDoc(t, store, vectorTenantA, lexicalTestDoc{
		id:      "doc-poor",
		content: "部署步骤仅出现一次。",
	})
	addLexicalDoc(t, store, vectorTenantA, lexicalTestDoc{
		id:      "doc-unrelated",
		content: "年假申请流程是怎样的？员工提交申请后由直属上级审批。",
	})

	hits, err := store.SearchLexical(ctx, "部署步骤", lexicalFilter())
	if err != nil {
		t.Fatalf("SearchLexical: %v", err)
	}
	if got := lexicalHits(hits); len(got) != 2 || got[0] != "doc-rich" || got[1] != "doc-poor" {
		t.Fatalf("hits = %v, want [doc-rich doc-poor] by cover density", got)
	}
	// Score 是 ts_rank_cd 相关性：未归一、大于 0、富文档严格更高。
	if hits[0].Score <= 0 || hits[0].Score <= hits[1].Score {
		t.Fatalf("scores = (%f, %f), want positive and descending", hits[0].Score, hits[1].Score)
	}
	// 命中携带原文与租户身份（与向量路同一行载体）。
	if hits[0].Document.Content == "" || hits[0].Document.Metadata["tenant_id"] != vectorTenantA.TenantID {
		t.Fatalf("hit document = %+v, want content and injected tenant_id", hits[0].Document)
	}
}

func TestVectorStore_lexical_search_applies_conditions_like_vector_path(t *testing.T) {
	t.Parallel()
	// conditions 口径与向量路共享：doc_status/有效期/kb_id 任一不满足
	// 即不可召回（词法路与向量路同一文档宇宙）。
	d := setupVectorTestDB(t)
	store := newVectorStoreForTest(t, d)
	ctx := vectorIdentityContext(vectorTenantA)

	matching := map[string]any{
		"doc_status":      1,
		"valid_from_unix": int64(1000),
		"valid_to_unix":   int64(2000),
		"kb_id":           "7",
	}
	addLexicalDoc(t, store, vectorTenantA, lexicalTestDoc{id: "doc-match", content: "部署步骤说明。", metadata: matching})
	addLexicalDoc(t, store, vectorTenantA, lexicalTestDoc{id: "doc-status-off", content: "部署步骤说明。", metadata: map[string]any{
		"doc_status": 0, "valid_from_unix": int64(1000), "valid_to_unix": int64(2000), "kb_id": "7",
	}})
	addLexicalDoc(t, store, vectorTenantA, lexicalTestDoc{id: "doc-expired", content: "部署步骤说明。", metadata: map[string]any{
		"doc_status": 1, "valid_from_unix": int64(1000), "valid_to_unix": int64(1500), "kb_id": "7",
	}})
	addLexicalDoc(t, store, vectorTenantA, lexicalTestDoc{id: "doc-other-kb", content: "部署步骤说明。", metadata: map[string]any{
		"doc_status": 1, "valid_from_unix": int64(1000), "valid_to_unix": int64(2000), "kb_id": "8",
	}})

	filter := lexicalFilter()
	filter.Conditions = []biz.MetadataCondition{
		{Key: "doc_status", Op: biz.VectorMetadataEq, Values: []any{1}},
		{Key: "valid_from_unix", Op: biz.VectorMetadataLte, Values: []any{1600}},
		{Key: "valid_to_unix", Op: biz.VectorMetadataGt, Values: []any{1600}},
		{Key: "kb_id", Op: biz.VectorMetadataIn, Values: []any{"7"}},
	}
	hits, err := store.SearchLexical(ctx, "部署步骤", filter)
	if err != nil {
		t.Fatalf("SearchLexical with conditions: %v", err)
	}
	if got := lexicalHits(hits); len(got) != 1 || got[0] != "doc-match" {
		t.Fatalf("hits = %v, want exactly [doc-match]", got)
	}
}

func TestVectorStore_lexical_search_round_trips_special_characters(t *testing.T) {
	t.Parallel()
	// 正文含单引号与反斜杠：写入（tsvector 字面量转义）→ 查询（tsquery
	// 字面量转义）round-trip 可召回，转义错误会直接报语法错误或漏召回。
	d := setupVectorTestDB(t)
	store := newVectorStoreForTest(t, d)
	ctx := vectorIdentityContext(vectorTenantA)

	addLexicalDoc(t, store, vectorTenantA, lexicalTestDoc{
		id:      "doc-quoted",
		content: `条款包含单引号'与反斜杠\以及单引号词元 o'brien。`,
	})
	addLexicalDoc(t, store, vectorTenantA, lexicalTestDoc{
		id:      "doc-plain",
		content: "普通正文无特殊字符。",
	})

	hits, err := store.SearchLexical(ctx, `单引号'反斜杠`, lexicalFilter())
	if err != nil {
		t.Fatalf("SearchLexical with quote/backslash query: %v", err)
	}
	if got := lexicalHits(hits); len(got) != 1 || got[0] != "doc-quoted" {
		t.Fatalf("hits = %v, want exactly [doc-quoted]", got)
	}

	// 注入形态词元：只作为字面量内部词元被解析，不逃逸出字面量。
	hits, err = store.SearchLexical(ctx, `o'brien'; DROP TABLE agent.agent_document; --`, lexicalFilter())
	if err != nil {
		t.Fatalf("SearchLexical with injection-shaped query: %v", err)
	}
	if got := lexicalHits(hits); len(got) != 1 || got[0] != "doc-quoted" {
		t.Fatalf("hits = %v, want exactly [doc-quoted]", got)
	}
}

func TestVectorStore_lexical_search_isolates_tenants(t *testing.T) {
	t.Parallel()
	d := setupVectorTestDB(t)
	store := newVectorStoreForTest(t, d)

	addLexicalDoc(t, store, vectorTenantA, lexicalTestDoc{
		id:      "doc-secret",
		content: "租户A的部署步骤密件。",
	})

	// 租户 B 同库同表：词法检索不可见租户 A 的行（应用层租户过滤与
	// 向量路同源）。
	hits, err := store.SearchLexical(vectorIdentityContext(vectorTenantB), "部署步骤", lexicalFilter())
	if err != nil {
		t.Fatalf("cross-tenant SearchLexical: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("cross-tenant lexical hits = %v, want empty (isolation must hold)", lexicalHits(hits))
	}
	// 租户 A 自身可召回。
	hits, err = store.SearchLexical(vectorIdentityContext(vectorTenantA), "部署步骤", lexicalFilter())
	if err != nil || len(hits) != 1 || hits[0].Document.ID != "doc-secret" {
		t.Fatalf("owner lexical hits = (%v, %v), want exactly [doc-secret]", lexicalHits(hits), err)
	}
}

func TestVectorStore_lexical_search_with_stop_words_only_query_returns_empty(t *testing.T) {
	t.Parallel()
	d := setupVectorTestDB(t)
	store := newVectorStoreForTest(t, d)
	ctx := vectorIdentityContext(vectorTenantA)

	addLexicalDoc(t, store, vectorTenantA, lexicalTestDoc{id: "doc-a", content: "部署步骤说明。"})

	// 查询分词为空（停用词被过滤）：空结果集，不触库、不报错。
	hits, err := store.SearchLexical(ctx, "是 的", lexicalFilter())
	if err != nil {
		t.Fatalf("SearchLexical(stop-words-only query) = %v, want empty result", err)
	}
	if hits == nil || len(hits) != 0 {
		t.Fatalf("hits = %#v, want empty non-nil slice", hits)
	}
}

func TestVectorStore_update_refreshes_lexical_column(t *testing.T) {
	t.Parallel()
	// Update 与 Add 同一 Tokenizer 口径重算词法列：旧词不再命中，新词
	// 可召回（无残留旧索引）。
	d := setupVectorTestDB(t)
	store := newVectorStoreForTest(t, d)
	ctx := vectorIdentityContext(vectorTenantA)

	addLexicalDoc(t, store, vectorTenantA, lexicalTestDoc{id: "doc-mut", content: "部署步骤说明。"})

	filter := lexicalFilter()
	filter.Limit = 10
	hits, err := store.SearchLexical(ctx, "部署步骤", filter)
	if err != nil || len(hits) != 1 {
		t.Fatalf("lexical hits before update = (%v, %v), want exactly doc-mut", lexicalHits(hits), err)
	}

	if err := store.Update(ctx, spaceDoc(&biz.VectorDocument{
		ID:        "doc-mut",
		Content:   "年假申请流程说明。",
		Embedding: unitVector(int(vectorDimension), 1),
		Metadata:  map[string]any{},
	})); err != nil {
		t.Fatalf("update: %v", err)
	}

	hits, err = store.SearchLexical(ctx, "部署步骤", filter)
	if err != nil {
		t.Fatalf("lexical hits after update: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("stale lexical hits = %v, want empty (old tokens dropped)", lexicalHits(hits))
	}
	hits, err = store.SearchLexical(ctx, "年假申请", filter)
	if err != nil || len(hits) != 1 || hits[0].Document.ID != "doc-mut" {
		t.Fatalf("lexical hits with new tokens = (%v, %v), want exactly doc-mut", lexicalHits(hits), err)
	}
}

func TestVectorStore_lexical_search_plan_uses_gin_index(t *testing.T) {
	t.Parallel()
	// 词法检索 SQL 必须命中部分 GIN 索引（部分谓词 WHERE lexical IS NOT
	// NULL + tsquery 匹配）；先播种足够多的行、仅少数行含查询词元（倒计
	// 选择性），避免优化器在低选择性下选租户前缀 btree 的计划波动。
	d := setupVectorTestDB(t)
	newVectorStoreForTest(t, d)
	ctx := context.Background()

	seed := `INSERT INTO agent.agent_document
(id, tenant_id, user_id, embedder, embed_dim, content, embedding, lexical, metadata, created_by, updated_by)
SELECT 'plan-'||g, 't-vec-a', 11, 'bge-m3@v1', 1024, 'seed',
       array_fill(0.1::float4, ARRAY[1024])::vector,
       CASE WHEN g <= 5 THEN to_tsvector('simple', '部署 步骤 rare-token') ELSE to_tsvector('simple', 'nomatch') END,
       '{}'::jsonb, 11, 11
FROM generate_series(1, 20000) g`
	if _, err := d.Pool().Exec(ctx, seed); err != nil {
		t.Fatalf("seed rows for plan assertion: %v", err)
	}
	// 统计先行：无 ANALYZE 时优化器用缺省选择率，租户等值（0.005 缺省）会
	// 压过 tsquery 的缺省估计，计划偏向租户前缀 btree。运行角色 grape_u 无
	// 表属主权限（ANALYZE 会被静默跳过），统计经管理员连接刷新；真实统计
	// 下租户等值选择性为 1.0、tsquery 命中 5/20000，GIN 部分索引是唯一
	// 合算路径。
	adminDSN := AdminDSNForVectorTest(t)
	adminCfg, err := pgx.ParseConfig(adminDSN)
	if err != nil {
		t.Fatalf("parse admin dsn: %v", err)
	}
	adminCfg.Database = deploy.DatabaseNameFor(t.Name())
	admin, err := pgx.ConnectConfig(ctx, adminCfg)
	if err != nil {
		t.Fatalf("connect admin for analyze: %v", err)
	}
	if _, err := admin.Exec(ctx, "ANALYZE agent.agent_document"); err != nil {
		t.Fatalf("analyze seeded table: %v", err)
	}
	if err := admin.Close(ctx); err != nil {
		t.Fatalf("close admin: %v", err)
	}
	// 与 SearchLexical 同形状的 SQL（含 ts_rank_cd 排序）。
	rows, err := d.Pool().Query(ctx, `EXPLAIN (COSTS OFF) SELECT id, ts_rank_cd(lexical, $3::tsquery) AS score
FROM agent.agent_document
WHERE tenant_id = $1 AND embedder = $2 AND embed_dim = 1024
  AND lexical IS NOT NULL AND lexical @@ $3::tsquery
ORDER BY score DESC, id LIMIT 5`, "t-vec-a", vectorTestSpace, "'部署' | '步骤'")
	if err != nil {
		t.Fatalf("explain lexical search: %v", err)
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
	if !strings.Contains(plan.String(), "agent_document_lexical_gin") {
		t.Fatalf("lexical search plan does not use the partial gin index:\n%s", plan.String())
	}
}
