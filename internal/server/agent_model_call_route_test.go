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

// modelCallRouteFakeRepo 是模型调用留痕路由测试的假仓储：认证链验证不触
// 真实存储。
type modelCallRouteFakeRepo struct{}

func (*modelCallRouteFakeRepo) Append(context.Context, *biz.ModelCallEntry) error {
	return nil
}

func (*modelCallRouteFakeRepo) List(context.Context, *biz.ModelCallFilter) (*biz.ModelCallList, error) {
	return &biz.ModelCallList{Items: []*biz.ModelCallLog{}, Total: 0}, nil
}

func (*modelCallRouteFakeRepo) Count(context.Context, *biz.ModelCallFilter) (int64, error) {
	return 0, nil
}

// newAgentModelCallTestServer 装配含模型调用留痕查询端点的完整 HTTP 服务
// 器：真实 trace / auth / 路由级超时中间件链（其余挂载位传 nil，本文件
// 用例只触达模型调用留痕路由）。
func newAgentModelCallTestServer(t *testing.T) *kratoshttp.Server {
	t.Helper()
	confServer := agentHTTPConf()
	traceMiddleware, err := entrymw.NewTraceMiddleware(confServer)
	if err != nil {
		t.Fatalf("build trace middleware: %v", err)
	}
	authMiddleware := entrymw.NewAuthMiddleware(biz.NewSessionUsecase(&agentFakeSessionRepo{}))
	modelCallService := service.NewAgentModelCallService(biz.NewModelCallLogUsecase(&modelCallRouteFakeRepo{}, nil))
	srv, err := NewHTTPServer(
		confServer,
		nil,
		traceMiddleware,
		NewRouteTimeoutMiddleware(confServer, nil),
		authMiddleware, nil,
		permStubMiddleware(),
		ops.NewMetricsHandler(nil, nil),
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		// 检索留痕、知识库、模型与静态托管本文件用例不触达，传 nil；模型
		// 调用留痕传被测服务（本文件用例触达其路由）。
		nil,
		modelCallService,
		nil, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("build http server: %v", err)
	}
	return srv
}

// TestAgentModelCallWithoutTokenReturns401 守卫模型调用留痕查询路由的认
// 证链：无令牌请求经完整挂载链必须被认证中间件拒绝（端点在册且经
// ctx.Middleware 入链的语义不因手写路由而放宽）。
func TestAgentModelCallWithoutTokenReturns401(t *testing.T) {
	srv := newAgentModelCallTestServer(t)
	for _, target := range []string{
		"/v1/agent/model-calls",
		"/v1/agent/model-calls?scenario=embed_document&status=error&document_id=7",
	} {
		rec := agentDo(t, srv, http.MethodGet, target, "", "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s without token: status = %d, want 401\n%s", target, rec.Code, rec.Body.String())
		}
	}
}
