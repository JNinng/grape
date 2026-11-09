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

func newDocumentUsecaseForTest(store VectorStore, embedder Embedder) (*DocumentUsecase, error) {
	dim, err := embedder.Dimension(testEmbedderSpace)
	if err != nil {
		return nil, err
	}
	return NewDocumentUsecase(store, embedder, &stubResolver{space: testEmbedderSpace, dim: dim})
}

func ingestCtx() context.Context {
	return WithIdentity(context.Background(), Identity{TenantID: "t-doc", UserID: 5})
}

func TestDocumentUsecase_when_constructed_with_missing_dependency_fails(t *testing.T) {
	// 构造期只做依赖齐全性与切分参数校验：空间与维度的对账在写路径
	// 逐次执行（解析空间 → embedder 声明 → 存储注册表三方一致才落库）。
	for name, fn := range map[string]func() error{
		"nil store": func() error {
			_, err := NewDocumentUsecase(nil, &stubEmbedder{dims: 4}, &stubResolver{space: testEmbedderSpace, dim: 4})
			return err
		},
		"nil embedder": func() error {
			_, err := NewDocumentUsecase(&mockVectorStore{}, nil, &stubResolver{space: testEmbedderSpace, dim: 4})
			return err
		},
		"nil resolver": func() error {
			_, err := NewDocumentUsecase(&mockVectorStore{}, &stubEmbedder{dims: 4}, nil)
			return err
		},
		"bad chunking": func() error {
			_, err := NewDocumentUsecase(&mockVectorStore{}, &stubEmbedder{dims: 4}, &stubResolver{space: testEmbedderSpace, dim: 4}, WithChunking(10, 10))
			return err
		},
		"oversize delimiter": func() error {
			_, err := NewDocumentUsecase(&mockVectorStore{}, &stubEmbedder{dims: 4}, &stubResolver{space: testEmbedderSpace, dim: 4}, WithChunkDelimiter(strings.Repeat("-", maxChunkDelimiterRunes+1)))
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
	u, err := NewDocumentUsecase(store, embedder, &stubResolver{space: testEmbedderSpace, dim: 8}, WithChunking(8, 2))
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
	u, err := NewDocumentUsecase(store, embedder, resolver)
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
	u, err := NewDocumentUsecase(store, embedder, &stubResolver{space: testEmbedderSpace, dim: 4})
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
	u, err := NewDocumentUsecase(store, &stubEmbedder{dims: 4}, &stubResolver{space: testEmbedderSpace, dim: 4}, WithChunking(4, 1))
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
	u, err := NewDocumentUsecase(store, &stubEmbedder{dims: 4}, &stubResolver{space: testEmbedderSpace, dim: 4}, WithChunkDelimiter(""))
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
