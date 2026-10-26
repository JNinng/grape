package agent

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"grape/internal/conf"

	"github.com/go-kratos/kratos/contrib/otel/v3/tracing"
	kratoslog "github.com/go-kratos/kratos/v3/log"
	"google.golang.org/protobuf/types/known/durationpb"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"

	"go.opentelemetry.io/otel/trace"
)

// ---------- 替身模型 ----------

// stubStep 是替身模型单次调用的脚本：延迟后逐块产出响应，或直接返回
// 系统级错误。延迟用真实短睡眠（最长 100ms，远小于测试超时），不引入
// 可注入时钟——模型层可靠性测试的时序判定以数量级分隔（慢候选 100ms
// 对快候选 0ms、hedge 偏移 1ms），固定值即确定，无需时钟抽象。
type stubStep struct {
	delay  time.Duration
	err    error
	chunks []*model.Response
}

// stubModel 实现 model.Model 的替身：按脚本循环响应调用（调用数超出脚
// 本时重复最后一步），并记录每次调用的 ctx 是否在返回前被取消（hedge
// 落败副本的取消断言依据）。只实现 Model 接口（channel 形态），同时
// 覆盖框架对非 IterModel 候选的适配路径与 loggingModel 的序列适配器。
type stubModel struct {
	name  string
	steps []stubStep

	mu        sync.Mutex
	calls     int
	cancelled []bool
}

func newStubModel(name string, steps ...stubStep) *stubModel {
	return &stubModel{name: name, steps: steps}
}

func (m *stubModel) Info() model.Info { return model.Info{Name: m.name} }

func (m *stubModel) GenerateContent(ctx context.Context, _ *model.Request) (<-chan *model.Response, error) {
	m.mu.Lock()
	i := m.calls
	m.calls++
	m.cancelled = append(m.cancelled, false)
	step := m.steps[min(i, len(m.steps)-1)]
	m.mu.Unlock()

	// 纯系统错误（无延迟）同步返回，保住「函数级错误」语义；带延迟的
	// 步骤在后台协程等待——GenerateContent 立即返回空 channel，ctx 取消
	// 能落在延迟窗口内（hedge 落败副本的取消可观测），超时后不产出任何
	// 响应块（框架按「无有效内容即失败」结算）。
	if step.err != nil && step.delay == 0 {
		return nil, step.err
	}
	ch := make(chan *model.Response, len(step.chunks))
	go func() {
		if step.delay > 0 {
			select {
			case <-time.After(step.delay):
			case <-ctx.Done():
				m.mu.Lock()
				m.cancelled[i] = true
				m.mu.Unlock()
				return
			}
		}
		if step.err != nil {
			close(ch)
			return
		}
		for _, chunk := range step.chunks {
			ch <- chunk
		}
		close(ch)
	}()
	return ch, nil
}

func (m *stubModel) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

// cancelledAt 报告第 i 次调用是否因 ctx 取消而中止；取消是 winner 产生
// 后的异步传播，断言经 waitFor 的最终状态窗口兜住调度抖动。
func (m *stubModel) cancelledAt(i int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return i < len(m.cancelled) && m.cancelled[i]
}

// ---------- 断言小工具 ----------

// textResponse 产出携带助手文本的响应块。
func textResponse(text string, done bool) *model.Response {
	return &model.Response{
		Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: text}}},
		Done:    done,
	}
}

// errorResponse 产出响应级错误块（框架 failover/hedge 的切换触发面）。
func errorResponse(msg string) *model.Response {
	return &model.Response{Error: &model.ResponseError{Message: msg, Type: "api_error"}, Done: true}
}

// drainIter 以调用方协程序列形态消费模型，收集全部响应块。被测对象
// 恒实现 IterModel（组合模型与 openai 适配器），断言失败即测试错误。
func drainIter(t *testing.T, ctx context.Context, m model.Model) []*model.Response {
	t.Helper()
	iter, ok := m.(model.IterModel)
	if !ok {
		t.Fatalf("model %T does not implement IterModel", m)
	}
	return drainRequest(t, ctx, iter, model.NewRequest([]model.Message{{Role: model.RoleUser, Content: "hi"}}))
}

// drainRequest 同 drainIter，请求体由调用方给定（提示词日志测试用）。
// 被测对象取序列形态（组合模型与 openai 适配器均实现 IterModel）。
func drainRequest(t *testing.T, ctx context.Context, m model.IterModel, req *model.Request) []*model.Response {
	t.Helper()
	seq, err := m.GenerateContentIter(ctx, req)
	if err != nil {
		t.Fatalf("GenerateContentIter: %v", err)
	}
	var out []*model.Response
	seq(func(resp *model.Response) bool {
		out = append(out, resp)
		return true
	})
	return out
}

// lastText 取响应块流的最后一块首个选择文本。
func lastText(chunks []*model.Response) string {
	last := chunks[len(chunks)-1]
	if len(last.Choices) == 0 {
		return ""
	}
	return last.Choices[0].Message.Content
}

// waitFor 轮询 cond 至成立或超时（最终状态断言的调度裕量）。
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not met within 2s")
}

// captureLLMLogger 把 llmLoggerOf 换为写入缓冲的 grape 同构 logger
// （kratos NewLogger + TraceAttrs extractor，与 main.go 同一构造面），
// 返回缓冲供断言，测试结束自动还原。
func captureLLMLogger(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	logger := kratoslog.NewLogger(slog.NewJSONHandler(&buf, nil), kratoslog.WithExtractor(tracing.TraceAttrs))
	prev := llmLoggerOf
	llmLoggerOf = func() *slog.Logger { return logger }
	t.Cleanup(func() { llmLoggerOf = prev })
	return &buf
}

// llmLoggingConf 是回调日志测试的配置构造器（enabled 恒开，按需开关）。
func llmLoggingConf(prompts, completions bool) *conf.Agent_LLM_Logging {
	return &conf.Agent_LLM_Logging{
		Enabled:        true,
		LogPrompts:     prompts,
		LogCompletions: completions,
	}
}

// ---------- 装配面：conf → 结构 ----------

func Test_newLLMModel_when_all_defaults_returns_bare_openai_model(t *testing.T) {
	m, err := newLLMModel(&conf.Agent_LLM{Model: "gpt-test"})
	if err != nil {
		t.Fatalf("newLLMModel: %v", err)
	}
	// 全缺省（failover/hedge/logging 全关）必须零包装：返回值即裸
	// OpenAI 适配器，与 B1 装配逐字节等价。
	if _, ok := m.(*openai.Model); !ok {
		t.Fatalf("model type = %T, want *openai.Model (zero wrapping)", m)
	}
}

func Test_newLLMModel_when_failover_disabled_ignores_candidates(t *testing.T) {
	m, err := newLLMModel(&conf.Agent_LLM{
		Model: "gpt-test",
		Failover: &conf.Agent_LLM_Failover{
			Enabled:    false,
			Candidates: []*conf.Agent_LLM_Failover_Candidate{{Model: "backup"}},
		},
	})
	if err != nil {
		t.Fatalf("newLLMModel: %v", err)
	}
	if _, ok := m.(*openai.Model); !ok {
		t.Fatalf("model type = %T, want *openai.Model (enabled=false means zero wrapping)", m)
	}
}

func Test_newLLMModel_when_failover_enabled_without_candidates_fails(t *testing.T) {
	_, err := newLLMModel(&conf.Agent_LLM{
		Model:    "gpt-test",
		Failover: &conf.Agent_LLM_Failover{Enabled: true},
	})
	if err == nil {
		t.Fatal("failover enabled without candidates: want error, got none")
	}
}

func Test_newLLMModel_when_failover_candidate_model_missing_fails(t *testing.T) {
	_, err := newLLMModel(&conf.Agent_LLM{
		Model: "gpt-test",
		Failover: &conf.Agent_LLM_Failover{
			Enabled:    true,
			Candidates: []*conf.Agent_LLM_Failover_Candidate{{Provider: "openai"}},
		},
	})
	if err == nil {
		t.Fatal("failover candidate without model: want error, got none")
	}
}

func Test_newHedgeModel_when_max_copies_below_two_fails(t *testing.T) {
	_, err := newHedgeModel(
		&conf.Agent_LLM_Hedge{Enabled: true, MaxCopies: 1},
		newStubModel("primary"),
	)
	if err == nil {
		t.Fatal("hedge with max_copies=1: want error, got none")
	}
}

// ---------- failover 语义（替身候选） ----------

func Test_composeModel_when_primary_error_response_backup_takes_over(t *testing.T) {
	primary := newStubModel("primary", stubStep{chunks: []*model.Response{errorResponse("boom")}})
	backup := newStubModel("backup", stubStep{chunks: []*model.Response{textResponse("from backup", true)}})
	m, err := composeModel(&conf.Agent_LLM{
		Failover: &conf.Agent_LLM_Failover{Enabled: true},
	}, []model.Model{primary, backup})
	if err != nil {
		t.Fatalf("composeModel: %v", err)
	}

	chunks := drainIter(t, context.Background(), m)

	if got := lastText(chunks); got != "from backup" {
		t.Fatalf("final text = %q, want backup content", got)
	}
	if primary.callCount() != 1 || backup.callCount() != 1 {
		t.Fatalf("calls: primary=%d backup=%d, want 1 and 1", primary.callCount(), backup.callCount())
	}
}

func Test_composeModel_when_all_candidates_fail_aggregated_error_raised(t *testing.T) {
	primary := newStubModel("primary", stubStep{chunks: []*model.Response{errorResponse("primary down")}})
	backup := newStubModel("backup", stubStep{chunks: []*model.Response{errorResponse("backup down")}})
	m, err := composeModel(&conf.Agent_LLM{
		Failover: &conf.Agent_LLM_Failover{Enabled: true},
	}, []model.Model{primary, backup})
	if err != nil {
		t.Fatalf("composeModel: %v", err)
	}

	chunks := drainIter(t, context.Background(), m)

	last := chunks[len(chunks)-1]
	if last.Error == nil {
		t.Fatal("final response carries no error, want aggregated failure")
	}
	for _, name := range []string{"primary", "backup"} {
		if !strings.Contains(last.Error.Message, name) {
			t.Fatalf("aggregated error %q missing candidate %q", last.Error.Message, name)
		}
	}
}

// ---------- hedge 语义（替身候选） ----------

func Test_composeModel_when_hedge_copy_wins_slow_launch_cancelled(t *testing.T) {
	// 同一替身实例的两份副本：第一次调用慢（100ms 后出 slow），第二次
	// 快（立即出 fast）。hedge delay 1ms 后启动副本，fast 先到获胜，
	// 慢的那份在等待中被取消。
	slowThenFast := newStubModel("hedged",
		stubStep{delay: 100 * time.Millisecond, chunks: []*model.Response{textResponse("slow", true)}},
		stubStep{chunks: []*model.Response{textResponse("fast", true)}},
	)
	m, err := composeModel(&conf.Agent_LLM{
		Hedge: &conf.Agent_LLM_Hedge{Enabled: true, Delay: durationpb.New(time.Millisecond), MaxCopies: 2},
	}, []model.Model{slowThenFast})
	if err != nil {
		t.Fatalf("composeModel: %v", err)
	}

	chunks := drainIter(t, context.Background(), m)

	if got := lastText(chunks); got != "fast" {
		t.Fatalf("winner text = %q, want fast copy content", got)
	}
	waitFor(t, func() bool { return slowThenFast.cancelledAt(0) })
}

func Test_composeModel_when_all_hedge_copies_fail_error_raised(t *testing.T) {
	alwaysError := newStubModel("hedged", stubStep{chunks: []*model.Response{errorResponse("always down")}})
	m, err := composeModel(&conf.Agent_LLM{
		Hedge: &conf.Agent_LLM_Hedge{Enabled: true, Delay: durationpb.New(time.Millisecond), MaxCopies: 2},
	}, []model.Model{alwaysError})
	if err != nil {
		t.Fatalf("composeModel: %v", err)
	}

	chunks := drainIter(t, context.Background(), m)

	last := chunks[len(chunks)-1]
	if last.Error == nil || !strings.Contains(last.Error.Message, "always down") {
		t.Fatalf("final error = %v, want failure carrying the candidate error", last.Error)
	}
}

func Test_composeModel_when_hedge_and_failover_enabled_backup_follows_hedged_primary(t *testing.T) {
	// 组合次序断言：failover 在外、hedge 只包主模型——主模型两份副本
	// 全部失败后，failover 切到备选成功。
	primary := newStubModel("primary", stubStep{chunks: []*model.Response{errorResponse("primary down")}})
	backup := newStubModel("backup", stubStep{chunks: []*model.Response{textResponse("from backup", true)}})
	m, err := composeModel(&conf.Agent_LLM{
		Failover: &conf.Agent_LLM_Failover{Enabled: true},
		Hedge:    &conf.Agent_LLM_Hedge{Enabled: true, Delay: durationpb.New(time.Millisecond), MaxCopies: 2},
	}, []model.Model{primary, backup})
	if err != nil {
		t.Fatalf("composeModel: %v", err)
	}

	chunks := drainIter(t, context.Background(), m)

	if got := lastText(chunks); got != "from backup" {
		t.Fatalf("final text = %q, want backup content after hedged primary failed", got)
	}
	if got := primary.callCount(); got != 2 {
		t.Fatalf("primary calls = %d, want 2 (hedged copies)", got)
	}
}

// ---------- 超时参数化 ----------

func Test_newOpenAIModel_when_upstream_exceeds_timeout_reports_error(t *testing.T) {
	// 回环 httptest（非真实网络）：上游悬挂超过 conf 超时，验证
	// agent.llm.timeout 落到 HTTP client 后真实生效。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(300 * time.Millisecond):
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)

	m := newOpenAIModel("", srv.URL, "key", "gpt-test", durationpb.New(50*time.Millisecond))
	chunks := drainIter(t, context.Background(), m)

	last := chunks[len(chunks)-1]
	if last.Error == nil {
		t.Fatal("final response carries no error, want timeout surfaced as response error")
	}
}

// ---------- 回调日志（脱敏面） ----------

func Test_loggingModel_when_enabled_logs_request_and_response_metadata(t *testing.T) {
	buf := captureLLMLogger(t)
	stub := newStubModel("meta-model", stubStep{chunks: []*model.Response{textResponse("ok", true)}})
	m := newLoggingModel(stub, llmLoggingConf(false, false))

	drainIter(t, context.Background(), m)

	out := buf.String()
	if !strings.Contains(out, `"msg":"llm model request"`) || !strings.Contains(out, `"msg":"llm model response"`) {
		t.Fatalf("log output missing request/response lines:\n%s", out)
	}
	if got := strings.Count(out, `"msg":"llm model request"`); got != 1 {
		t.Fatalf("request line count = %d, want 1:\n%s", got, out)
	}
}

func Test_loggingModel_when_log_prompts_enabled_prompt_logged(t *testing.T) {
	buf := captureLLMLogger(t)
	stub := newStubModel("p-model", stubStep{chunks: []*model.Response{textResponse("ok", true)}})
	m := newLoggingModel(stub, llmLoggingConf(true, false))

	drainRequest(t, context.Background(), m,
		model.NewRequest([]model.Message{{Role: model.RoleUser, Content: "secret user prompt"}}))

	if !strings.Contains(buf.String(), "secret user prompt") {
		t.Fatalf("prompt text missing from logs:\n%s", buf.String())
	}
}

func Test_loggingModel_when_log_prompts_disabled_prompt_absent(t *testing.T) {
	buf := captureLLMLogger(t)
	stub := newStubModel("p-model", stubStep{chunks: []*model.Response{textResponse("ok", true)}})
	m := newLoggingModel(stub, llmLoggingConf(false, false))

	drainRequest(t, context.Background(), m,
		model.NewRequest([]model.Message{{Role: model.RoleUser, Content: "secret user prompt"}}))

	if strings.Contains(buf.String(), "secret user prompt") {
		t.Fatalf("prompt text leaked with log_prompts=false:\n%s", buf.String())
	}
}

func Test_loggingModel_when_log_completions_enabled_stream_chunks_accumulated(t *testing.T) {
	buf := captureLLMLogger(t)
	stub := newStubModel("c-model", stubStep{chunks: []*model.Response{
		{Choices: []model.Choice{{Delta: model.Message{Content: "hel"}}}},
		{Choices: []model.Choice{{Delta: model.Message{Content: "lo"}}}, Done: true},
	}})
	m := newLoggingModel(stub, llmLoggingConf(false, true))

	drainIter(t, context.Background(), m)

	if !strings.Contains(buf.String(), `"completion":"hello"`) {
		t.Fatalf("accumulated completion missing from logs:\n%s", buf.String())
	}
}

func Test_loggingModel_when_log_completions_disabled_completion_absent(t *testing.T) {
	buf := captureLLMLogger(t)
	stub := newStubModel("c-model", stubStep{chunks: []*model.Response{textResponse("hidden text", true)}})
	m := newLoggingModel(stub, llmLoggingConf(false, false))

	drainIter(t, context.Background(), m)

	if strings.Contains(buf.String(), "hidden text") {
		t.Fatalf("completion text leaked with log_completions=false:\n%s", buf.String())
	}
}

func Test_loggingModel_when_prompt_exceeds_limit_truncated(t *testing.T) {
	buf := captureLLMLogger(t)
	stub := newStubModel("t-model", stubStep{chunks: []*model.Response{textResponse("ok", true)}})
	m := newLoggingModel(stub, llmLoggingConf(true, false))

	drainRequest(t, context.Background(), m,
		model.NewRequest([]model.Message{{Role: model.RoleUser, Content: strings.Repeat("x", promptLogLimit+100)}}))

	out := buf.String()
	if !strings.Contains(out, "...(truncated)") {
		t.Fatalf("truncation marker missing:\n%s", out)
	}
	if strings.Contains(out, strings.Repeat("x", promptLogLimit+100)) {
		t.Fatalf("untruncated prompt leaked:\n%s", out)
	}
}

func Test_loggingModel_when_api_key_configured_key_never_logged(t *testing.T) {
	// api_key 的唯一落点是 HTTP client 配置面（newOpenAIModel），请求/
	// 响应载体与日志面均不触达；以全开日志 + 配置密钥值守卫该
	// 「天然无密钥」的口径：日志代码只触达 Logging 子段，读不到密钥。
	const apiKey = "sk-grape-llm-super-secret-key"
	c := &conf.Agent_LLM{
		Model:   "gpt-test",
		ApiKey:  apiKey,
		Logging: llmLoggingConf(true, true),
	}
	buf := captureLLMLogger(t)
	stub := newStubModel(c.GetModel(), stubStep{chunks: []*model.Response{textResponse("ok", true)}})
	m := wrapLogging(stub, c.GetLogging())

	drainIter(t, context.Background(), m)

	if strings.Contains(buf.String(), apiKey) {
		t.Fatalf("api key leaked into logs:\n%s", buf.String())
	}
}

func Test_loggingModel_when_ctx_carries_span_trace_id_logged(t *testing.T) {
	// E1 桥接口径：ctx 携带 SpanContext 时 trace_id 由 logger extractor
	// 自动展开。用合成 SpanContext（无需 OTel SDK）验证展开生效。
	buf := captureLLMLogger(t)
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10},
		SpanID:     trace.SpanID{0x0f, 0x0e, 0x0d, 0x0c, 0x0b, 0x0a, 0x09, 0x08},
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)
	stub := newStubModel("trace-model", stubStep{chunks: []*model.Response{textResponse("ok", true)}})
	m := newLoggingModel(stub, llmLoggingConf(false, false))

	drainIter(t, ctx, m)

	if !strings.Contains(buf.String(), sc.TraceID().String()) {
		t.Fatalf("trace_id %s missing from logs:\n%s", sc.TraceID().String(), buf.String())
	}
}

func Test_loggingModel_when_error_response_logged_at_warn_with_error_fields(t *testing.T) {
	buf := captureLLMLogger(t)
	stub := newStubModel("e-model", stubStep{chunks: []*model.Response{errorResponse("provider exploded")}})
	m := newLoggingModel(stub, llmLoggingConf(false, false))

	drainIter(t, context.Background(), m)

	out := buf.String()
	if !strings.Contains(out, `"level":"WARN"`) || !strings.Contains(out, "provider exploded") {
		t.Fatalf("error response not logged at warn with message:\n%s", out)
	}
}

func Test_composeModel_when_failover_switches_every_attempt_logged(t *testing.T) {
	// logging 包装在候选内层：failover 的每次候选尝试各记一条请求日志，
	// 切换过程可见到候选粒度。
	buf := captureLLMLogger(t)
	logging := llmLoggingConf(false, false)
	primary := newStubModel("primary", stubStep{chunks: []*model.Response{errorResponse("boom")}})
	backup := newStubModel("backup", stubStep{chunks: []*model.Response{textResponse("ok", true)}})
	m, err := composeModel(&conf.Agent_LLM{
		Failover: &conf.Agent_LLM_Failover{Enabled: true},
		Logging:  logging,
	}, []model.Model{wrapLogging(primary, logging), wrapLogging(backup, logging)})
	if err != nil {
		t.Fatalf("composeModel: %v", err)
	}

	drainIter(t, context.Background(), m)

	if got := strings.Count(buf.String(), `"msg":"llm model request"`); got != 2 {
		t.Fatalf("request log lines = %d, want 2 (one per attempt):\n%s", got, buf.String())
	}
}
