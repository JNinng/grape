// Package service 是传输层适配器层：在边界完成 DTO ↔ DO 与动态查询协议
// 转换后委托 biz 用例，不实现业务规则；受保护端点的身份只从认证上下文取。
package service

import "github.com/google/wire"

// ProviderSet 是 service 层的 Wire 依赖注入集合。
var ProviderSet = wire.NewSet(NewUserService, NewDeptService, NewUserGroupService, NewRoleService, NewMenuService, NewPermissionService, NewDictService, NewConfigService, NewTenantConfigService, NewFeatureFlagService, NewTranslationService, NewTenantService, NewAuditService, NewSessionAdminService, NewSessionService, NewQueryService, NewAgentService, NewAgentSessionsService, NewAgentEventWindowService, NewAgentAuditService, NewAgentRetrievalService, NewAgentModelCallService, NewKnowledgeService, NewModelService)
