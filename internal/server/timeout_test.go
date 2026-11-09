package server

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	knowledgev1 "grape/api/knowledge/v1"
	"grape/internal/agent"
	"grape/internal/biz"
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
	stack, err := agent.NewStackWithOptions(agentConf(), nil, svc, agent.NewVectorStore(nil),
		biz.NewUserUsecase(&agentStubUserRepo{}), nil, &agentStubEmbedder{}, &agentStubEmbedderResolver{})
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

// newEmbeddingRouteTimeoutForTest 构建只关心向量化路由口径的中间件（无 agent
// 前缀装配）；embeddingTimeout 为 0 表示未配置。
func newEmbeddingRouteTimeoutForTest(requestTimeout, embeddingTimeout time.Duration) middleware.Middleware {
	confServer := agentHTTPConf()
	confServer.Http.RequestTimeout = durationpb.New(requestTimeout)
	if embeddingTimeout > 0 {
		confServer.Http.EmbeddingRequestTimeout = durationpb.New(embeddingTimeout)
	}
	return middleware.Middleware(NewRouteTimeoutMiddleware(confServer, nil))
}

// remainingDeadline 以给定操作名调用中间件，返回 handler 观察到的剩余时长；
// 无截止时间返回 ok=false。
func remainingDeadline(t *testing.T, mw middleware.Middleware, operation string) (time.Duration, bool) {
	t.Helper()
	var remaining time.Duration
	var ok bool
	handler := mw(func(ctx context.Context, _ any) (any, error) {
		var deadline time.Time
		deadline, ok = ctx.Deadline()
		remaining = time.Until(deadline)
		return nil, nil
	})
	if _, err := handler(transport.NewServerContext(context.Background(), &timeoutFakeTransporter{operation: operation}), nil); err != nil {
		t.Fatalf("handler: %v", err)
	}
	return remaining, ok
}

func TestRouteTimeout_embedding_operations_use_embedding_timeout(t *testing.T) {
	mw := newEmbeddingRouteTimeoutForTest(time.Second, time.Minute)
	for _, op := range []string{
		knowledgev1.OperationDocumentServiceCreateDocument,
		knowledgev1.OperationDocumentServiceUpdateDocument,
		knowledgev1.OperationDocumentServiceReindexDocument,
		knowledgev1.OperationDocumentServiceSearchDocuments,
	} {
		t.Run(op, func(t *testing.T) {
			remaining, ok := remainingDeadline(t, mw, op)
			if !ok || remaining <= time.Second || remaining > time.Minute {
				t.Fatalf("deadline = (%v, %v), want within (1s, 1m]", remaining, ok)
			}
		})
	}
}

func TestRouteTimeout_non_embedding_knowledge_operation_keeps_request_timeout(t *testing.T) {
	mw := newEmbeddingRouteTimeoutForTest(time.Second, time.Minute)

	remaining, ok := remainingDeadline(t, mw, knowledgev1.OperationDocumentServiceGetDocument)
	if !ok || remaining <= 0 || remaining > time.Second {
		t.Fatalf("deadline = (%v, %v), want within (0, 1s]", remaining, ok)
	}
}

func TestRouteTimeout_embedding_timeout_unset_falls_back_to_request_timeout(t *testing.T) {
	mw := newEmbeddingRouteTimeoutForTest(time.Second, 0)

	remaining, ok := remainingDeadline(t, mw, knowledgev1.OperationDocumentServiceCreateDocument)
	if !ok || remaining <= 0 || remaining > time.Second {
		t.Fatalf("deadline = (%v, %v), want within (0, 1s]", remaining, ok)
	}
}

func TestRouteTimeout_request_timeout_unset_passes_embedding_operation_through(t *testing.T) {
	mw := newEmbeddingRouteTimeoutForTest(0, time.Minute)

	if remaining, ok := remainingDeadline(t, mw, knowledgev1.OperationDocumentServiceCreateDocument); ok {
		t.Fatalf("deadline remaining = %v, want none (route timeout disabled)", remaining)
	}
}

// TestRouteTimeout_embedding_operation_exceeding_request_timeout_completes
// 回归守卫：嵌入端点耗时超过 request_timeout（本地模型冷启动）但在
// embedding_request_timeout 内时，条目摄取不再被截断。
func TestRouteTimeout_embedding_operation_exceeding_request_timeout_completes(t *testing.T) {
	const requestTimeout = 40 * time.Millisecond
	mw := newEmbeddingRouteTimeoutForTest(requestTimeout, time.Minute)

	slowIngest := mw(func(ctx context.Context, _ any) (any, error) {
		time.Sleep(3 * requestTimeout)
		return nil, ctx.Err()
	})
	_, err := slowIngest(transport.NewServerContext(context.Background(),
		&timeoutFakeTransporter{operation: knowledgev1.OperationDocumentServiceCreateDocument}), nil)
	if err != nil {
		t.Fatalf("slow embedding operation = %v, want completed within embedding timeout", err)
	}
}
