package vector

import (
	"testing"
	"time"

	"grape/internal/biz"
)

// metadata 条件与批量更新集成测试（真实 PostgreSQL，未设 GRAPE_TEST_* 时
// 跳过）：检索固定条件（状态/有效期/知识库）、UpdateMetadata 只改 metadata、
// 空过滤拒绝。

func activeConditions(libraryIDs ...string) []biz.MetadataCondition {
	now := time.Now().Unix()
	conditions := []biz.MetadataCondition{
		{Key: "doc_status", Op: biz.VectorMetadataEq, Values: []any{1}},
		{Key: "valid_from_unix", Op: biz.VectorMetadataLte, Values: []any{now}},
		{Key: "valid_to_unix", Op: biz.VectorMetadataGt, Values: []any{now}},
	}
	if len(libraryIDs) > 0 {
		values := make([]any, 0, len(libraryIDs))
		for _, id := range libraryIDs {
			values = append(values, id)
		}
		conditions = append(conditions, biz.MetadataCondition{Key: "kb_id", Op: biz.VectorMetadataIn, Values: values})
	}
	return conditions
}

func TestVectorStore_search_and_update_metadata_conditions(t *testing.T) {
	d := setupVectorTestDB(t)
	store := newVectorStoreForTest(t, d)
	ctx := vectorIdentityContext(vectorTenantA)
	now := time.Now().Unix()

	docs := []*biz.VectorDocument{
		{
			ID: "chunk-active", Content: "active", Embedder: vectorTestSpace, Dimension: int(vectorDimension), Embedding: unitVector(int(vectorDimension), 0),
			Metadata: map[string]any{
				"kb_id": "kb-1", "document_id": "doc-1", "doc_status": 1,
				"valid_from_unix": int64(0), "valid_to_unix": now + 3600,
			},
		},
		{
			ID: "chunk-inactive", Content: "inactive", Embedder: vectorTestSpace, Dimension: int(vectorDimension), Embedding: unitVector(int(vectorDimension), 1),
			Metadata: map[string]any{
				"kb_id": "kb-1", "document_id": "doc-2", "doc_status": 0,
				"valid_from_unix": int64(0), "valid_to_unix": now + 3600,
			},
		},
		{
			ID: "chunk-expired", Content: "expired", Embedder: vectorTestSpace, Dimension: int(vectorDimension), Embedding: unitVector(int(vectorDimension), 2),
			Metadata: map[string]any{
				"kb_id": "kb-2", "document_id": "doc-3", "doc_status": 1,
				"valid_from_unix": int64(0), "valid_to_unix": now - 10,
			},
		},
	}
	for _, doc := range docs {
		addVectorDocument(t, store, vectorTenantA, doc)
	}

	search := func(conditions []biz.MetadataCondition) []string {
		hits, err := store.Search(ctx, unitVector(int(vectorDimension), 0), &biz.VectorFilter{
			Embedder:   vectorTestSpace,
			Dimension:  int(vectorDimension),
			Limit:      10,
			Conditions: conditions,
		})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		ids := make([]string, 0, len(hits))
		for _, hit := range hits {
			ids = append(ids, hit.Document.ID)
		}
		return ids
	}

	// 默认条件：只有启用且有效期内、库 kb-1 的条目命中。
	if ids := search(activeConditions("kb-1")); len(ids) != 1 || ids[0] != "chunk-active" {
		t.Fatalf("active search = %v, want [chunk-active]", ids)
	}
	// 指定库 kb-2：过期条目被 valid_to 条件排除。
	if ids := search(activeConditions("kb-2")); len(ids) != 0 {
		t.Fatalf("expired search = %v, want none", ids)
	}
	// 库集合条件（in）：kb-1 + kb-2 仍只命中活跃条目。
	if ids := search(activeConditions("kb-1", "kb-2")); len(ids) != 1 || ids[0] != "chunk-active" {
		t.Fatalf("in search = %v, want [chunk-active]", ids)
	}

	// UpdateMetadata 只改 metadata：把活跃条目标记为停用后检索不到，正文与
	// 向量保持原值。
	updated, err := store.UpdateMetadata(ctx, &biz.VectorFilter{Conditions: []biz.MetadataCondition{
		{Key: "document_id", Op: biz.VectorMetadataEq, Values: []any{"doc-1"}},
	}}, map[string]any{"doc_status": 0})
	if err != nil {
		t.Fatalf("update metadata: %v", err)
	}
	if updated != 1 {
		t.Fatalf("updated rows = %d, want 1", updated)
	}
	if ids := search(activeConditions("kb-1")); len(ids) != 0 {
		t.Fatalf("search after disabling = %v, want none", ids)
	}
	doc, err := store.Get(ctx, "chunk-active")
	if err != nil {
		t.Fatalf("get after metadata update: %v", err)
	}
	if doc.Content != "active" || len(doc.Embedding) != int(vectorDimension) {
		t.Fatalf("metadata update touched content/embedding: %+v", doc)
	}
	if status, _ := doc.Metadata["doc_status"].(float64); status != 0 {
		t.Fatalf("doc_status metadata = %v, want 0 (json number)", doc.Metadata["doc_status"])
	}

	// 空过滤的批量改写与空 patch 都拒绝执行。
	if _, err := store.UpdateMetadata(ctx, nil, map[string]any{"doc_status": 1}); err == nil {
		t.Fatal("unfiltered UpdateMetadata = nil error, want rejection")
	}
	if _, err := store.UpdateMetadata(ctx, &biz.VectorFilter{Conditions: []biz.MetadataCondition{
		{Key: "document_id", Op: biz.VectorMetadataEq, Values: []any{"doc-1"}},
	}}, nil); err == nil {
		t.Fatal("empty patch UpdateMetadata = nil error, want rejection")
	}
}

func TestVectorStore_metadata_condition_ops(t *testing.T) {
	d := setupVectorTestDB(t)
	store := newVectorStoreForTest(t, d)
	ctx := vectorIdentityContext(vectorTenantA)

	for i, id := range []string{"c-1", "c-2", "c-3"} {
		addVectorDocument(t, store, vectorTenantA, &biz.VectorDocument{
			ID: id, Content: id, Embedder: vectorTestSpace, Dimension: int(vectorDimension), Embedding: unitVector(int(vectorDimension), i),
			Metadata: map[string]any{"rank": i + 1, "kb_id": "kb-x"},
		})
	}
	count := func(condition biz.MetadataCondition) int64 {
		total, err := store.Count(ctx, &biz.VectorFilter{Conditions: []biz.MetadataCondition{condition}})
		if err != nil {
			t.Fatalf("count with %s: %v", condition.Op, err)
		}
		return total
	}
	if got := count(biz.MetadataCondition{Key: "rank", Op: biz.VectorMetadataGte, Values: []any{2}}); got != 2 {
		t.Fatalf("gte count = %d, want 2", got)
	}
	if got := count(biz.MetadataCondition{Key: "rank", Op: biz.VectorMetadataLt, Values: []any{3}}); got != 2 {
		t.Fatalf("lt count = %d, want 2", got)
	}
	if got := count(biz.MetadataCondition{Key: "rank", Op: biz.VectorMetadataNe, Values: []any{2}}); got != 2 {
		t.Fatalf("ne count = %d, want 2", got)
	}
	if got := count(biz.MetadataCondition{Key: "kb_id", Op: biz.VectorMetadataIn, Values: []any{"kb-x", "kb-y"}}); got != 3 {
		t.Fatalf("in count = %d, want 3", got)
	}
	if _, err := store.Count(ctx, &biz.VectorFilter{Conditions: []biz.MetadataCondition{
		{Key: "rank", Op: biz.VectorMetadataLt, Values: []any{"not-a-number"}},
	}}); err == nil {
		t.Fatal("non-integral comparison value = nil error, want rejection")
	}
	if _, err := store.Count(ctx, &biz.VectorFilter{Conditions: []biz.MetadataCondition{
		{Key: "", Op: biz.VectorMetadataEq, Values: []any{1}},
	}}); err == nil {
		t.Fatal("empty condition key = nil error, want rejection")
	}}
