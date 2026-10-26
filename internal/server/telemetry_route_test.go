package server

import (
	"context"
	"io"
	"log/slog"
	nethttp "net/http"
	"sync"
	"testing"

	"grape/internal/agent"
	"grape/internal/biz"
	"grape/internal/conf"
	entrymw "grape/internal/middleware"
	"grape/internal/ops"
	"grape/internal/service"
	"grape/internal/telemetry"

	"github.com/jninng/trpc_agent_go_ack/acktest"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	nooptrace "go.opentelemetry.io/otel/trace/noop"
	frameworklog "trpc.group/trpc-go/trpc-agent-go/log"
	agenttelemetry "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

// discardLogger 是 server 层遥测测试的静默日志器替身。
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// frameworkLogGlobals 是 trpc-agent-go 日志门面全部可改写全局的快照载体，
// 恢复清单与 internal/telemetry/telemetry_test.go 的 restoreFramework 逐项
// 对照（Default/ContextDefault + 十一个 ctx 形态函数变量）。
type frameworkLogGlobals struct {
	defaultLogger, contextDefault frameworklog.Logger

	debug, debugfContext func(context.Context, ...any)
	debugfFmt            func(context.Context, string, ...any)
	info, infofContext   func(context.Context, ...any)
	infofFmt             func(context.Context, string, ...any)
	warn, warnfContext   func(context.Context, ...any)
	warnfFmt             func(context.Context, string, ...any)
	err, errfContext     func(context.Context, ...any)
	errfFmt              func(context.Context, string, ...any)
	fatal, fatalfContext func(context.Context, ...any)
	fatalfFmt            func(context.Context, string, ...any)
	tracefFmt            func(context.Context, string, ...any)
}

func snapshotFrameworkLogGlobals() frameworkLogGlobals {
	return frameworkLogGlobals{
		defaultLogger: frameworklog.Default, contextDefault: frameworklog.ContextDefault,
		debug: frameworklog.DebugContext, debugfFmt: frameworklog.DebugfContext,
		info: frameworklog.InfoContext, infofFmt: frameworklog.InfofContext,
		warn: frameworklog.WarnContext, warnfFmt: frameworklog.WarnfContext,
		err: frameworklog.ErrorContext, errfFmt: frameworklog.ErrorfContext,
		fatal: frameworklog.FatalContext, fatalfFmt: frameworklog.FatalfContext,
		tracefFmt: frameworklog.TracefContext,
	}
}

// restoreTelemetryGlobals 保存并注册恢复本包遥测测试改写的进程级全局：
// otel 全局 TracerProvider、agenttelemetry.Tracer 与框架日志门面。本测试
// 经 NewSetupWithExporter 实际只污染前两者（日志门面桥接仅 NewSetup 路径
// 安装），门面快照为防御性——装配改走 NewSetup 即被覆盖；otel 全局无读
// 回原 provider 的通道，按 telemetry 包同构口径恢复为 noop（与进程初始
// 语义等价）。
func restoreTelemetryGlobals(t *testing.T) {
	t.Helper()
	oldLogGlobals := snapshotFrameworkLogGlobals()
	oldTracer := agenttelemetry.Tracer
	t.Cleanup(func() {
		frameworklog.Default = oldLogGlobals.defaultLogger
		frameworklog.ContextDefault = oldLogGlobals.contextDefault
		frameworklog.DebugContext = oldLogGlobals.debug
		frameworklog.InfoContext = oldLogGlobals.info
		frameworklog.WarnContext = oldLogGlobals.warn
		frameworklog.ErrorContext = oldLogGlobals.err
		frameworklog.FatalContext = oldLogGlobals.fatal
		frameworklog.DebugfContext = oldLogGlobals.debugfFmt
		frameworklog.InfofContext = oldLogGlobals.infofFmt
		frameworklog.WarnfContext = oldLogGlobals.warnfFmt
		frameworklog.ErrorfContext = oldLogGlobals.errfFmt
		frameworklog.FatalfContext = oldLogGlobals.fatalfFmt
		frameworklog.TracefContext = oldLogGlobals.tracefFmt
		agenttelemetry.Tracer = oldTracer
		otel.SetTracerProvider(nooptrace.NewTracerProvider())
	})
}

// telemetryConf 返回启用态遥测配置（内存导出器路径，不发真实 OTLP）。
func telemetryConf() *conf.Telemetry {
	return &conf.Telemetry{
		Enabled: true, Endpoint: "localhost:4317", Insecure: true,
		SamplerRatio: 1, ServiceName: "grape-server-test",
	}
}

// snapshotExporter 包装内存导出器，在每次导出时自持快照：内存导出器的
// Shutdown 会清空自身缓存，而 server 的 telemetry cleanup（tp.Shutdown）
// 在断言前执行，快照让断言不受导出器清空影响。
type snapshotExporter struct {
	inner sdktrace.SpanExporter

	mu    sync.Mutex
	spans []tracetest.SpanStub
}

func newSnapshotExporter() *snapshotExporter {
	return &snapshotExporter{inner: tracetest.NewInMemoryExporter()}
}

func (e *snapshotExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	stubs := tracetest.SpanStubsFromReadOnlySpans(spans)
	e.mu.Lock()
	e.spans = append(e.spans, stubs...)
	e.mu.Unlock()
	return e.inner.ExportSpans(ctx, spans)
}

func (e *snapshotExporter) Shutdown(ctx context.Context) error {
	return e.inner.Shutdown(ctx)
}

func (e *snapshotExporter) snapshot() []tracetest.SpanStub {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]tracetest.SpanStub(nil), e.spans...)
}

// Test_server_span_exported_for_agent_route_when_telemetry_enabled 验证
// OTel 开启后 server span 真实产生并导出（TODO E1 验收口径）：AG-UI 路由
// 经 tracing.Server() 入链，401 快速路径即产生 span，telemetry cleanup
// 的 Shutdown 冲刷批量缓冲后进快照。
func Test_server_span_exported_for_agent_route_when_telemetry_enabled(t *testing.T) {
	restoreTelemetryGlobals(t)
	exp := newSnapshotExporter()
	tele, cleanup, err := telemetry.NewSetupWithExporter(telemetryConf(), exp, discardLogger())
	if err != nil {
		t.Fatalf("new telemetry setup: %v", err)
	}

	confServer := agentHTTPConf()
	svc, closeSessions, err := agent.NewMemorySessionService()
	if err != nil {
		t.Fatalf("build session service: %v", err)
	}
	t.Cleanup(closeSessions)
	stack, err := agent.NewStackWithOptions(agentConf(), nil, svc, agent.NewVectorStore(nil),
		biz.NewUserUsecase(&agentStubUserRepo{}), agent.WithModelOverride(acktest.NewScriptedModel("stub")))
	if err != nil {
		t.Fatalf("build agent stack: %v", err)
	}
	agentService := service.NewAgentService(stack)
	traceMiddleware, err := entrymw.NewTraceMiddleware(confServer)
	if err != nil {
		t.Fatalf("build trace middleware: %v", err)
	}
	authMiddleware := entrymw.NewAuthMiddleware(biz.NewSessionUsecase(&agentFakeSessionRepo{}))
	srv, err := NewHTTPServer(confServer, tele, traceMiddleware,
		NewRouteTimeoutMiddleware(confServer, agentService),
		authMiddleware, ops.NewMetricsHandler(nil), nil, nil, agentService, nil, nil)
	if err != nil {
		t.Fatalf("build http server: %v", err)
	}

	rec := agentDo(t, srv, nethttp.MethodPost, "/agui/", "", agentChatRequest("thread-1"))
	if rec.Code != nethttp.StatusUnauthorized {
		t.Fatalf("chat without token: status = %d, want 401", rec.Code)
	}

	// cleanup 触发 provider Shutdown，SDK 经 drainQueue 冲刷末批后停机。
	cleanup()
	stubs := exp.snapshot()
	if len(stubs) == 0 {
		t.Fatal("no span exported for agent route")
	}
	found := false
	for _, s := range stubs {
		if !s.SpanContext.TraceID().IsValid() {
			t.Fatalf("exported span %q has invalid trace id", s.Name)
		}
		if len(s.Name) >= 5 && s.Name[:5] == "/agui" {
			found = true
		}
	}
	if !found {
		names := make([]string, 0, len(stubs))
		for _, s := range stubs {
			names = append(names, s.Name)
		}
		t.Fatalf("no /agui span among exported spans: %v", names)
	}
}
