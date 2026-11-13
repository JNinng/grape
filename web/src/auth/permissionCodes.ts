// 管理面功能权限码常量：页面级码（*Page）用于导航/路由守卫的模块门禁，
// Query 码用于列表/树页的数据门禁（与页面码同时持有才放行），其余操作级
// 码用于按钮/行操作显隐；完整生产码表以
// `internal/data/pg_sqlc/sqlc/seed/` 的 `sys_menu.permission_code` 为准。入口层仍以数据库判定为准，前端显隐只做
// 界面收敛。
export const PERM = {
  UserPage: 'sys:user',
  UserQuery: 'sys:user:query',
  UserCreate: 'sys:user:create',
  UserUpdate: 'sys:user:update',
  UserDelete: 'sys:user:delete',
  UserResetPwd: 'sys:user:reset-pwd',
  UserAssignRole: 'sys:user:assign-role',
  UserTransferOwner: 'sys:user:transfer-owner',

  DeptPage: 'sys:dept',
  DeptQuery: 'sys:dept:query',
  DeptCreate: 'sys:dept:create',
  DeptUpdate: 'sys:dept:update',
  DeptDelete: 'sys:dept:delete',

  UserGroupPage: 'sys:user-group',
  UserGroupQuery: 'sys:user-group:query',
  UserGroupCreate: 'sys:user-group:create',
  UserGroupUpdate: 'sys:user-group:update',
  UserGroupDelete: 'sys:user-group:delete',
  UserGroupAssignMember: 'sys:user-group:assign-member',

  RolePage: 'sys:role',
  RoleQuery: 'sys:role:query',
  RoleCreate: 'sys:role:create',
  RoleUpdate: 'sys:role:update',
  RoleDelete: 'sys:role:delete',
  RoleSetDataScope: 'sys:role:set-data-scope',
  RoleSetMenus: 'sys:role:set-menus',

  MenuPage: 'sys:menu',
  MenuQuery: 'sys:menu:query',
  MenuCreate: 'sys:menu:create',
  MenuUpdate: 'sys:menu:update',
  MenuDelete: 'sys:menu:delete',

  DictPage: 'sys:dict',
  DictQuery: 'sys:dict:query',
  DictCreate: 'sys:dict:create',
  DictUpdate: 'sys:dict:update',
  DictDelete: 'sys:dict:delete',

  AuditPage: 'sys:audit',
  AuditOperationLog: 'sys:audit:operation-log',
  AuditLoginLog: 'sys:audit:login-log',

  SessionPage: 'sys:session',
  SessionQuery: 'sys:session:query',
  SessionRevoke: 'sys:session:revoke',

  ConfigPage: 'sys:config',
  ConfigGlobalQuery: 'sys:config:global:query',
  ConfigGlobalCreate: 'sys:config:global:create',
  ConfigGlobalUpdate: 'sys:config:global:update',
  ConfigGlobalDelete: 'sys:config:global:delete',
  ConfigTenantQuery: 'sys:config:tenant:query',
  ConfigTenantCreate: 'sys:config:tenant:create',
  ConfigTenantUpdate: 'sys:config:tenant:update',
  ConfigTenantDelete: 'sys:config:tenant:delete',
  ConfigFeatureFlagQuery: 'sys:config:feature-flag:query',
  ConfigFeatureFlagCreate: 'sys:config:feature-flag:create',
  ConfigFeatureFlagUpdate: 'sys:config:feature-flag:update',
  ConfigFeatureFlagDelete: 'sys:config:feature-flag:delete',
  ConfigTranslationQuery: 'sys:config:translation:query',
  ConfigTranslationCreate: 'sys:config:translation:create',
  ConfigTranslationUpdate: 'sys:config:translation:update',
  ConfigTranslationDelete: 'sys:config:translation:delete',

  TenantPage: 'sys:tenant',
  TenantQuery: 'sys:tenant:query',
  TenantCreate: 'sys:tenant:create',
  TenantUpdate: 'sys:tenant:update',
  TenantDelete: 'sys:tenant:delete',

  KnowledgePage: 'sys:knowledge',
  KnowledgeQuery: 'sys:knowledge:query',
  KnowledgeCreate: 'sys:knowledge:create',
  KnowledgeUpdate: 'sys:knowledge:update',
  KnowledgeDelete: 'sys:knowledge:delete',

  ModelPage: 'sys:model',
  ModelQuery: 'sys:model:query',
  ModelCreate: 'sys:model:create',
  ModelUpdate: 'sys:model:update',
  ModelDelete: 'sys:model:delete',
  ModelPlatformCreate: 'sys:model:platform:create',
  ModelPlatformUpdate: 'sys:model:platform:update',
  ModelPlatformDelete: 'sys:model:platform:delete',

  AgentModelCallPage: 'agent:model-call',
  AgentModelCallQuery: 'agent:model-call:query',
} as const

export type PermissionCode = (typeof PERM)[keyof typeof PERM]
