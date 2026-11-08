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

// bridgeFrameworkLog 把 trpc-agent-go 日志门面桥接到 grape logger：无 ctx
// 形态经适配器转发（不带 trace_id，框架既成事实），ctx 形态转发 ctx 由
// grape logger 的提取器自动补链路字段。级别归 grape 日志配置，Fatal 保持
// 记 error 后退出；完整局限见 README。
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

// bridgeFrameworkTracer 把框架 Tracer 全局变量桥到 grape 注入的全局
// TracerProvider，使框架经 internal/trace.StartSpan 产生的 span 真实导出。
// OTel 关闭时保持框架自带 no-op 默认值，本函数不被调用；grape 装配先于
// 框架自身 telemetry/trace.Start 的重设，生命周期内不被覆盖。
func bridgeFrameworkTracer() {
	agenttelemetry.Tracer = otel.GetTracerProvider().Tracer(frameworkInstrumentName)
}
