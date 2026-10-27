package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"grape/internal/biz"
	entrymw "grape/internal/middleware"
	"grape/internal/ops"
	"grape/internal/service"

	kratoshttp "github.com/go-kratos/kratos/v3/transport/http"
)

// auditFakeRepo 是审计路由测试的假仓储：认证链验证不触真实存储。
type auditFakeRepo struct{}

func (*auditFakeRepo) AppendToolCall(context.Context, *biz.ToolCallAudit) error {
	return nil
}

func (*auditFakeRepo) AppendConfirmation(context.Context, *biz.ConfirmationAudit) error {
	return nil
}

func (*auditFakeRepo) AppendBatch(context.Context, *biz.ToolCallAudit, *biz.ConfirmationAudit) error {
	return nil
}

func (*auditFakeRepo) ListToolCalls(context.Context, *biz.AuditFilter) (*biz.ToolCallAuditList, error) {
	return &biz.ToolCallAuditList{Items: []*biz.ToolCallAudit{}, Total: 0}, nil
}

func (*auditFakeRepo) ListConfirmations(context.Context, *biz.AuditFilter) (*biz.ConfirmationAuditList, error) {
	return &biz.ConfirmationAuditList{Items: []*biz.ConfirmationAudit{}, Total: 0}, nil
}

func (*auditFakeRepo) GetToolCall(context.Context, string) (*biz.ToolCallAudit, error) {
	return nil, biz.ErrAuditNotFound
}

func (*auditFakeRepo) GetConfirmation(context.Context, string) (*biz.ConfirmationAudit, error) {
	return nil, biz.ErrAuditNotFound
}

// newAgentAuditTestServer 装配含审计查询端点的完整 HTTP 服务器：真实
// trace / auth / 路由级超时中间件链（agent/会话列表/静态托管位传 nil，
// 本文件用例只触达审计路由），守卫端点在册时的认证语义。
func newAgentAuditTestServer(t *testing.T) *kratoshttp.Server {
	t.Helper()
	confServer := agentHTTPConf()
	traceMiddleware, err := entrymw.NewTraceMiddleware(confServer)
	if err != nil {
		t.Fatalf("build trace middleware: %v", err)
	}
	authMiddleware := entrymw.NewAuthMiddleware(biz.NewSessionUsecase(&agentFakeSessionRepo{}))
	auditService := service.NewAgentAuditService(biz.NewAuditUsecase(&auditFakeRepo{}, nil))
	srv, err := NewHTTPServer(confServer, nil, traceMiddleware,
		NewRouteTimeoutMiddleware(confServer, nil),
		authMiddleware, ops.NewMetricsHandler(nil), nil, nil, nil, nil,
		auditService, nil,
		// 检索留痕查询端点本文件用例不触达，传 nil。
		nil)
	if err != nil {
		t.Fatalf("build http server: %v", err)
	}
	return srv
}

// TestAgentAuditWithoutTokenReturns401 守卫审计查询路由的认证链：无令牌
// 请求经完整挂载链必须被认证中间件拒绝（端点在册且经 ctx.Middleware 入
// 链的语义不因手写路由而放宽）。
func TestAgentAuditWithoutTokenReturns401(t *testing.T) {
	srv := newAgentAuditTestServer(t)
	for _, target := range []string{"/v1/agent/audit/tool-calls", "/v1/agent/audit/confirmations"} {
		rec := agentDo(t, srv, http.MethodGet, target, "", "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s without token: status = %d, want 401\n%s", target, rec.Code, rec.Body.String())
		}
	}
}

// TestAgentAuditListWithTokenReturnsEmptyPage 带令牌的列表请求经真实链
// 走到端点：认证身份（假仓储恒解析为租户 t-1 用户 7）注入后返回空页
// 信封，items 为空数组而非 null。
func TestAgentAuditListWithTokenReturnsEmptyPage(t *testing.T) {
	srv := newAgentAuditTestServer(t)
	for _, target := range []string{"/v1/agent/audit/tool-calls", "/v1/agent/audit/confirmations"} {
		rec := agentDo(t, srv, http.MethodGet, target, "token-1", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s with token: status = %d, want 200\n%s", target, rec.Code, rec.Body.String())
		}
		if body := rec.Body.String(); !strings.Contains(body, `"items":[]`) || !strings.Contains(body, `"total":0`) {
			t.Fatalf("%s body = %q, want empty page envelope", target, body)
		}
	}
}
