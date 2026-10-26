package biz

import (
	"context"
	"errors"
	"slices"
	"testing"
)

// fakeGrantRepo 记录用例对仓储原语的调用次序与参数，按预置表返回：
// Resolve 的排序编排与权限码 gate 的行为面在这里验证。
type fakeGrantRepo struct {
	// 预置活跃许可：键 (userID, sessionID, toolName)，sessionID 空=全局级。
	active map[grantKey]*ToolGrant
	// hasPermission 按 code 预置判定结果。
	perm map[string]bool

	calls []string
}

type grantKey struct {
	userID    int64
	sessionID string
	toolName  string
}

func newFakeGrantRepo() *fakeGrantRepo {
	return &fakeGrantRepo{active: map[grantKey]*ToolGrant{}, perm: map[string]bool{}}
}

func (r *fakeGrantRepo) seed(userID int64, sessionID, toolName string) {
	g := &ToolGrant{
		ID: 1, TenantID: "t-1", UserID: userID, ToolName: toolName,
	}
	if sessionID != "" {
		s := sessionID
		g.SessionID = &s
	}
	r.active[grantKey{userID, sessionID, toolName}] = g
}

func (r *fakeGrantRepo) FindActiveForSession(_ context.Context, _ Identity, userID int64, sessionID, toolName string) (*ToolGrant, error) {
	r.calls = append(r.calls, "session")
	if g, ok := r.active[grantKey{userID, sessionID, toolName}]; ok {
		return g, nil
	}
	return nil, nil
}

func (r *fakeGrantRepo) FindActiveGlobal(_ context.Context, _ Identity, userID int64, toolName string) (*ToolGrant, error) {
	r.calls = append(r.calls, "global")
	if g, ok := r.active[grantKey{userID, "", toolName}]; ok {
		return g, nil
	}
	return nil, nil
}

func (r *fakeGrantRepo) Create(_ context.Context, _ Identity, g *ToolGrant) (bool, error) {
	r.calls = append(r.calls, "create")
	key := grantKey{g.UserID, "", g.ToolName}
	if g.SessionID != nil {
		key.sessionID = *g.SessionID
	}
	if _, exists := r.active[key]; exists {
		return false, nil
	}
	r.active[key] = g
	return true, nil
}

func (r *fakeGrantRepo) GetByID(_ context.Context, _ Identity, id int64) (*ToolGrant, error) {
	r.calls = append(r.calls, "get")
	for _, g := range r.active {
		if g.ID == id {
			return g, nil
		}
	}
	return nil, ErrGrantNotFound
}

func (r *fakeGrantRepo) ListActiveByUser(context.Context, Identity, int64) ([]*ToolGrant, error) {
	return nil, nil
}

func (r *fakeGrantRepo) ListActiveByTenant(_ context.Context, _ Identity) ([]*ToolGrant, error) {
	r.calls = append(r.calls, "list-tenant")
	return nil, nil
}

func (r *fakeGrantRepo) RevokeOwn(_ context.Context, _ Identity, id int64) error {
	r.calls = append(r.calls, "revoke-own")
	return r.revoke(id)
}

func (r *fakeGrantRepo) RevokeManaged(_ context.Context, _ Identity, id int64) error {
	r.calls = append(r.calls, "revoke-managed")
	return r.revoke(id)
}

func (r *fakeGrantRepo) revoke(id int64) error {
	for k, g := range r.active {
		if g.ID == id {
			delete(r.active, k)
			return nil
		}
	}
	return ErrGrantNotFound
}

func (r *fakeGrantRepo) HasPermission(_ context.Context, _ Identity, code string) (bool, error) {
	r.calls = append(r.calls, "has-permission:"+code)
	return r.perm[code], nil
}

var (
	_ GrantRepo = (*fakeGrantRepo)(nil)
)

// grantTestAuth 是用例测试的固定身份。
var grantTestAuth = Identity{TenantID: "t-1", UserID: 7}

// TestGrantResolve_when_session_level_exists_skips_global 验证会话级
// 命中即返回：不再查全局级（会话级先于全局级的短路语义）。
func TestGrantResolve_when_session_level_exists_skips_global(t *testing.T) {
	repo := newFakeGrantRepo()
	repo.seed(7, "thread-a", "update_my_nickname")
	repo.seed(7, "", "update_my_nickname")
	uc := NewGrantUsecase(repo)

	g, err := uc.Resolve(context.Background(), grantTestAuth, 7, "thread-a", "update_my_nickname")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if g == nil || g.SessionID == nil || *g.SessionID != "thread-a" {
		t.Fatalf("resolve must hit the session-level grant, got %+v", g)
	}
	if !slices.Equal(repo.calls, []string{"session"}) {
		t.Fatalf("global lookup must be skipped, calls: %v", repo.calls)
	}
}

// TestGrantResolve_when_session_level_missing_falls_back_global 验证
// 会话级未命中后落全局级：两级都查、返回全局级行。
func TestGrantResolve_when_session_level_missing_falls_back_global(t *testing.T) {
	repo := newFakeGrantRepo()
	repo.seed(7, "", "update_my_nickname")
	uc := NewGrantUsecase(repo)

	g, err := uc.Resolve(context.Background(), grantTestAuth, 7, "thread-a", "update_my_nickname")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if g == nil || g.SessionID != nil {
		t.Fatalf("resolve must fall back to the global grant, got %+v", g)
	}
	if !slices.Equal(repo.calls, []string{"session", "global"}) {
		t.Fatalf("lookup order must be session then global, calls: %v", repo.calls)
	}
}

// TestGrantResolve_when_both_levels_missing_returns_nil 验证两级都未
// 命中返回 (nil, nil)：miss 是正常控制流，确认照常发起。
func TestGrantResolve_when_both_levels_missing_returns_nil(t *testing.T) {
	uc := NewGrantUsecase(newFakeGrantRepo())

	g, err := uc.Resolve(context.Background(), grantTestAuth, 7, "thread-a", "update_my_nickname")
	if err != nil {
		t.Fatalf("resolve miss must not error, got %v", err)
	}
	if g != nil {
		t.Fatalf("resolve miss must return nil grant, got %+v", g)
	}
}

// TestGrantResolve_when_empty_session_queries_global_only 验证空会话
// 标识直接查全局级，不发起会话级查询。
func TestGrantResolve_when_empty_session_queries_global_only(t *testing.T) {
	repo := newFakeGrantRepo()
	repo.seed(7, "", "update_my_nickname")
	uc := NewGrantUsecase(repo)

	g, err := uc.Resolve(context.Background(), grantTestAuth, 7, "", "update_my_nickname")
	if err != nil || g == nil {
		t.Fatalf("resolve: %v, %+v", err, g)
	}
	if !slices.Equal(repo.calls, []string{"global"}) {
		t.Fatalf("empty session must query global only, calls: %v", repo.calls)
	}
}

// TestGrantRemember_when_same_key_active_skips_idempotently 验证同键
// 活跃许可已存在时幂等跳过：不覆盖、不报错，先到决定生效。
func TestGrantRemember_when_same_key_active_skips_idempotently(t *testing.T) {
	repo := newFakeGrantRepo()
	uc := NewGrantUsecase(repo)
	ctx := context.Background()

	if err := uc.Remember(ctx, grantTestAuth, "thread-a", "update_my_nickname"); err != nil {
		t.Fatalf("first remember: %v", err)
	}
	if err := uc.Remember(ctx, grantTestAuth, "thread-a", "update_my_nickname"); err != nil {
		t.Fatalf("duplicate remember must be idempotent, got %v", err)
	}
	if len(repo.active) != 1 {
		t.Fatalf("active grants = %d, want 1 (duplicate must not stack)", len(repo.active))
	}
}

// TestGrantRemember_when_empty_tool_name_rejected 验证空工具名拒绝。
func TestGrantRemember_when_empty_tool_name_rejected(t *testing.T) {
	uc := NewGrantUsecase(newFakeGrantRepo())

	err := uc.Remember(context.Background(), grantTestAuth, "thread-a", "")
	if !errors.Is(err, ErrGrantInvalidArgument) {
		t.Fatalf("empty tool name must return ErrGrantInvalidArgument, got %v", err)
	}
}

// TestGrantRemember_when_empty_session_creates_global_scope 验证空会话
// 落全局级形态（SessionID 为 nil）。
func TestGrantRemember_when_empty_session_creates_global_scope(t *testing.T) {
	repo := newFakeGrantRepo()
	uc := NewGrantUsecase(repo)

	if err := uc.Remember(context.Background(), grantTestAuth, "", "update_my_nickname"); err != nil {
		t.Fatalf("remember: %v", err)
	}
	g := repo.active[grantKey{7, "", "update_my_nickname"}]
	if g == nil || g.SessionID != nil {
		t.Fatalf("empty session must create a global grant, got %+v", g)
	}
}

// TestGrantListTenant_without_query_code_denied 验证全租户列表的权限
// 码 gate：无 agent:grant:query 返回 403 形态的 ErrGrantAccessDenied，
// 仓储列表原语不被触达。
func TestGrantListTenant_without_query_code_denied(t *testing.T) {
	repo := newFakeGrantRepo()
	uc := NewGrantUsecase(repo)

	_, err := uc.ListTenant(context.Background(), grantTestAuth)
	if !errors.Is(err, ErrGrantAccessDenied) {
		t.Fatalf("tenant list without code must return ErrGrantAccessDenied, got %v", err)
	}
	if slices.Contains(repo.calls, "list-tenant") {
		t.Fatalf("repo list must not be reached without the code, calls: %v", repo.calls)
	}
}

// TestGrantRevoke_when_owner_self_revokes_without_code 验证属主吊销
// 自己的授权不要求权限码，走属主定位原语。
func TestGrantRevoke_when_owner_self_revokes_without_code(t *testing.T) {
	repo := newFakeGrantRepo()
	repo.seed(7, "thread-a", "update_my_nickname")
	uc := NewGrantUsecase(repo)

	if err := uc.Revoke(context.Background(), grantTestAuth, 1); err != nil {
		t.Fatalf("self revoke: %v", err)
	}
	if !slices.Contains(repo.calls, "revoke-own") {
		t.Fatalf("self revoke must use the owner-scoped primitive, calls: %v", repo.calls)
	}
	if len(repo.active) != 0 {
		t.Fatalf("grant must be revoked, left: %v", repo.active)
	}
}

// TestGrantRevoke_when_other_user_without_revoke_code_denied 验证吊销
// 他人授权无 agent:grant:revoke 返回 ErrGrantAccessDenied，不触达吊销
// 原语。
func TestGrantRevoke_when_other_user_without_revoke_code_denied(t *testing.T) {
	repo := newFakeGrantRepo()
	repo.seed(8, "thread-b", "update_my_nickname")
	uc := NewGrantUsecase(repo)

	err := uc.Revoke(context.Background(), grantTestAuth, 1)
	if !errors.Is(err, ErrGrantAccessDenied) {
		t.Fatalf("revoke other's grant without code must return ErrGrantAccessDenied, got %v", err)
	}
	if slices.Contains(repo.calls, "revoke-managed") {
		t.Fatalf("managed revoke must not be reached without the code, calls: %v", repo.calls)
	}
	if len(repo.active) != 1 {
		t.Fatalf("other user's grant must survive, left: %v", repo.active)
	}
}

// TestGrantRevoke_when_other_user_with_revoke_code_manages 验证持有
// agent:grant:revoke 的用户可吊销他人授权，走管理原语。
func TestGrantRevoke_when_other_user_with_revoke_code_manages(t *testing.T) {
	repo := newFakeGrantRepo()
	repo.perm[PermAgentGrantRevoke] = true
	repo.seed(8, "thread-b", "update_my_nickname")
	uc := NewGrantUsecase(repo)

	if err := uc.Revoke(context.Background(), grantTestAuth, 1); err != nil {
		t.Fatalf("managed revoke: %v", err)
	}
	if !slices.Contains(repo.calls, "revoke-managed") {
		t.Fatalf("revoke with code must use the managed primitive, calls: %v", repo.calls)
	}
	if len(repo.active) != 0 {
		t.Fatalf("other user's grant must be revoked, left: %v", repo.active)
	}
}

// TestGrantRevoke_when_id_invisible_returns_not_found 验证不可见 id
// （不存在/跨租户）返回 ErrGrantNotFound，归属判定不泄露存在性。
func TestGrantRevoke_when_id_invisible_returns_not_found(t *testing.T) {
	uc := NewGrantUsecase(newFakeGrantRepo())

	err := uc.Revoke(context.Background(), grantTestAuth, 99)
	if !errors.Is(err, ErrGrantNotFound) {
		t.Fatalf("invisible id must return ErrGrantNotFound, got %v", err)
	}
}

// TestGrantRevoke_when_non_positive_id_rejected 验证非正 id 拒绝。
func TestGrantRevoke_when_non_positive_id_rejected(t *testing.T) {
	uc := NewGrantUsecase(newFakeGrantRepo())

	err := uc.Revoke(context.Background(), grantTestAuth, 0)
	if !errors.Is(err, ErrGrantInvalidArgument) {
		t.Fatalf("non-positive id must return ErrGrantInvalidArgument, got %v", err)
	}
}
