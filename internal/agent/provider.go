package agent

import (
	"errors"
	"log/slog"
	"time"

	"grape/internal/biz"
	"grape/internal/conf"

	"github.com/google/wire"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/postgres"
)

// sessionSchema / sessionTablePrefix 是会话存储的库内定位：
// 独立 schema agent + agent_ 表前缀，得到 agent.agent_session_states 等
// 六张框架表，与 public 业务表、登录会话 sys_session 严格区分。
const (
	sessionSchema      = "agent"
	sessionTablePrefix = "agent_"
)

// ProviderSet 是 agent 框架适配层的 Wire 依赖注入集合。
var ProviderSet = wire.NewSet(NewStack, NewSessionService, NewVectorStore)

// NewSessionService 提供 Agent 会话存储：框架 session/postgres，schema agent + agent_ 表前缀，表由部署面（sqlc/
// agent_session.sql）以超级用户预建并授权 grape_u，运行期
// WithSkipDBInit 跳过 DDL。
//
// 连接复用 grape 设施的口径：框架 postgres 实现不接受外部连接池、
// NewService 内部强制经自身 storage builder 建池，故这里复用的是
// data.database.master 的同一 DSN（同实例同库同角色 grape_u），而非
// 同一 pgxpool——运行期实际存在两条池（grape 业务池 + 框架会话池），
// 生命周期都收在各自 cleanup 内。
//
// TTL 口径：session/app/user state 的 0 = 不过期（框架默认），原样
// 传入；track 事件 TTL 的 0 在框架里是显式指针语义（禁用过期）而非
// 缺省（沿用 session_ttl），只在配置正值时传入。
func NewSessionService(c *conf.Agent, d *conf.Data) (session.Service, func(), error) {
	dsn := d.GetDatabase().GetMaster().GetSource()
	if dsn == "" {
		return nil, nil, errors.New("agent: data.database.master.source is required for session storage")
	}
	ss := c.GetSessionStorage()
	opts := []postgres.ServiceOpt{
		postgres.WithPostgresClientDSN(dsn),
		postgres.WithSchema(sessionSchema),
		postgres.WithTablePrefix(sessionTablePrefix),
		postgres.WithSkipDBInit(true),
		postgres.WithSessionTTL(ss.GetSessionTtl().AsDuration()),
		postgres.WithAppStateTTL(ss.GetAppStateTtl().AsDuration()),
		postgres.WithUserStateTTL(ss.GetUserStateTtl().AsDuration()),
	}
	if ttl, ok := trackEventTTLOf(ss); ok {
		opts = append(opts, postgres.WithTrackEventTTL(ttl))
	}
	svc, err := postgres.NewService(opts...)
	if err != nil {
		return nil, nil, err
	}
	return svc, func() {
		slog.Info("closing the agent session service")
		_ = svc.Close()
	}, nil
}

// trackEventTTLOf 翻译 track 事件 TTL 配置：0 值（未设置）不传选项，
// 沿用框架缺省——跟随 session_ttl；正值原样传入。框架里显式传入 0 的
// 语义是「禁用过期」（指针非 nil 即生效），与「未设置」不同，故不以
// 0 值接线。
func trackEventTTLOf(ss *conf.Agent_SessionStorage) (time.Duration, bool) {
	ttl := ss.GetTrackEventTtl().AsDuration()
	return ttl, ttl > 0
}

// NewMemorySessionService 提供进程内会话存储，仅供单测装配与本地体验：
// 进程重启即失、无跨请求持久；生产装配必须走 NewSessionService。
func NewMemorySessionService() (session.Service, func(), error) {
	svc := inmemory.NewSessionService()
	return svc, func() { _ = svc.Close() }, nil
}

// NewVectorStore 把 biz.VectorStore 契约适配为框架向量存储（向量层租户隔离的
// 强制应用层适配器）：契约实现与启动维度探针在 data 层装配，本提供器
// 只做形状转换，经 NewStack 进入骨架，供检索工具消费。
func NewVectorStore(store biz.VectorStore) vectorstore.VectorStore {
	return &vectorStoreAdapter{store: store}
}

// NewStack 构造 Agent 骨架装配物，server 层经 service 取其 Handler
// 唯一挂载；cleanup 释放 core runner 等框架资源。业务工具经注入的
// 用户用例访问数据，许可源经授权用例落 agent_tool_grant
// 表，检索工具经向量适配器与 embedder 访问向量；
// 运行面观测经注入的 observer 接 /metrics（ops 的 RuntimeMetrics 经
// wire.Bind 以 biz.AgentRunObserver 提供）；审计经注入的审计用例接
// AuditHook（工具执行终态与 ack 决定两个写入点，与复现视图查询端点
// 共用同一用例实例）；测试经 NewStackWithOptions 注入替身。
func NewStack(c *conf.Agent, logger *slog.Logger, sessions session.Service, vectors vectorstore.VectorStore, users *biz.UserUsecase, grants *biz.GrantUsecase, embedder biz.Embedder, observer biz.AgentRunObserver, audit *biz.AuditUsecase) (*Stack, func(), error) {
	st, err := NewStackWithOptions(c, logger, sessions, vectors, users, grants, embedder, WithAuditHook(audit), WithRunObserver(observer))
	if err != nil {
		return nil, nil, err
	}
	return st, st.Close, nil
}
