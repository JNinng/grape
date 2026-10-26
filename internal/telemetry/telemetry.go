// Package telemetry 是观测装配层：OTel TracerProvider 的生命周期（开关、
// 导出、采样、Shutdown）与框架观测面桥接（trpc-agent-go 日志门面与
// Tracer 全局变量）只在这里落子。装配经 Wire 以 *Setup 为依赖锚点——
// server / agent 等消费方持有它即可保证 OTel 全局注入先于一切 span 产生。
//
// 开关语义（TODO E1）：enabled=false 完全 noop——不构造 exporter、不发起
// 连接、不改写任何全局变量，全链路 span 保持 no-op；enabled=true 注入
// 全局 TracerProvider，Kratos otel 中间件与框架 span 随之真实导出。
// x-trace-id 响应头与日志 trace_id 字段来自 ctx 中的 SpanContext（由
// internal/middleware 链路中间件注入），与开关无关、恒可用。
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"grape/internal/conf"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// defaultServiceName 是 service.name 缺省值。
const defaultServiceName = "grape"

// shutdownTimeout 是进程退出时等待导出缓冲冲刷的时限：超时即放弃剩余
// span，不拖住停机。
const shutdownTimeout = 5 * time.Second

// Setup 是遥测装配物：enabled 事实 + Shutdown 清理。消费方（server /
// agent）以它为依赖锚点保证全局注入时序，本身只读 Enabled()。
type Setup struct {
	enabled bool
}

// Enabled 报告 OTel SDK 是否已注入全局。nil 接收者视为未启用（测试
// 装配常以 nil 占位）。
func (s *Setup) Enabled() bool {
	return s != nil && s.enabled
}

// NewSetup 装配 OTel SDK 与框架观测面桥接，返回注入物与进程退出清理。
//
// 框架日志桥接（bridgeFrameworkLog）与开关无关：无论是否启用 OTel，
// trpc-agent-go 的日志门面都在此接管到 grape logger，ctx 可得的调用点
// 自动携带 trace_id。enabled=false 时 SDK 完全 noop（零连接、零导出）。
func NewSetup(c *conf.Telemetry, logger *slog.Logger) (*Setup, func(), error) {
	if logger == nil {
		logger = slog.Default()
	}
	bridgeFrameworkLog(logger)
	if !c.GetEnabled() {
		return &Setup{}, func() {}, nil
	}
	if err := validate(c); err != nil {
		return nil, nil, err
	}
	opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(c.GetEndpoint())}
	// WithInsecure 无参开关：明文传输仅在配置声明时启用，缺省走系统证书 TLS。
	if c.GetInsecure() {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}
	exp, err := otlptracegrpc.New(context.Background(), opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("telemetry: create otlp exporter: %w", err)
	}
	return newSetupWithExporter(c, exp, logger)
}

// NewSetupWithExporter 以调用方提供的导出器走启用态装配，其余语义与
// NewSetup 一致。生产路径固定 OTLP gRPC 导出器；测试接缝注入内存导出器
// 断言 span 产生（真实 OTLP 导出不在单测验证面内）。
func NewSetupWithExporter(c *conf.Telemetry, exp sdktrace.SpanExporter, logger *slog.Logger) (*Setup, func(), error) {
	if logger == nil {
		logger = slog.Default()
	}
	return newSetupWithExporter(c, exp, logger)
}

// newSetupWithExporter 是启用态装配核心：构造 provider、注入全局、桥接
// 框架 Tracer，返回带 Shutdown 的 cleanup。
func newSetupWithExporter(c *conf.Telemetry, exp sdktrace.SpanExporter, logger *slog.Logger) (*Setup, func(), error) {
	tp, err := newTracerProvider(c, exp)
	if err != nil {
		return nil, nil, err
	}
	return &Setup{enabled: true}, func() {
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		// Shutdown 经 SDK 批量处理器的 drainQueue 冲刷末批后停掉后台
		// goroutine；超时即放弃剩余 span，不拖住停机。
		if err := tp.Shutdown(ctx); err != nil {
			logger.Error("otel tracer provider shutdown failed", "error", err)
		}
	}, nil
}

// validate 校验启用态配置：endpoint 必填；采样比例落在 [0,1]（0 视为
// 未设置按全采处理，需要零导出直接 enabled=false）。
func validate(c *conf.Telemetry) error {
	if c.GetEndpoint() == "" {
		return errors.New("telemetry: endpoint is required when enabled")
	}
	if r := c.GetSamplerRatio(); r < 0 || r > 1 {
		return fmt.Errorf("telemetry: sampler_ratio %g out of range [0,1]", r)
	}
	return nil
}

// newTracerProvider 构造 SDK TracerProvider 并注入全局：资源属性取
// conf（service.name 空回落默认，service.version 空省略），采样按
// trace-id 确定性比例判定。导出器由调用方注入（生产为 OTLP gRPC，
// 测试换内存导出器断言 span 产生）。全局注入是本函数的副作用——经
// NewSetup 的依赖锚点保证先于 server 构造执行。
func newTracerProvider(c *conf.Telemetry, exp sdktrace.SpanExporter) (*sdktrace.TracerProvider, error) {
	attrs := []attribute.KeyValue{
		attribute.String("service.name", orDefault(c.GetServiceName(), defaultServiceName)),
	}
	if v := c.GetServiceVersion(); v != "" {
		attrs = append(attrs, attribute.String("service.version", v))
	}
	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(attrs...))
	if err != nil {
		return nil, fmt.Errorf("telemetry: build resource: %w", err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.TraceIDRatioBased(orDefault(c.GetSamplerRatio(), 1))),
	)
	otel.SetTracerProvider(tp)
	bridgeFrameworkTracer()
	return tp, nil
}

// orDefault 是取值回落：v 为零值时取 fallback（proto3 标量零值与未设置
// 同形，sampler_ratio=0 按未设置语义回落全采）。
func orDefault[T comparable](v, fallback T) T {
	var zero T
	if v == zero {
		return fallback
	}
	return v
}
