package agent

import (
	"errors"
	"log/slog"
	"time"

	"grape/internal/biz"
	"grape/internal/conf"

	"github.com/google/wire"
	"grape/internal/agent/tools"
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

// NewSessionService 提供 Agent 会话存储：框架 session/postgres，schema
// agent + agent_ 表前缀，表由部署面预建并授权 grape_u，运行期跳过 DDL。
// 连接复用 master DSN（框架实现不接受外部连接池）；state 与 track 事件
// 的 TTL 0 值语义不同，见 README「会话与运行」。
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
// 强制应用层适配器）：契约实现与启动空间探针在 data 层装配，本提供器
// 只做形状转换（实现在 tools 包 NewVectorStoreAdapter），经 NewStack 进入
// 骨架，供检索工具消费。
func NewVectorStore(store biz.VectorStore) vectorstore.VectorStore {
	return tools.NewVectorStoreAdapter(store)
}

// NewStack 构造 Agent 骨架装配物，server 层经 service 取其 Handler 唯一
// 挂载：业务工具经用户用例访问数据，许可源经授权用例落 agent_tool_grant，
// 检索与列库工具共用检索用例（可见库集合与检索条件在 biz 收口），观测与
// 审计经注入契约接入；cleanup 释放框架资源。测试注入替身走
// NewStackWithOptions。
func NewStack(c *conf.Agent, logger *slog.Logger, sessions session.Service, vectors vectorstore.VectorStore, users *biz.UserUsecase, search *biz.KnowledgeSearchUsecase, grants *biz.GrantUsecase, catalog biz.TextModelCatalog, observer biz.AgentRunObserver, audit *biz.AuditUsecase) (*Stack, func(), error) {
	st, err := NewStackWithOptions(c, logger, sessions, vectors, users, search, grants,
		WithAuditHook(audit), WithRunObserver(observer), WithTextModelCatalog(catalog))
	if err != nil {
		return nil, nil, err
	}
	return st, st.Close, nil
}
