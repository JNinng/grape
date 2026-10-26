package pg_sqlc

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"grape/internal/biz"
	"grape/pkg/redact"
)

// 检索留痕装饰器单元测试（TODO 流 D 的 D3）：以替身内层仓储与替身留痕
// 仓储验证转发语义、留痕行内容、留痕失败不中断、脱敏与截断兜底。集成
// 面（真实库、RLS、管线摄取）见 retrieval_pg_test.go。

// fakeInnerStore 是内层向量仓储替身：记录调用并返回可控结果。
type fakeInnerStore struct {
	addErr    error
	searchErr error
	hits      []*biz.ScoredVectorDocument
	calls     []string
}

func (s *fakeInnerStore) Add(_ context.Context, _ *biz.VectorDocument) error {
	s.calls = append(s.calls, "add")
	return s.addErr
}

func (s *fakeInnerStore) Get(_ context.Context, id string) (*biz.VectorDocument, error) {
	s.calls = append(s.calls, "get")
	return &biz.VectorDocument{ID: id}, nil
}

func (s *fakeInnerStore) Update(_ context.Context, _ *biz.VectorDocument) error {
	s.calls = append(s.calls, "update")
	return nil
}

func (s *fakeInnerStore) Delete(_ context.Context, _ string) error {
	s.calls = append(s.calls, "delete")
	return nil
}

func (s *fakeInnerStore) Search(_ context.Context, _ []float32, _ *biz.VectorFilter) ([]*biz.ScoredVectorDocument, error) {
	s.calls = append(s.calls, "search")
	if s.searchErr != nil {
		return nil, s.searchErr
	}
	return s.hits, nil
}

func (s *fakeInnerStore) DeleteByFilter(_ context.Context, _ *biz.VectorFilter) (int64, error) {
	s.calls = append(s.calls, "delete_by_filter")
	return 2, nil
}

func (s *fakeInnerStore) Count(_ context.Context, _ *biz.VectorFilter) (int64, error) {
	s.calls = append(s.calls, "count")
	return 7, nil
}

func (s *fakeInnerStore) GetMetadata(_ context.Context, _ *biz.VectorFilter) (map[string]map[string]any, error) {
	s.calls = append(s.calls, "get_metadata")
	return map[string]map[string]any{}, nil
}

// fakeRetrievalRepo 是留痕仓储替身：记录写入行，可注入失败。
type fakeRetrievalRepo struct {
	appendErr error
	rows      []*biz.RetrievalLogRecord
}

func (r *fakeRetrievalRepo) Append(_ context.Context, rec *biz.RetrievalLogRecord) error {
	if r.appendErr != nil {
		return r.appendErr
	}
	r.rows = append(r.rows, rec)
	return nil
}

func (r *fakeRetrievalRepo) List(context.Context, *biz.RetrievalLogFilter) (*biz.RetrievalLogList, error) {
	return nil, errors.New("not implemented")
}

// retrievalTestContext 构造带链路/会话/查询文本事实的 ctx（模拟链路
// 中间件与 agent 层事实载体的注入结果）。
func retrievalTestContext(query string) context.Context {
	ctx := biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-d3", UserID: 71})
	ctx = biz.WithSessionID(ctx, "sess-d3")
	return biz.WithRetrievalQuery(ctx, query)
}

// newLoggedStore 构造被测装饰器与替身。
func newLoggedStore(inner biz.VectorStore, repo biz.RetrievalLogRepo, registry *redact.Registry) biz.VectorStore {
	store, _, err := NewLoggedVectorStore(VectorStoreSupply{VectorStore: inner}, repo, registry)
	if err != nil {
		panic(err)
	}
	return store
}

func TestLoggedVectorStore_requires_inner_store_and_repo(t *testing.T) {
	if _, _, err := NewLoggedVectorStore(VectorStoreSupply{}, &fakeRetrievalRepo{}, nil); err == nil {
		t.Fatalf("nil inner store must be rejected")
	}
	if _, _, err := NewLoggedVectorStore(VectorStoreSupply{VectorStore: &fakeInnerStore{}}, nil, nil); err == nil {
		t.Fatalf("nil log repo must be rejected")
	}
}

func TestLoggedVectorStore_search_forwards_and_records_row(t *testing.T) {
	inner := &fakeInnerStore{hits: []*biz.ScoredVectorDocument{{
		Document: &biz.VectorDocument{ID: "doc#0", Metadata: map[string]any{"anchor": "runes 0-800"}},
		Score:    0.87,
	}}}
	repo := &fakeRetrievalRepo{}
	store := newLoggedStore(inner, repo, redact.NewStandardRegistry())

	filter := &biz.VectorFilter{Limit: 3, Metadata: map[string]any{"source_id": "doc"}}
	hits, err := store.Search(retrievalTestContext("what is grape"), nil, filter)
	if err != nil || len(hits) != 1 {
		t.Fatalf("search = (%d hits, %v), want 1 hit", len(hits), err)
	}
	if len(inner.calls) != 1 || inner.calls[0] != "search" {
		t.Fatalf("inner calls = %v, want exactly one search", inner.calls)
	}
	if len(repo.rows) != 1 {
		t.Fatalf("recorded rows = %d, want 1", len(repo.rows))
	}
	rec := repo.rows[0]
	switch {
	case rec.Source != biz.RetrievalSourceTool:
		t.Fatalf("source = %q, want tool", rec.Source)
	case rec.Status != biz.RetrievalStatusSuccess:
		t.Fatalf("status = %q, want success", rec.Status)
	case rec.SessionID != "sess-d3":
		t.Fatalf("session id = %q, want sess-d3", rec.SessionID)
	case rec.QueryText != "what is grape":
		t.Fatalf("query text = %q, want original (short text untouched by standard registry)", rec.QueryText)
	case rec.RequestedTopK != 3 || rec.EffectiveTopK != 3:
		t.Fatalf("top_k = (%d, %d), want (3, 3)", rec.RequestedTopK, rec.EffectiveTopK)
	case rec.DurationMS < 0:
		t.Fatalf("duration = %d, want non-negative", rec.DurationMS)
	}
	if len(rec.Hits) != 1 || rec.Hits[0] != (biz.RetrievalHit{DocumentID: "doc#0", Score: 0.87, Anchor: "runes 0-800"}) {
		t.Fatalf("hits = %+v, want document id/score/anchor recorded", rec.Hits)
	}
	if rec.FilterSummary == nil || rec.FilterSummary["metadata"].(map[string]any)["source_id"] != "doc" {
		t.Fatalf("filter summary = %+v, want metadata carried", rec.FilterSummary)
	}
}

func TestLoggedVectorStore_search_without_filter_records_default_top_k(t *testing.T) {
	repo := &fakeRetrievalRepo{}
	store := newLoggedStore(&fakeInnerStore{}, repo, redact.NewStandardRegistry())

	if _, err := store.Search(context.Background(), nil, nil); err != nil {
		t.Fatalf("search: %v", err)
	}
	rec := repo.rows[0]
	if rec.RequestedTopK != 0 {
		t.Fatalf("requested top_k = %d, want 0 (falls to NULL)", rec.RequestedTopK)
	}
	if rec.EffectiveTopK != vectorDefaultLimit {
		t.Fatalf("effective top_k = %d, want store default %d", rec.EffectiveTopK, vectorDefaultLimit)
	}
	if rec.FilterSummary != nil || rec.Hits != nil {
		t.Fatalf("nil filter/hits must stay nil, got %+v / %+v", rec.FilterSummary, rec.Hits)
	}
}

func TestLoggedVectorStore_search_failure_records_error_row_and_propagates(t *testing.T) {
	inner := &fakeInnerStore{searchErr: biz.ErrVectorStoreDisabled}
	repo := &fakeRetrievalRepo{}
	store := newLoggedStore(inner, repo, redact.NewStandardRegistry())

	if _, err := store.Search(retrievalTestContext("q"), nil, nil); !errors.Is(err, biz.ErrVectorStoreDisabled) {
		t.Fatalf("search error = %v, want ErrVectorStoreDisabled propagated", err)
	}
	if len(repo.rows) != 1 {
		t.Fatalf("recorded rows = %d, want 1 (failed attempts are evidence too)", len(repo.rows))
	}
	if repo.rows[0].Status != biz.RetrievalStatusError || repo.rows[0].Hits != nil {
		t.Fatalf("row = (%s, hits %v), want error status and nil hits", repo.rows[0].Status, repo.rows[0].Hits)
	}
}

func TestLoggedVectorStore_log_write_failure_does_not_break_search(t *testing.T) {
	inner := &fakeInnerStore{hits: []*biz.ScoredVectorDocument{{Document: &biz.VectorDocument{ID: "doc#1"}, Score: 0.5}}}
	repo := &fakeRetrievalRepo{appendErr: errors.New("log storage down")}
	store := newLoggedStore(inner, repo, redact.NewStandardRegistry())

	hits, err := store.Search(retrievalTestContext("q"), nil, nil)
	if err != nil || len(hits) != 1 {
		t.Fatalf("search = (%d hits, %v), must succeed when logging fails", len(hits), err)
	}
}

func TestLoggedVectorStore_redacts_query_text_and_caps_fallback(t *testing.T) {
	registry := redact.NewRegistry()
	if err := registry.Register(redact.CategoryRetrieval, redact.NewMask("[masked]")); err != nil {
		t.Fatalf("register: %v", err)
	}
	repo := &fakeRetrievalRepo{}
	store := newLoggedStore(&fakeInnerStore{}, repo, registry)

	store.Search(biz.WithRetrievalQuery(context.Background(), "secret query"), nil, nil)
	if repo.rows[0].QueryText != "[masked]" {
		t.Fatalf("query text = %q, want masked by registered retrieval redactor", repo.rows[0].QueryText)
	}

	// 长度兜底：注册表放行原值（未登记类别）时仍按兜底上限截断。
	plain := newLoggedStore(&fakeInnerStore{}, repo, redact.NewRegistry())
	huge := strings.Repeat("字", retrievalQueryMaxRunes+10)
	plain.Search(biz.WithRetrievalQuery(context.Background(), huge), nil, nil)
	got := repo.rows[len(repo.rows)-1].QueryText
	if !strings.HasSuffix(got, "...(truncated)") {
		t.Fatalf("oversized query must be truncated with marker, suffix = %q", got[len(got)-20:])
	}
	if n := utf8.RuneCountInString(strings.TrimSuffix(got, "...(truncated)")); n != retrievalQueryMaxRunes {
		t.Fatalf("truncated length = %d runes, want %d", n, retrievalQueryMaxRunes)
	}
}

func TestLoggedVectorStore_filter_summary_redaction_and_structure_fallback(t *testing.T) {
	// 标准注册表（retrieval = 512 rune 截断）：小于截断阈值的摘要原样
	// 透传，jsonb 结构保持。
	repo := &fakeRetrievalRepo{}
	store := newLoggedStore(&fakeInnerStore{}, repo, redact.NewStandardRegistry())

	filter := &biz.VectorFilter{MinScore: 0.2, Metadata: map[string]any{"source_id": "doc"}}
	store.Search(context.Background(), nil, filter)
	summary := repo.rows[0].FilterSummary
	if summary["min_score"] != 0.2 {
		t.Fatalf("min_score = %v, want carried through", summary["min_score"])
	}
	if summary["metadata"].(map[string]any)["source_id"] != "doc" {
		t.Fatalf("metadata summary = %v, want carried through", summary["metadata"])
	}

	// 整段收缩型脱敏器（掩码）把 JSON 打碎：降级为截断声明形态，
	// 保留「发生过脱敏收缩」的事实。
	masked := redact.NewRegistry()
	if err := masked.Register(redact.CategoryRetrieval, redact.NewMask("[masked]")); err != nil {
		t.Fatalf("register: %v", err)
	}
	repoMasked := &fakeRetrievalRepo{}
	storeMasked := newLoggedStore(&fakeInnerStore{}, repoMasked, masked)
	storeMasked.Search(context.Background(), nil, filter)
	if repoMasked.rows[0].FilterSummary["summary_truncated"] != "[masked]" {
		t.Fatalf("shattered json must degrade to summary_truncated form, got %+v", repoMasked.rows[0].FilterSummary)
	}

	// 超长摘要被截断型脱敏器打碎：同样落结构兜底形态。
	repoLong := &fakeRetrievalRepo{}
	storeLong := newLoggedStore(&fakeInnerStore{}, repoLong, redact.NewStandardRegistry())
	long := &biz.VectorFilter{Metadata: map[string]any{"source_id": strings.Repeat("x", 1024)}}
	storeLong.Search(context.Background(), nil, long)
	preview, _ := repoLong.rows[0].FilterSummary["summary_truncated"].(string)
	if !strings.HasSuffix(preview, "...(truncated)") {
		t.Fatalf("oversized summary must degrade with truncation marker, got %q", preview)
	}
}

func TestLoggedVectorStore_add_and_update_record_pipeline_rows(t *testing.T) {
	repo := &fakeRetrievalRepo{}
	store := newLoggedStore(&fakeInnerStore{}, repo, redact.NewStandardRegistry())

	doc := &biz.VectorDocument{ID: "doc#0", Metadata: map[string]any{"anchor": "runes 0-800"}}
	if err := store.Add(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-d3", UserID: 71}), doc); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := store.Update(context.Background(), doc); err != nil {
		t.Fatalf("update: %v", err)
	}
	if len(repo.rows) != 2 {
		t.Fatalf("recorded rows = %d, want 2", len(repo.rows))
	}
	for i, rec := range repo.rows {
		switch {
		case rec.Source != biz.RetrievalSourcePipeline:
			t.Fatalf("row %d source = %q, want pipeline", i, rec.Source)
		case rec.Status != biz.RetrievalStatusSuccess:
			t.Fatalf("row %d status = %q, want success", i, rec.Status)
		case rec.QueryText != "" || rec.RequestedTopK != 0 || rec.EffectiveTopK != 0:
			t.Fatalf("row %d = (%q, %d, %d), want ingest-side nulls", i, rec.QueryText, rec.RequestedTopK, rec.EffectiveTopK)
		case len(rec.Hits) != 1 || rec.Hits[0] != (biz.RetrievalHit{DocumentID: "doc#0", Anchor: "runes 0-800"}):
			t.Fatalf("row %d hits = %+v, want single affected document with anchor", i, rec.Hits)
		}
	}
	if repo.rows[0].SessionID != "" {
		t.Fatalf("ingest row session id = %q, want empty (no framework session on pipeline path)", repo.rows[0].SessionID)
	}
}

func TestLoggedVectorStore_add_failure_records_error_row_and_propagates(t *testing.T) {
	repo := &fakeRetrievalRepo{}
	store := newLoggedStore(&fakeInnerStore{addErr: biz.ErrVectorDocNotFound}, repo, redact.NewStandardRegistry())

	if err := store.Add(context.Background(), &biz.VectorDocument{ID: "doc#x"}); !errors.Is(err, biz.ErrVectorDocNotFound) {
		t.Fatalf("add error = %v, want propagated", err)
	}
	if repo.rows[0].Status != biz.RetrievalStatusError {
		t.Fatalf("row status = %q, want error", repo.rows[0].Status)
	}
}

func TestLoggedVectorStore_delete_by_filter_records_pipeline_condition(t *testing.T) {
	repo := &fakeRetrievalRepo{}
	store := newLoggedStore(&fakeInnerStore{}, repo, redact.NewStandardRegistry())

	filter := &biz.VectorFilter{Metadata: map[string]any{"source_id": "doc"}}
	if _, err := store.DeleteByFilter(context.Background(), filter); err != nil {
		t.Fatalf("delete by filter: %v", err)
	}
	rec := repo.rows[0]
	if rec.Source != biz.RetrievalSourcePipeline {
		t.Fatalf("source = %q, want pipeline", rec.Source)
	}
	if rec.FilterSummary == nil || rec.FilterSummary["metadata"].(map[string]any)["source_id"] != "doc" {
		t.Fatalf("filter summary = %+v, want condition recorded", rec.FilterSummary)
	}
	if rec.Hits != nil || rec.RequestedTopK != 0 {
		t.Fatalf("delete row must not carry hits/top_k, got %+v/%d", rec.Hits, rec.RequestedTopK)
	}
}

func TestLoggedVectorStore_unlogged_methods_forward_without_rows(t *testing.T) {
	inner := &fakeInnerStore{}
	repo := &fakeRetrievalRepo{}
	store := newLoggedStore(inner, repo, redact.NewStandardRegistry())

	if _, err := store.Get(context.Background(), "doc#1"); err != nil {
		t.Fatalf("get: %v", err)
	}
	if err := store.Delete(context.Background(), "doc#1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.Count(context.Background(), nil); err != nil {
		t.Fatalf("count: %v", err)
	}
	if _, err := store.GetMetadata(context.Background(), nil); err != nil {
		t.Fatalf("get metadata: %v", err)
	}
	want := []string{"get", "delete", "count", "get_metadata"}
	if len(inner.calls) != len(want) {
		t.Fatalf("inner calls = %v, want %v", inner.calls, want)
	}
	for i, c := range want {
		if inner.calls[i] != c {
			t.Fatalf("inner calls = %v, want %v", inner.calls, want)
		}
	}
	if len(repo.rows) != 0 {
		t.Fatalf("recorded rows = %d, want 0 (point reads and enumeration are not evidence)", len(repo.rows))
	}
}

func TestLoggedVectorStore_hits_capped_at_declared_limit(t *testing.T) {
	hits := make([]*biz.ScoredVectorDocument, 0, retrievalMaxLoggedHits+10)
	for i := 0; i < retrievalMaxLoggedHits+10; i++ {
		hits = append(hits, &biz.ScoredVectorDocument{Document: &biz.VectorDocument{ID: "doc#" + string(rune('a'+i%26))}, Score: 0.1})
	}
	repo := &fakeRetrievalRepo{}
	store := newLoggedStore(&fakeInnerStore{hits: hits}, repo, redact.NewStandardRegistry())

	store.Search(context.Background(), nil, nil)
	if len(repo.rows[0].Hits) != retrievalMaxLoggedHits {
		t.Fatalf("recorded hits = %d, want capped at %d", len(repo.rows[0].Hits), retrievalMaxLoggedHits)
	}
}
