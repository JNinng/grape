package testsupport

import (
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"grape/internal/biz"

	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

// IdentityHandler 以固定身份包装 handler（等价认证中间件命中后的 ctx）。
func IdentityHandler(next nethttp.Handler, tenant string, userID int64) nethttp.Handler {
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		ctx := biz.WithIdentity(r.Context(), biz.Identity{TenantID: tenant, UserID: userID})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// PostJSON 执行一次 JSON 请求并返回响应记录。
func PostJSON(t *testing.T, h nethttp.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// NewMemorySessionService 提供进程内会话存储（框架 inmemory），形状与
// 根包同名提供器一致：子包的包内测试不能反向导入根包，经此取同一实现。
func NewMemorySessionService() (session.Service, func(), error) {
	svc := inmemory.NewSessionService()
	return svc, func() { _ = svc.Close() }, nil
}
