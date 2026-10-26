package server

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"grape/internal/agent"
	"grape/internal/service"

	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/transport"
	"google.golang.org/protobuf/types/known/durationpb"
)

// timeoutTestOperation* 是中间件直测用的操作名样本：普通生成端点、
// agent 前缀（精确与子路径模板）、与 agent 前缀相邻但不构成子路径的
// 反例。
const (
	timeoutTestPlainOperation       = "/grape.user.v1.UserService/ListUsers"
	timeoutTestAgentOperation       = "/agui/{rest:.*}"
	timeoutTestAdjacentApiOperation = "/aguiX/run"
)

// timeoutFakeTransporter 提供中间件读取的操作名事实（Header 全空实现）。
type timeoutFakeTransporter struct{ operation string }

func (f *timeoutFakeTransporter) Kind() transport.Kind            { return transport.KindHTTP }
func (f *timeoutFakeTransporter) Endpoint() string                { return "test://" }
func (f *timeoutFakeTransporter) Operation() string               { return f.operation }
func (f *timeoutFakeTransporter) RequestHeader() transport.Header { return timeoutFakeHeader{} }
func (f *timeoutFakeTransporter) ReplyHeader() transport.Header   { return timeoutFakeHeader{} }

type timeoutFakeHeader struct{}

func (timeoutFakeHeader) Get(string) string      { return "" }
func (timeoutFakeHeader) Set(string, string)     {}
func (timeoutFakeHeader) Add(string, string)     {}
func (timeoutFakeHeader) Keys() []string         { return nil }
func (timeoutFakeHeader) Values(string) []string { return nil }

// newRouteTimeoutForTest 构建待测中间件：真实 agent service 提供挂载
// 前缀，request_timeout 由用例给定（取小值避免慢测试）。
func newRouteTimeoutForTest(t *testing.T, requestTimeout time.Duration) middleware.Middleware {
	t.Helper()
	svc, closeSessions, err := agent.NewMemorySessionService()
	if err != nil {
		t.Fatalf("build session service: %v", err)
	}
	t.Cleanup(closeSessions)
	stack, err := agent.NewStackWithOptions(agentConf(), nil, svc, agent.NewVectorStore(nil))
	if err != nil {
		t.Fatalf("build agent stack: %v", err)
	}
	t.Cleanup(stack.Close)
	confServer := agentHTTPConf()
	confServer.Http.RequestTimeout = durationpb.New(requestTimeout)
	return middleware.Middleware(NewRouteTimeoutMiddleware(confServer, service.NewAgentService(stack)))
}

// TestRouteTimeoutTimesOutPlainOperationButPassesAgentOperation 直测
// 路由级超时中间件的两个分支：非 agent 前缀操作名在 handler 超过
// request_timeout 后以 DeadlineExceeded 失败（SSE 治理归还普通路由
// 限时的保证）；agent 前缀操作名直通，ctx 无截止时间。
func TestRouteTimeoutTimesOutPlainOperationButPassesAgentOperation(t *testing.T) {
	const requestTimeout = 40 * time.Millisecond
	mw := newRouteTimeoutForTest(t, requestTimeout)

	plain := mw(func(ctx context.Context, _ any) (any, error) {
		time.Sleep(3 * requestTimeout)
		return nil, ctx.Err()
	})
	_, err := plain(transport.NewServerContext(context.Background(),
		&timeoutFakeTransporter{operation: timeoutTestPlainOperation}), nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("plain operation: want DeadlineExceeded, got %v", err)
	}

	agentPass := mw(func(ctx context.Context, _ any) (any, error) {
		if deadline, ok := ctx.Deadline(); ok {
			return nil, fmt.Errorf("agent operation must not carry deadline, got %v", deadline)
		}
		return nil, nil
	})
	if _, err := agentPass(transport.NewServerContext(context.Background(),
		&timeoutFakeTransporter{operation: timeoutTestAgentOperation}), nil); err != nil {
		t.Fatalf("agent operation: want pass-through without deadline, got %v", err)
	}
}

// TestRouteTimeoutAppliesDeadlineToOperationAdjacentToAgentPrefix 反例
// 守卫跳过判定的边界匹配：与 agent 前缀相邻但不构成子路径的操作名
// （/aguiX）不得被误跳过，仍须携带 request_timeout 截止。
func TestRouteTimeoutAppliesDeadlineToOperationAdjacentToAgentPrefix(t *testing.T) {
	const requestTimeout = 40 * time.Millisecond
	mw := newRouteTimeoutForTest(t, requestTimeout)

	adjacent := mw(func(ctx context.Context, _ any) (any, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			return nil, errors.New("adjacent operation must carry deadline")
		}
		if remaining := time.Until(deadline); remaining <= 0 || remaining > requestTimeout {
			return nil, fmt.Errorf("deadline remaining = %v, want within (0, %v]", remaining, requestTimeout)
		}
		return nil, nil
	})
	if _, err := adjacent(transport.NewServerContext(context.Background(),
		&timeoutFakeTransporter{operation: timeoutTestAdjacentApiOperation}), nil); err != nil {
		t.Fatalf("adjacent operation: %v", err)
	}
}
