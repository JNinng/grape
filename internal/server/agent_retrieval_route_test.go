package server

import (
	"context"
	"net/http"
	"testing"

	"grape/internal/biz"
	entrymw "grape/internal/middleware"
	"grape/internal/ops"
	"grape/internal/service"

	kratoshttp "github.com/go-kratos/kratos/v3/transport/http"
)

// retrievalFakeRepo 是检索留痕路由测试的假仓储：认证链验证不触真实存储。
type retrievalRouteFakeRepo struct{}

func (*retrievalRouteFakeRepo) Append(context.Context, *biz.RetrievalLogRecord) error {
	return nil
}

func (*retrievalRouteFakeRepo) List(context.Context, *biz.RetrievalLogFilter) (*biz.RetrievalLogList, error) {
	return &biz.RetrievalLogList{Items: []*biz.RetrievalLog{}, Total: 0}, nil
}

// newAgentRetrievalTestServer 装配含检索留痕查询端点的完整 HTTP 服务器：
// 真实 trace / auth / 路由级超时中间件链（其余挂载位传 nil，本文件用例
// 只触达检索留痕路由）。
func newAgentRetrievalTestServer(t *testing.T) *kratoshttp.Server {
	t.Helper()
	confServer := agentHTTPConf()
	traceMiddleware, err := entrymw.NewTraceMiddleware(confServer)
	if err != nil {
		t.Fatalf("build trace middleware: %v", err)
	}
	authMiddleware := entrymw.NewAuthMiddleware(biz.NewSessionUsecase(&agentFakeSessionRepo{}))
	retrievalService := service.NewAgentRetrievalService(biz.NewRetrievalLogUsecase(&retrievalRouteFakeRepo{}))
	srv, err := NewHTTPServer(confServer, nil, traceMiddleware,
		NewRouteTimeoutMiddleware(confServer, nil),
		authMiddleware, ops.NewMetricsHandler(nil), nil, nil, nil, nil, nil,
		retrievalService, nil)
	if err != nil {
		t.Fatalf("build http server: %v", err)
	}
	return srv
}

// TestAgentRetrievalWithoutTokenReturns401 守卫检索留痕查询路由的认证链：
// 无令牌请求经完整挂载链必须被认证中间件拒绝（端点在册且经
// ctx.Middleware 入链的语义不因手写路由而放宽）。
func TestAgentRetrievalWithoutTokenReturns401(t *testing.T) {
	srv := newAgentRetrievalTestServer(t)
	for _, target := range []string{
		"/v1/agent/retrieval-logs",
		"/v1/agent/retrieval-logs?session_id=sess-1&source=tool",
	} {
		rec := agentDo(t, srv, http.MethodGet, target, "", "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s without token: status = %d, want 401\n%s", target, rec.Code, rec.Body.String())
		}
	}
}
