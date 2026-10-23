package middleware

import (
	"context"
	"encoding/hex"
	"fmt"
	"net"
	nethttp "net/http"
	"net/netip"
	"strings"

	"grape/internal/conf"
	"grape/pkg/utils"

	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/transport"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/peer"
)

// traceparentKey 是 W3C 链路传播头；置空即剥头（Header 接口无 Del）。
const traceparentKey = "traceparent"

const (
	// traceIDHeader 是链路 ID 的回传头，恒回传（成功与错误统一）。
	traceIDHeader = "x-trace-id"
	// requestIDHeader 是请求 ID 的回传头：上游带则原样回传，未带以 trace-id 兜底。
	requestIDHeader = "x-request-id"
)

// TraceMiddleware 是链路中间件的注入类型。它与认证中间件同为 Kratos 的
// middleware.Middleware，而 wire 按参数类型装配，同型双参无法区分，
// 故以命名类型承载；服务器入口装配链时转回 middleware.Middleware，
// 中间件本体仍是普通 middleware.Middleware。
type TraceMiddleware middleware.Middleware

// NewTraceMiddleware 构造链路标识中间件，是所有入站请求的链路唯一生成点，
// 单件同时服务 HTTP 与 gRPC（同一实现，两协议同链、同 trace-id）。
// trusted_networks 含非法 CIDR 时返回错误，装配失败即拒绝启动，与数据侧
// 构造函数的错误口径一致。
//
// 生命周期：
//   - 继承：上游 traceparent 格式合法（W3C）且对端 IP 落在
//     server.trace.trusted_networks 内才被继承；配置为空一律不可信
//     （fail-closed，新建根），不继承时剥头置空，下游不可再读到原值。
//   - 新建根：trace-id 取 utils.NewTraceID，flags 置 sampled；本跳
//     span-id 恒取 utils.NewSpanID（继承只认 trace-id，不认上游 span）。
//   - 注入 ctx：构造 otel SpanContext 放进 ctx，日志链路字段（trace_id/
//     span_id）由默认 logger 的 tracing.TraceAttrs extractor 自动携带，
//     下游 slog.XxxContext 无需手工拼链路字段。
//   - 回传：调 handler 之前写 ReplyHeader，错误路径同样回传——HTTP 走
//     响应头，gRPC 经初始 metadata，两协议统一口径；trace-id 不进响应体、
//     不进 errors.Metadata。将来配 CORS 时需补
//     Access-Control-Expose-Headers: x-trace-id, x-request-id。
//
// 本实现自研生成；接入 otel SDK 时只需把 NewTraceID/NewSpanID 换成真实
// tracer 的生成实现，中间件形态不变。对端取不到传输上下文时原样放行
// （无头可回、无链路可标，与 auth 的 fail-closed 不同）。
func NewTraceMiddleware(c *conf.Server) (TraceMiddleware, error) {
	// 网段在启动期解析一次：配置项非法属配置错误，返回错误不带病启动。
	var trusted []netip.Prefix
	for _, cidr := range c.GetTrace().GetTrustedNetworks() {
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil {
			return nil, fmt.Errorf("invalid trace trusted network %q: %w", cidr, err)
		}
		trusted = append(trusted, prefix)
	}
	return func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			tr, ok := transport.FromServerContext(ctx)
			if !ok {
				return handler(ctx, req)
			}

			upstreamID, _, flags, valid := parseTraceparent(tr.RequestHeader().Get(traceparentKey))
			if !valid || !remoteTrusted(ctx, tr, trusted) {
				// 剥头：carrier 无 Del，置空即可，下游不可再读到原值。
				tr.RequestHeader().Set(traceparentKey, "")
				upstreamID = utils.NewTraceID()
				flags = trace.FlagsSampled
			}
			sc := trace.NewSpanContext(trace.SpanContextConfig{
				TraceID:    upstreamID,
				SpanID:     utils.NewSpanID(),
				TraceFlags: flags,
				Remote:     true,
			})

			traceID := sc.TraceID().String()
			reply := tr.ReplyHeader()
			reply.Set(traceIDHeader, traceID)
			if requestID := tr.RequestHeader().Get(requestIDHeader); requestID != "" {
				reply.Set(requestIDHeader, requestID)
			} else {
				reply.Set(requestIDHeader, traceID)
			}

			return handler(trace.ContextWithSpanContext(ctx, sc), req)
		}
	}, nil
}

// parseTraceparent 按 W3C 格式解析 traceparent：
// version(2 位十六进制，≠ff)-trace-id(32 位十六进制，非零)-span-id(16 位
// 十六进制，非零)-flags(2 位十六进制)，大小写不敏感按小写处理。
// 任一段不合法即整体拒绝，返回 ok=false。
func parseTraceparent(value string) (trace.TraceID, trace.SpanID, trace.TraceFlags, bool) {
	var traceID trace.TraceID
	var spanID trace.SpanID
	parts := strings.Split(strings.ToLower(value), "-")
	if len(parts) != 4 {
		return traceID, spanID, 0, false
	}
	version, err := hex.DecodeString(parts[0])
	if err != nil || len(version) != 1 || parts[0] == "ff" {
		return traceID, spanID, 0, false
	}
	id, err := hex.DecodeString(parts[1])
	if err != nil || len(id) != 16 {
		return traceID, spanID, 0, false
	}
	copy(traceID[:], id)
	span, err := hex.DecodeString(parts[2])
	if err != nil || len(span) != 8 {
		return traceID, spanID, 0, false
	}
	copy(spanID[:], span)
	flagBytes, err := hex.DecodeString(parts[3])
	if err != nil || len(flagBytes) != 1 {
		return traceID, spanID, 0, false
	}
	if !traceID.IsValid() || !spanID.IsValid() {
		return traceID, spanID, 0, false
	}
	return traceID, spanID, trace.TraceFlags(flagBytes[0]), true
}

// remoteTrusted 判定请求对端 IP 是否落在可信网段内。对端地址优先取
// HTTP 的 RemoteAddr（Transporter 可断言出 Request()），gRPC 经 grpc-go
// 原生 peer（kratos 不注入对端，但 grpc-go 把 peer 放进了 handler ctx）；
// 都取不到视为不可信。网段匹配统一 Unmap 后比较，兼容 IPv4-mapped IPv6。
func remoteTrusted(ctx context.Context, tr transport.Transporter, trusted []netip.Prefix) bool {
	var addr string
	if htr, ok := tr.(interface{ Request() *nethttp.Request }); ok && htr.Request() != nil {
		addr = htr.Request().RemoteAddr
	} else if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		addr = p.Addr.String()
	} else {
		return false
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	for _, prefix := range trusted {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}
