package middleware

import (
	"context"

	auditv1 "grape/api/audit/v1"
	configv1 "grape/api/config/v1"
	dictv1 "grape/api/dict/v1"
	orgv1 "grape/api/org/v1"
	permv1 "grape/api/perm/v1"
	tenantv1 "grape/api/tenant/v1"
	userv1 "grape/api/user/v1"
	"grape/internal/biz"

	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/transport"
)

// operationPermissionCodes 是管理面端点的功能权限码表：tr.Operation()
// → 权限码（biz 常量，种子菜单权限点一一对应）。在表内即受功能权限
// 约束；不在表内的端点只受令牌校验与行级判定约束（认证生命周期
// Login/Refresh/Logout、agent 业务面、权限码查询端点 GetMyPermissions
// 不设码，口径见 permission.md）。判定由库内 fn_has_permission 推导，
// 与前端导航过滤共用同一权限码集合。
//
// 覆盖面（入口层按操作声明权限码）：用户/部门/用户组/角色/
// 菜单/字典/配置（全局 + 租户）管理面与审计日志/会话治理面的全部写端点
// 与管理读端点；super 写端点（受控写面）同样在列。一个码可覆盖多个操作
// （读类并入 :query、结构移动并入 :update）——码的粒度对齐种子权限点，
// 不为每个 RPC 各造一码。
var operationPermissionCodes = map[string]string{
	// 用户管理
	userv1.OperationUserServiceGetUser:           biz.PermUserQuery,
	userv1.OperationUserServiceListUsers:         biz.PermUserQuery,
	userv1.OperationUserServiceCreateUser:        biz.PermUserCreate,
	userv1.OperationUserServiceUpdateUser:        biz.PermUserUpdate,
	userv1.OperationUserServiceSetUserPassword:   biz.PermUserResetPwd,
	userv1.OperationUserServiceDeleteUser:        biz.PermUserDelete,
	userv1.OperationUserServiceAssignRoles:       biz.PermUserAssignRole,
	userv1.OperationUserServiceTransferUserOwner: biz.PermUserTransferOwner,
	// 用户分配选择器与角色整组替换：候选接口按分配动作设权限码（角色
	// 选择器与当前/设置角色并入 assign-role，三个归属候选并入
	// transfer-owner）。
	userv1.OperationUserServiceListAssignableRoles:   biz.PermUserAssignRole,
	userv1.OperationUserServiceListUserRoles:         biz.PermUserAssignRole,
	userv1.OperationUserServiceSetUserRoles:          biz.PermUserAssignRole,
	userv1.OperationUserServiceListOwnerUserOptions:  biz.PermUserTransferOwner,
	userv1.OperationUserServiceListOwnerDeptOptions:  biz.PermUserTransferOwner,
	userv1.OperationUserServiceListOwnerGroupOptions: biz.PermUserTransferOwner,
	// 部门管理
	orgv1.OperationDeptServiceGetDeptTree: biz.PermDeptQuery,
	orgv1.OperationDeptServiceGetDept:     biz.PermDeptQuery,
	orgv1.OperationDeptServiceListDepts:   biz.PermDeptQuery,
	orgv1.OperationDeptServiceCreateDept:  biz.PermDeptCreate,
	orgv1.OperationDeptServiceUpdateDept:  biz.PermDeptUpdate,
	orgv1.OperationDeptServiceMoveDept:    biz.PermDeptUpdate,
	orgv1.OperationDeptServiceDeleteDept:  biz.PermDeptDelete,
	// 用户组管理
	orgv1.OperationUserGroupServiceGetUserGroup:           biz.PermUserGroupQuery,
	orgv1.OperationUserGroupServiceListUserGroups:         biz.PermUserGroupQuery,
	orgv1.OperationUserGroupServiceListUserGroupMembers:   biz.PermUserGroupQuery,
	orgv1.OperationUserGroupServiceCreateUserGroup:        biz.PermUserGroupCreate,
	orgv1.OperationUserGroupServiceUpdateUserGroup:        biz.PermUserGroupUpdate,
	orgv1.OperationUserGroupServiceDeleteUserGroup:        biz.PermUserGroupDelete,
	orgv1.OperationUserGroupServiceAddUserGroupMembers:    biz.PermUserGroupAssignMember,
	orgv1.OperationUserGroupServiceRemoveUserGroupMembers: biz.PermUserGroupAssignMember,
	// 角色管理
	permv1.OperationRoleServiceGetRole:          biz.PermRoleQuery,
	permv1.OperationRoleServiceListRoles:        biz.PermRoleQuery,
	permv1.OperationRoleServiceGetRoleDataScope: biz.PermRoleQuery,
	permv1.OperationRoleServiceListRoleMenus:    biz.PermRoleQuery,
	permv1.OperationRoleServiceCreateRole:       biz.PermRoleCreate,
	permv1.OperationRoleServiceUpdateRole:       biz.PermRoleUpdate,
	permv1.OperationRoleServiceDeleteRole:       biz.PermRoleDelete,
	permv1.OperationRoleServiceSetRoleDataScope: biz.PermRoleSetDataScope,
	permv1.OperationRoleServiceSetRoleMenus:     biz.PermRoleSetMenus,
	// 菜单管理
	permv1.OperationMenuServiceGetMenuTree:     biz.PermMenuQuery,
	permv1.OperationMenuServiceGetMenu:         biz.PermMenuQuery,
	permv1.OperationMenuServiceCreateMenuSuper: biz.PermMenuCreate,
	permv1.OperationMenuServiceUpdateMenuSuper: biz.PermMenuUpdate,
	permv1.OperationMenuServiceMoveMenuSuper:   biz.PermMenuUpdate,
	permv1.OperationMenuServiceDeleteMenuSuper: biz.PermMenuDelete,
	// 字典管理
	dictv1.OperationDictServiceGetDictType:    biz.PermDictQuery,
	dictv1.OperationDictServiceListDictTypes:  biz.PermDictQuery,
	dictv1.OperationDictServiceGetDictData:    biz.PermDictQuery,
	dictv1.OperationDictServiceListDictDatas:  biz.PermDictQuery,
	dictv1.OperationDictServiceCreateDictType: biz.PermDictCreate,
	dictv1.OperationDictServiceCreateDictData: biz.PermDictCreate,
	dictv1.OperationDictServiceUpdateDictType: biz.PermDictUpdate,
	dictv1.OperationDictServiceUpdateDictData: biz.PermDictUpdate,
	dictv1.OperationDictServiceDeleteDictType: biz.PermDictDelete,
	dictv1.OperationDictServiceDeleteDictData: biz.PermDictDelete,
	// 配置管理（全局配置 + 租户配置）
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
	// 功能开关与通用翻译（租户级配置族，读写均走 RLS 与普通 DML）
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
	// 租户管理（平台级租户实体管理；super 命名不表示绕过 RLS）。
	// 停用/恢复并入 :update（状态变更属编辑形态）。
	tenantv1.OperationTenantServiceGetTenantSuper:     biz.PermTenantQuery,
	tenantv1.OperationTenantServiceListTenantsSuper:   biz.PermTenantQuery,
	tenantv1.OperationTenantServiceCreateTenantSuper:  biz.PermTenantCreate,
	tenantv1.OperationTenantServiceUpdateTenantSuper:  biz.PermTenantUpdate,
	tenantv1.OperationTenantServiceDisableTenantSuper: biz.PermTenantUpdate,
	tenantv1.OperationTenantServiceRestoreTenantSuper: biz.PermTenantUpdate,
	tenantv1.OperationTenantServiceDeleteTenantSuper:  biz.PermTenantDelete,
	// 审计日志与会话治理
	auditv1.OperationAuditServiceListLoginLogs:        biz.PermAuditLoginLog,
	auditv1.OperationAuditServiceGetLoginLog:          biz.PermAuditLoginLog,
	auditv1.OperationAuditServiceListOperationLogs:    biz.PermAuditOperationLog,
	auditv1.OperationAuditServiceGetOperationLog:      biz.PermAuditOperationLog,
	auditv1.OperationSessionAdminServiceListSessions:  biz.PermSessionQuery,
	auditv1.OperationSessionAdminServiceRevokeSession: biz.PermSessionRevoke,
}

// PermissionMiddleware 是功能权限码判定中间件的注入类型。它与认证、链路
// 中间件同为 Kratos 的 middleware.Middleware，同型多实例须用命名类型
// 承载，否则 Wire 无法按类型装配（TraceMiddleware 先例）。
type PermissionMiddleware middleware.Middleware

// NewPermissionMiddleware 构造功能权限码判定中间件：对码表内声明的端点，
// 以认证中间件注入的身份经 fn_has_permission 判定（角色 → 菜单 → 权限码
// 链，库内推导），不通过返回 ErrPermissionDenied（403）。必须在认证中间
// 件之后入链：身份事实由前者注入。fail-closed 口径：取不到传输上下文
// （端点是否受控无从判定）或受控端点上身份缺失（装配错误/绕过认证链）
// 一律拒绝。码表外的端点原样放行。
func NewPermissionMiddleware(perms *biz.PermissionUsecase) PermissionMiddleware {
	return PermissionMiddleware(func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			tr, ok := transport.FromServerContext(ctx)
			if !ok {
				return nil, biz.ErrSessionInvalid
			}
			code, gated := operationPermissionCodes[tr.Operation()]
			if !gated {
				return handler(ctx, req)
			}
			auth, ok := biz.IdentityFromContext(ctx)
			if !ok {
				return nil, biz.ErrSessionInvalid
			}
			granted, err := perms.HasPermission(ctx, auth, code)
			if err != nil {
				return nil, err
			}
			if !granted {
				return nil, biz.ErrPermissionDenied
			}
			return handler(ctx, req)
		}
	})
}
