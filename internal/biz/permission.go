package biz

import (
	"context"

	v1 "grape/api/perm/v1"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
)

// ErrPermissionDenied 表示入口层功能权限码判定不通过：当前用户（角色 →
// 菜单 → 权限码链）不持有端点声明的权限码。存在性不敏感的明确拒绝
// （403），与行级判定的 NotFound 口径（不可见即 404）分工。
var ErrPermissionDenied = kratoserrors.Forbidden(v1.ErrorReason_PERM_ACCESS_DENIED.String(), "permission denied")

// 管理面功能权限码常量：<资源>:<操作> 形态，与生产种子菜单的权限点
// 一一对应。码只被入口层（middleware 的操作码表）与测试引用，业务用例
// 不携带权限参数。
const (
	// 用户管理（sys_user，种子菜单 202 子树）。
	PermUserQuery         = "sys:user:query"
	PermUserCreate        = "sys:user:create"
	PermUserUpdate        = "sys:user:update"
	PermUserDelete        = "sys:user:delete"
	PermUserResetPwd      = "sys:user:reset-pwd"
	PermUserAssignRole    = "sys:user:assign-role"
	PermUserTransferOwner = "sys:user:transfer-owner"

	// 部门管理（sys_dept，种子菜单 236 子树）。结构移动（MoveDept）
	// 并入 sys:dept:update——移动是编辑结构形态，种子权限点不设 move 码。
	PermDeptQuery  = "sys:dept:query"
	PermDeptCreate = "sys:dept:create"
	PermDeptUpdate = "sys:dept:update"
	PermDeptDelete = "sys:dept:delete"

	// 用户组管理（sys_user_group，种子菜单 241 子树）。成员映射随宿主
	// 维护，成员增删单设码（对齐分配角色先例：授权动作与元数据编辑分级）。
	PermUserGroupQuery        = "sys:user-group:query"
	PermUserGroupCreate       = "sys:user-group:create"
	PermUserGroupUpdate       = "sys:user-group:update"
	PermUserGroupDelete       = "sys:user-group:delete"
	PermUserGroupAssignMember = "sys:user-group:assign-member"

	// 角色管理（sys_role，种子菜单 209 子树）。数据范围与菜单授权是
	// 授权决定（谁能看哪些数据、谁能进哪些功能），单设码与角色元数据
	// 编辑分级；查询类（含数据范围/授权明细的读取）并入 sys:role:query。
	PermRoleQuery        = "sys:role:query"
	PermRoleCreate       = "sys:role:create"
	PermRoleUpdate       = "sys:role:update"
	PermRoleDelete       = "sys:role:delete"
	PermRoleSetDataScope = "sys:role:set-data-scope"
	PermRoleSetMenus     = "sys:role:set-menus"

	// 菜单管理（sys_menu，种子菜单 214 子树）。结构移动（MoveMenuSuper）
	// 并入 sys:menu:update，口径同部门。
	PermMenuQuery  = "sys:menu:query"
	PermMenuCreate = "sys:menu:create"
	PermMenuUpdate = "sys:menu:update"
	PermMenuDelete = "sys:menu:delete"

	// 字典管理（sys_dict_type / sys_dict_data，种子菜单 224 子树：操作级
	// 权限点 225-228 由 seed 包预铺）。类型与数据两级共用同一组操作码
	//（同一管理页内的两级维护，权限粒度不细分到级）。
	PermDictQuery  = "sys:dict:query"
	PermDictCreate = "sys:dict:create"
	PermDictUpdate = "sys:dict:update"
	PermDictDelete = "sys:dict:delete"

	// 配置管理（sys_config / sys_tenant_config，种子菜单 233/234 子树：
	// 操作级权限点 258-265 由 seed 包预铺）。全局配置读取本身
	// 即全局口径（sys_config 无 tenant_id、无 RLS，读取不设 super），
	// 写端点经受控写函数（Create/Update/DeleteSuper）；写码只应由平台
	// 根租户持有，且 ConfigUsecase 的写路径另强制根租户校验（子租户即使
	// 持有写码也拒绝）。租户配置读写均走 RLS 与普通 DML，无 super。
	// 两组操作码按表分设（全局/租户）。
	PermConfigGlobalQuery  = "sys:config:global:query"
	PermConfigGlobalCreate = "sys:config:global:create"
	PermConfigGlobalUpdate = "sys:config:global:update"
	PermConfigGlobalDelete = "sys:config:global:delete"
	PermConfigTenantQuery  = "sys:config:tenant:query"
	PermConfigTenantCreate = "sys:config:tenant:create"
	PermConfigTenantUpdate = "sys:config:tenant:update"
	PermConfigTenantDelete = "sys:config:tenant:delete"

	// 功能开关（sys_feature_flag，种子菜单 235 子树：操作级权限点
	// 266-269 由 seed 包预铺）与通用翻译（sys_translation，
	// 种子菜单 257 子树：操作级权限点 270-273 同批预铺）。两表均为租户
	// 表，读写走 RLS 与普通 DML，无 super；操作码按表分设。
	PermConfigFeatureFlagQuery  = "sys:config:feature-flag:query"
	PermConfigFeatureFlagCreate = "sys:config:feature-flag:create"
	PermConfigFeatureFlagUpdate = "sys:config:feature-flag:update"
	PermConfigFeatureFlagDelete = "sys:config:feature-flag:delete"
	PermConfigTranslationQuery  = "sys:config:translation:query"
	PermConfigTranslationCreate = "sys:config:translation:create"
	PermConfigTranslationUpdate = "sys:config:translation:update"
	PermConfigTranslationDelete = "sys:config:translation:delete"

	// 审计日志（sys_login_log / sys_operation_log，种子菜单 230/231）：两表
	// append-only 只读面，沿用 seed 包预铺的页面级权限点，List 与点查
	// 同码。
	PermAuditOperationLog = "sys:audit:operation-log"
	PermAuditLoginLog     = "sys:audit:login-log"

	// 会话治理（sys_session，种子菜单 254 子树：查询/撤销两枚操作码
	// 255/256 由 seed 包预铺）。
	PermSessionQuery  = "sys:session:query"
	PermSessionRevoke = "sys:session:revoke"

	// 租户管理（sys_tenant，种子菜单 219 子树：操作级权限点 220-223 由
	// seed 包预铺）。平台级租户实体管理面：方法名带 super 表达「管理
	// 一整个租户实体」，租户树边界仍由 RLS 承担。停用/恢复并入
	// sys:tenant:update（状态变更属编辑形态，种子权限点不设独立码）。
	PermTenantQuery  = "sys:tenant:query"
	PermTenantCreate = "sys:tenant:create"
	PermTenantUpdate = "sys:tenant:update"
	PermTenantDelete = "sys:tenant:delete"

	// 知识库（kb_library / kb_document，种子菜单 274 子树：操作级权限点
	// 275-278 由 seed 包预铺）。库与条目共用同一组操作码（同一管理页内的
	// 两级维护）；重建索引并入 sys:knowledge:update。模型选项端点
	// （ListModelOptions）也走 sys:knowledge:query，避免知识库管理员被迫
	// 持有 sys:model:query。
	PermKnowledgeQuery  = "sys:knowledge:query"
	PermKnowledgeCreate = "sys:knowledge:create"
	PermKnowledgeUpdate = "sys:knowledge:update"
	PermKnowledgeDelete = "sys:knowledge:delete"

	// 模型目录（sys_model，种子菜单 279 子树：操作级权限点 280-286 由
	// seed 包预铺）。租户行 CRUD 与平台行 super 写分码：平台行对所有租户
	// 可见，写面只应由平台管理员持有。
	PermModelQuery          = "sys:model:query"
	PermModelCreate         = "sys:model:create"
	PermModelUpdate         = "sys:model:update"
	PermModelDelete         = "sys:model:delete"
	PermModelPlatformCreate = "sys:model:platform:create"
	PermModelPlatformUpdate = "sys:model:platform:update"
	PermModelPlatformDelete = "sys:model:platform:delete"
)

// PermissionRepo 是功能权限码仓储接口：fn_has_permission / GetPermissionCodes
// 两个库内推导的薄封装。判定与枚举都经存储层会话门面在租户事务内执行
// （RLS），跨租户权限码等同于不存在。
type PermissionRepo interface {
	// HasPermission 判断用户是否持有功能权限码（角色 → 菜单 → 权限码，
	// 与数据范围正交）；用户、角色、菜单任一环无效即 false。
	HasPermission(ctx context.Context, auth Identity, code string) (bool, error)
	// ListCodes 枚举用户持有的全部权限码（去重升序）；用户无效时返回
	// 空集——空集是正常返回，不是错误。
	ListCodes(ctx context.Context, auth Identity) ([]string, error)
}

// PermissionUsecase 是功能权限码用例：入口层（判定中间件）与权限码查询
// 端点的业务面。判定是每请求库内直查（无缓存——sys_perm_version 是外部
// 进程缓存设计预留的失效信号，应用内无消费方，不发明新缓存）。
type PermissionUsecase struct {
	repo PermissionRepo
}

// NewPermissionUsecase 构造功能权限码用例。
func NewPermissionUsecase(repo PermissionRepo) *PermissionUsecase {
	return &PermissionUsecase{repo: repo}
}

// HasPermission 判断用户是否持有指定功能权限码，供入口层做需要 403
// 语义的前置判定。
func (uc *PermissionUsecase) HasPermission(ctx context.Context, auth Identity, code string) (bool, error) {
	return uc.repo.HasPermission(ctx, auth, code)
}

// Codes 枚举当前用户持有的全部权限码（去重升序），供权限码查询端点
// 返回给前端做导航过滤。
func (uc *PermissionUsecase) Codes(ctx context.Context, auth Identity) ([]string, error) {
	return uc.repo.ListCodes(ctx, auth)
}
