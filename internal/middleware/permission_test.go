package middleware

import (
	"context"
	"errors"
	"strings"
	"testing"

	auditv1 "grape/api/audit/v1"
	configv1 "grape/api/config/v1"
	dictv1 "grape/api/dict/v1"
	orgv1 "grape/api/org/v1"
	permv1 "grape/api/perm/v1"
	tenantv1 "grape/api/tenant/v1"
	userv1 "grape/api/user/v1"
	"grape/internal/biz"

	"github.com/go-kratos/kratos/v3/middleware"
)

// fakePermissionRepo 捕获判定入参并返回预设结论，记录调用次数供放行
// 旁路（码表外端点零调用）断言。
type fakePermissionRepo struct {
	granted bool
	err     error

	hasPermCalls int
	gotAuth      biz.Identity
	gotCode      string
}

func (f *fakePermissionRepo) HasPermission(_ context.Context, auth biz.Identity, code string) (bool, error) {
	f.hasPermCalls++
	f.gotAuth = auth
	f.gotCode = code
	return f.granted, f.err
}

func (f *fakePermissionRepo) ListCodes(context.Context, biz.Identity) ([]string, error) {
	return nil, nil
}

// identityContext 把身份事实放进 ctx，等价认证中间件命中后的构造。
func identityContext(ctx context.Context, auth biz.Identity) context.Context {
	return biz.WithIdentity(ctx, auth)
}

// TestPermissionMiddleware_ungated_operation_skips_lookup 验证码表外端点
// （agent 业务面/认证生命周期/权限码查询）直接放行，权限仓储零调用——
// 未声明权限码的操作只受令牌校验与行级判定约束。
func TestPermissionMiddleware_ungated_operation_skips_lookup(t *testing.T) {
	repo := &fakePermissionRepo{granted: false}
	m := middleware.Middleware(NewPermissionMiddleware(biz.NewPermissionUsecase(repo)))
	ctx := identityContext(serverContext(context.Background(),
		permv1.OperationPermissionServiceGetMyPermissions, fakeHeader{}), biz.Identity{TenantID: "t-1", UserID: 7})

	called, err := invoke(ctx, m)
	if err != nil {
		t.Fatalf("ungated operation: %v", err)
	}
	if !called {
		t.Fatal("handler must run for ungated operation")
	}
	if repo.hasPermCalls != 0 {
		t.Fatalf("has-permission calls = %d, want 0", repo.hasPermCalls)
	}
}

// TestPermissionMiddleware_denies_without_transport 验证 fail-closed：
// 取不到传输上下文时端点是否受控无从判定，一律拒绝且不触达 handler。
func TestPermissionMiddleware_denies_without_transport(t *testing.T) {
	repo := &fakePermissionRepo{}
	m := middleware.Middleware(NewPermissionMiddleware(biz.NewPermissionUsecase(repo)))

	called, err := invoke(context.Background(), m)
	if called {
		t.Fatal("handler must not run without a transport")
	}
	if !errors.Is(err, biz.ErrSessionInvalid) {
		t.Fatalf("err = %v, want ErrSessionInvalid", err)
	}
	if repo.hasPermCalls != 0 {
		t.Fatalf("has-permission calls = %d, want 0", repo.hasPermCalls)
	}
}

// TestPermissionMiddleware_fail_closed_when_identity_missing 验证受控端
// 点上身份缺失（装配错误或绕过认证链）按未认证拒绝，不做无身份判定。
func TestPermissionMiddleware_fail_closed_when_identity_missing(t *testing.T) {
	repo := &fakePermissionRepo{}
	m := middleware.Middleware(NewPermissionMiddleware(biz.NewPermissionUsecase(repo)))
	ctx := serverContext(context.Background(), userv1.OperationUserServiceDeleteUser, fakeHeader{})

	called, err := invoke(ctx, m)
	if called {
		t.Fatal("handler must not run without identity on gated operation")
	}
	if !errors.Is(err, biz.ErrSessionInvalid) {
		t.Fatalf("err = %v, want ErrSessionInvalid", err)
	}
	if repo.hasPermCalls != 0 {
		t.Fatalf("has-permission calls = %d, want 0", repo.hasPermCalls)
	}
}

// TestPermissionMiddleware_denies_without_permission 验证无权限：判定
// false 返回 ErrPermissionDenied（403），handler 不执行，且透传身份与
// 端点声明的权限码给仓储。
func TestPermissionMiddleware_denies_without_permission(t *testing.T) {
	repo := &fakePermissionRepo{granted: false}
	m := middleware.Middleware(NewPermissionMiddleware(biz.NewPermissionUsecase(repo)))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}
	ctx := identityContext(serverContext(context.Background(),
		userv1.OperationUserServiceDeleteUser, fakeHeader{}), auth)

	called, err := invoke(ctx, m)
	if called {
		t.Fatal("handler must not run without permission")
	}
	if !errors.Is(err, biz.ErrPermissionDenied) {
		t.Fatalf("err = %v, want ErrPermissionDenied", err)
	}
	if repo.gotAuth != auth || repo.gotCode != biz.PermUserDelete {
		t.Fatalf("lookup = (%+v, %s), want (%+v, %s)", repo.gotAuth, repo.gotCode, auth, biz.PermUserDelete)
	}
}

// TestPermissionMiddleware_granted_operation_reaches_handler 验证有权限：
// 判定 true 放行，仓储收到认证身份与端点声明码。
func TestPermissionMiddleware_granted_operation_reaches_handler(t *testing.T) {
	repo := &fakePermissionRepo{granted: true}
	m := middleware.Middleware(NewPermissionMiddleware(biz.NewPermissionUsecase(repo)))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}
	ctx := identityContext(serverContext(context.Background(),
		orgv1.OperationUserGroupServiceAddUserGroupMembers, fakeHeader{}), auth)

	called, err := invoke(ctx, m)
	if err != nil {
		t.Fatalf("granted operation: %v", err)
	}
	if !called {
		t.Fatal("handler must run with permission")
	}
	if repo.gotAuth != auth || repo.gotCode != biz.PermUserGroupAssignMember {
		t.Fatalf("lookup = (%+v, %s), want (%+v, %s)", repo.gotAuth, repo.gotCode, auth, biz.PermUserGroupAssignMember)
	}
}

// TestPermissionMiddleware_propagates_lookup_error 验证仓储错误原样传播
// （不吞错不放行，也不映射成 403）。
func TestPermissionMiddleware_propagates_lookup_error(t *testing.T) {
	repoErr := errors.New("lookup failed")
	repo := &fakePermissionRepo{err: repoErr}
	m := middleware.Middleware(NewPermissionMiddleware(biz.NewPermissionUsecase(repo)))
	ctx := identityContext(serverContext(context.Background(),
		userv1.OperationUserServiceListUsers, fakeHeader{}), biz.Identity{TenantID: "t-1", UserID: 7})

	called, err := invoke(ctx, m)
	if called {
		t.Fatal("handler must not run when lookup fails")
	}
	if !errors.Is(err, repoErr) {
		t.Fatalf("err = %v, want repo error", err)
	}
}

// TestOperationPermissionCodes_match_config_family_codes 验证功能开关与
// 通用翻译的 10 个端点逐一映射到预期的权限码常量（精确相等，不只校验
// 码形态）：读并入 :query，写各对应 create/update/delete。
func TestOperationPermissionCodes_match_config_family_codes(t *testing.T) {
	want := map[string]string{
		configv1.OperationFeatureFlagServiceGetFeatureFlag:    biz.PermConfigFeatureFlagQuery,
		configv1.OperationFeatureFlagServiceListFeatureFlags:  biz.PermConfigFeatureFlagQuery,
		configv1.OperationFeatureFlagServiceCreateFeatureFlag: biz.PermConfigFeatureFlagCreate,
		configv1.OperationFeatureFlagServiceUpdateFeatureFlag: biz.PermConfigFeatureFlagUpdate,
		configv1.OperationFeatureFlagServiceDeleteFeatureFlag: biz.PermConfigFeatureFlagDelete,
		configv1.OperationTranslationServiceGetTranslation:    biz.PermConfigTranslationQuery,
		configv1.OperationTranslationServiceListTranslations:  biz.PermConfigTranslationQuery,
		configv1.OperationTranslationServiceCreateTranslation: biz.PermConfigTranslationCreate,
		configv1.OperationTranslationServiceUpdateTranslation: biz.PermConfigTranslationUpdate,
		configv1.OperationTranslationServiceDeleteTranslation: biz.PermConfigTranslationDelete,
	}
	for operation, code := range want {
		got, ok := operationPermissionCodes[operation]
		if !ok {
			t.Fatalf("operation %s not covered", operation)
		}
		if got != code {
			t.Fatalf("operation %s maps to %q, want %q", operation, got, code)
		}
	}
}

// TestOperationPermissionCodes_match_config_global_and_tenant_codes 验证
// 全局配置与租户配置共 10 个端点逐一映射到预期的权限码常量（精确相等）：
// 读并入 :query，写各对应 create/update/delete。
func TestOperationPermissionCodes_match_config_global_and_tenant_codes(t *testing.T) {
	want := map[string]string{
		configv1.OperationConfigServiceGetConfig:                biz.PermConfigGlobalQuery,
		configv1.OperationConfigServiceListConfigs:              biz.PermConfigGlobalQuery,
		configv1.OperationConfigServiceCreateConfigSuper:        biz.PermConfigGlobalCreate,
		configv1.OperationConfigServiceUpdateConfigSuper:        biz.PermConfigGlobalUpdate,
		configv1.OperationConfigServiceDeleteConfigSuper:        biz.PermConfigGlobalDelete,
		configv1.OperationTenantConfigServiceGetTenantConfig:    biz.PermConfigTenantQuery,
		configv1.OperationTenantConfigServiceListTenantConfigs:  biz.PermConfigTenantQuery,
		configv1.OperationTenantConfigServiceCreateTenantConfig: biz.PermConfigTenantCreate,
		configv1.OperationTenantConfigServiceUpdateTenantConfig: biz.PermConfigTenantUpdate,
		configv1.OperationTenantConfigServiceDeleteTenantConfig: biz.PermConfigTenantDelete,
	}
	for operation, code := range want {
		got, ok := operationPermissionCodes[operation]
		if !ok {
			t.Fatalf("operation %s not covered", operation)
		}
		if got != code {
			t.Fatalf("operation %s maps to %q, want %q", operation, got, code)
		}
	}
}

// TestOperationPermissionCodes_match_tenant_codes 验证租户管理 7 个端点
// 逐一映射到预期的权限码常量（精确相等）：读并入 :query，创建/更新/删除
// 各对应操作码，停用/恢复并入 :update（状态变更属编辑形态）。
func TestOperationPermissionCodes_match_tenant_codes(t *testing.T) {
	want := map[string]string{
		tenantv1.OperationTenantServiceGetTenantSuper:     biz.PermTenantQuery,
		tenantv1.OperationTenantServiceListTenantsSuper:   biz.PermTenantQuery,
		tenantv1.OperationTenantServiceCreateTenantSuper:  biz.PermTenantCreate,
		tenantv1.OperationTenantServiceUpdateTenantSuper:  biz.PermTenantUpdate,
		tenantv1.OperationTenantServiceDisableTenantSuper: biz.PermTenantUpdate,
		tenantv1.OperationTenantServiceRestoreTenantSuper: biz.PermTenantUpdate,
		tenantv1.OperationTenantServiceDeleteTenantSuper:  biz.PermTenantDelete,
	}
	for operation, code := range want {
		got, ok := operationPermissionCodes[operation]
		if !ok {
			t.Fatalf("operation %s not covered", operation)
		}
		if got != code {
			t.Fatalf("operation %s maps to %q, want %q", operation, got, code)
		}
	}
}

func TestOperationPermissionCodes_cover_gated_endpoints(t *testing.T) {
	covered := map[string]struct{}{
		// 用户管理
		userv1.OperationUserServiceGetUser:           {},
		userv1.OperationUserServiceListUsers:         {},
		userv1.OperationUserServiceCreateUser:        {},
		userv1.OperationUserServiceUpdateUser:        {},
		userv1.OperationUserServiceSetUserPassword:   {},
		userv1.OperationUserServiceDeleteUser:        {},
		userv1.OperationUserServiceAssignRoles:       {},
		userv1.OperationUserServiceTransferUserOwner: {},
		// 部门管理
		orgv1.OperationDeptServiceGetDeptTree: {},
		orgv1.OperationDeptServiceGetDept:     {},
		orgv1.OperationDeptServiceListDepts:   {},
		orgv1.OperationDeptServiceCreateDept:  {},
		orgv1.OperationDeptServiceUpdateDept:  {},
		orgv1.OperationDeptServiceMoveDept:    {},
		orgv1.OperationDeptServiceDeleteDept:  {},
		// 用户组管理
		orgv1.OperationUserGroupServiceGetUserGroup:           {},
		orgv1.OperationUserGroupServiceListUserGroups:         {},
		orgv1.OperationUserGroupServiceListUserGroupMembers:   {},
		orgv1.OperationUserGroupServiceCreateUserGroup:        {},
		orgv1.OperationUserGroupServiceUpdateUserGroup:        {},
		orgv1.OperationUserGroupServiceDeleteUserGroup:        {},
		orgv1.OperationUserGroupServiceAddUserGroupMembers:    {},
		orgv1.OperationUserGroupServiceRemoveUserGroupMembers: {},
		// 角色管理
		permv1.OperationRoleServiceGetRole:          {},
		permv1.OperationRoleServiceListRoles:        {},
		permv1.OperationRoleServiceGetRoleDataScope: {},
		permv1.OperationRoleServiceListRoleMenus:    {},
		permv1.OperationRoleServiceCreateRole:       {},
		permv1.OperationRoleServiceUpdateRole:       {},
		permv1.OperationRoleServiceDeleteRole:       {},
		permv1.OperationRoleServiceSetRoleDataScope: {},
		permv1.OperationRoleServiceSetRoleMenus:     {},
		// 菜单管理
		permv1.OperationMenuServiceGetMenuTree:     {},
		permv1.OperationMenuServiceGetMenu:         {},
		permv1.OperationMenuServiceCreateMenuSuper: {},
		permv1.OperationMenuServiceUpdateMenuSuper: {},
		permv1.OperationMenuServiceMoveMenuSuper:   {},
		permv1.OperationMenuServiceDeleteMenuSuper: {},
		// 字典管理
		dictv1.OperationDictServiceGetDictType:    {},
		dictv1.OperationDictServiceListDictTypes:  {},
		dictv1.OperationDictServiceGetDictData:    {},
		dictv1.OperationDictServiceListDictDatas:  {},
		dictv1.OperationDictServiceCreateDictType: {},
		dictv1.OperationDictServiceCreateDictData: {},
		dictv1.OperationDictServiceUpdateDictType: {},
		dictv1.OperationDictServiceUpdateDictData: {},
		dictv1.OperationDictServiceDeleteDictType: {},
		dictv1.OperationDictServiceDeleteDictData: {},
		// 配置管理（全局配置 + 租户配置）
		configv1.OperationConfigServiceGetConfig:                {},
		configv1.OperationConfigServiceListConfigs:              {},
		configv1.OperationConfigServiceCreateConfigSuper:        {},
		configv1.OperationConfigServiceUpdateConfigSuper:        {},
		configv1.OperationConfigServiceDeleteConfigSuper:        {},
		configv1.OperationTenantConfigServiceGetTenantConfig:    {},
		configv1.OperationTenantConfigServiceListTenantConfigs:  {},
		configv1.OperationTenantConfigServiceCreateTenantConfig: {},
		configv1.OperationTenantConfigServiceUpdateTenantConfig: {},
		configv1.OperationTenantConfigServiceDeleteTenantConfig: {},
		// 功能开关与通用翻译
		configv1.OperationFeatureFlagServiceGetFeatureFlag:    {},
		configv1.OperationFeatureFlagServiceListFeatureFlags:  {},
		configv1.OperationFeatureFlagServiceCreateFeatureFlag: {},
		configv1.OperationFeatureFlagServiceUpdateFeatureFlag: {},
		configv1.OperationFeatureFlagServiceDeleteFeatureFlag: {},
		configv1.OperationTranslationServiceGetTranslation:    {},
		configv1.OperationTranslationServiceListTranslations:  {},
		configv1.OperationTranslationServiceCreateTranslation: {},
		configv1.OperationTranslationServiceUpdateTranslation: {},
		configv1.OperationTranslationServiceDeleteTranslation: {},
		// 租户管理
		tenantv1.OperationTenantServiceGetTenantSuper:     {},
		tenantv1.OperationTenantServiceListTenantsSuper:   {},
		tenantv1.OperationTenantServiceCreateTenantSuper:  {},
		tenantv1.OperationTenantServiceUpdateTenantSuper:  {},
		tenantv1.OperationTenantServiceDisableTenantSuper: {},
		tenantv1.OperationTenantServiceRestoreTenantSuper: {},
		tenantv1.OperationTenantServiceDeleteTenantSuper:  {},
		// 审计日志与会话治理
		auditv1.OperationAuditServiceListLoginLogs:        {},
		auditv1.OperationAuditServiceGetLoginLog:          {},
		auditv1.OperationAuditServiceListOperationLogs:    {},
		auditv1.OperationAuditServiceGetOperationLog:      {},
		auditv1.OperationSessionAdminServiceListSessions:  {},
		auditv1.OperationSessionAdminServiceRevokeSession: {},
	}
	for op := range covered {
		code, ok := operationPermissionCodes[op]
		if !ok {
			t.Fatalf("operation %s not covered by permission code table", op)
		}
		if code == "" || !strings.Contains(code, ":") {
			t.Fatalf("operation %s maps to invalid code %q", op, code)
		}
	}
	if len(operationPermissionCodes) != len(covered) {
		t.Fatalf("code table size = %d, want %d (no undeclared entries)", len(operationPermissionCodes), len(covered))
	}
}
