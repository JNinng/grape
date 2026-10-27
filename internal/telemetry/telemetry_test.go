package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"grape/internal/conf"

	ktracing "github.com/go-kratos/kratos/contrib/otel/v3/tracing"
	klog "github.com/go-kratos/kratos/v3/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	nooptrace "go.opentelemetry.io/otel/trace/noop"
	frameworklog "trpc.group/trpc-go/trpc-agent-go/log"
	agenttelemetry "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

// spanFixture 是 W3C 规范样例链路事实：ctx 注入与断言共用。
var spanFixture = mustSpanContext(
	"4bf92f3577b34da6a3ce929d0e0e4736", "00f067aa0ba902b7")

func mustSpanContext(traceIDHex, spanIDHex string) trace.SpanContext {
	tid, err := trace.TraceIDFromHex(traceIDHex)
	if err != nil {
		panic(err)
	}
	sid, err := trace.SpanIDFromHex(spanIDHex)
	if err != nil {
		panic(err)
	}
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled, Remote: true,
	})
}

// captureHandler 是并发安全的 slog JSON 捕获器：断言读缓冲与日志写并发
// 安全（桥接安装是进程级副作用，测试内可能被框架异步日志触碰）。
type captureHandler struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	attrs := map[string]string{}
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.String()
		return true
	})
	return json.NewEncoder(&h.buf).Encode(map[string]any{
		"level": r.Level.String(), "msg": r.Message, "attrs": attrs,
	})
}

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

func (h *captureHandler) contains(substr string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return strings.Contains(h.buf.String(), substr)
}

func testLogger() *slog.Logger { return slog.New(&captureHandler{}) }

// frameworkCtxVars 是框架全部 ctx 形态日志函数变量的快照载体：桥接改写
// 进程级全局，用例保存现场、退出恢复，测试互不依赖。
type frameworkCtxVars struct {
	debug, debugf func(context.Context, ...any)
	debugfFmt     func(context.Context, string, ...any)
	info, infof   func(context.Context, ...any)
	infofFmt      func(context.Context, string, ...any)
	warn, warnf   func(context.Context, ...any)
	warnfFmt      func(context.Context, string, ...any)
	err, errf     func(context.Context, ...any)
	errfFmt       func(context.Context, string, ...any)
	fatal, fatalf func(context.Context, ...any)
	fatalfFmt     func(context.Context, string, ...any)
	tracefFmt     func(context.Context, string, ...any)
}

func snapshotFrameworkCtxVars() frameworkCtxVars {
	return frameworkCtxVars{
		debug: frameworklog.DebugContext, debugfFmt: frameworklog.DebugfContext,
		info: frameworklog.InfoContext, infofFmt: frameworklog.InfofContext,
		warn: frameworklog.WarnContext, warnfFmt: frameworklog.WarnfContext,
		err: frameworklog.ErrorContext, errfFmt: frameworklog.ErrorfContext,
		fatal: frameworklog.FatalContext, fatalfFmt: frameworklog.FatalfContext,
		tracefFmt: frameworklog.TracefContext,
	}
}

// restoreFramework 恢复框架日志门面、Tracer 与 otel 全局：桥接是进程级
// 改写，用例各自保存现场。
func restoreFramework(t *testing.T) {
	t.Helper()
	oldDefault := frameworklog.Default
	oldCtxDefault := frameworklog.ContextDefault
	oldCtxVars := snapshotFrameworkCtxVars()
	oldTracer := agenttelemetry.Tracer
	t.Cleanup(func() {
		frameworklog.Default = oldDefault
		frameworklog.ContextDefault = oldCtxDefault
		frameworklog.DebugContext = oldCtxVars.debug
		frameworklog.InfoContext = oldCtxVars.info
		frameworklog.WarnContext = oldCtxVars.warn
		frameworklog.ErrorContext = oldCtxVars.err
		frameworklog.FatalContext = oldCtxVars.fatal
		frameworklog.DebugfContext = oldCtxVars.debugfFmt
		frameworklog.InfofContext = oldCtxVars.infofFmt
		frameworklog.WarnfContext = oldCtxVars.warnfFmt
		frameworklog.ErrorfContext = oldCtxVars.errfFmt
		frameworklog.FatalfContext = oldCtxVars.fatalfFmt
		frameworklog.TracefContext = oldCtxVars.tracefFmt
		agenttelemetry.Tracer = oldTracer
		otel.SetTracerProvider(nooptrace.NewTracerProvider())
	})
}

// enabledConf 返回启用态遥测配置（endpoint 仅满足校验，测试不真连）。
func enabledConf() *conf.Telemetry {
	return &conf.Telemetry{
		Enabled: true, Endpoint: "localhost:4317", Insecure: true,
		SamplerRatio: 1, ServiceName: "grape-test",
	}
}

func Test_new_setup_disabled_is_fully_noop(t *testing.T) {
	restoreFramework(t)
	setup, cleanup, err := NewSetup(&conf.Telemetry{Enabled: false}, testLogger())
	if err != nil {
		t.Fatalf("new setup disabled: %v", err)
	}
	defer cleanup()
	if setup.Enabled() {
		t.Fatal("disabled setup must report not enabled")
	}
	_, span := otel.Tracer("telemetry-test").Start(context.Background(), "disabled-span")
	defer span.End()
	if span.IsRecording() {
		t.Fatal("global provider must stay noop when telemetry disabled")
	}
}

func Test_new_setup_enabled_requires_endpoint(t *testing.T) {
	restoreFramework(t)
	c := enabledConf()
	c.Endpoint = ""
	if _, _, err := NewSetup(c, testLogger()); err == nil {
		t.Fatal("enabled setup without endpoint must fail")
	}
}

func Test_new_setup_enabled_rejects_ratio_out_of_range(t *testing.T) {
	restoreFramework(t)
	for _, ratio := range []float64{-0.5, 1.5} {
		c := enabledConf()
		c.SamplerRatio = ratio
		if _, _, err := NewSetup(c, testLogger()); err == nil {
			t.Fatalf("sampler_ratio %g must be rejected", ratio)
		}
	}
}

func Test_new_setup_enabled_injects_global_provider_and_cleans_up(t *testing.T) {
	restoreFramework(t)
	setup, cleanup, err := NewSetup(enabledConf(), testLogger())
	if err != nil {
		t.Fatalf("new setup enabled: %v", err)
	}
	if !setup.Enabled() {
		t.Fatal("enabled setup must report enabled")
	}
	_, span := otel.Tracer("telemetry-test").Start(context.Background(), "enabled-span")
	// IsRecording 必须在 End 之前断言：SDK 的 recordingSpan 结束后恒报
	// false（已结束即不再记录），这是 SDK 语义而非导出失败。
	if !span.IsRecording() {
		t.Fatal("global provider must record spans when telemetry enabled")
	}
	span.End()
	cleanup() // Shutdown 干净收尾：不悬挂、不报错
}

// Test_new_tracer_provider_exports_spans_to_memory_exporter 以内存导出器
// 断言 span 真实产生与导出：全局 provider 注入后，
// grape 侧与框架桥接侧的 span 都进导出器，资源属性与 scope 身份正确。
func Test_new_tracer_provider_exports_spans_to_memory_exporter(t *testing.T) {
	restoreFramework(t)
	exp := tracetest.NewInMemoryExporter()
	tp, err := newTracerProvider(enabledConf(), exp)
	if err != nil {
		t.Fatalf("new tracer provider: %v", err)
	}
	defer func() { _ = tp.Shutdown(context.Background()) }()

	ctx, span := otel.Tracer("telemetry-test").Start(context.Background(), "grape-side-span")
	_, fwSpan := agenttelemetry.Tracer.Start(ctx, "framework-side-span")
	fwSpan.End()
	span.End()
	// 批量导出器异步冲刷，断言前强制落盘保证确定性。
	if err := tp.ForceFlush(context.Background()); err != nil {
		t.Fatalf("force flush: %v", err)
	}

	stubs := exp.GetSpans()
	var grapeName, frameworkName, serviceName string
	for _, s := range stubs {
		switch s.Name {
		case "grape-side-span":
			grapeName = s.Name
			serviceName = serviceNameAttr(s)
		case "framework-side-span":
			frameworkName = s.Name
			if s.InstrumentationScope.Name != frameworkInstrumentName {
				t.Fatalf("framework span scope = %q, want %q",
					s.InstrumentationScope.Name, frameworkInstrumentName)
			}
		}
	}
	if grapeName == "" {
		t.Fatalf("grape side span not exported, got %d spans", len(stubs))
	}
	if frameworkName == "" {
		t.Fatalf("framework side span not exported, got %d spans", len(stubs))
	}
	if serviceName != "grape-test" {
		t.Fatalf("service.name resource attr = %q, want %q", serviceName, "grape-test")
	}
}

func serviceNameAttr(s tracetest.SpanStub) string {
	for _, attr := range s.Resource.Attributes() {
		if string(attr.Key) == "service.name" {
			return attr.Value.AsString()
		}
	}
	return ""
}

// --- 桥接 ---

// Test_bridge_ctx_log_carries_trace_id 验证框架日志桥接的链路贯通：
// ctx 形态调用经 grape logger（kratos extractor）自动补 trace_id/span_id。
func Test_bridge_ctx_log_carries_trace_id(t *testing.T) {
	restoreFramework(t)
	h := &captureHandler{}
	logger := klog.NewLogger(h, klog.WithExtractor(ktracing.TraceAttrs))
	bridgeFrameworkLog(logger)

	ctx := trace.ContextWithSpanContext(context.Background(), spanFixture)
	frameworklog.WarnfContext(ctx, "bridge probe %d", 7)

	if !h.contains(`"msg":"bridge probe 7"`) {
		t.Fatalf("bridged log missing message")
	}
	if !h.contains(`"trace_id":"` + spanFixture.TraceID().String() + `"`) {
		t.Fatalf("bridged log missing trace_id")
	}
	if !h.contains(`"span_id":"` + spanFixture.SpanID().String() + `"`) {
		t.Fatalf("bridged log missing span_id")
	}
}

// Test_bridge_plain_log_without_context_has_no_trace_id 如实固化局限：
// 框架无 ctx 调用点不伪造链路字段。
func Test_bridge_plain_log_without_context_has_no_trace_id(t *testing.T) {
	restoreFramework(t)
	h := &captureHandler{}
	bridgeFrameworkLog(klog.NewLogger(h))
	frameworklog.Infof("plain probe %s", "abc")
	if !h.contains(`"msg":"plain probe abc"`) {
		t.Fatalf("bridged plain log missing message")
	}
	if h.contains("trace_id") {
		t.Fatalf("ctx-less framework log must not fabricate trace_id")
	}
}

func Test_bridge_tracef_respects_trace_enabled_flag(t *testing.T) {
	restoreFramework(t)
	h := &captureHandler{}
	bridgeFrameworkLog(klog.NewLogger(h))

	frameworklog.SetTraceEnabled(false)
	frameworklog.TracefContext(context.Background(), "trace off %d", 1)
	if h.contains("trace off 1") {
		t.Fatal("TracefContext must be gated by trace flag")
	}
	frameworklog.SetTraceEnabled(true)
	t.Cleanup(func() { frameworklog.SetTraceEnabled(false) })
	frameworklog.TracefContext(context.Background(), "trace on %d", 2)
	if !h.contains(`"msg":"[TRACE] trace on 2"`) {
		t.Fatalf("TracefContext output missing")
	}
}

func Test_bridge_sugar_logger_interface_levels(t *testing.T) {
	restoreFramework(t)
	h := &captureHandler{}
	bridgeFrameworkLog(klog.NewLogger(h))
	frameworklog.Debug("d")
	frameworklog.Info("i")
	frameworklog.Warn("w")
	frameworklog.Error("e")
	frameworklog.Debugf("df %d", 1)
	frameworklog.Infof("if %d", 1)
	frameworklog.Warnf("wf %d", 1)
	frameworklog.Errorf("ef %d", 1)
	for _, want := range []string{
		`"msg":"d"`, `"msg":"i"`, `"msg":"w"`, `"msg":"e"`,
		`"msg":"df 1"`, `"msg":"if 1"`, `"msg":"wf 1"`, `"msg":"ef 1"`,
	} {
		if !h.contains(want) {
			t.Fatalf("expected %s in bridged output", want)
		}
	}
}

// Test_bridge_ctx_var_family_levels 覆盖 ctx 形态函数变量的级别映射：
// Info/Warn/Error 三级各一条，防桥接遗漏某个框架调用面。
func Test_bridge_ctx_var_family_levels(t *testing.T) {
	restoreFramework(t)
	h := &captureHandler{}
	bridgeFrameworkLog(klog.NewLogger(h))
	ctx := context.Background()
	frameworklog.InfoContext(ctx, "ci")
	frameworklog.WarnContext(ctx, "cw")
	frameworklog.ErrorContext(ctx, "ce")
	frameworklog.InfofContext(ctx, "cif %d", 1)
	frameworklog.WarnfContext(ctx, "cwf %d", 1)
	frameworklog.ErrorfContext(ctx, "cef %d", 1)
	for _, want := range []string{
		`"msg":"ci"`, `"msg":"cw"`, `"msg":"ce"`,
		`"msg":"cif 1"`, `"msg":"cwf 1"`, `"msg":"cef 1"`,
	} {
		if !h.contains(want) {
			t.Fatalf("expected %s in bridged output", want)
		}
	}
}
