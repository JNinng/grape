package vector

import (
	"context"
	"testing"
	"time"

	"grape/internal/biz"
)

// 重摄差量对账与退役标记的集成测试（真实 PostgreSQL，未设
// GRAPE_VECTOR_TEST_* 时跳过）：同内容重摄走刷新、内容变化标记退役、
// 内容改回旧版本复活、读面排除退役行、复现反查可还原命中时原文。
// 摄取管线与 data 层仓储一起验证——对账编排在 biz、标记与读面 SQL 在本层。

// supersedeDocMetadata 是与知识条目同口径的 chunk metadata（检索固定条件
// 可命中）：kb 行事实由 biz 的 effectiveChunkMetadata 产出，用例等值构造。
func supersedeDocMetadata() map[string]any {
	return map[string]any{
		"kb_id":           "1",
		"doc_status":      1,
		"valid_from_unix": int64(0),
		"valid_to_unix":   int64(253402300799),
	}
}

// storedChunks 读取本来源的全部切片（List 不过滤退役行，反查同一读面），
// 返回 ID→正文与退役行集合。
func storedChunks(t *testing.T, ctx context.Context, store biz.VectorStore, sourceID string) (map[string]string, map[string]bool) {
	t.Helper()
	docs, err := store.List(ctx, &biz.VectorFilter{Metadata: map[string]any{"source_id": sourceID}})
	if err != nil {
		t.Fatalf("list stored chunks of %s: %v", sourceID, err)
	}
	content := make(map[string]string, len(docs))
	retired := make(map[string]bool, len(docs))
	for _, doc := range docs {
		content[doc.ID] = doc.Content
		if _, ok := doc.Metadata[biz.VectorMetadataSupersededAt]; ok {
			retired[doc.ID] = true
		}
	}
	return content, retired
}

// activeChunks 通过 is-null 条件枚举现行切片（管理/检索读面口径）。
func activeChunks(t *testing.T, ctx context.Context, store biz.VectorStore, sourceID string) []*biz.VectorDocument {
	t.Helper()
	docs, err := store.List(ctx, &biz.VectorFilter{
		Metadata: map[string]any{"source_id": sourceID},
		Conditions: []biz.MetadataCondition{
			{Key: biz.VectorMetadataSupersededAt, Op: biz.VectorMetadataIsNull},
		},
	})
	if err != nil {
		t.Fatalf("list active chunks of %s: %v", sourceID, err)
	}
	return docs
}

func TestVectorStore_reingest_differential_reconcile_and_reverse_lookup(t *testing.T) {
	t.Parallel()
	d := setupVectorTestDB(t)
	store := newVectorStoreForTest(t, d)
	ctx := vectorIdentityContext(vectorTenantA)
	// 小块切分制造多块场景（默认切分下单块）。
	uc, err := biz.NewDocumentUsecase(store, pipelineEmbedder{}, fixedSpaceResolver{}, nil, biz.WithChunking(8, 2))
	if err != nil {
		t.Fatalf("build document usecase: %v", err)
	}
	ingest := func(content string) *biz.IngestResult {
		t.Helper()
		result, err := uc.Ingest(ctx, &biz.Document{ID: "rev", Content: content, Metadata: supersedeDocMetadata()})
		if err != nil {
			t.Fatalf("ingest %q: %v", content, err)
		}
		return result
	}

	// v1 首摄：全部走 Add。
	v1 := ingest("01234567890123")
	if v1.Chunks != 2 || v1.Replaced != 0 {
		t.Fatalf("v1 = %+v, want 2 chunks and no retirement", v1)
	}
	v1Chunks, retired := storedChunks(t, ctx, store, "rev")
	if len(v1Chunks) != v1.Chunks || len(retired) != 0 {
		t.Fatalf("v1 stored = (%d chunks, %d retired), want %d/0", len(v1Chunks), len(retired), v1.Chunks)
	}

	// 同内容重摄：ID 不变，差量对账走 Update，无退役行、行数不增。
	v2 := ingest("01234567890123")
	if v2.Chunks != v1.Chunks || v2.Replaced != 0 {
		t.Fatalf("same-content reingest = %+v, want %d chunks and no retirement", v2, v1.Chunks)
	}
	sameChunks, retired := storedChunks(t, ctx, store, "rev")
	if len(sameChunks) != len(v1Chunks) || len(retired) != 0 {
		t.Fatalf("same-content stored = (%d chunks, %d retired), want %d/0", len(sameChunks), len(retired), len(v1Chunks))
	}
	if total, err := store.Count(ctx, &biz.VectorFilter{Metadata: map[string]any{"source_id": "rev"}}); err != nil || total != int64(v1.Chunks) {
		t.Fatalf("count after same-content reingest = (%d, %v), want %d", total, err, v1.Chunks)
	}

	// 内容变化重摄：旧块全部标记退役，现行只剩新块；旧 ID 仍可反查原文。
	v3 := ingest("abcdefghijklmn")
	if v3.Chunks != 2 || v3.Replaced != int64(len(v1Chunks)) {
		t.Fatalf("changed reingest = %+v, want 2 chunks and %d retired", v3, len(v1Chunks))
	}
	all, retired := storedChunks(t, ctx, store, "rev")
	if len(all) != len(v1Chunks)+v3.Chunks || len(retired) != len(v1Chunks) {
		t.Fatalf("stored after change = (%d total, %d retired), want %d/%d",
			len(all), len(retired), len(v1Chunks)+v3.Chunks, len(v1Chunks))
	}
	active := activeChunks(t, ctx, store, "rev")
	if len(active) != v3.Chunks {
		t.Fatalf("active chunks = %d, want %d (superseded excluded)", len(active), v3.Chunks)
	}
	for _, doc := range active {
		if retired[doc.ID] {
			t.Fatalf("active chunk %s is superseded", doc.ID)
		}
	}
	// 复现反查：旧留痕行的候选 ID 仍取到命中当时的原文（退役行可读），
	// 且带退役标记；管理/检索读面看不到它。
	for id, want := range v1Chunks {
		doc, err := store.Get(ctx, id)
		if err != nil {
			t.Fatalf("reverse lookup %s: %v", id, err)
		}
		if doc.Content != want {
			t.Fatalf("reverse lookup %s content = %q, want hit-time %q", id, doc.Content, want)
		}
		if _, ok := doc.Metadata[biz.VectorMetadataSupersededAt]; !ok {
			t.Fatalf("reverse lookup %s metadata = %v, want superseded_at", id, doc.Metadata)
		}
	}
	// 检索固定条件（含 superseded is-null）只召回现行块。
	conditions := append(activeConditions("1"),
		biz.MetadataCondition{Key: biz.VectorMetadataSupersededAt, Op: biz.VectorMetadataIsNull})
	hits, err := store.Search(ctx, unitVector(int(vectorDimension), 0), &biz.VectorFilter{
		Embedder:   vectorTestSpace,
		Dimension:  int(vectorDimension),
		Limit:      10,
		Conditions: conditions,
	})
	if err != nil {
		t.Fatalf("search active conditions: %v", err)
	}
	for _, hit := range hits {
		if retired[hit.Document.ID] {
			t.Fatalf("search returned superseded chunk %s", hit.Document.ID)
		}
	}

	// 内容改回旧版本：旧 ID 重现于新批 → Update 整行刷新，退役标记被
	// metadata 整列替换清掉（复活）；v3 的块成为退役行。
	v4 := ingest("01234567890123")
	if v4.Replaced != int64(v3.Chunks) {
		t.Fatalf("revert reingest = %+v, want %d retired", v4, v3.Chunks)
	}
	revived, retired := storedChunks(t, ctx, store, "rev")
	for id, want := range v1Chunks {
		if retired[id] {
			t.Fatalf("old chunk %s still superseded after content revert", id)
		}
		if revived[id] != want {
			t.Fatalf("revived chunk %s content = %q, want %q", id, revived[id], want)
		}
	}
	active = activeChunks(t, ctx, store, "rev")
	if len(active) != len(v1Chunks) {
		t.Fatalf("active chunks after revert = %d, want %d", len(active), len(v1Chunks))
	}
	if total, err := store.Count(ctx, &biz.VectorFilter{Metadata: map[string]any{"source_id": "rev"}}); err != nil || total != int64(len(v1Chunks)) {
		t.Fatalf("count after revert = (%d, %v), want %d", total, err, len(v1Chunks))
	}
}

func TestVectorStore_mark_superseded_requires_filter(t *testing.T) {
	t.Parallel()
	d := setupVectorTestDB(t)
	store := newVectorStoreForTest(t, d)
	ctx := vectorIdentityContext(vectorTenantA)

	if _, err := store.MarkSuperseded(ctx, nil, time.Now()); err == nil {
		t.Fatal("unfiltered MarkSuperseded = nil error, want rejection")
	}
}
