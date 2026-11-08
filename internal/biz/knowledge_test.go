package biz

import (
	"testing"
	"time"
)

// 知识库用例的纯函数单测：chunk metadata 的生效状态与有效期交集、正文
// 摘要变更判定与切分参数归一化。

func tsPtr(t time.Time) *time.Time { return &t }

func TestEffectiveChunkMetadata_status_and_validity_intersection(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	lib := &KnowledgeLibrary{
		ID:        10,
		Status:    KnowledgeStatusEnabled,
		ValidFrom: tsPtr(base.Add(2 * time.Hour)),
		ValidTo:   tsPtr(base.Add(10 * time.Hour)),
	}
	doc := &KnowledgeDocument{
		ID:        20,
		Status:    KnowledgeStatusEnabled,
		ValidFrom: tsPtr(base.Add(1 * time.Hour)),
		ValidTo:   tsPtr(base.Add(8 * time.Hour)),
	}
	meta := effectiveChunkMetadata(lib, doc)
	if meta["kb_id"] != "10" || meta["document_id"] != "20" {
		t.Fatalf("ids = %v, want decimal strings", meta)
	}
	if meta["doc_status"] != 1 {
		t.Fatalf("doc_status = %v, want 1", meta["doc_status"])
	}
	// 生效时间取交集：库的 2h 晚于条目的 1h；失效时间取较早的 8h。
	if meta["valid_from_unix"] != lib.ValidFrom.Unix() {
		t.Fatalf("valid_from_unix = %v, want library lower bound", meta["valid_from_unix"])
	}
	if meta["valid_to_unix"] != doc.ValidTo.Unix() {
		t.Fatalf("valid_to_unix = %v, want document upper bound", meta["valid_to_unix"])
	}

	// 库停用：doc_status 取与为 0（向量保留，检索条件排除）。
	lib.Status = KnowledgeStatusDisabled
	if got := effectiveChunkMetadata(lib, doc)["doc_status"]; got != 0 {
		t.Fatalf("doc_status with disabled library = %v, want 0", got)
	}

	// 无限期边界：均空时 from=0、to=253402300799。
	open := effectiveChunkMetadata(&KnowledgeLibrary{ID: 1, Status: 1}, &KnowledgeDocument{ID: 2, Status: 1})
	if open["valid_from_unix"] != int64(0) || open["valid_to_unix"] != unixInfinite {
		t.Fatalf("open bounds = (%v, %v), want (0, %d)", open["valid_from_unix"], open["valid_to_unix"], unixInfinite)
	}
}

func TestHashContent_and_contentChanged(t *testing.T) {
	a := "alpha"
	b := "beta"
	ha := hashContent(&a)
	hb := hashContent(&b)
	if ha == nil || hb == nil || *ha == *hb {
		t.Fatalf("hashes = (%v, %v), want distinct non-nil", ha, hb)
	}
	if contentChanged(ha, hb) != true {
		t.Fatal("contentChanged(distinct) = false, want true")
	}
	if contentChanged(ha, hashContent(&a)) {
		t.Fatal("contentChanged(equal) = true, want false")
	}
	if contentChanged(nil, nil) {
		t.Fatal("contentChanged(nil, nil) = true, want false")
	}
	if !contentChanged(nil, ha) {
		t.Fatal("contentChanged(nil, hash) = false, want true")
	}
}

func TestNormalizeChunking(t *testing.T) {
	cases := []struct {
		in, want KnowledgeLibrary
	}{
		{KnowledgeLibrary{}, KnowledgeLibrary{ChunkSize: 800, ChunkOverlap: 100}},
		{KnowledgeLibrary{ChunkSize: 400}, KnowledgeLibrary{ChunkSize: 400, ChunkOverlap: 0}},
		{KnowledgeLibrary{ChunkSize: 400, ChunkOverlap: 50}, KnowledgeLibrary{ChunkSize: 400, ChunkOverlap: 50}},
	}
	for _, tc := range cases {
		got := tc.in
		normalizeChunking(&got)
		if got.ChunkSize != tc.want.ChunkSize || got.ChunkOverlap != tc.want.ChunkOverlap {
			t.Fatalf("normalizeChunking(%+v) = (%d, %d), want (%d, %d)",
				tc.in, got.ChunkSize, got.ChunkOverlap, tc.want.ChunkSize, tc.want.ChunkOverlap)
		}
	}
}
