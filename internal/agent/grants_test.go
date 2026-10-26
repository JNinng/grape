package agent

import (
	"context"
	"encoding/json"
	"errors"
	nethttp "net/http"
	"strings"
	"sync"
	"testing"

	"grape/internal/biz"

	"github.com/jninng/trpc_agent_go_ack"
)

// fakeGrantRepo 是 agent 层单测用的授权仓储替身：实现 biz.GrantRepo，
// 记录 Resolve/Remember 收到的参数，供适配器映射断言。
type fakeGrantRepo struct {
	mu sync.Mutex
	// active 以「userKey 解码前的原始键」为索引：键 (userID, sessionID,
	// toolName)，sessionID 空=全局级。
	active map[agentGrantKey]*biz.ToolGrant

	lastResolveSession string
	lastRememberTool   string
	lastRememberSess   string
}

type agentGrantKey struct {
	userID    int64
	sessionID string
	toolName  string
}

func newFakeGrantRepo() *fakeGrantRepo {
	return &fakeGrantRepo{active: map[agentGrantKey]*biz.ToolGrant{}}
}

func (r *fakeGrantRepo) seed(userID int64, sessionID, toolName string) {
	g := &biz.ToolGrant{ID: 1, TenantID: "t-1", UserID: userID, ToolName: toolName}
	if sessionID != "" {
		s := sessionID
		g.SessionID = &s
	}
	r.active[agentGrantKey{userID, sessionID, toolName}] = g
}

func (r *fakeGrantRepo) FindActiveForSession(_ context.Context, _ biz.Identity, userID int64, sessionID, toolName string) (*biz.ToolGrant, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastResolveSession = sessionID
	if g, ok := r.active[agentGrantKey{userID, sessionID, toolName}]; ok {
		return g, nil
	}
	return nil, nil
}

func (r *fakeGrantRepo) FindActiveGlobal(_ context.Context, _ biz.Identity, userID int64, toolName string) (*biz.ToolGrant, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if g, ok := r.active[agentGrantKey{userID, "", toolName}]; ok {
		return g, nil
	}
	return nil, nil
}

func (r *fakeGrantRepo) Create(_ context.Context, auth biz.Identity, g *biz.ToolGrant) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastRememberTool = g.ToolName
	if g.SessionID != nil {
		r.lastRememberSess = *g.SessionID
	}
	key := agentGrantKey{g.UserID, "", g.ToolName}
	if g.SessionID != nil {
		key.sessionID = *g.SessionID
	}
	if _, exists := r.active[key]; exists {
		return false, nil
	}
	r.active[key] = g
	return true, nil
}

func (r *fakeGrantRepo) GetByID(_ context.Context, _ biz.Identity, id int64) (*biz.ToolGrant, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, g := range r.active {
		if g.ID == id {
			return g, nil
		}
	}
	return nil, biz.ErrGrantNotFound
}

func (r *fakeGrantRepo) ListActiveByUser(_ context.Context, _ biz.Identity, userID int64) ([]*biz.ToolGrant, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*biz.ToolGrant
	for _, g := range r.active {
		if g.UserID == userID {
			out = append(out, g)
		}
	}
	return out, nil
}

func (r *fakeGrantRepo) ListActiveByTenant(context.Context, biz.Identity) ([]*biz.ToolGrant, error) {
	return nil, nil
}

func (r *fakeGrantRepo) RevokeOwn(_ context.Context, _ biz.Identity, id int64) error {
	return r.revoke(id)
}

func (r *fakeGrantRepo) RevokeManaged(_ context.Context, _ biz.Identity, id int64) error {
	return r.revoke(id)
}

func (r *fakeGrantRepo) revoke(id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, g := range r.active {
		if g.ID == id {
			delete(r.active, k)
			return nil
		}
	}
	return biz.ErrGrantNotFound
}

func (r *fakeGrantRepo) HasPermission(_ context.Context, _ biz.Identity, code string) (bool, error) {
	return map[string]bool{biz.PermAgentGrantQuery: true, biz.PermAgentGrantRevoke: true}[code], nil
}

var _ biz.GrantRepo = (*fakeGrantRepo)(nil)

// TestStoredGrants_when_userkey_malformed_lookup_misses 验证畸形 userKey
// 的许可查询按未命中处理（不报错、不命中）：fail-closed 的代价只是重复
// 确认，绝不因身份异常放行。
func TestStoredGrants_when_userkey_malformed_lookup_misses(t *testing.T) {
	s := newStoredGrants(newGrantUsecaseForTest(newFakeGrantRepo()))
	for _, key := range []string{"", "no-separator", "t-1:", "t-1:abc", "t-1:0", "t:1:2"} {
		if g, ok := s.Lookup(context.Background(), ackQuery(key, "thread-a", echoToolName)); ok {
			t.Fatalf("malformed userKey %q must miss, got %+v", key, g)
		}
	}
}

// TestStoredGrants_when_saved_lookup_hits_with_session_priority 验证
// 落库后的许可按「会话级先于全局级」命中：会话级存在时优先返回会话级。
func TestStoredGrants_when_saved_lookup_hits_with_session_priority(t *testing.T) {
	repo := newFakeGrantRepo()
	s := newStoredGrants(newGrantUsecaseForTest(repo))

	if err := s.Save(context.Background(), ackGrant("t-1:7", "", "update_my_nickname")); err != nil {
		t.Fatalf("save global grant: %v", err)
	}
	if err := s.Save(context.Background(), ackGrant("t-1:7", "thread-a", "update_my_nickname")); err != nil {
		t.Fatalf("save session grant: %v", err)
	}

	g, ok := s.Lookup(context.Background(), ackQuery("t-1:7", "thread-a", "update_my_nickname"))
	if !ok {
		t.Fatal("grant must hit after save")
	}
	if g.SessionID != "thread-a" {
		t.Fatalf("session-level grant must win, got %+v", g)
	}

	// 其他会话只被全局级覆盖。
	g, ok = s.Lookup(context.Background(), ackQuery("t-1:7", "thread-b", "update_my_nickname"))
	if !ok {
		t.Fatal("global grant must hit for other threads")
	}
	if g.SessionID != "thread-b" {
		t.Fatalf("global hit must echo the queried session, got %+v", g)
	}

	// 其他用户不可见。
	if _, ok := s.Lookup(context.Background(), ackQuery("t-1:8", "thread-a", "update_my_nickname")); ok {
		t.Fatal("other user's grant must not hit")
	}
}

// TestStoredGrants_when_save_maps_session_and_global_scopes 验证 Save
// 的作用域映射：SessionID 非空落会话级、空落全局级。
func TestStoredGrants_when_save_maps_session_and_global_scopes(t *testing.T) {
	repo := newFakeGrantRepo()
	s := newStoredGrants(newGrantUsecaseForTest(repo))

	if err := s.Save(context.Background(), ackGrant("t-1:7", "thread-a", "update_my_nickname")); err != nil {
		t.Fatalf("save session grant: %v", err)
	}
	if repo.lastRememberSess != "thread-a" {
		t.Fatalf("session scope must pass through, got %q", repo.lastRememberSess)
	}

	if err := s.Save(context.Background(), ackGrant("t-1:7", "", "update_my_nickname")); err != nil {
		t.Fatalf("save global grant: %v", err)
	}
	g := repo.active[agentGrantKey{7, "", "update_my_nickname"}]
	if g == nil || g.SessionID != nil {
		t.Fatalf("empty session must save as global grant, got %+v", g)
	}
}

// TestStoredGrants_when_save_userkey_malformed_rejected 验证畸形 userKey
// 的写入显式报错（Save 有错误返回通道，不吞错）。
func TestStoredGrants_when_save_userkey_malformed_rejected(t *testing.T) {
	s := newStoredGrants(newGrantUsecaseForTest(newFakeGrantRepo()))

	err := s.Save(context.Background(), ackGrant("broken", "thread-a", "update_my_nickname"))
	if err == nil {
		t.Fatal("malformed userKey save must be rejected")
	}
}

// TestStoredGrants_when_lookup_error_fails_closed 验证存储故障按未命中
// 处理：确认照常发起（fail-closed），不因故障静默放行。
func TestStoredGrants_when_lookup_error_fails_closed(t *testing.T) {
	repo := &failingGrantRepo{}
	s := newStoredGrants(newGrantUsecaseForTest(repo))

	if _, ok := s.Lookup(context.Background(), ackQuery("t-1:7", "thread-a", "update_my_nickname")); ok {
		t.Fatal("storage failure must be treated as a miss (fail-closed)")
	}
}

// failingGrantRepo 是评估原语恒故障的仓储替身。
type failingGrantRepo struct{ fakeGrantRepo }

func (*failingGrantRepo) FindActiveForSession(context.Context, biz.Identity, int64, string, string) (*biz.ToolGrant, error) {
	return nil, errors.New("db down")
}

func (*failingGrantRepo) FindActiveGlobal(context.Context, biz.Identity, int64, string) (*biz.ToolGrant, error) {
	return nil, errors.New("db down")
}

// grantAdminMux 以与 newMux 相同的模式注册管理端点：PathValue 依赖
// ServeMux 的模式路由，直调 handler 取不到 {id}，测试必须走同一挂载形态。
func grantAdminMux(h nethttp.Handler) nethttp.Handler {
	mux := nethttp.NewServeMux()
	mux.Handle(nethttp.MethodGet+" /agui/grants", h)
	mux.Handle(nethttp.MethodDelete+" /agui/grants/{id}", h)
	return mux
}

// TestGrantAdmin_when_list_and_revoke_over_http 验证管理端点的 HTTP 面：
// 本人列表 200 与 DTO 形态、吊销 200、不可见 id 404、非法 id 400、
// 身份缺失 401、非法 scope 400。
func TestGrantAdmin_when_list_and_revoke_over_http(t *testing.T) {
	repo := newFakeGrantRepo()
	repo.seed(7, "thread-a", "update_my_nickname")
	repo.seed(7, "", "echo")
	h := grantAdminMux(identityHandler(newGrantAdmin(biz.NewGrantUsecase(repo)), "t-1", 7))

	// 本人列表：200，DTO 形态与会话/全局作用域标注。
	rec := postJSON(t, h, nethttp.MethodGet, "/agui/grants", "")
	if rec.Code != nethttp.StatusOK {
		t.Fatalf("list self status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	var reply grantListReply
	if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
		t.Fatalf("parse list reply: %v\n%s", err, rec.Body.String())
	}
	if len(reply.Grants) != 2 {
		t.Fatalf("list self grants = %d, want 2\n%s", len(reply.Grants), rec.Body.String())
	}
	scopes := map[string]bool{}
	for _, item := range reply.Grants {
		scopes[item.Scope] = true
	}
	if !scopes["session"] || !scopes["global"] {
		t.Fatalf("scopes must cover session and global, got %v", scopes)
	}

	// 吊销存在的授权：200。
	rec = postJSON(t, h, nethttp.MethodDelete, "/agui/grants/1", "")
	if rec.Code != nethttp.StatusOK || !strings.Contains(rec.Body.String(), `"revoked":true`) {
		t.Fatalf("revoke status = %d\n%s", rec.Code, rec.Body.String())
	}
	// 吊销后列表只剩全局级一条。
	rec = postJSON(t, h, nethttp.MethodGet, "/agui/grants", "")
	if !strings.Contains(rec.Body.String(), "echo") || strings.Contains(rec.Body.String(), "update_my_nickname") {
		t.Fatalf("revoked grant must disappear from the list:\n%s", rec.Body.String())
	}

	// 不可见 id：404（不泄露存在性）。
	rec = postJSON(t, h, nethttp.MethodDelete, "/agui/grants/999", "")
	if rec.Code != nethttp.StatusNotFound {
		t.Fatalf("revoke invisible id status = %d, want 404\n%s", rec.Code, rec.Body.String())
	}
	// 非法 id：400。
	rec = postJSON(t, h, nethttp.MethodDelete, "/agui/grants/abc", "")
	if rec.Code != nethttp.StatusBadRequest {
		t.Fatalf("revoke invalid id status = %d, want 400\n%s", rec.Code, rec.Body.String())
	}
	// 非法 scope：400。
	rec = postJSON(t, h, nethttp.MethodGet, "/agui/grants?scope=everyone", "")
	if rec.Code != nethttp.StatusBadRequest {
		t.Fatalf("invalid scope status = %d, want 400\n%s", rec.Code, rec.Body.String())
	}

	// 身份缺失（绕过认证中间件）：401。
	rec = postJSON(t, newGrantAdmin(biz.NewGrantUsecase(repo)), nethttp.MethodGet, "/agui/grants", "")
	if rec.Code != nethttp.StatusUnauthorized {
		t.Fatalf("missing identity status = %d, want 401\n%s", rec.Code, rec.Body.String())
	}
}

// TestGrantAdmin_when_cross_user_boundary_enforced 验证跨用户边界：
// 无 agent:grant:query 的用户全租户列表 403；无 agent:grant:revoke 的
// 用户吊销他人授权 403；持码用户两个操作都放行。
func TestGrantAdmin_when_cross_user_boundary_enforced(t *testing.T) {
	repo := newFakeGrantRepo()
	repo.seed(8, "thread-b", "update_my_nickname")
	uc := biz.NewGrantUsecase(repo)

	// 普通用户（HasPermission 恒 false 的替身仓储；以全新内层构造避免
	// 复制带锁的替身值）。
	plainRepo := &noPermissionGrantRepo{fakeGrantRepo{active: repo.active}}
	plain := grantAdminMux(identityHandler(newGrantAdmin(biz.NewGrantUsecase(plainRepo)), "t-1", 7))
	if rec := postJSON(t, plain, nethttp.MethodGet, "/agui/grants?scope=tenant", ""); rec.Code != nethttp.StatusForbidden {
		t.Fatalf("tenant list without code status = %d, want 403\n%s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(t, plain, nethttp.MethodDelete, "/agui/grants/1", ""); rec.Code != nethttp.StatusForbidden {
		t.Fatalf("revoke other's grant without code status = %d, want 403\n%s", rec.Code, rec.Body.String())
	}

	// 持码用户（fakeGrantRepo 的 HasPermission 对两个码都放行）。
	manager := grantAdminMux(identityHandler(newGrantAdmin(uc), "t-1", 7))
	rec := postJSON(t, manager, nethttp.MethodGet, "/agui/grants?scope=tenant", "")
	if rec.Code != nethttp.StatusOK {
		t.Fatalf("tenant list with code status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	rec = postJSON(t, manager, nethttp.MethodDelete, "/agui/grants/1", "")
	if rec.Code != nethttp.StatusOK {
		t.Fatalf("managed revoke status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
}

// noPermissionGrantRepo 是权限码判定恒 false 的替身（复用 fakeGrantRepo
// 的其余行为）。
type noPermissionGrantRepo struct{ fakeGrantRepo }

func (*noPermissionGrantRepo) HasPermission(context.Context, biz.Identity, string) (bool, error) {
	return false, nil
}

// newGrantUsecaseForTest 以替身仓储构造授权用例。
func newGrantUsecaseForTest(repo biz.GrantRepo) *biz.GrantUsecase {
	return biz.NewGrantUsecase(repo)
}

// ackQuery 构造 ack 许可查询。
func ackQuery(userKey, sessionID, toolName string) ack.GrantQuery {
	return ack.GrantQuery{UserID: userKey, SessionID: sessionID, ToolName: toolName}
}

// ackGrant 构造 ack 许可写入（CreatedBy 与归属同键，与 ackhttp 决定流
// 的实际形态一致：DecidedBy 缺省回落 uid）。
func ackGrant(userKey, sessionID, toolName string) ack.Grant {
	return ack.Grant{UserID: userKey, SessionID: sessionID, ToolName: toolName, CreatedBy: userKey}
}
