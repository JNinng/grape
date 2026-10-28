package biz

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// stubEmbedder 是管线单测的替身 embedder：按文本长度派生确定性向量
// （首元素为 rune 数，其余补零），维度可指定。
type stubEmbedder struct {
	dims  int
	calls []string
}

func (s *stubEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	s.calls = append(s.calls, text)
	vec := make([]float32, s.dims)
	vec[0] = float32(len([]rune(text)))
	return vec, nil
}

func (s *stubEmbedder) Dimensions() int { return s.dims }

// mockVectorStore 是管线单测的向量存储替身：记录调用序列与入参，行为
// 可编程。
type mockVectorStore struct {
	added      []*VectorDocument
	deleted    *VectorFilter
	deleteHits int64
	deleteErr  error
	addErrAt   int // 第 n 次 Add 返回错误（1 起；0 = 永不）
}

func (m *mockVectorStore) Add(_ context.Context, doc *VectorDocument) error {
	if m.addErrAt > 0 && len(m.added)+1 == m.addErrAt {
		return errors.New("add failed")
	}
	m.added = append(m.added, doc)
	return nil
}

func (m *mockVectorStore) DeleteByFilter(_ context.Context, filter *VectorFilter) (int64, error) {
	if m.deleteErr != nil {
		return 0, m.deleteErr
	}
	m.deleted = filter
	return m.deleteHits, nil
}

func (*mockVectorStore) Get(context.Context, string) (*VectorDocument, error) {
	return nil, ErrVectorDocNotFound
}
func (*mockVectorStore) Update(context.Context, *VectorDocument) error { return nil }
func (*mockVectorStore) Delete(context.Context, string) error          { return nil }
func (*mockVectorStore) Search(context.Context, []float32, *VectorFilter) ([]*ScoredVectorDocument, error) {
	return nil, nil
}
func (*mockVectorStore) Count(context.Context, *VectorFilter) (int64, error) {
	return 0, nil
}
func (*mockVectorStore) GetMetadata(context.Context, *VectorFilter) (map[string]map[string]any, error) {
	return nil, nil
}

func newDocumentUsecaseForTest(store VectorStore, embedder Embedder) (*DocumentUsecase, error) {
	return NewDocumentUsecase(store, embedder, embedder.Dimensions())
}

func ingestCtx() context.Context {
	return WithIdentity(context.Background(), Identity{TenantID: "t-doc", UserID: 5})
}

func TestDocumentUsecase_when_constructed_with_mismatched_dimension_fails(t *testing.T) {
	// 维度链路对账：embedder 声明 4 维而 conf 传 1024 → 构造期拒绝
	//（启动即失败，与 conf vs DDL 探针同口径）。
	_, err := NewDocumentUsecase(&mockVectorStore{}, &stubEmbedder{dims: 4}, 1024)
	if err == nil || !strings.Contains(err.Error(), "dimension") {
		t.Fatalf("NewDocumentUsecase(mismatched dims) = %v, want dimension error", err)
	}
	for name, fn := range map[string]func() error{
		"nil store":      func() error { _, err := NewDocumentUsecase(nil, &stubEmbedder{dims: 4}, 4); return err },
		"nil embedder":   func() error { _, err := NewDocumentUsecase(&mockVectorStore{}, nil, 4); return err },
		"zero dimension": func() error { _, err := NewDocumentUsecase(&mockVectorStore{}, &stubEmbedder{dims: 4}, 0); return err },
		"bad chunking": func() error {
			_, err := NewDocumentUsecase(&mockVectorStore{}, &stubEmbedder{dims: 4}, 4, WithChunking(10, 10))
			return err
		},
	} {
		if err := fn(); err == nil {
			t.Fatalf("NewDocumentUsecase(%s) = nil error, want rejection", name)
		}
	}
}

func TestDocumentUsecase_when_ingest_chunks_embeds_and_adds_with_anchor(t *testing.T) {
	store := &mockVectorStore{}
	embedder := &stubEmbedder{dims: 8}
	// 小块切分：20 rune 内容按 size 8 / overlap 2 切 3 块。
	u, err := NewDocumentUsecase(store, embedder, 8, WithChunking(8, 2))
	if err != nil {
		t.Fatalf("NewDocumentUsecase: %v", err)
	}
	result, err := u.Ingest(ingestCtx(), &Document{
		ID:       "kb-1",
		Name:     "handbook",
		Content:  strings.Repeat("字", 20),
		Metadata: map[string]any{"dept": "hr"},
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if result.DocumentID != "kb-1" || result.Chunks != 3 || result.Replaced != 0 {
		t.Fatalf("result = %+v, want kb-1/3/0", result)
	}
	if len(store.added) != 3 || len(embedder.calls) != 3 {
		t.Fatalf("adds = %d embeds = %d, want 3/3", len(store.added), len(embedder.calls))
	}
	for i, doc := range store.added {
		if doc.ID != fmt.Sprintf("kb-1#%d", i) {
			t.Fatalf("chunk id = %s, want kb-1#%d", doc.ID, i)
		}
		if doc.Content == "" || doc.Embedding[0] == 0 {
			t.Fatalf("chunk %d content/embedding not populated: %+v", i, doc)
		}
		if doc.Metadata["source_id"] != "kb-1" || doc.Metadata["chunk_index"] != i || doc.Metadata["chunk_total"] != 3 {
			t.Fatalf("chunk %d metadata = %v, want source/index/total", i, doc.Metadata)
		}
		anchor, _ := doc.Metadata["anchor"].(string)
		if !strings.HasPrefix(anchor, "runes ") {
			t.Fatalf("chunk %d anchor = %v, want rune range", i, doc.Metadata["anchor"])
		}
		if doc.Metadata["dept"] != "hr" {
			t.Fatalf("chunk %d must keep caller metadata, got %v", i, doc.Metadata)
		}
	}
	// 覆盖锚点先于写入：DeleteByFilter 以 source_id 为条件。
	if store.deleted == nil || store.deleted.Metadata["source_id"] != "kb-1" {
		t.Fatalf("delete filter = %+v, want source_id kb-1", store.deleted)
	}
}

func TestDocumentUsecase_when_reingest_replaces_previous_chunks(t *testing.T) {
	store := &mockVectorStore{deleteHits: 3}
	embedder := &stubEmbedder{dims: 4}
	u, err := newDocumentUsecaseForTest(store, embedder)
	if err != nil {
		t.Fatalf("NewDocumentUsecase: %v", err)
	}
	result, err := u.Ingest(ingestCtx(), &Document{ID: "kb-1", Content: "v2 content"})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if result.Replaced != 3 {
		t.Fatalf("Replaced = %d, want 3 (previous version cleared)", result.Replaced)
	}
	// 切片 ID 确定性：同来源重摄复用同一 ID 空间（覆盖语义的前提）。
	if store.added[0].ID != "kb-1#0" {
		t.Fatalf("chunk id = %s, want kb-1#0", store.added[0].ID)
	}
}

func TestDocumentUsecase_when_ingest_input_invalid_rejected(t *testing.T) {
	u, err := newDocumentUsecaseForTest(&mockVectorStore{}, &stubEmbedder{dims: 4})
	if err != nil {
		t.Fatalf("NewDocumentUsecase: %v", err)
	}
	for name, doc := range map[string]*Document{
		"nil document":  nil,
		"blank id":      {ID: "  ", Content: "c"},
		"blank content": {ID: "kb-1", Content: "   "},
		"oversize id":   {ID: strings.Repeat("x", maxSourceIDChars+1), Content: "c"},
	} {
		if _, err := u.Ingest(ingestCtx(), doc); err == nil {
			t.Fatalf("Ingest(%s) = nil error, want rejection", name)
		}
	}
	if _, err := u.Ingest(ingestCtx(), &Document{ID: strings.Repeat("x", maxSourceIDChars+1), Content: "c"}); !errors.Is(err, ErrDocumentSourceTooLong) {
		t.Fatalf("Ingest(oversize id) = %v, want ErrDocumentSourceTooLong", err)
	}
}

func TestDocumentUsecase_when_identity_missing_fails_closed(t *testing.T) {
	u, err := newDocumentUsecaseForTest(&mockVectorStore{}, &stubEmbedder{dims: 4})
	if err != nil {
		t.Fatalf("NewDocumentUsecase: %v", err)
	}
	if _, err := u.Ingest(context.Background(), &Document{ID: "kb-1", Content: "c"}); err == nil {
		t.Fatal("Ingest without identity = nil error, want rejection")
	}
}

func TestDocumentUsecase_when_embed_fails_keeps_store_untouched(t *testing.T) {
	store := &mockVectorStore{}
	embedder := &failingEmbedder{dims: 4}
	u, err := NewDocumentUsecase(store, embedder, 4)
	if err != nil {
		t.Fatalf("NewDocumentUsecase: %v", err)
	}
	if _, err := u.Ingest(ingestCtx(), &Document{ID: "kb-1", Content: "content"}); err == nil {
		t.Fatal("Ingest with failing embedder = nil error, want rejection")
	}
	// embed 失败先于删旧写新：存量数据不被破坏。
	if len(store.added) != 0 || store.deleted != nil {
		t.Fatalf("store touched on embed failure: added=%d deleted=%+v", len(store.added), store.deleted)
	}
}

func TestDocumentUsecase_when_add_fails_midway_reports(t *testing.T) {
	store := &mockVectorStore{addErrAt: 2}
	// 小块切分保证多块写入，第 2 次 Add 失败。
	u, err := NewDocumentUsecase(store, &stubEmbedder{dims: 4}, 4, WithChunking(4, 1))
	if err != nil {
		t.Fatalf("NewDocumentUsecase: %v", err)
	}
	if _, err := u.Ingest(ingestCtx(), &Document{ID: "kb-1", Content: strings.Repeat("x", 10)}); err == nil {
		t.Fatal("Ingest with mid-way Add failure = nil error, want error")
	}
}

type failingEmbedder struct{ dims int }

func (f *failingEmbedder) Embed(context.Context, string) ([]float32, error) {
	return nil, errors.New("endpoint down")
}
func (f *failingEmbedder) Dimensions() int { return f.dims }

func TestChunkText_boundaries_overlap_and_multibyte(t *testing.T) {
	// 短文本：单块且保留原文。
	one := chunkText("短文", 10, 2)
	if len(one) != 1 || one[0].content != "短文" || one[0].start != 0 || one[0].end != 2 {
		t.Fatalf("short text chunks = %+v, want single original chunk", one)
	}
	// 恰好等于 size：单块。
	exact := chunkText(strings.Repeat("a", 8), 8, 2)
	if len(exact) != 1 {
		t.Fatalf("exact-size chunks = %d, want 1", len(exact))
	}
	// 超长：相邻块重叠 overlap 个 rune，末块推进到原文末尾。
	long := chunkText(strings.Repeat("x", 20), 8, 2)
	if len(long) != 3 {
		t.Fatalf("long chunks = %d, want 3", len(long))
	}
	if long[1].start != 6 || long[1].end != 14 || long[2].start != 12 || long[2].end != 20 {
		t.Fatalf("overlap windows drifted: %+v", long)
	}
	// overlap 0：不重叠。
	noOverlap := chunkText(strings.Repeat("x", 16), 8, 0)
	if len(noOverlap) != 2 || noOverlap[1].start != 8 {
		t.Fatalf("no-overlap chunks = %+v", noOverlap)
	}
	// 多字节 rune 不被劈开：中文内容切块后可无损拼回原文。
	cjk := chunkText(strings.Repeat("中", 10), 4, 1)
	var rebuilt strings.Builder
	for i, c := range cjk {
		if i > 0 {
			prev := []rune(cjk[i-1].content)
			cur := []rune(c.content)
			if string(prev[len(prev)-1]) != string(cur[0]) {
				t.Fatalf("rune boundary broken between chunk %d and %d", i-1, i)
			}
		}
		rebuilt.WriteString(c.content)
	}
	if got := rebuilt.String(); len([]rune(got)) < 10 {
		t.Fatalf("multibyte chunks lost content: %q", got)
	}
	// 全空白原文：无块产出（上游正文校验兜底）。
	if chunks := chunkText("   \n ", 4, 1); len(chunks) != 0 {
		t.Fatalf("blank content chunks = %+v, want none", chunks)
	}
}
