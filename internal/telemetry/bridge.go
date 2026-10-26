package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"go.opentelemetry.io/otel"
	frameworklog "trpc.group/trpc-go/trpc-agent-go/log"
	agenttelemetry "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

// frameworkInstrumentName 与框架 internal/telemetry.InstrumentName 同值：
// 桥接后框架 span 的 instrumentation scope 保持 "trpc.agent.go" 身份，
// 后端侧按 scope 聚合的视图不因桥接变形。
const frameworkInstrumentName = "trpc.agent.go"

// bridgeFrameworkLog 把 trpc-agent-go 的日志门面（package log）桥接到
// grape logger，覆盖 LLMAgent / runner / AG-UI / model 等全部框架日志面。
//
// 两类形态分别处理：
//   - 无 ctx 形态（log.Infof 等 → Default / ContextDefault）：替换为适配
//     器实现框架 log.Logger 接口，转发到 grape logger。门面 API 不收
//     ctx，这类日志不带 trace_id——框架在无 ctx 调用点的既成事实，如实
//     保留局限，不伪造链路字段。
//   - ctx 形态（log.InfofContext 等：package 级函数变量）：覆盖为转发
//     ctx 的实现。grape logger 由 kratos log.NewLogger(WithExtractor(
//     tracing.TraceAttrs)) 构造，ctx 中的 SpanContext 在落盘时自动展开
//     为 trace_id / span_id 字段，桥接层不重复拼装。
//
// 已知局限（随桥接如实声明）：
//   - 框架 log.SetLevel 治理的是框架自带 zap logger，桥接后不再影响
//     任何输出——级别归 grape 日志配置；
//   - log.Tracef / TracefContext 的开关语义保留（IsTraceEnabled 门控），
//     输出级别落 grape debug；
//   - Fatal 保持 zap 语义：记 error 后 os.Exit(1)。当前依赖面（llmagent
//     / agui / runner / session / model）无 Fatal 调用点，纯语义保真。
func bridgeFrameworkLog(logger *slog.Logger) {
	adapter := &frameworkLogger{logger: logger}
	frameworklog.Default = adapter
	frameworklog.ContextDefault = adapter

	frameworklog.DebugContext = func(ctx context.Context, args ...any) {
		logger.DebugContext(ctx, fmt.Sprint(args...))
	}
	frameworklog.DebugfContext = func(ctx context.Context, format string, args ...any) {
		logger.DebugContext(ctx, fmt.Sprintf(format, args...))
	}
	frameworklog.InfoContext = func(ctx context.Context, args ...any) {
		logger.InfoContext(ctx, fmt.Sprint(args...))
	}
	frameworklog.InfofContext = func(ctx context.Context, format string, args ...any) {
		logger.InfoContext(ctx, fmt.Sprintf(format, args...))
	}
	frameworklog.WarnContext = func(ctx context.Context, args ...any) {
		logger.WarnContext(ctx, fmt.Sprint(args...))
	}
	frameworklog.WarnfContext = func(ctx context.Context, format string, args ...any) {
		logger.WarnContext(ctx, fmt.Sprintf(format, args...))
	}
	frameworklog.ErrorContext = func(ctx context.Context, args ...any) {
		logger.ErrorContext(ctx, fmt.Sprint(args...))
	}
	frameworklog.ErrorfContext = func(ctx context.Context, format string, args ...any) {
		logger.ErrorContext(ctx, fmt.Sprintf(format, args...))
	}
	frameworklog.FatalContext = func(ctx context.Context, args ...any) {
		logger.ErrorContext(ctx, fmt.Sprint(args...))
		os.Exit(1)
	}
	frameworklog.FatalfContext = func(ctx context.Context, format string, args ...any) {
		logger.ErrorContext(ctx, fmt.Sprintf(format, args...))
		os.Exit(1)
	}
	frameworklog.TracefContext = func(ctx context.Context, format string, args ...any) {
		if !frameworklog.IsTraceEnabled() {
			return
		}
		logger.DebugContext(ctx, "[TRACE] "+fmt.Sprintf(format, args...))
	}
}

// frameworkLogger 适配框架无 ctx 的 log.Logger 接口到 grape logger：
// 全部调用以 context.Background() 落盘（无链路字段），Fatal 保持退出
// 语义。方法集与框架 log.Logger 一一对应。
type frameworkLogger struct {
	logger *slog.Logger
}

func (l *frameworkLogger) Debug(args ...any) {
	l.logger.Log(context.Background(), slog.LevelDebug, fmt.Sprint(args...))
}

func (l *frameworkLogger) Debugf(format string, args ...any) {
	l.logger.Log(context.Background(), slog.LevelDebug, fmt.Sprintf(format, args...))
}

func (l *frameworkLogger) Info(args ...any) {
	l.logger.Log(context.Background(), slog.LevelInfo, fmt.Sprint(args...))
}

func (l *frameworkLogger) Infof(format string, args ...any) {
	l.logger.Log(context.Background(), slog.LevelInfo, fmt.Sprintf(format, args...))
}

func (l *frameworkLogger) Warn(args ...any) {
	l.logger.Log(context.Background(), slog.LevelWarn, fmt.Sprint(args...))
}

func (l *frameworkLogger) Warnf(format string, args ...any) {
	l.logger.Log(context.Background(), slog.LevelWarn, fmt.Sprintf(format, args...))
}

func (l *frameworkLogger) Error(args ...any) {
	l.logger.Log(context.Background(), slog.LevelError, fmt.Sprint(args...))
}

func (l *frameworkLogger) Errorf(format string, args ...any) {
	l.logger.Log(context.Background(), slog.LevelError, fmt.Sprintf(format, args...))
}

func (l *frameworkLogger) Fatal(args ...any) {
	l.logger.Log(context.Background(), slog.LevelError, fmt.Sprint(args...))
	os.Exit(1)
}

func (l *frameworkLogger) Fatalf(format string, args ...any) {
	l.logger.Log(context.Background(), slog.LevelError, fmt.Sprintf(format, args...))
	os.Exit(1)
}

// bridgeFrameworkTracer 把框架的 Tracer 全局变量桥到 grape 注入的全局
// TracerProvider：LLM / 工具 / agent 的框架 span（经 internal/trace
// StartSpan 读该变量）随之真实产生并导出。OTel 关闭时框架变量保持其
// 自带 no-op 默认值，本函数不被调用。
//
// 框架在自身 telemetry/trace.Start 里也会重设该变量（其自有 SDK 接入
// 路径，grape 未使用）；grape 侧装配先于 AG-UI 请求服务，正常生命周期
// 内不会被覆盖。
func bridgeFrameworkTracer() {
	agenttelemetry.Tracer = otel.GetTracerProvider().Tracer(frameworkInstrumentName)
}
