package agent

// ackOwnerGuard 直测：守卫是自建安全面，异常分支与取值源对齐必须显式
// 覆盖，不依赖挂载链上的间接断言。

import (
	"bytes"
	"context"
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"grape/internal/biz"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

// guardInnerStub 是守卫内层的透传探针：被触达即记 418，供断言「放行/
// 拦截」而非具体下游行为。
type guardInnerStub struct {
	reached bool
}

func (h *guardInnerStub) ServeHTTP(w nethttp.ResponseWriter, _ *nethttp.Request) {
	h.reached = true
	w.WriteHeader(nethttp.StatusTeapot)
}

// guardErrorSessions 是 GetSession 恒出错的会话存储替身：守卫只调
// GetSession，其余方法经内嵌接口 nil 兜底（直测路径不可达）。
type guardErrorSessions struct {
	session.Service
}

func (guardErrorSessions) GetSession(context.Context, session.Key, ...session.Option) (*session.Session, error) {
	return nil, errors.New("storage unavailable")
}

// guardFixture 组装守卫直测件：真实 inmemory 会话存储 + 透传探针。
type guardFixture struct {
	g     *ackOwnerGuard
	inner *guardInnerStub
	svc   session.Service
}

func newGuardFixture(t *testing.T) *guardFixture {
	t.Helper()
	svc, closeSvc, err := NewMemorySessionService()
	if err != nil {
		t.Fatalf("build session service: %v", err)
	}
	t.Cleanup(closeSvc)
	inner := &guardInnerStub{}
	return &guardFixture{
		g:     newAckOwnerGuard(inner, svc, confirmTestAppName),
		inner: inner,
		svc:   svc,
	}
}

// guardIdentityCtx 注入归属者身份（t-1:7）的请求 ctx。
func guardIdentityCtx() context.Context {
	return biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: confirmTestOwnerID})
}

// guardDo 执行一次守卫请求并返回响应与探针状态。
func (f *guardFixture) do(t *testing.T, ctx context.Context, method, target, body string) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	f.g.ServeHTTP(rec, req)
	return rec, f.inner.reached
}

func TestAckOwnerGuard_when_post_body_unreadable_returns_400(t *testing.T) {
	f := newGuardFixture(t)
	req := httptest.NewRequest(nethttp.MethodPost, "/agui/ack/confirmation?threadId=s1", errReader{})
	req = req.WithContext(guardIdentityCtx())
	rec := httptest.NewRecorder()
	f.g.ServeHTTP(rec, req)
	if rec.Code != nethttp.StatusBadRequest {
		t.Fatalf("unreadable body status = %d, want 400", rec.Code)
	}
	if f.inner.reached {
		t.Fatal("guard must not relay requests with unreadable bodies")
	}
}

func TestAckOwnerGuard_when_identity_missing_fails_closed_401(t *testing.T) {
	f := newGuardFixture(t)
	rec, reached := f.do(t, context.Background(), nethttp.MethodPost,
		"/agui/ack/confirmation", `{"threadId":"s1","confirmationId":"c1","approved":true}`)
	if rec.Code != nethttp.StatusUnauthorized {
		t.Fatalf("missing identity status = %d, want 401", rec.Code)
	}
	if reached {
		t.Fatal("guard must not relay requests without identity")
	}
}

func TestAckOwnerGuard_when_session_lookup_errors_returns_500(t *testing.T) {
	f := &guardFixture{
		g:     newAckOwnerGuard(&guardInnerStub{}, guardErrorSessions{}, confirmTestAppName),
		inner: &guardInnerStub{},
	}
	rec, reached := f.do(t, guardIdentityCtx(), nethttp.MethodGet,
		"/agui/ack/confirmations?threadId=s1", "")
	if rec.Code != nethttp.StatusInternalServerError {
		t.Fatalf("lookup error status = %d, want 500", rec.Code)
	}
	if reached {
		t.Fatal("guard must not relay when session lookup fails")
	}
}

// TestAckOwnerGuard_when_post_ignores_query_param_and_trusts_body 锁定
// POST 取值源与 ackhttp 的对齐关系：查询参数不参与归属判定——查询参数
// 指向自己名下会话 + body 指向他人会话必须 404（采信查询参数的实现对
// 本用例失守）；反向组合（查询参数指向他人 + body 指向自己）必须放行，
// 守卫不越权收紧。
func TestAckOwnerGuard_when_post_ignores_query_param_and_trusts_body(t *testing.T) {
	f := newGuardFixture(t)
	ctx := guardIdentityCtx()
	ownKey := session.Key{AppName: confirmTestAppName, UserID: "t-1:7", SessionID: "own-session"}
	if _, err := f.svc.CreateSession(context.Background(), ownKey, nil); err != nil {
		t.Fatalf("create own session: %v", err)
	}

	rec, reached := f.do(t, ctx, nethttp.MethodPost,
		"/agui/ack/confirmation?threadId=own-session",
		`{"threadId":"foreign-session","confirmationId":"c1","approved":true}`)
	if rec.Code != nethttp.StatusNotFound {
		t.Fatalf("query=own + body=foreign status = %d, want 404\n%s", rec.Code, rec.Body.String())
	}
	if reached {
		t.Fatal("guard must block when body threadId is foreign")
	}

	rec, reached = f.do(t, ctx, nethttp.MethodPost,
		"/agui/ack/confirmation?threadId=foreign-session",
		`{"threadId":"own-session","confirmationId":"c1","approved":true}`)
	if rec.Code != nethttp.StatusTeapot {
		t.Fatalf("query=foreign + body=own status = %d, want pass-through 418", rec.Code)
	}
	if !reached {
		t.Fatal("guard must relay when body threadId is owned")
	}
}

// TestAckOwnerGuard_when_post_body_has_trailing_data_still_checked 锁定
// 守卫解码方式与 ackhttp 一致（Decode 只取首个 JSON 值、容忍尾随数据）：
// 「合法 JSON + 尾随数据」必须照常经过归属判定——若退化为 json.Unmarshal
//（拒绝尾随数据），该形态会被守卫误判形状非法而放行，ackhttp 却能正常
// 解码照常决定，形成跨用户绕过（复评实证形态，变异检验钉住）。
func TestAckOwnerGuard_when_post_body_has_trailing_data_still_checked(t *testing.T) {
	f := newGuardFixture(t)
	ctx := guardIdentityCtx()
	ownKey := session.Key{AppName: confirmTestAppName, UserID: "t-1:7", SessionID: "own-session"}
	if _, err := f.svc.CreateSession(context.Background(), ownKey, nil); err != nil {
		t.Fatalf("create own session: %v", err)
	}

	rec, reached := f.do(t, ctx, nethttp.MethodPost,
		"/agui/ack/confirmation?threadId=own-session",
		`{"threadId":"foreign-session","confirmationId":"c1","approved":true}{}`)
	if rec.Code != nethttp.StatusNotFound {
		t.Fatalf("body with trailing data (foreign) status = %d, want 404\n%s", rec.Code, rec.Body.String())
	}
	if reached {
		t.Fatal("guard must block foreign threadId even with trailing data")
	}

	rec, reached = f.do(t, ctx, nethttp.MethodPost,
		"/agui/ack/confirmation?threadId=foreign-session",
		`{"threadId":"own-session","confirmationId":"c1","approved":true}{}`)
	if rec.Code != nethttp.StatusTeapot {
		t.Fatalf("body with trailing data (own) status = %d, want pass-through 418", rec.Code)
	}
	if !reached {
		t.Fatal("guard must relay owned threadId even with trailing data")
	}
}

func TestAckOwnerGuard_when_get_thread_id_foreign_returns_404(t *testing.T) {
	f := newGuardFixture(t)
	ctx := guardIdentityCtx()
	ownKey := session.Key{AppName: confirmTestAppName, UserID: "t-1:7", SessionID: "own-session"}
	if _, err := f.svc.CreateSession(context.Background(), ownKey, nil); err != nil {
		t.Fatalf("create own session: %v", err)
	}

	rec, reached := f.do(t, ctx, nethttp.MethodGet,
		"/agui/ack/confirmations?threadId=foreign-session", "")
	if rec.Code != nethttp.StatusNotFound {
		t.Fatalf("foreign threadId status = %d, want 404", rec.Code)
	}
	if reached {
		t.Fatal("guard must not relay foreign sessions")
	}

	rec, reached = f.do(t, ctx, nethttp.MethodGet,
		"/agui/ack/confirmations?threadId=own-session", "")
	if rec.Code != nethttp.StatusTeapot {
		t.Fatalf("owned threadId status = %d, want pass-through 418", rec.Code)
	}
	if !reached {
		t.Fatal("guard must relay owned sessions")
	}
}

// TestAckOwnerGuard_when_malformed_or_oversized_body_handled 验证请求体
// 边界：形状非法的 POST 放行给内层（400 语义归 ackhttp，守卫只在
// threadId 明确成立时加锁）；超过 ackGuardMaxBodyBytes 的请求体读失败
// 按 400 收口，不透传。
func TestAckOwnerGuard_when_malformed_or_oversized_body_handled(t *testing.T) {
	f := newGuardFixture(t)
	rec, reached := f.do(t, guardIdentityCtx(), nethttp.MethodPost,
		"/agui/ack/confirmation", `{"threadId":`)
	if rec.Code != nethttp.StatusTeapot {
		t.Fatalf("malformed body status = %d, want pass-through 418", rec.Code)
	}
	if !reached {
		t.Fatal("guard must relay malformed bodies to ackhttp for its 400")
	}

	oversized := newGuardFixture(t)
	rec2, reached2 := oversized.do(t, guardIdentityCtx(), nethttp.MethodPost,
		"/agui/ack/confirmation", string(bytes.Repeat([]byte("a"), ackGuardMaxBodyBytes+1)))
	if rec2.Code != nethttp.StatusBadRequest {
		t.Fatalf("oversized body status = %d, want 400", rec2.Code)
	}
	if reached2 {
		t.Fatal("guard must not relay oversized bodies")
	}
}

// errReader 是读取即失败的请求体替身（模拟传输中途断开）。
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("connection reset mid-body") }
