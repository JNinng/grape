package agent

import (
	"log/slog"

	"grape/internal/biz"
	"grape/internal/conf"

	"github.com/google/wire"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

// ProviderSet 是 agent 框架适配层的 Wire 依赖注入集合。
var ProviderSet = wire.NewSet(NewStack, NewSessionService, NewVectorStore)

// NewSessionService 提供 Agent 会话存储。B1 骨架用框架 inmemory 实现
// 占位（进程内、重启即失）；B2 按 TODO 决策 1 换 session/postgres
// （独立 schema agent + agent_ 表前缀），本提供器是唯一替换点。
func NewSessionService() (session.Service, func(), error) {
	svc := inmemory.NewSessionService()
	return svc, func() { _ = svc.Close() }, nil
}

// NewVectorStore 把 biz.VectorStore 契约适配为框架向量存储（决策 6 的
// 强制应用层适配器）：契约实现与启动维度探针在 data 层装配，本提供器
// 只做形状转换，经 NewStack 进入骨架，供检索工具（D2）消费。
func NewVectorStore(store biz.VectorStore) vectorstore.VectorStore {
	return &vectorStoreAdapter{store: store}
}

// NewStack 构造 Agent 骨架装配物，server 层经 service 取其 Handler
// 唯一挂载；cleanup 释放 core runner 等框架资源。测试经
// NewStackWithOptions 注入替身。
func NewStack(c *conf.Agent, logger *slog.Logger, sessions session.Service, vectors vectorstore.VectorStore) (*Stack, func(), error) {
	st, err := NewStackWithOptions(c, logger, sessions, vectors)
	if err != nil {
		return nil, nil, err
	}
	return st, st.Close, nil
}
