package biz

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"go.opentelemetry.io/otel/trace"

	"grape/pkg/redact"
)

// 模型调用留痕用例单测：观察面纪律（脱敏截断、身份缺失放弃、旁路失败
// 不反噬、超时预算）与查询侧的过滤归一、身份门禁。仓储以替身记录收到的
// 行与 ctx，行为断言全部落在用例侧。
type modelCallFakeRepo struct {
	appended []*ModelCallEntry
	appendFn func(ctx context.Context, e *ModelCallEntry) error
	gotList  *ModelCallFilter
	listErr  error
}

func (r *modelCallFakeRepo) Append(ctx context.Context, e *ModelCallEntry) error {
	if r.appendFn != nil {
		if err := r.appendFn(ctx, e); err != nil {
			return err
		}
	}
	r.appended = append(r.appended, e)
	return nil
}

func (r *modelCallFakeRepo) List(_ context.Context, f *ModelCallFilter) (*ModelCallList, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	r.gotList = f
	return &ModelCallList{Items: []*ModelCallLog{}, Total: 0}, nil
}

func (r *modelCallFakeRepo) Count(context.Context, *ModelCallFilter) (int64, error) {
	return 0, nil
}

func modelCallIdentityContext() context.Context {
	return WithIdentity(context.Background(), Identity{TenantID: "t-1", UserID: 9})
}

// modelCallEntryOf 是合法写入载体的样板：按用例逐字段覆盖。
func modelCallEntryOf() *ModelCallEntry {
	return &ModelCallEntry{
		Scenario:      ModelCallScenarioEmbedDocument,
		ModelKey:      "bge-m1024",
		Endpoint:      "http://embed.example/v1/embeddings",
		InputSummary:  "chunk text",
		OutputSummary: "embedding vector of 1024 dimensions (vector not persisted)",
		InputTokens:   32,
		DurationMS:    18,
		Status:        ModelCallStatusSuccess,
	}
}

func TestModelCallObserver_redacts_truncates_and_fills_context_facts(t *testing.T) {
	repo := &modelCallFakeRepo{}
	uc := NewModelCallLogUsecase(repo, newModelCallTestRegistry())

	entry := modelCallEntryOf()
	entry.InputSummary = strings.Repeat("grape", 1200) // 6000 rune，超 4096 上限
	// model_call 类别的语义是 4096 rune 截断（对齐审计摘要口径）：限内
	// 文本按注册表输出原样保留（本测试断言截断上限与已登记放行路径）。
	entry.OutputSummary = "completion text within limit"
	entry.Error = ""
	// 链路事实与场景载体：trace 从 SpanContext 取，会话 ID 经载体注入。
	ctx := modelCallIdentityContext()
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    [16]byte{0xdc, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
		SpanID:     [8]byte{8, 8, 8, 8, 8, 8, 8, 8},
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	ctx = trace.ContextWithSpanContext(ctx, sc)
	ctx = WithSessionID(ctx, "sess-model-call")
	uc.ObserveModelCall(ctx, entry)

	if len(repo.appended) != 1 {
		t.Fatalf("appended = %d rows, want 1", len(repo.appended))
	}
	got := repo.appended[0]
	switch {
	case got.TraceID == "" || got.SessionID != "sess-model-call":
		t.Fatalf("facts = (trace %q, session %q), want trace id and session filled from ctx", got.TraceID, got.SessionID)
	case utf8.RuneCountInString(got.InputSummary) > ModelCallSummaryMaxRunes+len("...(truncated)"):
		t.Fatalf("input summary = %d runes, want truncated at %d", utf8.RuneCountInString(got.InputSummary), ModelCallSummaryMaxRunes)
	case !strings.HasSuffix(got.InputSummary, "...(truncated)"):
		t.Fatalf("input summary = %q, want truncation marker", got.InputSummary)
	case got.OutputSummary != "completion text within limit":
		t.Fatalf("output summary = %q, want verbatim within limit (registered category)", got.OutputSummary)
	case got.Scenario != ModelCallScenarioEmbedDocument || got.Status != ModelCallStatusSuccess:
		t.Fatalf("row = (%s/%s), want embed_document/success", got.Scenario, got.Status)
	case got.InputTokens != 32 || got.DurationMS != 18 || got.DocumentID != 0:
		t.Fatalf("row fields = (tokens %d, duration %d, document %d), want passthrough and NULL document", got.InputTokens, got.DurationMS, got.DocumentID)
	}
}

func TestModelCallObserver_truncates_error_summary_to_512(t *testing.T) {
	repo := &modelCallFakeRepo{}
	uc := NewModelCallLogUsecase(repo, nil)

	entry := modelCallEntryOf()
	entry.Status = ModelCallStatusError
	entry.Error = strings.Repeat("e", 900)
	uc.ObserveModelCall(modelCallIdentityContext(), entry)

	got := repo.appended[0]
	if utf8.RuneCountInString(got.Error) > ModelCallErrorMaxRunes+len("...(truncated)") {
		t.Fatalf("error = %d runes, want capped at %d", utf8.RuneCountInString(got.Error), ModelCallErrorMaxRunes)
	}
	if !strings.HasSuffix(got.Error, "...(truncated)") {
		t.Fatalf("error = %q, want truncation marker", got.Error)
	}
}

func TestModelCallObserver_drops_rows_without_identity(t *testing.T) {
	repo := &modelCallFakeRepo{}
	uc := NewModelCallLogUsecase(repo, nil)

	uc.ObserveModelCall(context.Background(), modelCallEntryOf())
	if len(repo.appended) != 0 {
		t.Fatalf("appended = %d rows, want 0 (fail-closed: no row without identity)", len(repo.appended))
	}
}

func TestModelCallObserver_drops_rows_with_unknown_scenario_or_status(t *testing.T) {
	repo := &modelCallFakeRepo{}
	uc := NewModelCallLogUsecase(repo, nil)
	ctx := modelCallIdentityContext()

	bad := modelCallEntryOf()
	bad.Scenario = "unknown_scene" // 词表外场景：放弃该行，不伪造
	uc.ObserveModelCall(ctx, bad)
	if len(repo.appended) != 0 {
		t.Fatalf("unknown scenario appended, want drop")
	}

	badStatus := modelCallEntryOf()
	badStatus.Status = "timeout"
	uc.ObserveModelCall(ctx, badStatus)
	if len(repo.appended) != 0 {
		t.Fatalf("unknown status appended, want drop")
	}

	uc.ObserveModelCall(ctx, nil)
	if len(repo.appended) != 0 {
		t.Fatalf("nil entry appended, want no-op")
	}
}

func TestModelCallObserver_bypass_failure_does_not_propagate(t *testing.T) {
	repo := &modelCallFakeRepo{appendFn: func(context.Context, *ModelCallEntry) error {
		return errors.New("model call log storage down")
	}}
	uc := NewModelCallLogUsecase(repo, nil)

	// 存储故障不得向调用方传播（观察面无返回值），也不 panic。
	uc.ObserveModelCall(modelCallIdentityContext(), modelCallEntryOf())
}

func TestModelCallObserver_write_runs_under_short_timeout_budget(t *testing.T) {
	// 仓储阻塞至写入 ctx 被取消：预算（测试注入 50ms）到点后放弃该行，
	// ObserveModelCall 不得阻塞超过预算的量级。
	budget := 50 * time.Millisecond
	prev := modelCallWriteTimeout
	modelCallWriteTimeout = budget
	t.Cleanup(func() { modelCallWriteTimeout = prev })

	blocked := make(chan struct{})
	repo := &modelCallFakeRepo{appendFn: func(ctx context.Context, _ *ModelCallEntry) error {
		close(blocked)
		<-ctx.Done()
		return ctx.Err()
	}}
	uc := NewModelCallLogUsecase(repo, nil)

	done := make(chan struct{})
	go func() {
		uc.ObserveModelCall(modelCallIdentityContext(), modelCallEntryOf())
		close(done)
	}()
	select {
	case <-blocked:
	case <-time.After(5 * time.Second):
		t.Fatalf("repo append never started")
	}
	select {
	case <-done:
	case <-time.After(10 * budget):
		t.Fatalf("observer blocked %v, want release around the %v budget", 10*budget, budget)
	}
}

func TestModelCallObserver_write_survives_parent_context_cancellation(t *testing.T) {
	// WithoutCancel 语义：写入 ctx 脱离请求生命周期——存储悬挂期间父 ctx
	//（请求侧）取消，已起飞的留痕写入仍按自身预算完成落行，不随请求取消
	// 丢弃证据。预算注入 50ms 控制测试时长（同形于超时预算用例）。
	budget := 50 * time.Millisecond
	prev := modelCallWriteTimeout
	modelCallWriteTimeout = budget
	t.Cleanup(func() { modelCallWriteTimeout = prev })

	parent, cancel := context.WithCancel(modelCallIdentityContext())
	started := make(chan struct{})
	parentCancelled := make(chan struct{})
	repo := &modelCallFakeRepo{appendFn: func(ctx context.Context, _ *ModelCallEntry) error {
		close(started)
		<-parentCancelled
		// 写入 ctx 若被父取消连带取消，此处返回错误 → 行放弃 → 落行断言失败。
		return ctx.Err()
	}}
	uc := NewModelCallLogUsecase(repo, nil)

	done := make(chan struct{})
	go func() {
		uc.ObserveModelCall(parent, modelCallEntryOf())
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatalf("repo append never started")
	}
	cancel() // 请求生命周期终止
	close(parentCancelled)
	select {
	case <-done:
	case <-time.After(10 * budget):
		t.Fatalf("write did not complete within %v of parent cancellation", 10*budget)
	}
	if len(repo.appended) != 1 {
		t.Fatalf("appended = %d rows, want 1 (row lands despite parent cancellation)", len(repo.appended))
	}
}

func TestModelCallObserver_recovers_from_repo_panic(t *testing.T) {
	repo := &modelCallFakeRepo{appendFn: func(context.Context, *ModelCallEntry) error {
		panic("model call log storage exploded")
	}}
	uc := NewModelCallLogUsecase(repo, nil)

	// panic 只记日志，不得向上传播反噬调用方。
	uc.ObserveModelCall(modelCallIdentityContext(), modelCallEntryOf())
}

func TestModelCallUsecase_list_filter_normalization_and_identity_gate(t *testing.T) {
	repo := &modelCallFakeRepo{}
	uc := NewModelCallLogUsecase(repo, nil)
	ctx := modelCallIdentityContext()

	if _, err := uc.List(ctx, &ModelCallFilter{
		Scenario:    ModelCallScenarioIngestSummary,
		ModelKey:    "glm-4",
		Status:      ModelCallStatusError,
		DocumentID:  7,
		CreatedFrom: time.Unix(1700000000, 0),
		CreatedTo:   time.Unix(1700000001, 0),
		Limit:       5,
		Offset:      10,
	}); err != nil {
		t.Fatalf("list: %v", err)
	}
	f := repo.gotList
	switch {
	case f.Scenario != ModelCallScenarioIngestSummary || f.Status != ModelCallStatusError:
		t.Fatalf("filter = %+v, want scenario/status preserved", f)
	case f.ModelKey != "glm-4" || f.DocumentID != 7:
		t.Fatalf("filter = %+v, want model key/document preserved", f)
	case f.Limit != 5 || f.Offset != 10:
		t.Fatalf("filter = %+v, want explicit pagination preserved", f)
	}

	// 缺省页大小补齐。
	if _, err := uc.List(ctx, nil); err != nil {
		t.Fatalf("list(nil): %v", err)
	}

	// 上限/词表/时间序校验 fail-closed，不静默截断。
	for name, filter := range map[string]*ModelCallFilter{
		"limit over max":     {Limit: 201},
		"limit negative":     {Limit: -1},
		"offset negative":    {Offset: -1},
		"scenario unknown":   {Scenario: "unknown_scene"},
		"status unknown":     {Status: "timeout"},
		"model key too long": {ModelKey: strings.Repeat("k", 129)},
		"document negative":  {DocumentID: -1},
		"range inverted":     {CreatedFrom: time.Unix(1700000002, 0), CreatedTo: time.Unix(1700000001, 0)},
	} {
		if _, err := uc.List(ctx, filter); !errors.Is(err, ErrModelCallInvalidArgument) {
			t.Fatalf("%s: err = %v, want ErrModelCallInvalidArgument", name, err)
		}
	}

	// 身份缺失按未认证 fail-closed。
	if _, err := uc.List(context.Background(), nil); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("err = %v, want ErrSessionInvalid (read side is identity-bound too)", err)
	}
}

func TestValidModelCallScenario_vocabulary(t *testing.T) {
	for _, ok := range []string{
		ModelCallScenarioIngestSummary, ModelCallScenarioIngestPrefix,
		ModelCallScenarioEmbedDocument, ModelCallScenarioEmbedQuery,
		ModelCallScenarioParseDocument, ModelCallScenarioRerank,
	} {
		if !ValidModelCallScenario(ok) {
			t.Fatalf("scenario %q = false, want in vocabulary", ok)
		}
	}
	if ValidModelCallScenario("") || ValidModelCallScenario("unknown_scene") {
		t.Fatalf("empty/unknown scenario accepted, want rejected")
	}
}

// newModelCallTestRegistry 构造标准注册表（与生产 wire 装配同一默认值）。
func newModelCallTestRegistry() *redact.Registry {
	return NewRedactRegistry()
}
