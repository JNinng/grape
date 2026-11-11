package middleware

import (
	"context"

	auditv1 "grape/api/audit/v1"
	configv1 "grape/api/config/v1"
	dictv1 "grape/api/dict/v1"
	knowledgev1 "grape/api/knowledge/v1"
	modelv1 "grape/api/model/v1"
	orgv1 "grape/api/org/v1"
	permv1 "grape/api/perm/v1"
	tenantv1 "grape/api/tenant/v1"
	userv1 "grape/api/user/v1"
	"grape/internal/biz"

	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/transport"
)

// operationPermissionCodes 是管理面端点的功能权限码表（tr.Operation()
// → biz 权限码常量，与种子菜单权限点一一对应）。在表内即受功能权限约束，
// 不在表内只受令牌校验与行级判定约束（登录生命周期、agent 业务面、
// GetMyPermissions 不设码）。判定由库内 fn_has_permission 推导，与前端
// 导航过滤共用同一权限码集合。
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
	// 用户分配选择器与角色整组替换：候选接口按分配动作设码（角色相关
	// 并入 assign-role，归属候选并入 transfer-owner）。
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
	// 知识库与条目（库与条目共用同一组操作码；模型选项走 knowledge:query）
	knowledgev1.OperationLibraryServiceGetLibrary:            biz.PermKnowledgeQuery,
	knowledgev1.OperationLibraryServiceListLibraries:         biz.PermKnowledgeQuery,
	knowledgev1.OperationLibraryServiceListModelOptions:      biz.PermKnowledgeQuery,
	knowledgev1.OperationLibraryServiceCreateLibrary:         biz.PermKnowledgeCreate,
	knowledgev1.OperationLibraryServiceUpdateLibrary:         biz.PermKnowledgeUpdate,
	knowledgev1.OperationLibraryServiceDeleteLibrary:         biz.PermKnowledgeDelete,
	knowledgev1.OperationDocumentServiceGetDocument:          biz.PermKnowledgeQuery,
	knowledgev1.OperationDocumentServiceListDocuments:        biz.PermKnowledgeQuery,
	knowledgev1.OperationDocumentServiceListLibraryDocuments: biz.PermKnowledgeQuery,
	knowledgev1.OperationDocumentServiceCreateDocument:       biz.PermKnowledgeCreate,
	knowledgev1.OperationDocumentServiceUpdateDocument:       biz.PermKnowledgeUpdate,
	knowledgev1.OperationDocumentServiceDeleteDocument:       biz.PermKnowledgeDelete,
	knowledgev1.OperationDocumentServiceReindexDocument:      biz.PermKnowledgeUpdate,
	knowledgev1.OperationDocumentServiceSearchDocuments:      biz.PermKnowledgeQuery,
	knowledgev1.OperationDocumentServiceListDocumentChunks:   biz.PermKnowledgeQuery,
	// 文件上传的手写路由（multipart，无生成操作名）：操作名即路径模板，
	// 与「创建条目」同设 sys:knowledge:create（同一管理页的上传形态）。
	"/v1/knowledge/documents/upload": biz.PermKnowledgeCreate,
	// 模型目录（租户行与平台 super 写分码）
	modelv1.OperationModelServiceGetModel:         biz.PermModelQuery,
	modelv1.OperationModelServiceListModels:       biz.PermModelQuery,
	modelv1.OperationModelServiceCreateModel:      biz.PermModelCreate,
	modelv1.OperationModelServiceUpdateModel:      biz.PermModelUpdate,
	modelv1.OperationModelServiceDeleteModel:      biz.PermModelDelete,
	modelv1.OperationModelServiceCreateModelSuper: biz.PermModelPlatformCreate,
	modelv1.OperationModelServiceUpdateModelSuper: biz.PermModelPlatformUpdate,
	modelv1.OperationModelServiceDeleteModelSuper: biz.PermModelPlatformDelete,
}

// PermissionMiddleware 是功能权限码判定中间件的注入类型。它与认证、链路
// 中间件同为 Kratos 的 middleware.Middleware，同型多实例须用命名类型
// 承载，否则 Wire 无法按类型装配（TraceMiddleware 先例）。
type PermissionMiddleware middleware.Middleware

// NewPermissionMiddleware 构造功能权限码判定中间件：码表内端点以认证
// 注入的身份经 fn_has_permission 判定，不通过返回 ErrPermissionDenied；
// 必须排在认证之后。fail-closed：无传输上下文或受控端点缺身份一律拒绝
// （装配错误或绕过认证链）；码表外端点原样放行。
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
