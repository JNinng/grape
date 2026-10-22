package server

import (
	"context"
	"net"
	nethttp "net/http"
	"testing"

	"grape/internal/conf"

	"github.com/go-kratos/kratos/v3/transport"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/peer"
)

// 上游 traceparent 样例（W3C trace-context 规范示例值）。
const (
	upstreamTraceID     = "4bf92f3577b34da6a3ce929d0e0e4736"
	upstreamSpanID      = "00f067aa0ba902b7"
	upstreamTraceparent = "00-" + upstreamTraceID + "-" + upstreamSpanID + "-01"
)

// httpTraceTransporter 模拟 Kratos HTTP 传输：可断言出 Request() 以提供
// RemoteAddr；请求/响应头分离，避免响应写入污染请求读取的断言。
type httpTraceTransporter struct {
	reqHeader   transport.Header
	replyHeader transport.Header
	request     *nethttp.Request
}

func (f *httpTraceTransporter) Kind() transport.Kind            { return transport.KindHTTP }
func (f *httpTraceTransporter) Endpoint() string                { return "test://" }
func (f *httpTraceTransporter) Operation() string               { return "test.Operation" }
func (f *httpTraceTransporter) RequestHeader() transport.Header { return f.reqHeader }
func (f *httpTraceTransporter) ReplyHeader() transport.Header   { return f.replyHeader }
func (f *httpTraceTransporter) Request() *nethttp.Request       { return f.request }

// newHTTPTraceTransporter 构造 HTTP 形态传输事实：响应头默认独立准备，
// 与 Kratos 传输的请求/响应头分离形态一致。
func newHTTPTraceTransporter(remoteAddr string, reqHeader transport.Header) *httpTraceTransporter {
	return &httpTraceTransporter{
		reqHeader:   reqHeader,
		replyHeader: fakeHeader{},
		request:     &nethttp.Request{RemoteAddr: remoteAddr},
	}
}

// grpcTraceTransporter 模拟 Kratos gRPC 传输：没有 Request()，对端地址
// 经 grpc-go 原生 peer 放进 ctx。
type grpcTraceTransporter struct {
	reqHeader   transport.Header
	replyHeader transport.Header
}

func (f *grpcTraceTransporter) Kind() transport.Kind            { return transport.KindGRPC }
func (f *grpcTraceTransporter) Endpoint() string                { return "test://" }
func (f *grpcTraceTransporter) Operation() string               { return "test.Operation" }
func (f *grpcTraceTransporter) RequestHeader() transport.Header { return f.reqHeader }
func (f *grpcTraceTransporter) ReplyHeader() transport.Header   { return f.replyHeader }

// newGRPCTraceTransporter 构造 gRPC 形态传输事实，响应头同样独立准备。
func newGRPCTraceTransporter(reqHeader transport.Header) *grpcTraceTransporter {
	return &grpcTraceTransporter{
		reqHeader:   reqHeader,
		replyHeader: fakeHeader{},
	}
}

// trustedTraceConfig 返回信任本机回环网段的配置。
func trustedTraceConfig() *conf.Server {
	return &conf.Server{Trace: &conf.Server_Trace{
		TrustedNetworks: []string{"127.0.0.1/32", "::1/128"},
	}}
}

// runTrace 跑通中间件并捕获 handler 收到的 span context。
func runTrace(t *testing.T, m TraceMiddleware, ctx context.Context, tr transport.Transporter) trace.SpanContext {
	t.Helper()
	var sc trace.SpanContext
	if _, err := m(func(ctx context.Context, req any) (any, error) {
		sc = trace.SpanContextFromContext(ctx)
		return nil, nil
	})(transport.NewServerContext(ctx, tr), struct{}{}); err != nil {
		t.Fatalf("middleware: %v", err)
	}
	return sc
}

// TestTraceMiddlewareInheritsTraceparentFromTrustedHTTPPeer 验证可信来源
// 的合法 traceparent 被继承：trace-id 沿用、Remote 置位、span-id 为本跳
// 新生成（非零且不等于上游），x-request-id 原样回传。
func TestTraceMiddlewareInheritsTraceparentFromTrustedHTTPPeer(t *testing.T) {
	tr := newHTTPTraceTransporter("127.0.0.1:54321",
		fakeHeader{traceparentKey: upstreamTraceparent, requestIDHeader: "req-42"})
	sc := runTrace(t, NewTraceMiddleware(trustedTraceConfig()), context.Background(), tr)

	wantTraceID, err := trace.TraceIDFromHex(upstreamTraceID)
	if err != nil {
		t.Fatalf("parse upstream trace id: %v", err)
	}
	if sc.TraceID() != wantTraceID {
		t.Fatalf("trace id = %s, want inherited %s", sc.TraceID(), wantTraceID)
	}
	if !sc.IsRemote() {
		t.Fatal("inherited span context must be remote")
	}
	if sc.TraceFlags() != trace.FlagsSampled {
		t.Fatalf("trace flags = %d, want sampled from header", sc.TraceFlags())
	}
	wantSpanID, err := trace.SpanIDFromHex(upstreamSpanID)
	if err != nil {
		t.Fatalf("parse upstream span id: %v", err)
	}
	if !sc.SpanID().IsValid() || sc.SpanID() == wantSpanID {
		t.Fatalf("span id = %s, want fresh nonzero id different from upstream %s", sc.SpanID(), wantSpanID)
	}
	if got := tr.ReplyHeader().Get(traceIDHeader); got != upstreamTraceID {
		t.Fatalf("reply x-trace-id = %q, want %q", got, upstreamTraceID)
	}
	if got := tr.ReplyHeader().Get(requestIDHeader); got != "req-42" {
		t.Fatalf("reply x-request-id = %q, want echoed %q", got, "req-42")
	}
}

// TestTraceMiddlewareNewRootForUntrustedHTTPPeer 验证不可信来源的合法
// traceparent 不被继承：新建根、请求头被剥空、x-request-id 以 trace-id 兜底。
func TestTraceMiddlewareNewRootForUntrustedHTTPPeer(t *testing.T) {
	tr := newHTTPTraceTransporter("10.0.0.9:50000", fakeHeader{traceparentKey: upstreamTraceparent})
	sc := runTrace(t, NewTraceMiddleware(trustedTraceConfig()), context.Background(), tr)

	wantTraceID, err := trace.TraceIDFromHex(upstreamTraceID)
	if err != nil {
		t.Fatalf("parse upstream trace id: %v", err)
	}
	if sc.TraceID() == wantTraceID {
		t.Fatalf("trace id = %s, must be a fresh root different from upstream", sc.TraceID())
	}
	if sc.TraceFlags() != trace.FlagsSampled {
		t.Fatalf("trace flags = %d, want sampled on new root", sc.TraceFlags())
	}
	if got := tr.reqHeader.Get(traceparentKey); got != "" {
		t.Fatalf("request traceparent = %q, want stripped", got)
	}
	if got := tr.ReplyHeader().Get(traceIDHeader); got != sc.TraceID().String() {
		t.Fatalf("reply x-trace-id = %q, want %q", got, sc.TraceID())
	}
	if got := tr.ReplyHeader().Get(requestIDHeader); got != sc.TraceID().String() {
		t.Fatalf("reply x-request-id = %q, want trace id fallback %q", got, sc.TraceID())
	}
}

// TestTraceMiddlewareNewRootForMalformedTraceparent 验证格式非法的
// traceparent 即使来自可信来源也一律新建根：坏版本、段数不全、短
// trace-id、全零 trace-id、全零 span-id、非十六进制字符。
func TestTraceMiddlewareNewRootForMalformedTraceparent(t *testing.T) {
	for name, header := range map[string]string{
		"version ff":            "ff-" + upstreamTraceID + "-" + upstreamSpanID + "-01",
		"missing flags segment": "00-" + upstreamTraceID + "-" + upstreamSpanID,
		"short trace id":        "00-4bf9-" + upstreamSpanID + "-01",
		"all zero trace id":     "00-00000000000000000000000000000000-" + upstreamSpanID + "-01",
		"all zero span id":      "00-" + upstreamTraceID + "-0000000000000000-01",
		"non hex trace id":      "00-4bf92f3577b34da6a3ce929d0e0e473g-" + upstreamSpanID + "-01",
	} {
		t.Run(name, func(t *testing.T) {
			tr := newHTTPTraceTransporter("127.0.0.1:54321", fakeHeader{traceparentKey: header})
			sc := runTrace(t, NewTraceMiddleware(trustedTraceConfig()), context.Background(), tr)

			if !sc.HasTraceID() {
				t.Fatalf("%s: handler context must carry a fresh root span context", name)
			}
			wantTraceID, err := trace.TraceIDFromHex(upstreamTraceID)
			if err != nil {
				t.Fatalf("parse upstream trace id: %v", err)
			}
			if sc.TraceID() == wantTraceID {
				t.Fatalf("%s: trace id = %s, malformed header must not be inherited", name, sc.TraceID())
			}
			if sc.TraceFlags() != trace.FlagsSampled {
				t.Fatalf("%s: trace flags = %d, want sampled on new root", name, sc.TraceFlags())
			}
			if got := tr.reqHeader.Get(traceparentKey); got != "" {
				t.Fatalf("%s: request traceparent = %q, want stripped", name, got)
			}
		})
	}
}

// TestTraceMiddlewareNewRootWithoutTraceparent 验证 gRPC 可信来源但无
// traceparent 时新建根并回传两枚头（经 ReplyHeader，即 gRPC 初始 metadata）。
func TestTraceMiddlewareNewRootWithoutTraceparent(t *testing.T) {
	tr := newGRPCTraceTransporter(fakeHeader{})
	ctx := peer.NewContext(context.Background(), &peer.Peer{Addr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 50000}})
	sc := runTrace(t, NewTraceMiddleware(trustedTraceConfig()), ctx, tr)

	if !sc.HasTraceID() {
		t.Fatal("handler context must carry a fresh root span context")
	}
	if sc.TraceFlags() != trace.FlagsSampled {
		t.Fatalf("trace flags = %d, want sampled on new root", sc.TraceFlags())
	}
	traceID := sc.TraceID().String()
	if got := tr.ReplyHeader().Get(traceIDHeader); got != traceID {
		t.Fatalf("reply x-trace-id = %q, want %q", got, traceID)
	}
	if got := tr.ReplyHeader().Get(requestIDHeader); got != traceID {
		t.Fatalf("reply x-request-id = %q, want trace id fallback %q", got, traceID)
	}
}

// TestTraceMiddlewareNewRootForUntrustedGRPCPeer 验证 gRPC 对端经 peer
// 判定可信性：合法 traceparent 但对端不在可信网段时新建根并剥头。
func TestTraceMiddlewareNewRootForUntrustedGRPCPeer(t *testing.T) {
	tr := newGRPCTraceTransporter(fakeHeader{traceparentKey: upstreamTraceparent})
	ctx := peer.NewContext(context.Background(), &peer.Peer{Addr: &net.TCPAddr{IP: net.IPv4(10, 0, 0, 9), Port: 50000}})
	sc := runTrace(t, NewTraceMiddleware(trustedTraceConfig()), ctx, tr)

	wantTraceID, err := trace.TraceIDFromHex(upstreamTraceID)
	if err != nil {
		t.Fatalf("parse upstream trace id: %v", err)
	}
	if sc.TraceID() == wantTraceID {
		t.Fatalf("trace id = %s, must be a fresh root different from upstream", sc.TraceID())
	}
	if got := tr.reqHeader.Get(traceparentKey); got != "" {
		t.Fatalf("request traceparent = %q, want stripped", got)
	}
}

// TestTraceMiddlewareIgnoresTraceparentWhenNoTrustedNetworks 验证可信
// 网段为空时一律不可信（fail-closed）：合法 traceparent 也不继承。
func TestTraceMiddlewareIgnoresTraceparentWhenNoTrustedNetworks(t *testing.T) {
	tr := newHTTPTraceTransporter("127.0.0.1:54321", fakeHeader{traceparentKey: upstreamTraceparent})
	sc := runTrace(t, NewTraceMiddleware(&conf.Server{}), context.Background(), tr)

	wantTraceID, err := trace.TraceIDFromHex(upstreamTraceID)
	if err != nil {
		t.Fatalf("parse upstream trace id: %v", err)
	}
	if sc.TraceID() == wantTraceID {
		t.Fatalf("trace id = %s, empty trusted networks must force a fresh root", sc.TraceID())
	}
	if got := tr.reqHeader.Get(traceparentKey); got != "" {
		t.Fatalf("request traceparent = %q, want stripped", got)
	}
}

// TestTraceMiddlewarePassesThroughWithoutTransport 验证取不到传输上下文
// 时原样放行：handler 照常执行且 ctx 不携带链路、不写任何头。
func TestTraceMiddlewarePassesThroughWithoutTransport(t *testing.T) {
	m := NewTraceMiddleware(trustedTraceConfig())

	var sc trace.SpanContext
	called := false
	if _, err := m(func(ctx context.Context, req any) (any, error) {
		called = true
		sc = trace.SpanContextFromContext(ctx)
		return nil, nil
	})(context.Background(), struct{}{}); err != nil {
		t.Fatalf("middleware: %v", err)
	}
	if !called {
		t.Fatal("handler must run through without a transport")
	}
	if sc.HasTraceID() {
		t.Fatalf("span context must stay absent, got trace id %s", sc.TraceID())
	}
}

// TestTraceMiddlewareTrustsIPv4MappedIPv6HTTPPeer 验证 IPv4-mapped IPv6
// 形式的对端地址经 Unmap 后命中 IPv4 可信网段。
func TestTraceMiddlewareTrustsIPv4MappedIPv6HTTPPeer(t *testing.T) {
	tr := newHTTPTraceTransporter("[::ffff:127.0.0.1]:54321", fakeHeader{traceparentKey: upstreamTraceparent})
	sc := runTrace(t, NewTraceMiddleware(trustedTraceConfig()), context.Background(), tr)

	wantTraceID, err := trace.TraceIDFromHex(upstreamTraceID)
	if err != nil {
		t.Fatalf("parse upstream trace id: %v", err)
	}
	if sc.TraceID() != wantTraceID {
		t.Fatalf("trace id = %s, want inherited %s", sc.TraceID(), wantTraceID)
	}
}
