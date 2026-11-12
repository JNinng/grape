package biz

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// searchTestNow 是检索单测的固定时钟（有效期条件可精确断言）。
var searchTestNow = time.Date(2026, 11, 9, 8, 0, 0, 0, time.UTC)

// recordingSearchStore 是检索用例单测的向量存储替身：记录 Search /
// SearchLexical 入参与 ctx 留痕事实，返回可编程结果；其余方法沿用
// mockVectorStore。词法路默认返回空切片（nil results），可编程注入。
type recordingSearchStore struct {
	mockVectorStore
	results    []*ScoredVectorDocument
	err        error
	lexical    []*ScoredVectorDocument
	lexicalErr error

	calls        int // 向量路调用次数
	lexicalCalls int
	vector       []float32
	filter       *VectorFilter
	queries      []string // 词法路收到的查询文本序列
	query        string
	source       string
	session      string
}

func (s *recordingSearchStore) Search(ctx context.Context, embedding []float32, filter *VectorFilter) ([]*ScoredVectorDocument, error) {
	s.calls++
	s.vector = embedding
	s.filter = filter
	s.query = RetrievalQueryFromContext(ctx)
	s.source = RetrievalSourceFromContext(ctx)
	s.session = SessionIDFromContext(ctx)
	return s.results, s.err
}

func (s *recordingSearchStore) SearchLexical(ctx context.Context, query string, filter *VectorFilter) ([]*ScoredVectorDocument, error) {
	s.lexicalCalls++
	s.queries = append(s.queries, query)
	_ = filter
	if s.lexicalErr != nil {
		return nil, s.lexicalErr
	}
	return s.lexical, nil
}

// stubLibraryDirectory 是可见库集合替身：固定返回库列表或错误，记录调用
// 次数（现场解析断言）。
type stubLibraryDirectory struct {
	libraries []*KnowledgeLibrary
	err       error
	calls     int
}

func (d *stubLibraryDirectory) VisibleLibraries(context.Context, Identity) ([]*KnowledgeLibrary, error) {
	d.calls++
	if d.err != nil {
		return nil, d.err
	}
	return d.libraries, nil
}

// defaultSearchLibraries 是检索单测的缺省可见库集合。
func defaultSearchLibraries() *stubLibraryDirectory {
	return &stubLibraryDirectory{libraries: []*KnowledgeLibrary{{ID: 7}, {ID: 8}}}
}

func newSearchUsecaseForTest(t *testing.T, store VectorStore, embedder Embedder, resolver EmbedderResolver, libraries KnowledgeLibraryDirectory) *KnowledgeSearchUsecase {
	t.Helper()
	uc, err := NewKnowledgeSearchUsecase(store, embedder, resolver, libraries)
	if err != nil {
		t.Fatalf("NewKnowledgeSearchUsecase: %v", err)
	}
	uc.now = func() time.Time { return searchTestNow }
	return uc
}

func searchCtx() context.Context {
	return WithIdentity(context.Background(), Identity{TenantID: "t-search", UserID: 9})
}

func TestNewKnowledgeSearchUsecase_missing_dependency_returns_error(t *testing.T) {
	resolver := &stubResolver{space: testEmbedderSpace, dim: 4}
	for name, fn := range map[string]func() error{
		"nil store": func() error {
			_, err := NewKnowledgeSearchUsecase(nil, &stubEmbedder{dims: 4}, resolver, defaultSearchLibraries())
			return err
		},
		"nil embedder": func() error {
			_, err := NewKnowledgeSearchUsecase(&recordingSearchStore{}, nil, resolver, defaultSearchLibraries())
			return err
		},
		"nil resolver": func() error {
			_, err := NewKnowledgeSearchUsecase(&recordingSearchStore{}, &stubEmbedder{dims: 4}, nil, defaultSearchLibraries())
			return err
		},
		"nil library directory": func() error {
			_, err := NewKnowledgeSearchUsecase(&recordingSearchStore{}, &stubEmbedder{dims: 4}, resolver, nil)
			return err
		},
	} {
		if err := fn(); err == nil {
			t.Fatalf("NewKnowledgeSearchUsecase(%s) = nil error, want rejection", name)
		}
	}
}

func TestKnowledgeSearch_identity_missing_returns_session_invalid(t *testing.T) {
	store := &recordingSearchStore{}
	libraries := defaultSearchLibraries()
	uc := newSearchUsecaseForTest(t, store, &stubEmbedder{dims: 4}, &stubResolver{space: testEmbedderSpace, dim: 4}, libraries)

	_, err := uc.Search(context.Background(), &KnowledgeSearchQuery{Query: "grape"})
	if !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("Search without identity = %v, want ErrSessionInvalid", err)
	}
	if store.calls != 0 || libraries.calls != 0 {
		t.Fatalf("store/library calls = %d/%d, want 0/0", store.calls, libraries.calls)
	}
}

func TestKnowledgeSearch_invalid_input_returns_invalid_argument(t *testing.T) {
	for name, q := range map[string]*KnowledgeSearchQuery{
		"nil query":        nil,
		"empty query":      {Query: ""},
		"blank query":      {Query: " \t\n "},
		"oversize query":   {Query: strings.Repeat("检", MaxKnowledgeSearchQueryRunes+1)},
		"negative top_k":   {Query: "grape", TopK: -1},
		"negative library": {Query: "grape", LibraryID: -1},
	} {
		t.Run(name, func(t *testing.T) {
			store := &recordingSearchStore{}
			embedder := &stubEmbedder{dims: 4}
			libraries := defaultSearchLibraries()
			uc := newSearchUsecaseForTest(t, store, embedder, &stubResolver{space: testEmbedderSpace, dim: 4}, libraries)

			_, err := uc.Search(searchCtx(), q)
			if !errors.Is(err, ErrKnowledgeSearchInvalidArgument) {
				t.Fatalf("Search = %v, want ErrKnowledgeSearchInvalidArgument", err)
			}
			if store.calls != 0 || len(embedder.calls) != 0 || libraries.calls != 0 {
				t.Fatalf("store/embed/library calls = %d/%d/%d, want 0/0/0 (validate before resolution)",
					store.calls, len(embedder.calls), libraries.calls)
			}
		})
	}
}

func TestKnowledgeSearch_query_at_rune_limit_embeds_full_text(t *testing.T) {
	embedder := &stubEmbedder{dims: 4}
	uc := newSearchUsecaseForTest(t, &recordingSearchStore{}, embedder, &stubResolver{space: testEmbedderSpace, dim: 4}, defaultSearchLibraries())
	query := strings.Repeat("检", MaxKnowledgeSearchQueryRunes)

	if _, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: query}); err != nil {
		t.Fatalf("Search(512 runes) = %v, want accepted", err)
	}
	if !reflect.DeepEqual(embedder.calls, []string{query}) {
		t.Fatalf("embedded %d texts, want the full 512-rune query once", len(embedder.calls))
	}
}

func TestKnowledgeSearch_top_k_maps_to_positive_clamped_limit(t *testing.T) {
	for _, tc := range []struct {
		name string
		topK int
		want int
	}{
		{"omitted uses default", 0, DefaultKnowledgeSearchTopK},
		{"minimum kept", 1, 1},
		{"within range kept", 7, 7},
		{"at max kept", MaxKnowledgeSearchTopK, MaxKnowledgeSearchTopK},
		{"above max clamped", MaxKnowledgeSearchTopK + 1, MaxKnowledgeSearchTopK},
		{"far above max clamped", 1000, MaxKnowledgeSearchTopK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &recordingSearchStore{}
			uc := newSearchUsecaseForTest(t, store, &stubEmbedder{dims: 4}, &stubResolver{space: testEmbedderSpace, dim: 4}, defaultSearchLibraries())

			if _, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape", TopK: tc.topK}); err != nil {
				t.Fatalf("Search: %v", err)
			}
			// 融合只看排名：每路各取 2×top_k 的宽召回。
			want := tc.want * KnowledgeSearchRecallFactor
			if store.filter.Limit != want {
				t.Fatalf("vector filter limit = %d, want %d", store.filter.Limit, want)
			}
			if store.lexicalCalls != 1 {
				t.Fatalf("lexical calls = %d, want 1", store.lexicalCalls)
			}
		})
	}
}

func TestKnowledgeSearch_embeds_trimmed_query_in_default_space(t *testing.T) {
	store := &recordingSearchStore{}
	embedder := &stubEmbedder{dims: 4}
	uc := newSearchUsecaseForTest(t, store, embedder, &stubResolver{space: testEmbedderSpace, dim: 4}, defaultSearchLibraries())

	if _, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "  年假  "}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !reflect.DeepEqual(embedder.calls, []string{"年假"}) || !reflect.DeepEqual(embedder.spaces, []string{testEmbedderSpace}) {
		t.Fatalf("embed calls = %v in %v, want [年假] in [%s]", embedder.calls, embedder.spaces, testEmbedderSpace)
	}
	if !reflect.DeepEqual(store.vector, []float32{2, 0, 0, 0}) {
		t.Fatalf("search vector = %v, want embedder output", store.vector)
	}
	if store.filter.Embedder != testEmbedderSpace || store.filter.Dimension != 4 {
		t.Fatalf("filter space = %s/%d, want %s/4", store.filter.Embedder, store.filter.Dimension, testEmbedderSpace)
	}
}

func TestKnowledgeSearch_without_library_applies_fixed_conditions_over_visible_libraries(t *testing.T) {
	store := &recordingSearchStore{}
	libraries := defaultSearchLibraries()
	uc := newSearchUsecaseForTest(t, store, &stubEmbedder{dims: 4}, &stubResolver{space: testEmbedderSpace, dim: 4}, libraries)

	if _, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape"}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	now := searchTestNow.Unix()
	want := []MetadataCondition{
		{Key: "doc_status", Op: VectorMetadataEq, Values: []any{1}},
		{Key: "valid_from_unix", Op: VectorMetadataLte, Values: []any{now}},
		{Key: "valid_to_unix", Op: VectorMetadataGt, Values: []any{now}},
		{Key: "kb_id", Op: VectorMetadataIn, Values: []any{"7", "8"}},
	}
	if !reflect.DeepEqual(store.filter.Conditions, want) {
		t.Fatalf("conditions = %+v, want %+v", store.filter.Conditions, want)
	}
	if libraries.calls != 1 {
		t.Fatalf("library resolution calls = %d, want 1 (resolved per search)", libraries.calls)
	}
}

func TestKnowledgeSearch_with_visible_library_narrows_to_it(t *testing.T) {
	store := &recordingSearchStore{}
	uc := newSearchUsecaseForTest(t, store, &stubEmbedder{dims: 4}, &stubResolver{space: testEmbedderSpace, dim: 4}, defaultSearchLibraries())

	// 42 不在可见集合（7/8）：不可见与不存在统一按 ErrLibraryNotFound 拒绝。
	if _, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape", LibraryID: 42}); !errors.Is(err, ErrLibraryNotFound) {
		t.Fatalf("Search with invisible library = %v, want ErrLibraryNotFound", err)
	}

	libraries := &stubLibraryDirectory{libraries: []*KnowledgeLibrary{{ID: 42}, {ID: 43}}}
	uc = newSearchUsecaseForTest(t, store, &stubEmbedder{dims: 4}, &stubResolver{space: testEmbedderSpace, dim: 4}, libraries)
	if _, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape", LibraryID: 42}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	conditions := store.filter.Conditions
	last := conditions[len(conditions)-1]
	want := MetadataCondition{Key: "kb_id", Op: VectorMetadataIn, Values: []any{"42"}}
	if len(conditions) != 4 || !reflect.DeepEqual(last, want) {
		t.Fatalf("conditions = %+v, want fixed three plus %+v", conditions, want)
	}
}

func TestKnowledgeSearch_invisible_or_missing_library_rejected_before_embedding(t *testing.T) {
	store := &recordingSearchStore{}
	embedder := &stubEmbedder{dims: 4}
	uc := newSearchUsecaseForTest(t, store, embedder, &stubResolver{space: testEmbedderSpace, dim: 4}, defaultSearchLibraries())

	_, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape", LibraryID: 404})
	if !errors.Is(err, ErrLibraryNotFound) {
		t.Fatalf("Search(invisible library) = %v, want ErrLibraryNotFound", err)
	}
	if len(embedder.calls) != 0 || store.calls != 0 {
		t.Fatalf("embed/store calls = %d/%d, want 0/0 (rejected before retrieval)", len(embedder.calls), store.calls)
	}
}

func TestKnowledgeSearch_no_visible_library_returns_empty_without_facility_access(t *testing.T) {
	store := &recordingSearchStore{}
	embedder := &stubEmbedder{dims: 4}
	uc := newSearchUsecaseForTest(t, store, embedder, &stubResolver{space: testEmbedderSpace, dim: 4}, &stubLibraryDirectory{})

	hits, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if hits == nil || len(hits) != 0 {
		t.Fatalf("hits = %#v, want empty non-nil slice", hits)
	}
	if len(embedder.calls) != 0 || store.calls != 0 {
		t.Fatalf("embed/store calls = %d/%d, want 0/0", len(embedder.calls), store.calls)
	}
}

func TestKnowledgeSearch_library_resolution_error_propagates(t *testing.T) {
	libraries := &stubLibraryDirectory{err: errors.New("scope lookup failed")}
	uc := newSearchUsecaseForTest(t, &recordingSearchStore{}, &stubEmbedder{dims: 4}, &stubResolver{space: testEmbedderSpace, dim: 4}, libraries)

	if _, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape"}); err == nil || !strings.Contains(err.Error(), "resolve visible libraries") {
		t.Fatalf("Search = %v, want wrapped library resolution error", err)
	}
}

func TestKnowledgeSearch_declares_console_source_and_query_without_session(t *testing.T) {
	store := &recordingSearchStore{}
	uc := newSearchUsecaseForTest(t, store, &stubEmbedder{dims: 4}, &stubResolver{space: testEmbedderSpace, dim: 4}, defaultSearchLibraries())

	if _, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: " grape "}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if store.source != RetrievalSourceConsole || store.query != "grape" || store.session != "" {
		t.Fatalf("ctx facts = (source %q, query %q, session %q), want (console, grape, empty)", store.source, store.query, store.session)
	}
}

func TestKnowledgeSearch_keeps_caller_declared_source(t *testing.T) {
	store := &recordingSearchStore{}
	uc := newSearchUsecaseForTest(t, store, &stubEmbedder{dims: 4}, &stubResolver{space: testEmbedderSpace, dim: 4}, defaultSearchLibraries())
	ctx := WithRetrievalSource(searchCtx(), RetrievalSourceTool)

	if _, err := uc.Search(ctx, &KnowledgeSearchQuery{Query: "grape"}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if store.source != RetrievalSourceTool {
		t.Fatalf("source = %q, want tool (caller declaration must win)", store.source)
	}
}

func TestKnowledgeSearch_resolves_visible_libraries_per_search_without_cache(t *testing.T) {
	store := &recordingSearchStore{}
	libraries := &stubLibraryDirectory{libraries: []*KnowledgeLibrary{{ID: 7}}}
	uc := newSearchUsecaseForTest(t, store, &stubEmbedder{dims: 4}, &stubResolver{space: testEmbedderSpace, dim: 4}, libraries)
	ctx := searchCtx()

	if _, err := uc.Search(ctx, &KnowledgeSearchQuery{Query: "grape"}); err != nil {
		t.Fatalf("first Search: %v", err)
	}
	first := store.filter.Conditions[len(store.filter.Conditions)-1]
	if !reflect.DeepEqual(first.Values, []any{"7"}) {
		t.Fatalf("first kb_id values = %v, want [7]", first.Values)
	}

	// 权限运行时可变：第二次检索必须重新解析，不做长生命周期缓存。
	libraries.libraries = []*KnowledgeLibrary{{ID: 9}}
	if _, err := uc.Search(ctx, &KnowledgeSearchQuery{Query: "grape"}); err != nil {
		t.Fatalf("second Search: %v", err)
	}
	second := store.filter.Conditions[len(store.filter.Conditions)-1]
	if libraries.calls != 2 {
		t.Fatalf("library resolution calls = %d, want 2 (resolved per search)", libraries.calls)
	}
	if !reflect.DeepEqual(second.Values, []any{"9"}) {
		t.Fatalf("second kb_id values = %v, want [9] (per-search resolution)", second.Values)
	}
}

func TestKnowledgeSearch_VisibleLibraries_delegates_and_requires_identity(t *testing.T) {
	libraries := &stubLibraryDirectory{libraries: []*KnowledgeLibrary{{ID: 5, Name: "handbook"}}}
	uc := newSearchUsecaseForTest(t, &recordingSearchStore{}, &stubEmbedder{dims: 4}, &stubResolver{space: testEmbedderSpace, dim: 4}, libraries)

	got, err := uc.VisibleLibraries(searchCtx())
	if err != nil {
		t.Fatalf("VisibleLibraries: %v", err)
	}
	if len(got) != 1 || got[0].ID != 5 || got[0].Name != "handbook" {
		t.Fatalf("libraries = %+v, want the directory result", got)
	}
	if _, err := uc.VisibleLibraries(context.Background()); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("VisibleLibraries without identity = %v, want ErrSessionInvalid", err)
	}
}

func TestKnowledgeSearch_maps_hits_from_metadata_and_raw_content(t *testing.T) {
	store := &recordingSearchStore{results: []*ScoredVectorDocument{
		{Document: &VectorDocument{ID: "12#0", Content: "<b>raw</b> text", Metadata: map[string]any{
			"source_id": "12", "source_name": "handbook", "anchor": "runes 0-15",
		}}, Score: 0.91},
		{Document: nil, Score: 0.5},
		{Document: &VectorDocument{ID: "13#2", Content: "bare", Metadata: map[string]any{"source_id": 13}}, Score: 0.4},
	}}
	uc := newSearchUsecaseForTest(t, store, &stubEmbedder{dims: 4}, &stubResolver{space: testEmbedderSpace, dim: 4}, defaultSearchLibraries())

	hits, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// Score 是 RRF 融合分：单路命中即 1/(k+rank)，nil 文档不计名次。
	// 期望值按运行时 float64 口径计算（k 为变量，规避常量折叠的舍入差）。
	k := float64(DefaultKnowledgeSearchFusionRRFK)
	want := []*KnowledgeSearchHit{
		{DocumentID: "12#0", SourceID: "12", SourceName: "handbook", Anchor: "runes 0-15",
			Score: 1 / (k + 1), Content: "<b>raw</b> text"},
		{DocumentID: "13#2", Score: 1 / (k + 2), Content: "bare"},
	}
	if !reflect.DeepEqual(hits, want) {
		t.Fatalf("hits = %+v, want %+v", hits, want)
	}
}

func TestKnowledgeSearch_no_results_returns_empty_hits(t *testing.T) {
	uc := newSearchUsecaseForTest(t, &recordingSearchStore{}, &stubEmbedder{dims: 4}, &stubResolver{space: testEmbedderSpace, dim: 4}, defaultSearchLibraries())

	hits, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if hits == nil || len(hits) != 0 {
		t.Fatalf("hits = %#v, want empty non-nil slice", hits)
	}
}

func TestKnowledgeSearch_resolver_disabled_propagates_without_embedding(t *testing.T) {
	store := &recordingSearchStore{}
	embedder := &stubEmbedder{dims: 4}
	uc := newSearchUsecaseForTest(t, store, embedder, &stubResolver{err: ErrEmbedderDisabled}, defaultSearchLibraries())

	_, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape"})
	if !errors.Is(err, ErrEmbedderDisabled) {
		t.Fatalf("Search = %v, want ErrEmbedderDisabled", err)
	}
	if len(embedder.calls) != 0 || store.calls != 0 {
		t.Fatalf("embed/store calls = %d/%d, want 0/0", len(embedder.calls), store.calls)
	}
}

func TestKnowledgeSearch_embedder_disabled_propagates_without_search(t *testing.T) {
	store := &recordingSearchStore{}
	uc := newSearchUsecaseForTest(t, store, &stubEmbedder{dims: 4, stubErr: ErrEmbedderDisabled}, &stubResolver{space: testEmbedderSpace, dim: 4}, defaultSearchLibraries())

	_, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape"})
	if !errors.Is(err, ErrEmbedderDisabled) {
		t.Fatalf("Search = %v, want ErrEmbedderDisabled", err)
	}
	if store.calls != 0 {
		t.Fatalf("store calls = %d, want 0", store.calls)
	}
}

func TestKnowledgeSearch_vector_store_disabled_propagates(t *testing.T) {
	store := &recordingSearchStore{err: ErrVectorStoreDisabled}
	uc := newSearchUsecaseForTest(t, store, &stubEmbedder{dims: 4}, &stubResolver{space: testEmbedderSpace, dim: 4}, defaultSearchLibraries())

	_, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape"})
	if !errors.Is(err, ErrVectorStoreDisabled) {
		t.Fatalf("Search = %v, want ErrVectorStoreDisabled", err)
	}
}

func TestKnowledgeSearch_variants_recall_individually_over_both_routes(t *testing.T) {
	store := &recordingSearchStore{}
	embedder := &stubEmbedder{dims: 4}
	uc := newSearchUsecaseForTest(t, store, embedder, &stubResolver{space: testEmbedderSpace, dim: 4}, defaultSearchLibraries())

	q := &KnowledgeSearchQuery{Query: "年假", Variants: []string{"假期 申请", "  ", "annual leave"}}
	if _, err := uc.Search(searchCtx(), q); err != nil {
		t.Fatalf("Search: %v", err)
	}
	// 主查询 + 2 个非空白变体：embed 与两路召回各 3 次（空白变体被跳过）。
	if len(embedder.calls) != 3 {
		t.Fatalf("embed calls = %d, want 3 (main + 2 non-blank variants)", len(embedder.calls))
	}
	if store.calls != 3 || store.lexicalCalls != 3 {
		t.Fatalf("route calls = (vector %d, lexical %d), want (3, 3)", store.calls, store.lexicalCalls)
	}
	if !reflect.DeepEqual(store.queries, []string{"年假", "假期 申请", "annual leave"}) {
		t.Fatalf("lexical queries = %v, want main query then trimmed variants", store.queries)
	}
	// 变体只影响召回：留痕查询文本恒为主查询。
	if store.query != "年假" {
		t.Fatalf("retrieval query = %q, want main query", store.query)
	}
}

func TestKnowledgeSearch_fuses_vector_and_lexical_routes_by_rrf(t *testing.T) {
	// 向量路 [d1, d2] + 词法路 [d2, d3]：d2 两路累加居首，文档记录取
	// 首次出现者（向量路的 metadata/content）。
	store := &recordingSearchStore{
		results: []*ScoredVectorDocument{
			{Document: &VectorDocument{ID: "d1", Content: "vector d1", Metadata: map[string]any{"source_id": "11"}}, Score: 0.9},
			{Document: &VectorDocument{ID: "d2", Content: "vector d2", Metadata: map[string]any{"source_id": "12"}}, Score: 0.8},
		},
		lexical: []*ScoredVectorDocument{
			{Document: &VectorDocument{ID: "d2", Content: "lexical d2", Metadata: map[string]any{"source_id": "other"}}, Score: 0.7},
			{Document: &VectorDocument{ID: "d3", Content: "lexical d3", Metadata: map[string]any{"source_id": "13"}}, Score: 0.6},
		},
	}
	uc := newSearchUsecaseForTest(t, store, &stubEmbedder{dims: 4}, &stubResolver{space: testEmbedderSpace, dim: 4}, defaultSearchLibraries())

	hits, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	wantOrder := []string{"d2", "d1", "d3"}
	for i, id := range wantOrder {
		if hits[i].DocumentID != id {
			t.Fatalf("hit %d = %s, want %s (RRF order)", i, hits[i].DocumentID, id)
		}
	}
	// Score 为 RRF 分：d2 两路累加 1/(k+1)+1/(k+2)，其余各单路一次
	//（k 为变量，运行时 float64 口径与实现一致）。
	k := float64(DefaultKnowledgeSearchFusionRRFK)
	if hits[0].Score != 1/(k+1)+1/(k+2) {
		t.Fatalf("d2 score = %f, want two-route accumulation", hits[0].Score)
	}
	// 文档记录取首次出现（向量路）：source_id 不会被词法路的值覆盖。
	if hits[0].SourceID != "12" || hits[0].Content != "vector d2" {
		t.Fatalf("d2 record = (%s, %s), want first occurrence (vector route)", hits[0].SourceID, hits[0].Content)
	}
}

func TestKnowledgeSearch_lexical_failure_degrades_to_vector_only(t *testing.T) {
	store := &recordingSearchStore{
		lexicalErr: errors.New("lexical index unavailable"),
		results: []*ScoredVectorDocument{
			{Document: &VectorDocument{ID: "d1", Content: "vector d1"}, Score: 0.9},
		},
	}
	uc := newSearchUsecaseForTest(t, store, &stubEmbedder{dims: 4}, &stubResolver{space: testEmbedderSpace, dim: 4}, defaultSearchLibraries())

	hits, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape"})
	if err != nil {
		t.Fatalf("Search with lexical failure = %v, want degraded success", err)
	}
	if len(hits) != 1 || hits[0].DocumentID != "d1" {
		t.Fatalf("hits = %v, want vector-only result", hits)
	}
	k := float64(DefaultKnowledgeSearchFusionRRFK)
	if hits[0].Score != 1/(k+1) {
		t.Fatalf("score = %f, want single-route rank-1 RRF score", hits[0].Score)
	}
}

func TestKnowledgeSearch_vector_failure_fails_whole_search(t *testing.T) {
	store := &recordingSearchStore{err: errors.New("vector index unavailable")}
	uc := newSearchUsecaseForTest(t, store, &stubEmbedder{dims: 4}, &stubResolver{space: testEmbedderSpace, dim: 4}, defaultSearchLibraries())

	if _, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape"}); err == nil ||
		!strings.Contains(err.Error(), "vector index unavailable") {
		t.Fatalf("Search with vector failure = %v, want whole-search failure", err)
	}
}

func TestKnowledgeSearch_invalid_variant_returns_invalid_argument(t *testing.T) {
	cases := map[string][]string{
		"oversize variant":   {strings.Repeat("检", MaxKnowledgeSearchQueryRunes+1)},
		"too many variants":  {"a", "b", "c", "d", "e"},
		"blank entries only": {"   ", "\t"},
	}
	for name, variants := range cases {
		t.Run(name, func(t *testing.T) {
			store := &recordingSearchStore{}
			embedder := &stubEmbedder{dims: 4}
			uc := newSearchUsecaseForTest(t, store, embedder, &stubResolver{space: testEmbedderSpace, dim: 4}, defaultSearchLibraries())

			_, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape", Variants: variants})
			// 空白-only 变体全部被跳过后等同无变体，检索照常执行；
			// 超长与超限是非法入参。
			if name == "blank entries only" {
				if err != nil {
					t.Fatalf("Search(blank-only variants) = %v, want accepted", err)
				}
				if store.calls != 1 || store.lexicalCalls != 1 {
					t.Fatalf("route calls = (%d, %d), want (1, 1) (blank variants skipped)", store.calls, store.lexicalCalls)
				}
				return
			}
			if !errors.Is(err, ErrKnowledgeSearchInvalidArgument) {
				t.Fatalf("Search(%s) = %v, want ErrKnowledgeSearchInvalidArgument", name, err)
			}
			if store.calls != 0 || len(embedder.calls) != 0 {
				t.Fatalf("store/embed calls = %d/%d, want 0/0 (validate before retrieval)",
					store.calls, len(embedder.calls))
			}
		})
	}
}

func TestKnowledgeSearch_variant_count_at_limit_accepted(t *testing.T) {
	store := &recordingSearchStore{}
	embedder := &stubEmbedder{dims: 4}
	uc := newSearchUsecaseForTest(t, store, embedder, &stubResolver{space: testEmbedderSpace, dim: 4}, defaultSearchLibraries())

	variants := []string{"v1", "v2", "v3", "v4"}
	if _, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape", Variants: variants}); err != nil {
		t.Fatalf("Search(at variant limit) = %v, want accepted", err)
	}
	if store.calls != MaxKnowledgeSearchQueryVariants+1 || store.lexicalCalls != MaxKnowledgeSearchQueryVariants+1 {
		t.Fatalf("route calls = (%d, %d), want (%d, %d)",
			store.calls, store.lexicalCalls, MaxKnowledgeSearchQueryVariants+1, MaxKnowledgeSearchQueryVariants+1)
	}
}
