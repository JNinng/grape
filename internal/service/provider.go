package service

import "github.com/google/wire"

// ProviderSet 是 service 层的 Wire 依赖注入集合。
var ProviderSet = wire.NewSet(NewUserService, NewDeptService, NewUserGroupService, NewRoleService, NewMenuService, NewPermissionService, NewSessionService, NewAgentService, NewAgentSessionsService, NewAgentEventWindowService, NewAgentAuditService, NewAgentRetrievalService)
