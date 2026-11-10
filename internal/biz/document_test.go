package biz

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// testEmbedderSpace 是管线单测的固定嵌入空间键（stubResolver 解析产出）。
const testEmbedderSpace = "bge-m3@v1"

// stubEmbedder 是管线单测的替身 embedder：按文本长度派生确定性向量
// （首元素为 rune 数，其余补零），维度可指定；记录每次调用的空间键；
// stubErr 注入端点故障。
type stubEmbedder struct {
	dims    int
	stubErr error
	calls   []string
	spaces  []string
}

func (s *stubEmbedder) Embed(_ context.Context, embedder string, text string) ([]float32, error) {
	if s.stubErr != nil {
		return nil, s.stubErr
	}
	s.calls = append(s.calls, text)
	s.spaces = append(s.spaces, embedder)
	vec := make([]float32, s.dims)
	vec[0] = float32(len([]rune(text)))
	return vec, nil
}

func (s *stubEmbedder) Dimension(embedder string) (int, error) {
	if embedder == "" {
		return 0, ErrEmbedderUnknown
	}
	return s.dims, nil
}

// stubResolver 是管线单测的替身解析器：返回固定空间或注入错误。
type stubResolver struct {
	space string
	dim   int
	err   error
}

func (r *stubResolver) ResolveDefaultEmbedder(context.Context) (string, int, error) {
	if r.err != nil {
		return "", 0, r.err
	}
	return r.space, r.dim, nil
}

func (r *stubResolver) ResolveEmbeddingSpace(context.Context, string) (EmbeddingSpace, error) {
	if r.err != nil {
		return EmbeddingSpace{}, r.err
	}
	return EmbeddingSpace{Key: r.space, Dimension: r.dim}, nil
}

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
func (*mockVectorStore) UpdateMetadata(context.Context, *VectorFilter, map[string]any) (int64, error) {
	return 0, nil
}
func (*mockVectorStore) GetMetadata(context.Context, *VectorFilter) (map[string]map[string]any, error) {
	return nil, nil
}
func (*mockVectorStore) List(context.Context, *VectorFilter) ([]*VectorDocument, error) {
	return nil, nil
}

func newDocumentUsecaseForTest(store VectorStore, embedder Embedder) (*DocumentUsecase, error) {
	dim, err := embedder.Dimension(testEmbedderSpace)
	if err != nil {
		return nil, err
	}
	return NewDocumentUsecase(store, embedder, &stubResolver{space: testEmbedderSpace, dim: dim}, nil)
}

func ingestCtx() context.Context {
	return WithIdentity(context.Background(), Identity{TenantID: "t-doc", UserID: 5})
}

func TestDocumentUsecase_when_constructed_with_missing_dependency_fails(t *testing.T) {
	// 构造期只做依赖齐全性与切分参数校验：空间与维度的对账在写路径
	// 逐次执行（解析空间 → embedder 声明 → 存储注册表三方一致才落库）。
	for name, fn := range map[string]func() error{
		"nil store": func() error {
			_, err := NewDocumentUsecase(nil, &stubEmbedder{dims: 4}, &stubResolver{space: testEmbedderSpace, dim: 4}, nil)
			return err
		},
		"nil embedder": func() error {
			_, err := NewDocumentUsecase(&mockVectorStore{}, nil, &stubResolver{space: testEmbedderSpace, dim: 4}, nil)
			return err
		},
		"nil resolver": func() error {
			_, err := NewDocumentUsecase(&mockVectorStore{}, &stubEmbedder{dims: 4}, nil, nil)
			return err
		},
		"bad chunking": func() error {
			_, err := NewDocumentUsecase(&mockVectorStore{}, &stubEmbedder{dims: 4}, &stubResolver{space: testEmbedderSpace, dim: 4}, nil, WithChunking(10, 10))
			return err
		},
		"oversize delimiter": func() error {
			_, err := NewDocumentUsecase(&mockVectorStore{}, &stubEmbedder{dims: 4}, &stubResolver{space: testEmbedderSpace, dim: 4}, nil, WithChunkDelimiter(strings.Repeat("-", maxChunkDelimiterRunes+1)))
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
	u, err := NewDocumentUsecase(store, embedder, &stubResolver{space: testEmbedderSpace, dim: 8}, nil, WithChunking(8, 2))
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
		// 嵌入空间身份与维度成对落在每个切片上，embedder 调用同空间。
		if doc.Embedder != testEmbedderSpace || doc.Dimension != 8 {
			t.Fatalf("chunk %d space = %s/%d, want %s/8", i, doc.Embedder, doc.Dimension, testEmbedderSpace)
		}
		if embedder.spaces[i] != testEmbedderSpace {
			t.Fatalf("embed call %d space = %s, want %s", i, embedder.spaces[i], testEmbedderSpace)
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
	// 覆盖锚点先于写入：DeleteByFilter 以 source_id 为条件（跨空间清除，
	// 不限嵌入空间——换默认空间的重摄取正是迁移路径）。
	if store.deleted == nil || store.deleted.Metadata["source_id"] != "kb-1" {
		t.Fatalf("delete filter = %+v, want source_id kb-1", store.deleted)
	}
	if store.deleted.Embedder != "" {
		t.Fatalf("delete filter embedder = %s, want empty (cross-space)", store.deleted.Embedder)
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

func TestDocumentUsecase_when_resolver_fails_ingest_rejected_and_store_untouched(t *testing.T) {
	store := &mockVectorStore{}
	embedder := &stubEmbedder{dims: 4}
	resolver := &stubResolver{err: ErrEmbedderDisabled}
	u, err := NewDocumentUsecase(store, embedder, resolver, nil)
	if err != nil {
		t.Fatalf("NewDocumentUsecase: %v", err)
	}
	if _, err := u.Ingest(ingestCtx(), &Document{ID: "kb-1", Content: "content"}); !errors.Is(err, ErrEmbedderDisabled) {
		t.Fatalf("Ingest with failing resolver = %v, want ErrEmbedderDisabled", err)
	}
	// 空间解析先于删旧写新：解析失败不触存储。
	if len(store.added) != 0 || store.deleted != nil {
		t.Fatalf("store touched on resolver failure: added=%d deleted=%+v", len(store.added), store.deleted)
	}
}

func TestDocumentUsecase_when_embed_dimension_drifts_ingest_rejected(t *testing.T) {
	// 维度对账：resolver 声明 4 维而 embedder 产出 8 维向量 → 调用点
	// 显式报错（端点维度漂移不落库）。
	store := &mockVectorStore{}
	embedder := &stubEmbedder{dims: 8}
	u, err := NewDocumentUsecase(store, embedder, &stubResolver{space: testEmbedderSpace, dim: 4}, nil)
	if err != nil {
		t.Fatalf("NewDocumentUsecase: %v", err)
	}
	if _, err := u.Ingest(ingestCtx(), &Document{ID: "kb-1", Content: "content"}); err == nil || !strings.Contains(err.Error(), "dimension") {
		t.Fatalf("Ingest with drifting embedder = %v, want dimension error", err)
	}
	if len(store.added) != 0 || store.deleted != nil {
		t.Fatalf("store touched on dimension drift: added=%d deleted=%+v", len(store.added), store.deleted)
	}
}

func TestDocumentUsecase_when_embed_fails_keeps_store_untouched(t *testing.T) {
	store := &mockVectorStore{}
	embedder := &stubEmbedder{dims: 4, stubErr: errors.New("endpoint down")}
	u, err := newDocumentUsecaseForTest(store, embedder)
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
	u, err := NewDocumentUsecase(store, &stubEmbedder{dims: 4}, &stubResolver{space: testEmbedderSpace, dim: 4}, nil, WithChunking(4, 1))
	if err != nil {
		t.Fatalf("NewDocumentUsecase: %v", err)
	}
	if _, err := u.Ingest(ingestCtx(), &Document{ID: "kb-1", Content: strings.Repeat("x", 10)}); err == nil {
		t.Fatal("Ingest with mid-way Add failure = nil error, want error")
	}
}

func TestChunkText_without_delimiter_boundaries_overlap_and_multibyte(t *testing.T) {
	// 短文本：单块且保留原文。
	one := chunkText("短文", 10, 2, "")
	if len(one) != 1 || one[0].content != "短文" || one[0].start != 0 || one[0].end != 2 {
		t.Fatalf("short text chunks = %+v, want single original chunk", one)
	}
	// 恰好等于 size：单块。
	exact := chunkText(strings.Repeat("a", 8), 8, 2, "")
	if len(exact) != 1 {
		t.Fatalf("exact-size chunks = %d, want 1", len(exact))
	}
	// 超长：相邻块重叠 overlap 个 rune，末块推进到原文末尾。
	long := chunkText(strings.Repeat("x", 20), 8, 2, "")
	if len(long) != 3 {
		t.Fatalf("long chunks = %d, want 3", len(long))
	}
	if long[1].start != 6 || long[1].end != 14 || long[2].start != 12 || long[2].end != 20 {
		t.Fatalf("overlap windows drifted: %+v", long)
	}
	// overlap 0：不重叠。
	noOverlap := chunkText(strings.Repeat("x", 16), 8, 0, "")
	if len(noOverlap) != 2 || noOverlap[1].start != 8 {
		t.Fatalf("no-overlap chunks = %+v", noOverlap)
	}
	// 多字节 rune 不被劈开：中文内容切块后可无损拼回原文。
	cjk := chunkText(strings.Repeat("中", 10), 4, 1, "")
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
	if chunks := chunkText("   \n ", 4, 1, ""); len(chunks) != 0 {
		t.Fatalf("blank content chunks = %+v, want none", chunks)
	}
}

// chunkSpan 把切分产物折叠为 (content, start, end) 三元组，便于整体断言。
type chunkSpan struct {
	content    string
	start, end int
}

func chunkSpans(chunks []chunk) []chunkSpan {
	out := make([]chunkSpan, 0, len(chunks))
	for _, c := range chunks {
		out = append(out, chunkSpan{c.content, c.start, c.end})
	}
	return out
}

func assertChunkSpans(t *testing.T, got []chunk, want []chunkSpan) {
	t.Helper()
	if !reflect.DeepEqual(chunkSpans(got), want) {
		t.Fatalf("chunks = %+v, want %+v", chunkSpans(got), want)
	}
}

// assertAnchorsMapToOriginal 断言每块锚点区间切回原文恰为块正文（引用可追溯）。
func assertAnchorsMapToOriginal(t *testing.T, content string, chunks []chunk) {
	t.Helper()
	runes := []rune(content)
	for i, c := range chunks {
		if got := string(runes[c.start:c.end]); got != c.content {
			t.Fatalf("chunk %d anchor [%d,%d) = %q, want content %q", i, c.start, c.end, got, c.content)
		}
	}
}

func TestChunkText_with_delimiter_splits_paragraphs_and_drops_separator(t *testing.T) {
	content := "ab@@cd"
	got := chunkText(content, 10, 2, "@@")
	assertChunkSpans(t, got, []chunkSpan{{"ab", 0, 2}, {"cd", 4, 6}})
	assertAnchorsMapToOriginal(t, content, got)
}

func TestChunkText_with_consecutive_delimiters_drops_empty_blocks(t *testing.T) {
	got := chunkText("ab@@@@cd", 10, 2, "@@")
	assertChunkSpans(t, got, []chunkSpan{{"ab", 0, 2}, {"cd", 6, 8}})
}

func TestChunkText_with_whitespace_only_block_drops_it(t *testing.T) {
	got := chunkText("ab@@ \n @@cd", 10, 2, "@@")
	assertChunkSpans(t, got, []chunkSpan{{"ab", 0, 2}, {"cd", 9, 11}})
}

func TestChunkText_with_delimiter_at_head_and_tail_keeps_inner_block(t *testing.T) {
	got := chunkText("@@ab@@", 10, 2, "@@")
	assertChunkSpans(t, got, []chunkSpan{{"ab", 2, 4}})
}

func TestChunkText_with_oversize_paragraph_rewindows_with_overlap(t *testing.T) {
	content := strings.Repeat("x", 20) + "@@yy"
	got := chunkText(content, 8, 2, "@@")
	assertChunkSpans(t, got, []chunkSpan{
		{strings.Repeat("x", 8), 0, 8},
		{strings.Repeat("x", 8), 6, 14},
		{strings.Repeat("x", 8), 12, 20},
		{"yy", 22, 24},
	})
	assertAnchorsMapToOriginal(t, content, got)
}

func TestChunkText_with_paragraph_exactly_size_keeps_single_block(t *testing.T) {
	got := chunkText(strings.Repeat("x", 8)+"@@y", 8, 2, "@@")
	assertChunkSpans(t, got, []chunkSpan{{strings.Repeat("x", 8), 0, 8}, {"y", 10, 11}})
}

func TestChunkText_with_multi_rune_cjk_delimiter_matches_runes(t *testing.T) {
	content := "甲乙。。丙丁"
	got := chunkText(content, 10, 2, "。。")
	assertChunkSpans(t, got, []chunkSpan{{"甲乙", 0, 2}, {"丙丁", 4, 6}})
	assertAnchorsMapToOriginal(t, content, got)
}

func TestChunkText_with_partial_delimiter_match_keeps_text(t *testing.T) {
	got := chunkText("a--b---c", 10, 2, "---")
	assertChunkSpans(t, got, []chunkSpan{{"a--b", 0, 4}, {"c", 7, 8}})
}

func TestChunkText_with_default_paragraph_delimiter_splits_blank_lines(t *testing.T) {
	content := "第一段\n第一段续\n\n第二段"
	got := chunkText(content, 800, 100, defaultChunkDelimiter)
	assertChunkSpans(t, got, []chunkSpan{{"第一段\n第一段续", 0, 8}, {"第二段", 10, 13}})
	assertAnchorsMapToOriginal(t, content, got)
}

func TestChunkText_without_delimiter_ignores_paragraph_breaks(t *testing.T) {
	got := chunkText("ab\n\ncd", 10, 2, "")
	assertChunkSpans(t, got, []chunkSpan{{"ab\n\ncd", 0, 6}})
}

func TestChunkText_with_only_delimiters_produces_nothing(t *testing.T) {
	if got := chunkText("@@@@@@", 4, 1, "@@"); len(got) != 0 {
		t.Fatalf("chunks = %+v, want none", chunkSpans(got))
	}
}

func TestChunkText_with_delimiter_longer_than_content_keeps_whole_content(t *testing.T) {
	got := chunkText("ab", 10, 2, "@@@@@")
	assertChunkSpans(t, got, []chunkSpan{{"ab", 0, 2}})
}

func TestChunkText_with_absent_delimiter_and_oversize_content_uses_fixed_window(t *testing.T) {
	got := chunkText(strings.Repeat("x", 12), 8, 2, "@@")
	assertChunkSpans(t, got, []chunkSpan{{strings.Repeat("x", 8), 0, 8}, {strings.Repeat("x", 6), 6, 12}})
}

func TestChunkText_with_multibyte_text_before_ascii_delimiter_keeps_rune_offsets(t *testing.T) {
	content := "中文段落@@English"
	got := chunkText(content, 20, 2, "@@")
	assertChunkSpans(t, got, []chunkSpan{{"中文段落", 0, 4}, {"English", 6, 13}})
	assertAnchorsMapToOriginal(t, content, got)
}

func TestChunkText_with_mixed_content_anchors_map_to_original(t *testing.T) {
	content := "标题\n\n" + strings.Repeat("正文", 9) + "\n\n\n\n尾段 tail"
	assertAnchorsMapToOriginal(t, content, chunkText(content, 5, 1, defaultChunkDelimiter))
}

func TestDocChunkDelimiter_merges_default_and_override(t *testing.T) {
	empty, custom := "", "|"
	atLimit := strings.Repeat("|", maxChunkDelimiterRunes)
	oversize := strings.Repeat("|", maxChunkDelimiterRunes+1)
	for name, tc := range map[string]struct {
		override *string
		want     string
	}{
		"nil uses default":               {nil, "@@"},
		"empty disables":                 {&empty, ""},
		"custom overrides":               {&custom, "|"},
		"at limit overrides":             {&atLimit, atLimit},
		"oversize falls back to default": {&oversize, "@@"},
	} {
		if got := docChunkDelimiter("@@", tc.override); got != tc.want {
			t.Fatalf("%s: docChunkDelimiter = %q, want %q", name, got, tc.want)
		}
	}
}

func TestDocumentUsecase_ingest_without_delimiter_override_splits_on_default(t *testing.T) {
	store := &mockVectorStore{}
	u, err := newDocumentUsecaseForTest(store, &stubEmbedder{dims: 4})
	if err != nil {
		t.Fatalf("NewDocumentUsecase: %v", err)
	}
	if _, err := u.Ingest(ingestCtx(), &Document{ID: "kb-1", Content: "para one\n\npara two"}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if len(store.added) != 2 || store.added[0].Metadata["anchor"] != "runes 0-8" || store.added[1].Metadata["anchor"] != "runes 10-18" {
		t.Fatalf("added = %d chunks, want two paragraph chunks anchored 0-8 / 10-18", len(store.added))
	}
}

func TestDocumentUsecase_ingest_with_empty_delimiter_override_uses_fixed_window(t *testing.T) {
	store := &mockVectorStore{}
	u, err := newDocumentUsecaseForTest(store, &stubEmbedder{dims: 4})
	if err != nil {
		t.Fatalf("NewDocumentUsecase: %v", err)
	}
	empty := ""
	if _, err := u.Ingest(ingestCtx(), &Document{ID: "kb-1", Content: "para one\n\npara two", ChunkDelimiter: &empty}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if len(store.added) != 1 || store.added[0].Content != "para one\n\npara two" {
		t.Fatalf("added = %d chunks, want one fixed-window chunk", len(store.added))
	}
}

func TestDocumentUsecase_ingest_with_custom_delimiter_override_splits_on_it(t *testing.T) {
	store := &mockVectorStore{}
	u, err := newDocumentUsecaseForTest(store, &stubEmbedder{dims: 4})
	if err != nil {
		t.Fatalf("NewDocumentUsecase: %v", err)
	}
	custom := "|"
	if _, err := u.Ingest(ingestCtx(), &Document{ID: "kb-1", Content: "a|b|c", ChunkDelimiter: &custom}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if len(store.added) != 3 || store.added[2].Content != "c" {
		t.Fatalf("added = %d chunks, want three", len(store.added))
	}
}

func TestDocumentUsecase_ingest_with_oversize_delimiter_override_falls_back_to_default(t *testing.T) {
	store := &mockVectorStore{}
	u, err := newDocumentUsecaseForTest(store, &stubEmbedder{dims: 4})
	if err != nil {
		t.Fatalf("NewDocumentUsecase: %v", err)
	}
	oversize := strings.Repeat("|", maxChunkDelimiterRunes+1)
	if _, err := u.Ingest(ingestCtx(), &Document{ID: "kb-1", Content: "para one\n\npara two", ChunkDelimiter: &oversize}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if len(store.added) != 2 || store.added[1].Content != "para two" {
		t.Fatalf("added = %d chunks, want two default-delimiter chunks", len(store.added))
	}
}

func TestDocumentUsecase_constructed_with_empty_delimiter_uses_fixed_window(t *testing.T) {
	store := &mockVectorStore{}
	u, err := NewDocumentUsecase(store, &stubEmbedder{dims: 4}, &stubResolver{space: testEmbedderSpace, dim: 4}, nil, WithChunkDelimiter(""))
	if err != nil {
		t.Fatalf("NewDocumentUsecase: %v", err)
	}
	if _, err := u.Ingest(ingestCtx(), &Document{ID: "kb-1", Content: "para one\n\npara two"}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if len(store.added) != 1 {
		t.Fatalf("added = %d chunks, want one", len(store.added))
	}
}

func TestDocumentUsecase_ingest_with_only_delimiters_rejected(t *testing.T) {
	u, err := newDocumentUsecaseForTest(&mockVectorStore{}, &stubEmbedder{dims: 4})
	if err != nil {
		t.Fatalf("NewDocumentUsecase: %v", err)
	}
	custom := "@@"
	if _, err := u.Ingest(ingestCtx(), &Document{ID: "kb-1", Content: "@@@@", ChunkDelimiter: &custom}); err == nil || !strings.Contains(err.Error(), "chunks to nothing") {
		t.Fatalf("Ingest = %v, want chunks to nothing rejection", err)
	}
}

// markdownPaths 折叠切分产物为 (content, headingPath) 序列，便于断言标题链。
func markdownPaths(chunks []chunk) []string {
	out := make([]string, 0, len(chunks))
	for _, c := range chunks {
		out = append(out, c.headingPath)
	}
	return out
}

func TestChunkMarkdown_splits_atx_sections_with_heading_path(t *testing.T) {
	content := "# 部署\n\n前言\n\n## 数据库\n\n正文一\n\n## 数据库升级\n\n正文二\n\n### 升级注意\n\n正文三"
	got := chunkMarkdown(content, 800, 100)
	if len(got) != 4 {
		t.Fatalf("chunks = %d, want 4", len(got))
	}
	wantPaths := []string{"部署", "部署 > 数据库", "部署 > 数据库升级", "部署 > 数据库升级 > 升级注意"}
	if !reflect.DeepEqual(markdownPaths(got), wantPaths) {
		t.Fatalf("heading paths = %v, want %v", markdownPaths(got), wantPaths)
	}
	// 节含节头标题行；锚点切回原文。
	if got[0].content != "# 部署\n\n前言" || got[0].start != 0 {
		t.Fatalf("chunk 0 = %q [%d,%d), want heading section from document head", got[0].content, got[0].start, got[0].end)
	}
	if !strings.HasPrefix(got[1].content, "## 数据库") || strings.Contains(got[1].content, "前言") {
		t.Fatalf("chunk 1 = %q, want section with own heading and no previous content", got[1].content)
	}
	assertAnchorsMapToOriginal(t, content, got)
}

func TestChunkMarkdown_recognizes_setext_headings(t *testing.T) {
	// 单行文本 + ===（一级）/ ---（二级）下划线；标题行与下划线都留在节内，
	// --- 作为二级标题挂到上一级标题链之后。
	content := "概述\n=====\n\n正文\n\n步骤说明\n---\n\n尾段"
	got := chunkMarkdown(content, 800, 100)
	if len(got) != 2 {
		t.Fatalf("chunks = %d, want 2", len(got))
	}
	if !reflect.DeepEqual(markdownPaths(got), []string{"概述", "概述 > 步骤说明"}) {
		t.Fatalf("heading paths = %v, want 概述 / 概述 > 步骤说明", markdownPaths(got))
	}
	if !strings.HasPrefix(got[0].content, "概述\n=====") {
		t.Fatalf("chunk 0 = %q, want setext heading lines kept in section", got[0].content)
	}
	if !strings.HasPrefix(got[1].content, "步骤说明\n---") {
		t.Fatalf("chunk 1 = %q, want setext level-2 heading lines kept", got[1].content)
	}
	assertAnchorsMapToOriginal(t, content, got)
}

func TestChunkMarkdown_multiline_paragraph_becomes_setext_heading(t *testing.T) {
	// 多行段落 + 下划线：整段为标题文本，段内行以空格拼接。
	content := "foo\nbar\n===\n\nbody"
	got := chunkMarkdown(content, 800, 100)
	if len(got) != 1 || got[0].headingPath != "foo bar" {
		t.Fatalf("chunks = %+v, want single section with path 'foo bar'", markdownPaths(got))
	}
}

func TestChunkMarkdown_crlf_input_recognizes_setext_headings(t *testing.T) {
	// CRLF 行尾不破坏 Setext 判定：下划线行的 '\r' 不入行文本，===/---
	// 识别与 LF 输入一致；标题行与下划线行留在节内，'\r' 不入行区间。
	content := "概述\r\n=====\r\n\r\n正文\r\n\r\n步骤说明\r\n---\r\n\r\n尾段"
	got := chunkMarkdown(content, 800, 100)
	if len(got) != 2 || !reflect.DeepEqual(markdownPaths(got), []string{"概述", "概述 > 步骤说明"}) {
		t.Fatalf("chunks = %d paths = %v, want 概述 / 概述 > 步骤说明 under CRLF", len(got), markdownPaths(got))
	}
	if !strings.HasPrefix(got[0].content, "概述\r\n=====") {
		t.Fatalf("chunk 0 = %q, want setext heading lines kept in section", got[0].content)
	}
	assertAnchorsMapToOriginal(t, content, got)
}

func TestChunkMarkdown_underline_without_preceding_paragraph_stays_text(t *testing.T) {
	// 标题行后直接跟下划线、或下划线在文档开头：不构成 Setext，按普通正文。
	content := "# A\n===\n\ntail"
	got := chunkMarkdown(content, 800, 100)
	if len(got) != 1 || got[0].headingPath != "A" {
		t.Fatalf("chunks = %+v, want single section A with === as plain text", markdownPaths(got))
	}
	if !strings.Contains(got[0].content, "===") {
		t.Fatalf("chunk content = %q, want === kept", got[0].content)
	}
}

func TestChunkMarkdown_content_before_first_heading_gets_empty_path(t *testing.T) {
	content := "\n\n开场白\n\n# A\n\n正文"
	got := chunkMarkdown(content, 800, 100)
	if len(got) != 2 {
		t.Fatalf("chunks = %d, want 2", len(got))
	}
	if got[0].headingPath != "" || !strings.Contains(got[0].content, "开场白") {
		t.Fatalf("chunk 0 = %q path %q, want pre-heading section with empty path", got[0].content, got[0].headingPath)
	}
	if got[1].headingPath != "A" {
		t.Fatalf("chunk 1 path = %q, want A", got[1].headingPath)
	}
}

func TestChunkMarkdown_without_headings_keeps_single_pathless_section(t *testing.T) {
	content := "甲\n\n乙"
	got := chunkMarkdown(content, 800, 100)
	if len(got) != 1 || got[0].content != "甲\n\n乙" || got[0].headingPath != "" {
		t.Fatalf("chunks = %+v, want one pathless section", got)
	}
	assertAnchorsMapToOriginal(t, content, got)
}

func TestChunkMarkdown_oversize_section_repacks_at_block_boundaries(t *testing.T) {
	// 超长节回退块级装箱：块边界切分、不重叠，每块不超过窗口；锚点为原文区间。
	para := strings.Repeat("x", 10)
	content := "# A\n\n" + para + "\n\n" + para + "\n\n" + para + "\n\n" + para
	got := chunkMarkdown(content, 16, 4)
	if len(got) < 3 {
		t.Fatalf("chunks = %d, want repacked blocks", len(got))
	}
	for i, c := range got {
		if c.end-c.start > 16 {
			t.Fatalf("chunk %d spans %d runes, want <= size", i, c.end-c.start)
		}
		if c.headingPath != "A" {
			t.Fatalf("chunk %d path = %q, want A", i, c.headingPath)
		}
	}
	// 块边界切分不重叠：相邻块切片互不重叠。
	for i := 1; i < len(got); i++ {
		if got[i].start < got[i-1].end {
			t.Fatalf("chunk %d [%d,%d) overlaps previous [%d,%d)", i, got[i].start, got[i].end, got[i-1].start, got[i-1].end)
		}
	}
	assertAnchorsMapToOriginal(t, content, got)
}

func TestChunkMarkdown_keeps_fenced_code_block_atomic(t *testing.T) {
	// 节超长但代码块不超过窗口：代码块整体装箱，不被从中间切开。
	code := "```\nline one\n\nline two\n```"
	content := "# A\n\n" + code + "\n\n" + strings.Repeat("y", 30)
	got := chunkMarkdown(content, 40, 4)
	whole := false
	for _, c := range got {
		if strings.Contains(c.content, code) {
			whole = true
		}
	}
	if !whole {
		t.Fatalf("chunks = %+v, want fenced block kept whole in one chunk", got)
	}
	assertAnchorsMapToOriginal(t, content, got)
}

func TestChunkMarkdown_ignores_headings_inside_fences(t *testing.T) {
	content := "# A\n\n```md\n# not a heading\n```\n\ntail"
	got := chunkMarkdown(content, 800, 100)
	if len(got) != 1 || got[0].headingPath != "A" {
		t.Fatalf("chunks = %+v, want single section A (fenced # not a heading)", markdownPaths(got))
	}
}

func TestChunkMarkdown_crlf_input_closes_fenced_code_block(t *testing.T) {
	// CRLF 围栏闭行（``` + 行尾 '\r'）可闭合围栏：其后标题恢复识别，
	// 不被未闭合围栏吞掉。
	content := "# A\r\n\r\n```md\r\n# not a heading\r\n```\r\n\r\n## B\r\n\r\ntail"
	got := chunkMarkdown(content, 800, 100)
	if len(got) != 2 || !reflect.DeepEqual(markdownPaths(got), []string{"A", "A > B"}) {
		t.Fatalf("chunks = %d paths = %v, want sections A / A > B (fence closed under CRLF)", len(got), markdownPaths(got))
	}
	assertAnchorsMapToOriginal(t, content, got)
}

func TestChunkMarkdown_keeps_table_atomic(t *testing.T) {
	table := "| a | b |\n|---|---|\n| 1 | 2 |"
	content := "# T\n\n" + table + "\n\n" + strings.Repeat("z", 30)
	got := chunkMarkdown(content, 40, 4)
	whole := false
	for _, c := range got {
		if strings.Contains(c.content, table) {
			whole = true
		}
	}
	if !whole {
		t.Fatalf("chunks = %+v, want table kept whole in one chunk", got)
	}
	assertAnchorsMapToOriginal(t, content, got)
}

func TestChunkMarkdown_windows_oversize_atomic_block(t *testing.T) {
	// 原子块自身超过窗口：回退固定窗口切开（嵌入输入上限优先于原子性），
	// 窗口重叠 overlap 个 rune。
	code := "```\n" + strings.Repeat("c", 30) + "\n```"
	content := "# A\n\n" + code
	got := chunkMarkdown(content, 12, 3)
	if len(got) < 3 {
		t.Fatalf("chunks = %d, want windows over the oversize fence", len(got))
	}
	for i, c := range got {
		if c.end-c.start > 12 {
			t.Fatalf("chunk %d spans %d runes, want <= size", i, c.end-c.start)
		}
		if c.headingPath != "A" {
			t.Fatalf("chunk %d path = %q, want A", i, c.headingPath)
		}
	}
	assertAnchorsMapToOriginal(t, content, got)
}

func TestChunkMarkdown_multibyte_anchors_map_to_original(t *testing.T) {
	content := "# 中文标题\n\n中文段落一\n\n## 子标题\n\n中文段落二\n\n```\n代码块中文\n```"
	assertAnchorsMapToOriginal(t, content, chunkMarkdown(content, 5, 1))
}

func TestChunkMarkdown_crlf_input_anchors_map_to_original(t *testing.T) {
	// CRLF 全要素（ATX/Setext/围栏/表格 + 超长节重装箱）下锚点不变式：
	// 每块 content 恒等于原文按锚点区间的 rune 切片。
	content := "# 标题\r\n\r\n" + strings.Repeat("段落", 10) + "\r\n\r\n概述\r\n=====\r\n\r\n```go\r\ncode\r\n```\r\n\r\n| a | b |\r\n|---|---|\r\n| 1 | 2 |\r\n\r\n尾段"
	assertAnchorsMapToOriginal(t, content, chunkMarkdown(content, 12, 3))
}

func TestChunkMarkdown_empty_atx_heading_skipped_in_heading_path(t *testing.T) {
	// 空 ATX 标题（###### 后无文字）只切节、不入标题链：路径无悬空
	// " > " 分隔符，节跟随外层标题链。
	content := "# A\n\n######\n\nbody\n\n## B\n\ntail"
	got := chunkMarkdown(content, 800, 100)
	if len(got) != 3 || !reflect.DeepEqual(markdownPaths(got), []string{"A", "A", "A > B"}) {
		t.Fatalf("chunks = %d paths = %v, want paths A / A / A > B without dangling separator", len(got), markdownPaths(got))
	}
	assertAnchorsMapToOriginal(t, content, got)
}

func TestChunkByKind_dispatches_by_document_kind(t *testing.T) {
	content := "# A\n\nbody"
	md, err := chunkByKind(DocumentKindMarkdown, content, 800, 100, "\n\n")
	if err != nil {
		t.Fatalf("chunkByKind(markdown): %v", err)
	}
	if len(md) != 1 || md[0].headingPath != "A" || md[0].content != content {
		t.Fatalf("markdown chunks = %+v, want single headed section", md)
	}
	// text（含空值）走区分串切分，锚点同形。
	for _, kind := range []string{"", DocumentKindText} {
		text, err := chunkByKind(kind, "para one\n\npara two", 800, 100, "\n\n")
		if err != nil {
			t.Fatalf("chunkByKind(%q): %v", kind, err)
		}
		if len(text) != 2 || text[0].content != "para one" || text[0].headingPath != "" {
			t.Fatalf("text chunks = %+v, want paragraph split without heading path", text)
		}
	}
	// file 的正文是解析产物 markdown，切分口径同 markdown。
	file, err := chunkByKind(DocumentKindFile, content, 800, 100, "\n\n")
	if err != nil {
		t.Fatalf("chunkByKind(file): %v", err)
	}
	if len(file) != 1 || file[0].headingPath != "A" {
		t.Fatalf("file chunks = %+v, want markdown-shaped chunks", file)
	}
	if _, err := chunkByKind("html", content, 800, 100, "\n\n"); err == nil || !strings.Contains(err.Error(), "unknown document kind") {
		t.Fatalf("chunkByKind(html) = %v, want unknown kind rejection", err)
	}
}

func TestDocumentUsecase_markdown_ingest_carries_heading_path_metadata(t *testing.T) {
	store := &mockVectorStore{}
	u, err := newDocumentUsecaseForTest(store, &stubEmbedder{dims: 4})
	if err != nil {
		t.Fatalf("NewDocumentUsecase: %v", err)
	}
	if _, err := u.Ingest(ingestCtx(), &Document{
		ID:      "kb-1",
		Kind:    DocumentKindMarkdown,
		Name:    "handbook",
		Content: "# 部署\n\n前言\n\n## 数据库\n\n正文",
	}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if len(store.added) != 2 {
		t.Fatalf("chunks = %d, want 2", len(store.added))
	}
	if store.added[0].Metadata["heading_path"] != "部署" || store.added[1].Metadata["heading_path"] != "部署 > 数据库" {
		t.Fatalf("heading paths = %v / %v, want 部署 / 部署 > 数据库",
			store.added[0].Metadata["heading_path"], store.added[1].Metadata["heading_path"])
	}
}

func TestDocumentUsecase_text_ingest_keeps_metadata_without_heading_path(t *testing.T) {
	store := &mockVectorStore{}
	u, err := newDocumentUsecaseForTest(store, &stubEmbedder{dims: 4})
	if err != nil {
		t.Fatalf("NewDocumentUsecase: %v", err)
	}
	if _, err := u.Ingest(ingestCtx(), &Document{ID: "kb-1", Content: "para one\n\npara two"}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	for i, doc := range store.added {
		if _, ok := doc.Metadata["heading_path"]; ok {
			t.Fatalf("chunk %d carries heading_path, want text metadata shape unchanged", i)
		}
	}
}

// stubCompleter 是管线单测的替身补全器：记录每次 prompt 调用，按固定
// 文本应答或注入错误/空白产出（验证降级路径）。failFrom 指定从第几次
// （1 起）调用开始报错，0 表示只要 err 非空就次次报错，用于模拟中途失败。
type stubCompleter struct {
	text     string
	err      error
	failFrom int
	prompts  []string
}

func (s *stubCompleter) Complete(_ context.Context, prompt string) (string, error) {
	s.prompts = append(s.prompts, prompt)
	if s.err != nil && (s.failFrom <= 0 || len(s.prompts) >= s.failFrom) {
		return "", s.err
	}
	return s.text, nil
}

func newContextualUsecaseForTest(store VectorStore, embedder Embedder, completer Completer) (*DocumentUsecase, error) {
	return NewDocumentUsecase(store, embedder, &stubResolver{space: testEmbedderSpace, dim: 8}, completer, WithChunking(8, 2))
}

func TestDocumentUsecase_when_contextual_enabled_embeds_prefixed_input_only(t *testing.T) {
	store := &mockVectorStore{}
	embedder := &stubEmbedder{dims: 8}
	completer := &stubCompleter{text: "部署手册章节"}
	u, err := newContextualUsecaseForTest(store, embedder, completer)
	if err != nil {
		t.Fatalf("NewDocumentUsecase: %v", err)
	}
	content := strings.Repeat("字", 20)
	result, err := u.Ingest(ingestCtx(), &Document{ID: "kb-1", Name: "handbook", Content: content, Contextual: true})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if result.Chunks != 3 {
		t.Fatalf("chunks = %d, want 3", result.Chunks)
	}
	// 成本控制：文档级摘要一次，逐块前缀共享（1 + N 次调用）。
	if len(completer.prompts) != 4 {
		t.Fatalf("completer calls = %d, want 4 (1 summary + 3 chunks)", len(completer.prompts))
	}
	if !strings.Contains(completer.prompts[0], "handbook") || !strings.Contains(completer.prompts[0], content) {
		t.Fatalf("summary prompt = %q, want document name and content", completer.prompts[0])
	}
	if !strings.Contains(completer.prompts[1], "部署手册章节") {
		t.Fatalf("chunk prompt = %q, want shared document summary", completer.prompts[1])
	}
	// 前缀只拼进 embed 输入：向量化输入带前缀，落库正文/锚点保持切分原样。
	for i, doc := range store.added {
		if !strings.HasPrefix(embedder.calls[i], "部署手册章节\n\n") || !strings.HasSuffix(embedder.calls[i], doc.Content) {
			t.Fatalf("embed input %d = %q, want prefix + raw chunk %q", i, embedder.calls[i], doc.Content)
		}
		if doc.Content == embedder.calls[i] || strings.Contains(doc.Content, "部署手册章节") {
			t.Fatalf("stored content %d = %q, want raw chunk without prefix", i, doc.Content)
		}
		anchor, _ := doc.Metadata["anchor"].(string)
		if !strings.HasPrefix(anchor, "runes ") {
			t.Fatalf("chunk %d anchor = %v, want rune range unchanged", i, doc.Metadata["anchor"])
		}
	}
}

func TestDocumentUsecase_when_completer_fails_ingest_degrades_without_prefix(t *testing.T) {
	store := &mockVectorStore{}
	embedder := &stubEmbedder{dims: 8}
	completer := &stubCompleter{err: errors.New("endpoint down")}
	u, err := newContextualUsecaseForTest(store, embedder, completer)
	if err != nil {
		t.Fatalf("NewDocumentUsecase: %v", err)
	}
	result, err := u.Ingest(ingestCtx(), &Document{ID: "kb-1", Name: "handbook", Content: strings.Repeat("字", 20), Contextual: true})
	if err != nil {
		t.Fatalf("Ingest with failing completer = %v, want degraded success", err)
	}
	if result.Chunks != 3 || len(store.added) != 3 {
		t.Fatalf("degraded ingest = %d chunks, want 3", result.Chunks)
	}
	// 摘要失败即整篇降级：不再发起逐块调用。
	if len(completer.prompts) != 1 {
		t.Fatalf("completer calls = %d, want 1 (summary only)", len(completer.prompts))
	}
	for i, doc := range store.added {
		if doc.Content != embedder.calls[i] {
			t.Fatalf("chunk %d embed input %q != stored content %q, want raw content without prefix", i, embedder.calls[i], doc.Content)
		}
	}
}

func TestDocumentUsecase_when_chunk_prefix_call_fails_whole_document_degrades_without_prefix(t *testing.T) {
	store := &mockVectorStore{}
	embedder := &stubEmbedder{dims: 8}
	// 第 1 次（摘要）成功应答，第 2 次起的块级调用一律报错。
	completer := &stubCompleter{text: "部署手册章节", err: errors.New("chunk endpoint down"), failFrom: 2}
	u, err := newContextualUsecaseForTest(store, embedder, completer)
	if err != nil {
		t.Fatalf("NewDocumentUsecase: %v", err)
	}
	result, err := u.Ingest(ingestCtx(), &Document{ID: "kb-1", Name: "handbook", Content: strings.Repeat("字", 20), Contextual: true})
	if err != nil {
		t.Fatalf("Ingest with chunk-level completer failure = %v, want degraded success", err)
	}
	if result.Chunks != 3 || len(store.added) != 3 {
		t.Fatalf("degraded ingest = %d chunks, want 3", result.Chunks)
	}
	// 块级失败同样整篇降级：不再发起后续块级调用（1 次摘要 + 1 次首个块级调用）。
	if len(completer.prompts) != 2 {
		t.Fatalf("completer calls = %d, want 2 (1 summary + 1 chunk before abort)", len(completer.prompts))
	}
	for i, doc := range store.added {
		if doc.Content != embedder.calls[i] {
			t.Fatalf("chunk %d embed input %q != stored content %q, want raw content without prefix", i, embedder.calls[i], doc.Content)
		}
	}
}

func TestDocumentUsecase_when_contextual_disabled_completer_not_called(t *testing.T) {
	store := &mockVectorStore{}
	embedder := &stubEmbedder{dims: 8}
	// 用必失败的替身守卫：开关关闭时任何补全调用都会留下痕迹。
	completer := &stubCompleter{err: errors.New("must not be called")}
	u, err := newContextualUsecaseForTest(store, embedder, completer)
	if err != nil {
		t.Fatalf("NewDocumentUsecase: %v", err)
	}
	if _, err := u.Ingest(ingestCtx(), &Document{ID: "kb-1", Content: strings.Repeat("字", 20)}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if len(completer.prompts) != 0 {
		t.Fatalf("completer calls = %d, want 0 when switch is off", len(completer.prompts))
	}
	if len(embedder.calls) != 3 {
		t.Fatalf("embeds = %d, want 3", len(embedder.calls))
	}
}

func TestDocumentUsecase_when_completer_missing_degrades_silently(t *testing.T) {
	store := &mockVectorStore{}
	embedder := &stubEmbedder{dims: 8}
	u, err := newContextualUsecaseForTest(store, embedder, nil)
	if err != nil {
		t.Fatalf("NewDocumentUsecase: %v", err)
	}
	result, err := u.Ingest(ingestCtx(), &Document{ID: "kb-1", Content: strings.Repeat("字", 20), Contextual: true})
	if err != nil {
		t.Fatalf("Ingest without completer = %v, want degraded success", err)
	}
	if result.Chunks != 3 {
		t.Fatalf("chunks = %d, want 3", result.Chunks)
	}
	for i, doc := range store.added {
		if doc.Content != embedder.calls[i] {
			t.Fatalf("chunk %d embed input carries prefix, want raw content", i)
		}
	}
}

func TestDocumentUsecase_when_completer_returns_blank_degrades_without_prefix(t *testing.T) {
	store := &mockVectorStore{}
	embedder := &stubEmbedder{dims: 8}
	completer := &stubCompleter{text: "   \n  "}
	u, err := newContextualUsecaseForTest(store, embedder, completer)
	if err != nil {
		t.Fatalf("NewDocumentUsecase: %v", err)
	}
	result, err := u.Ingest(ingestCtx(), &Document{ID: "kb-1", Content: strings.Repeat("字", 20), Contextual: true})
	if err != nil {
		t.Fatalf("Ingest with blank completion = %v, want degraded success", err)
	}
	if result.Chunks != 3 {
		t.Fatalf("chunks = %d, want 3", result.Chunks)
	}
	for i, doc := range store.added {
		if doc.Content != embedder.calls[i] {
			t.Fatalf("chunk %d embed input %q != raw content %q", i, embedder.calls[i], doc.Content)
		}
	}
}

func TestSanitizeContextText_trims_folds_and_caps_runes(t *testing.T) {
	// 首尾空白与配对引号剥离、内部换行折叠为单空格。
	got := sanitizeContextText("  “第一行\n第二行”  ", 100)
	if got != "第一行 第二行" {
		t.Fatalf("sanitize = %q, want folded single-line text", got)
	}
	// 超长按 rune 边界截断，多字节字符不被劈开。
	long := strings.Repeat("中", 50)
	if got := sanitizeContextText(long, 10); got != strings.Repeat("中", 10) {
		t.Fatalf("truncate = %q, want 10 runes", got)
	}
	// 纯空白产出按降级口径折叠为空串。
	if got := sanitizeContextText("\n\t ", 10); got != "" {
		t.Fatalf("blank sanitize = %q, want empty", got)
	}
}

func TestDocumentSummaryPrompt_truncates_document_body(t *testing.T) {
	prompt := documentSummaryPrompt(&Document{ID: "kb-1", Name: "handbook", Content: strings.Repeat("字", maxContextDocRunes+50)})
	if !strings.Contains(prompt, "handbook") {
		t.Fatalf("prompt = %q, want document name", prompt)
	}
	if strings.Contains(prompt, strings.Repeat("字", maxContextDocRunes+50)) {
		t.Fatalf("prompt carries untruncated body, want capped at %d runes", maxContextDocRunes)
	}
	if !strings.Contains(prompt, strings.Repeat("字", maxContextDocRunes)) {
		t.Fatalf("prompt = %q, want truncated body present", prompt)
	}
}
