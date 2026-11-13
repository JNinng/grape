package server

import (
	"context"
	"net/http"
	"testing"

	"grape/internal/biz"
	entrymw "grape/internal/middleware"
	"grape/internal/ops"
	"grape/internal/service"

	"github.com/jninng/sqlc_gen_dynamic/dynq"
	kratoshttp "github.com/go-kratos/kratos/v3/transport/http"
)

// modelCallRouteFakeRepo 是模型调用留痕路由测试的假仓储：认证链验证不触
// 真实存储。
type modelCallRouteFakeRepo struct{}

func (*modelCallRouteFakeRepo) Append(context.Context, *biz.ModelCallEntry) error {
	return nil
}

func (*modelCallRouteFakeRepo) List(context.Context, biz.Identity, *dynq.Query) (*biz.ModelCallLogList, error) {
	return &biz.ModelCallLogList{}, nil
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
		// agent 手写路由与检索留痕、知识库、模型、静态托管本文件用例不触达，
		// 传 nil；模型调用留痕传被测服务（本文件用例触达其路由）。
		nil, nil, nil, nil, nil,
		modelCallService,
		nil, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("build http server: %v", err)
	}
	return srv
}

// TestAgentModelCallWithoutTokenReturns401 守卫模型调用留痕查询路由的认
// 证链：无令牌请求经完整挂载链必须被认证中间件拒绝（生成端点的认证不因
// 动态查询协议而放宽）。
func TestAgentModelCallWithoutTokenReturns401(t *testing.T) {
	srv := newAgentModelCallTestServer(t)
	const target = "/v1/agent/model-calls/list"
	rec := agentDo(t, srv, http.MethodPost, target, "", `{"pagination":{"page":1}}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("%s without token: status = %d, want 401\n%s", target, rec.Code, rec.Body.String())
	}
}
