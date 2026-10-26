package agent

// sessionList 直测：会话列表是自建端点面，分页边界、身份 fail-closed 与
// userKey 归属过滤必须显式覆盖，不依赖挂载链上的间接断言（注册顺序与
// 认证链语义由 internal/server 的守卫测试锁定）。

import (
	"context"
	"encoding/json"
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"grape/internal/biz"

	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

// sessionListTestAppName 是直测装配的 app 名：只需与播种用的框架 Key
// 一致，不依赖配置。
const sessionListTestAppName = "grape-agent"

// sessionListTestIdentity 是归属者身份（编码为 userKey t-1:7）。
var sessionListTestIdentity = biz.Identity{TenantID: "t-1", UserID: 7}

// sessionListErrorSessions 是 ListSessions 恒出错的会话存储替身：端点只
// 调 ListSessions，其余方法经内嵌接口 nil 兜底（直测路径不可达）。
type sessionListErrorSessions struct {
	session.Service
}

func (sessionListErrorSessions) ListSessions(context.Context, session.UserKey, ...session.Option) ([]*session.Session, error) {
	return nil, errors.New("storage unavailable")
}

// newSessionListFixture 组装直测件：真实 inmemory 会话存储 + 列表装配物。
func newSessionListFixture(t *testing.T) (*sessionList, *inmemory.SessionService) {
	t.Helper()
	svc, closeSvc, err := NewMemorySessionService()
	if err != nil {
		t.Fatalf("build session service: %v", err)
	}
	t.Cleanup(closeSvc)
	memory, ok := svc.(*inmemory.SessionService)
	if !ok {
		t.Fatalf("session service type = %T, want *inmemory.SessionService", svc)
	}
	return newSessionList(memory, sessionListTestAppName), memory
}

// seedSessionListSessions 以指定 userKey 播种 n 个会话；userKey 经
// EncodeUserKey 产出，与端点的归属编码共用同一代码点。
func seedSessionListSessions(t *testing.T, memory *inmemory.SessionService, tenant string, userID int64, prefix string, n int) {
	t.Helper()
	userKey, err := EncodeUserKey(tenant, userID)
	if err != nil {
		t.Fatalf("encode user key: %v", err)
	}
	for i := 0; i < n; i++ {
		sess, err := memory.CreateSession(context.Background(), session.Key{
			AppName:   sessionListTestAppName,
			UserID:    userKey,
			SessionID: prefix + "-" + strconv.Itoa(i),
		}, nil)
		if err != nil {
			t.Fatalf("seed session %s-%d for %s: %v", prefix, i, userKey, err)
		}
		if sess == nil {
			t.Fatalf("seed session %s-%d for %s: nil session", prefix, i, userKey)
		}
	}
}

// sessionListDo 以给定身份（nil = 不注入，模拟绕过认证中间件）执行一次
// 列表请求。
func sessionListDo(t *testing.T, h nethttp.Handler, identity *biz.Identity, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(nethttp.MethodGet, "/agui/sessions"+query, nil)
	if identity != nil {
		req = req.WithContext(biz.WithIdentity(req.Context(), *identity))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// sessionListIDs 解析列表响应体并返回会话 ID 集合；顺带守卫时间戳的
// UTC RFC3339 渲染契约。
func sessionListIDs(t *testing.T, body string) []string {
	t.Helper()
	var reply struct {
		Sessions []struct {
			ID        string `json:"id"`
			CreatedAt string `json:"createdAt"`
			UpdatedAt string `json:"updatedAt"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(body), &reply); err != nil {
		t.Fatalf("parse sessions reply %q: %v", body, err)
	}
	ids := make([]string, 0, len(reply.Sessions))
	for _, item := range reply.Sessions {
		ids = append(ids, item.ID)
		if _, err := time.Parse(time.RFC3339, item.CreatedAt); err != nil {
			t.Fatalf("createdAt %q is not RFC3339: %v", item.CreatedAt, err)
		}
		if _, err := time.Parse(time.RFC3339, item.UpdatedAt); err != nil {
			t.Fatalf("updatedAt %q is not RFC3339: %v", item.UpdatedAt, err)
		}
	}
	return ids
}

// TestSessionList_when_pagination_invalid_returns_400 验证分页参数校验：
// 负 offset、非正 limit、超上限、非数字一律 400（fail-closed，不静默
// 截断）；超上限的文案单独断言，锁定「拒绝而非裁剪」语义。
func TestSessionList_when_pagination_invalid_returns_400(t *testing.T) {
	h, _ := newSessionListFixture(t)
	for _, query := range []string{"?offset=-1", "?limit=0", "?limit=-2", "?limit=101", "?offset=abc"} {
		rec := sessionListDo(t, h, &sessionListTestIdentity, query)
		if rec.Code != nethttp.StatusBadRequest {
			t.Fatalf("sessions%s status = %d, want 400\n%s", query, rec.Code, rec.Body.String())
		}
	}
	rec := sessionListDo(t, h, &sessionListTestIdentity, "?limit=101")
	if body := rec.Body.String(); !strings.Contains(body, "limit exceeds maximum") {
		t.Fatalf("over-limit body = %q, want limit exceeds maximum", body)
	}
}

// TestSessionList_when_query_unset_applies_default_limit 验证缺省分页：
// 不带查询参数时按默认 limit 截断（播种 25 条只回 20，与「不限制」「上
// 限 100」均可区分），offset 缺省 0。
func TestSessionList_when_query_unset_applies_default_limit(t *testing.T) {
	h, memory := newSessionListFixture(t)
	seedSessionListSessions(t, memory, "t-1", 7, "default", 25)
	rec := sessionListDo(t, h, &sessionListTestIdentity, "")
	if rec.Code != nethttp.StatusOK {
		t.Fatalf("default query status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	if ids := sessionListIDs(t, rec.Body.String()); len(ids) != sessionListDefaultLimit {
		t.Fatalf("default query count = %d, want %d", len(ids), sessionListDefaultLimit)
	}
}

// TestSessionList_when_offset_pagination_applies_bounds 验证 offset 分页
// 边界：limit 截断、offset 跳过、offset 越界返回空列表而非错误。
func TestSessionList_when_offset_pagination_applies_bounds(t *testing.T) {
	h, memory := newSessionListFixture(t)
	seedSessionListSessions(t, memory, "t-1", 7, "page", 3)

	rec := sessionListDo(t, h, &sessionListTestIdentity, "?limit=2")
	if rec.Code != nethttp.StatusOK {
		t.Fatalf("limit=2 status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	if ids := sessionListIDs(t, rec.Body.String()); len(ids) != 2 {
		t.Fatalf("limit=2 count = %d, want 2", len(ids))
	}

	rec = sessionListDo(t, h, &sessionListTestIdentity, "?offset=1&limit=10")
	if rec.Code != nethttp.StatusOK {
		t.Fatalf("offset=1 status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	if ids := sessionListIDs(t, rec.Body.String()); len(ids) != 2 {
		t.Fatalf("offset=1 count = %d, want 2", len(ids))
	}

	rec = sessionListDo(t, h, &sessionListTestIdentity, "?offset=99")
	if rec.Code != nethttp.StatusOK {
		t.Fatalf("offset=99 status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	if ids := sessionListIDs(t, rec.Body.String()); len(ids) != 0 {
		t.Fatalf("offset=99 count = %d, want 0", len(ids))
	}
}

// TestSessionList_when_caller_identity_scopes_userkey_visibility 验证
// userKey 编码消费：归属过滤以认证身份经 EncodeUserKey 编码的 userKey
// 为唯一来源——播种即用同一编码产出 key，调用者只看到自己名下的会话，
// 其他用户的会话绝不出现。
func TestSessionList_when_caller_identity_scopes_userkey_visibility(t *testing.T) {
	h, memory := newSessionListFixture(t)
	seedSessionListSessions(t, memory, "t-1", 7, "mine", 2)
	seedSessionListSessions(t, memory, "t-1", 8, "theirs", 1)

	rec := sessionListDo(t, h, &sessionListTestIdentity, "")
	if rec.Code != nethttp.StatusOK {
		t.Fatalf("sessions status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	ids := sessionListIDs(t, rec.Body.String())
	if len(ids) != 2 {
		t.Fatalf("session count = %d (%v), want 2 caller-owned sessions", len(ids), ids)
	}
	for _, id := range ids {
		if strings.Contains(id, "theirs") {
			t.Fatalf("sessions %v leak another user's session %q", ids, id)
		}
	}
}

// TestSessionList_when_identity_missing_fails_closed_401 验证身份缺失
// （绕过认证中间件的调用）fail-closed：401 且不触会话存储。
func TestSessionList_when_identity_missing_fails_closed_401(t *testing.T) {
	h, _ := newSessionListFixture(t)
	rec := sessionListDo(t, h, nil, "")
	if rec.Code != nethttp.StatusUnauthorized {
		t.Fatalf("missing identity status = %d, want 401", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, errMissingIdentityMsg) {
		t.Fatalf("missing identity body = %q, want %q", body, errMissingIdentityMsg)
	}
}

// TestSessionList_when_userkey_unencodable_returns_401 验证编码失败分支：
// 身份存在但 EncodeUserKey 拒绝（非正数用户 ID）时按未认证处理。
func TestSessionList_when_userkey_unencodable_returns_401(t *testing.T) {
	h, _ := newSessionListFixture(t)
	rec := sessionListDo(t, h, &biz.Identity{TenantID: "t-1", UserID: 0}, "")
	if rec.Code != nethttp.StatusUnauthorized {
		t.Fatalf("unencodable identity status = %d, want 401", rec.Code)
	}
}

// TestSessionList_when_storage_errors_returns_internal_error 验证存储
// 故障分支：错误细节只进日志，响应为 500 固定文案。
func TestSessionList_when_storage_errors_returns_internal_error(t *testing.T) {
	h := newSessionList(sessionListErrorSessions{}, sessionListTestAppName)
	rec := sessionListDo(t, h, &sessionListTestIdentity, "")
	if rec.Code != nethttp.StatusInternalServerError {
		t.Fatalf("storage error status = %d, want 500", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "internal error") {
		t.Fatalf("storage error body = %q, want fixed internal error message", body)
	}
}
