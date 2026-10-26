package agent

import (
	"log/slog"

	"grape/internal/conf"

	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"github.com/google/wire"
)

// ProviderSet 是 agent 框架适配层的 Wire 依赖注入集合。
var ProviderSet = wire.NewSet(NewStack, NewSessionService)

// NewSessionService 提供 Agent 会话存储。B1 骨架用框架 inmemory 实现
// 占位（进程内、重启即失）；B2 按 TODO 决策 1 换 session/postgres
//（独立 schema agent + agent_ 表前缀），本提供器是唯一替换点。
func NewSessionService() (session.Service, func(), error) {
	svc := inmemory.NewSessionService()
	return svc, func() { _ = svc.Close() }, nil
}

// NewStack 构造 Agent 骨架装配物，server 层经 service 取其 Handler
// 唯一挂载；测试经 NewStackWithOptions 注入替身。
func NewStack(c *conf.Agent, logger *slog.Logger, sessions session.Service) (*Stack, error) {
	return NewStackWithOptions(c, logger, sessions)
}
