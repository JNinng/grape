package agent

// B2 会话存储集成测试：以真实 PostgreSQL 验证框架 session/postgres 接入
// 的存储边界与 AG-UI 会话能力。使用本套件专属库 grape_agent_b2_test，
// 不触碰其他套件的测试库。设置以下环境变量后运行，未设置时跳过：
//
//	GRAPE_B2_TEST_ADMIN_DSN=postgres://postgresql:root@127.0.0.1:5432/postgres \
//	go test ./internal/agent -run TestPGSession -count=1 -v
//
// 装载取舍：按 DeployFiles 全清单部署（清单已尾随 agent_session.sql），
// 不裁剪最小子集。理由：role.sql 的授权/回收语句引用 public 业务表
// （sys_menu、sys_config 等），任何「最小子集」都随 role.sql 的授权
// 面漂移而脆断；agent schema 虽与 public 域零耦合，grape_u 角色却
// 依赖完整公域先行存在，全清单是唯一稳态，且回归面与 dbdeploy 一致。
//
// 分层说明：本测试文件引用 grape/internal/data/pg_sqlc 仅为复用部署
// 目录定位与口令占位符替换（部署清单的单一事实源），属测试夹具依赖，
// 不改变 agent 层的运行期 import 方向。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"grape/internal/biz"
	"grape/internal/conf"
	"grape/internal/data/pg_sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jninng/trpc_agent_go_ack/acktest"
	"google.golang.org/protobuf/types/known/durationpb"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// b2TestDatabase 是本套件专属测试库；其余测试库绝不触碰。
const b2TestDatabase = "grape_agent_b2_test"

// b2GrapeUPassword 与 role.sql 占位符替换值：grape_u 的既定测试口令。
const b2GrapeUPassword = "grape_u_pwd"

// 全栈测试共用的会话键要素：app 名、租户与用户（userKey = t-1:7）。
const (
	b2AppName = "grape-agent"
	b2Tenant  = "t-1"
	b2UserID  = int64(7)
)

// b2SetupDatabase 重建专属测试库并按 DeployFiles 全清单部署（清单尾部
// 即 agent_session.sql），返回 grape_u DSN。
func b2SetupDatabase(t *testing.T) string {
	t.Helper()
	adminDSN := os.Getenv("GRAPE_B2_TEST_ADMIN_DSN")
	if adminDSN == "" {
		t.Skip("GRAPE_B2_TEST_ADMIN_DSN not set, skip agent session storage integration tests")
	}
	ctx := context.Background()

	cfg, err := pgx.ParseConfig(adminDSN)
	if err != nil {
		t.Fatalf("parse admin dsn: %v", err)
	}
	cfg.Database = "postgres"
	admin, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+b2TestDatabase+" WITH (FORCE)"); err != nil {
		t.Fatalf("drop test database: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+b2TestDatabase); err != nil {
		t.Fatalf("create test database: %v", err)
	}

	// 全清单装载：DeployFiles（与 dbdeploy 共用单一事实源）尾随
	// agent_session.sql。文件含多语句与 DO 块，走 simple protocol。
	deployDir, err := pg_sqlc.DeployDir()
	if err != nil {
		t.Fatalf("locate deploy dir: %v", err)
	}
	if err := pg_sqlc.ValidateDeployFiles(deployDir); err != nil {
		t.Fatalf("validate deploy files: %v", err)
	}
	deployCfg := *cfg
	deployCfg.Database = b2TestDatabase
	deployCfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	deployConn, err := pgx.ConnectConfig(ctx, &deployCfg)
	if err != nil {
		t.Fatalf("connect deploy: %v", err)
	}
	defer deployConn.Close(ctx)
	for _, name := range pg_sqlc.DeployFiles {
		sql, err := os.ReadFile(deployDir + "/" + name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		deployed := pg_sqlc.ResolveDeployPasswords(string(sql), b2GrapeUPassword, "test_u_pwd")
		if _, err := deployConn.Exec(ctx, deployed); err != nil {
			t.Fatalf("deploy %s: %v", name, err)
		}
	}
	// CONNECT 授权依赖库名，由部署方执行（与 deploy_db.md 口径一致）。
	if _, err := deployConn.Exec(ctx, "GRANT CONNECT ON DATABASE "+b2TestDatabase+" TO grape_u, test_u"); err != nil {
		t.Fatalf("grant connect: %v", err)
	}

	dsn, err := b2GrapeUDSN(adminDSN)
	if err != nil {
		t.Fatalf("build grape_u dsn: %v", err)
	}
	return dsn
}

// b2GrapeUDSN 把管理员 DSN 改写为 grape_u 连接专属测试库的 DSN。
func b2GrapeUDSN(adminDSN string) (string, error) {
	u, err := url.Parse(adminDSN)
	if err != nil {
		return "", err
	}
	u.User = url.UserPassword("grape_u", b2GrapeUPassword)
	u.Path = "/" + b2TestDatabase
	return u.String(), nil
}

// b2SessionService 经生产装配函数构造会话服务（postgres 后端）。
func b2SessionService(t *testing.T, grapeUDSN string, sessionTTL time.Duration) (session.Service, func()) {
	t.Helper()
	c := &conf.Agent{
		AppName: b2AppName,
		SessionStorage: &conf.Agent_SessionStorage{
			SessionTtl: durationpb.New(sessionTTL),
		},
	}
	d := &conf.Data{Database: &conf.Data_Database{Master: &conf.Data_Database_Slot{Source: grapeUDSN}}}
	svc, cleanup, err := NewSessionService(c, d)
	if err != nil {
		t.Fatalf("build session service: %v", err)
	}
	return svc, cleanup
}

// b2Key 构造 userKey = tenant_id:user_id 的会话键。
func b2Key(sessionID string) session.Key {
	return session.Key{AppName: b2AppName, UserID: "t-1:7", SessionID: sessionID}
}

// b2UserTextEvent 构造带原文的用户消息事件（会话事件流的原料）。
func b2UserTextEvent(content string) *event.Event {
	evt := event.New("b2-invocation", "user")
	evt.Response = &model.Response{
		Object: model.ObjectTypeChatCompletion,
		Done:   true,
		Choices: []model.Choice{{
			Index:   0,
			Message: model.Message{Role: model.RoleUser, Content: content},
		}},
	}
	return evt
}

// TestPGSession_when_service_rebuilt_same_userkey_sees_persisted_events
// 验证跨请求持久与重启语义：同一 userKey 下写入的会话与事件，在服务
// 关闭、连接池重建（模拟进程重启）后仍完整可读，事件原文逐字保留。
func TestPGSession_when_service_rebuilt_same_userkey_sees_persisted_events(t *testing.T) {
	dsn := b2SetupDatabase(t)
	ctx := context.Background()
	const rawText = "b2-raw-payload-persistent-marker"
	const sessID = "sess-persist"

	svc, cleanup := b2SessionService(t, dsn, 0)
	sess, err := svc.CreateSession(ctx, b2Key(sessID), nil)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := svc.AppendEvent(ctx, sess, b2UserTextEvent(rawText)); err != nil {
		t.Fatalf("append event: %v", err)
	}
	cleanup()

	// 重建服务（新连接池）：等价于进程重启后的首查。
	svc2, cleanup2 := b2SessionService(t, dsn, 0)
	defer cleanup2()
	got, err := svc2.GetSession(ctx, b2Key(sessID))
	if err != nil {
		t.Fatalf("get session after rebuild: %v", err)
	}
	if got == nil || got.ID != sessID {
		t.Fatalf("session after rebuild = %+v, want id %s", got, sessID)
	}
	var found string
	for _, evt := range got.Events {
		for _, choice := range evt.Response.Choices {
			found += choice.Message.Content
		}
	}
	if !strings.Contains(found, rawText) {
		t.Fatalf("persisted events lost raw content %q, got %q", rawText, found)
	}
}

// TestPGSession_when_other_userkey_reads_finds_nothing 验证跨用户
// userKey 隔离：会话归属锚定在 userKey 编码上，同租户其他用户与跨
// 租户用户都读不到该会话。
func TestPGSession_when_other_userkey_reads_finds_nothing(t *testing.T) {
	dsn := b2SetupDatabase(t)
	ctx := context.Background()
	const sessID = "sess-isolated"

	svc, cleanup := b2SessionService(t, dsn, 0)
	defer cleanup()
	if _, err := svc.CreateSession(ctx, b2Key(sessID), nil); err != nil {
		t.Fatalf("create session: %v", err)
	}

	for _, other := range []session.Key{
		{AppName: b2AppName, UserID: "t-1:8", SessionID: sessID}, // 同租户其他用户
		{AppName: b2AppName, UserID: "t-2:7", SessionID: sessID}, // 跨租户同编号用户
	} {
		got, err := svc.GetSession(ctx, other)
		if err != nil {
			t.Fatalf("get session as %s: %v", other.UserID, err)
		}
		if got != nil {
			t.Fatalf("user %s must not read user t-1:7 session", other.UserID)
		}
		list, err := svc.ListSessions(ctx, session.UserKey{AppName: b2AppName, UserID: other.UserID})
		if err != nil {
			t.Fatalf("list sessions as %s: %v", other.UserID, err)
		}
		if len(list) != 0 {
			t.Fatalf("user %s listed %d foreign sessions, want 0", other.UserID, len(list))
		}
	}
}

// TestPGSession_when_ttl_configured_sets_expires_at 验证 TTL 配置生效
// 面：session_ttl 配置后写入行的 expires_at 落库（能设进框架即算，
// 不等过期），未配置时保持 NULL（永不过期）。
func TestPGSession_when_ttl_configured_sets_expires_at(t *testing.T) {
	dsn := b2SetupDatabase(t)
	ctx := context.Background()

	svc, cleanup := b2SessionService(t, dsn, time.Hour)
	defer cleanup()
	if _, err := svc.CreateSession(ctx, b2Key("sess-ttl"), nil); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// 对照组：未配置 TTL（0 = 永不过期）时 expires_at 保持 NULL。
	svc0, cleanup0 := b2SessionService(t, dsn, 0)
	defer cleanup0()
	if _, err := svc0.CreateSession(ctx, b2Key("sess-ttl-zero"), nil); err != nil {
		t.Fatalf("create zero-ttl session: %v", err)
	}

	pool, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect grape_u: %v", err)
	}
	defer pool.Close(ctx)
	var zeroExpires *time.Time
	if err := pool.QueryRow(ctx,
		"SELECT expires_at FROM agent.agent_session_states WHERE session_id = $1", "sess-ttl-zero",
	).Scan(&zeroExpires); err != nil {
		t.Fatalf("query zero-ttl expires_at: %v", err)
	}
	if zeroExpires != nil {
		t.Fatalf("zero-ttl expires_at = %v, want NULL", zeroExpires)
	}
	// 用 expires_at - created_at 的库内差值断言，规避 timestamp 无时区
	// 列在写入/读取两侧的时区解释差（框架写本地墙钟，pgx 按 UTC 读回）。
	var seconds float64
	if err := pool.QueryRow(ctx,
		"SELECT EXTRACT(EPOCH FROM (expires_at - created_at)) FROM agent.agent_session_states WHERE session_id = $1",
		"sess-ttl",
	).Scan(&seconds); err != nil {
		if err == pgx.ErrNoRows {
			t.Fatal("ttl session row missing")
		}
		t.Fatalf("query ttl diff: %v", err)
	}
	if seconds != 3600 {
		t.Fatalf("expires_at - created_at = %vs, want 3600s (session_ttl=1h)", seconds)
	}
}

// capturingModel 包装脚本化模型，记录每次送入的模型请求：压缩断言的
// 观察点（投影副本改写了什么，session 事件流留了什么）。
type capturingModel struct {
	inner    model.Model
	mu       sync.Mutex
	requests []*model.Request
}

func (m *capturingModel) Info() model.Info { return m.inner.Info() }

func (m *capturingModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	m.mu.Lock()
	m.requests = append(m.requests, req)
	m.mu.Unlock()
	return m.inner.GenerateContent(ctx, req)
}

func (m *capturingModel) snapshot() []*model.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*model.Request, len(m.requests))
	copy(out, m.requests)
	return out
}

// callScriptModel 按模型调用序数回放脚本：第 N 次 GenerateContent 返回
// 第 N 步。与 acktest.ScriptedModel（按请求历史中工具消息数推进）不同，
// 它不受历史内容影响，可精确编排跨运行的多轮工具调用；脚本耗尽后重复
// 最后一步。
type callScriptModel struct {
	mu    sync.Mutex
	steps []acktest.Step
	calls int
}

func newCallScriptModel(steps ...acktest.Step) *callScriptModel {
	return &callScriptModel{steps: steps}
}

func (m *callScriptModel) Info() model.Info { return model.Info{Name: "call-script"} }

func (m *callScriptModel) GenerateContent(_ context.Context, _ *model.Request) (<-chan *model.Response, error) {
	m.mu.Lock()
	idx := m.calls
	if idx >= len(m.steps) {
		idx = len(m.steps) - 1
	}
	step := m.steps[idx]
	m.calls++
	m.mu.Unlock()

	msg := model.Message{Role: model.RoleAssistant, Content: step.Text}
	finish := "stop"
	if len(step.ToolCalls) > 0 {
		finish = "tool_calls"
		for _, tc := range step.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, model.ToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: model.FunctionDefinitionParam{
					Name:      tc.Name,
					Arguments: []byte(tc.Args),
				},
			})
		}
	}
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		ID: "call-script", Object: model.ObjectTypeChatCompletion, Done: true,
		Choices: []model.Choice{{Index: 0, Message: msg, FinishReason: &finish}},
	}
	close(ch)
	return ch, nil
}

// blockingModel 阻塞直到运行 ctx 被取消：cancel 生效验证的替身模型。
type blockingModel struct {
	block time.Duration
}

func (*blockingModel) Info() model.Info { return model.Info{Name: "blocking"} }

func (m *blockingModel) GenerateContent(ctx context.Context, _ *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	go func() {
		defer close(ch)
		select {
		case <-ctx.Done():
		case <-time.After(m.block):
		}
	}()
	return ch, nil
}

// b2StackConf 组装全栈测试的骨架配置。
func b2StackConf(comp *conf.Agent_Compression) *conf.Agent {
	return &conf.Agent{
		AppName:     b2AppName,
		Llm:         &conf.Agent_LLM{Model: "stub"},
		Compression: comp,
		Agui: &conf.Agent_AGUI{
			BasePath:          "/agui",
			Timeout:           durationpb.New(30 * time.Second),
			HeartbeatInterval: durationpb.New(100 * time.Millisecond),
		},
	}
}

// b2IdentityHandler 用注入身份的 ctx 包装 Stack handler：等价认证
// 中间件命中后的形态（agent 层测试不经过 Kratos 挂载，链上治理由
// internal/server 套件守卫）。
func b2IdentityHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := biz.WithIdentity(r.Context(), biz.Identity{TenantID: b2Tenant, UserID: b2UserID})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// b2Post 执行一次带身份的 POST 并返回响应记录。
func b2Post(t *testing.T, h http.Handler, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// b2EventTypes 解析 SSE 响应体中的事件类型序列。
func b2EventTypes(t *testing.T, body string) []string {
	t.Helper()
	var types []string
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var evt struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &evt); err != nil {
			t.Fatalf("parse sse event %q: %v", line, err)
		}
		types = append(types, evt.Type)
	}
	return types
}

// b2HasEvent 断言事件序列包含指定类型。
func b2HasEvent(t *testing.T, types []string, want string) {
	t.Helper()
	for _, typ := range types {
		if typ == want {
			return
		}
	}
	t.Fatalf("event types %v do not contain %q", types, want)
}

// b2HasAnyEvent 断言事件序列包含任一给定类型。
func b2HasAnyEvent(t *testing.T, types []string, want ...string) {
	t.Helper()
	for _, typ := range types {
		for _, w := range want {
			if typ == w {
				return
			}
		}
	}
	t.Fatalf("event types %v contain none of %v", types, want)
}

// b2RunChat 走一次完整 AG-UI chat 并返回 SSE 响应体。
func b2RunChat(t *testing.T, h http.Handler, threadID, runID, message string) string {
	t.Helper()
	body := `{"threadId":"` + threadID + `","runId":"` + runID + `","messages":[{"id":"m-` + runID + `","role":"user","content":"` + message + `"}]}`
	rec := b2Post(t, h, "/agui/", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("chat status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// b2RunHistory 请求 /history 快照并返回 SSE 响应体。
func b2RunHistory(t *testing.T, h http.Handler, threadID string) string {
	t.Helper()
	rec := b2Post(t, h, "/agui/history", `{"threadId":"`+threadID+`","runId":"snap-1","messages":[]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("history status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// b2WaitHistoryPoll 轮询 /history 直到快照包含期望文本：track 事件在
// 运行收尾期持久化，chat 流关闭与其落库之间存在窄窗口，轮询消除竞态
// （同毫秒 follow 缺口见 TODO §1.2，此处只做快照形态验证）。
func b2WaitHistoryPoll(t *testing.T, h http.Handler, threadID, wantContains string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		body := b2RunHistory(t, h, threadID)
		if strings.Contains(body, wantContains) {
			return body
		}
		if time.Now().After(deadline) {
			t.Fatalf("history snapshot missing %q after polling:\n%s", wantContains, body)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestPGSession_when_compaction_enabled_projection_placeholders_and_events_raw
// 验证压缩投影与事件原文的分野：同一会话连续三次运行，第三次运行的
// 模型请求中，首次运行的工具结果（超出阈值且落在保护窗口外）被替换为
// 携带 event_id 的占位符；压缩只作用于请求投影副本，session 事件流与
// 库内事件表保留全部原文；按 event_id 经 GetEventWindow 可取回原文。
// 注意压缩单元按运行（请求）归属，单次运行内的历史属于当前单元受
// 保护，故必须跨运行构造历史。
func TestPGSession_when_compaction_enabled_projection_placeholders_and_events_raw(t *testing.T) {
	dsn := b2SetupDatabase(t)
	ctx := context.Background()
	const (
		sessID    = "sess-compact"
		markerOne = "B2-MARKER-ONE-oversized-tool-result"
		markerTwo = "B2-MARKER-TWO-second-tool-result"
	)

	svc, cleanupSvc := b2SessionService(t, dsn, 0)
	defer cleanupSvc()

	scripted := newCallScriptModel(
		acktest.Step{ToolCalls: []acktest.ToolCallSpec{{ID: "call-1", Name: "echo",
			Args: `{"text":"` + markerOne + ` ` + strings.Repeat("x", 512) + `"}`}}},
		acktest.Step{Text: "first done"},
		acktest.Step{ToolCalls: []acktest.ToolCallSpec{{ID: "call-2", Name: "echo",
			Args: `{"text":"` + markerTwo + `"}`}}},
		acktest.Step{Text: "second done"},
		acktest.Step{Text: "third done"},
	)
	llm := &capturingModel{inner: scripted}
	stack, err := NewStackWithOptions(b2StackConf(&conf.Agent_Compression{
		Enabled:             true,
		ToolResultMaxTokens: 1, // 阈值调到最小：任何非空结果都命中 Pass 1
	}), nil, svc, WithModelOverride(llm))
	if err != nil {
		t.Fatalf("build stack: %v", err)
	}
	defer stack.Close()
	h := b2IdentityHandler(stack.Handler())

	// 三次运行同会话：run-1 产出 markerOne、run-2 产出 markerTwo、
	// run-3 纯文本。投影断言取 run-3 的模型请求。
	b2HasEvent(t, b2EventTypes(t, b2RunChat(t, h, sessID, "run-1", "hi-one")), "RUN_FINISHED")
	b2HasEvent(t, b2EventTypes(t, b2RunChat(t, h, sessID, "run-2", "hi-two")), "RUN_FINISHED")
	b2HasEvent(t, b2EventTypes(t, b2RunChat(t, h, sessID, "run-3", "hi-three")), "RUN_FINISHED")

	// 事件流原文完整：两次工具结果都在 session 事件里（压缩只作用于
	// 请求投影副本）。
	got, err := svc.GetSession(ctx, b2Key(sessID))
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	var sessionRaw strings.Builder
	for _, evt := range got.Events {
		if evt.Response == nil {
			continue
		}
		for _, choice := range evt.Response.Choices {
			sessionRaw.WriteString(choice.Message.Content)
		}
	}
	if !strings.Contains(sessionRaw.String(), markerOne) || !strings.Contains(sessionRaw.String(), markerTwo) {
		t.Fatalf("session events lost raw tool results:\n%s", sessionRaw.String())
	}

	// 库内事件表原文完整（grape_u 直读，排除内存视图假象）。
	pool, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect grape_u: %v", err)
	}
	defer pool.Close(ctx)
	var eventCount int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM agent.agent_session_events WHERE session_id = $1 AND event::text LIKE $2",
		sessID, "%"+markerOne+"%",
	).Scan(&eventCount); err != nil {
		t.Fatalf("query raw events: %v", err)
	}
	if eventCount == 0 {
		t.Fatal("raw tool result missing from agent_session_events table")
	}

	// 会话归属锚定 userKey 编码而非框架默认 "user"。
	var userID string
	if err := pool.QueryRow(ctx,
		"SELECT user_id FROM agent.agent_session_states WHERE session_id = $1", sessID,
	).Scan(&userID); err != nil {
		t.Fatalf("query session owner: %v", err)
	}
	if userID != "t-1:7" {
		t.Fatalf("session owner user_id = %q, want t-1:7", userID)
	}

	// 投影观察：run-3 的模型请求（历史含前两次运行的工具结果）中，
	// run-1 的工具结果（请求单元落在保护窗口外）被占位符替换，原文不再
	// 出现；run-2 的工具结果（最近完成请求，受 keep_recent_requests=1
	// 保护）保持原样。
	requests := llm.snapshot()
	if len(requests) < 5 {
		t.Fatalf("model saw %d requests, want >= 5", len(requests))
	}
	final := requests[len(requests)-1]
	var projected strings.Builder
	for _, msg := range final.Messages {
		projected.WriteString(msg.Content)
	}
	if strings.Contains(projected.String(), markerOne) {
		t.Fatalf("oversized historical tool result not compacted in projection:\n%s", projected.String())
	}
	if !strings.Contains(projected.String(), "event_id:") {
		t.Fatalf("projection placeholder missing event_id reference:\n%s", projected.String())
	}
	if !strings.Contains(projected.String(), markerTwo) {
		t.Fatal("recent protected tool result must stay intact in projection")
	}

	// 按占位符携带的 event_id 取回原文（WindowService 路径）；占位符
	// 的引用行是 event_id: <uuid> 形式。
	ref := regexp.MustCompile(`event_id:\s*([A-Za-z0-9-]+)`).FindStringSubmatch(projected.String())
	if ref == nil {
		t.Fatalf("cannot parse event_id from projection:\n%s", projected.String())
	}
	window, err := svc.(session.WindowService).GetEventWindow(ctx, session.EventWindowRequest{
		Key:           b2Key(sessID),
		AnchorEventID: ref[1],
		Before:        0,
		After:         0,
	})
	if err != nil {
		t.Fatalf("get event window for %s: %v", ref[1], err)
	}
	var recovered strings.Builder
	for _, entry := range window.Entries {
		if entry.Event.Response == nil {
			continue
		}
		for _, choice := range entry.Event.Response.Choices {
			recovered.WriteString(choice.Message.Content)
		}
	}
	if !strings.Contains(recovered.String(), markerOne) {
		t.Fatalf("event window lost raw content for %s:\n%s", ref[1], recovered.String())
	}
}

// TestPGSession_when_history_requested_snapshot_replays_and_survives_restart
// 验证 /history 快照回放与重启持久：chat 运行结束后按 threadId 取快照
// 含完整对话；关闭服务并重建（模拟重启）后快照仍在。
func TestPGSession_when_history_requested_snapshot_replays_and_survives_restart(t *testing.T) {
	dsn := b2SetupDatabase(t)
	const (
		threadID = "thread-history"
		reply    = "history-replay-reply"
	)

	newStack := func(s session.Service) *Stack {
		st, err := NewStackWithOptions(b2StackConf(nil), nil, s,
			WithModelOverride(acktest.NewScriptedModel("stub", acktest.Step{Text: reply})))
		if err != nil {
			t.Fatalf("build stack: %v", err)
		}
		return st
	}

	svc, cleanupSvc := b2SessionService(t, dsn, 0)
	stack := newStack(svc)
	h := b2IdentityHandler(stack.Handler())
	b2RunChat(t, h, threadID, "run-hist", "hi")
	snapshot := b2WaitHistoryPoll(t, h, threadID, reply)
	b2HasEvent(t, b2EventTypes(t, snapshot), "MESSAGES_SNAPSHOT")
	stack.Close()
	cleanupSvc()

	// 重启：重建池与服务后，快照仍可回放且对话完整。
	svc2, cleanupSvc2 := b2SessionService(t, dsn, 0)
	defer cleanupSvc2()
	stack2 := newStack(svc2)
	defer stack2.Close()
	history2 := b2WaitHistoryPoll(t, b2IdentityHandler(stack2.Handler()), threadID, reply)
	b2HasEvent(t, b2EventTypes(t, history2), "MESSAGES_SNAPSHOT")
	if !strings.Contains(history2, "hi") {
		t.Fatalf("history snapshot lost user message after restart:\n%s", history2)
	}
}

// TestPGSession_when_cancel_requested_blocked_run_terminates 验证 cancel
// 生效：阻塞中的运行收到 /agui/cancel 后及时终止并落终态事件，不挂死
// 到模型超时（框架对显式取消以 RUN_FINISHED 结算，异常路径为
// RUN_ERROR，二者都证明运行已终止）。
func TestPGSession_when_cancel_requested_blocked_run_terminates(t *testing.T) {
	dsn := b2SetupDatabase(t)
	const threadID = "thread-cancel"

	svc, cleanupSvc := b2SessionService(t, dsn, 0)
	defer cleanupSvc()
	stack, err := NewStackWithOptions(b2StackConf(nil), nil, svc,
		WithModelOverride(&blockingModel{block: time.Minute}))
	if err != nil {
		t.Fatalf("build stack: %v", err)
	}
	defer stack.Close()
	h := b2IdentityHandler(stack.Handler())

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		body := `{"threadId":"` + threadID + `","runId":"run-1","messages":[{"id":"m-1","role":"user","content":"hi"}]}`
		req := httptest.NewRequest(http.MethodPost, "/agui/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		done <- rec
	}()
	// 等运行真正起来再取消。
	time.Sleep(300 * time.Millisecond)
	rec := b2Post(t, h, "/agui/cancel", `{"threadId":"`+threadID+`","runId":"run-1","messages":[]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}

	select {
	case chatRec := <-done:
		if chatRec.Code != http.StatusOK {
			t.Fatalf("chat status = %d, want 200\n%s", chatRec.Code, chatRec.Body.String())
		}
		b2HasAnyEvent(t, b2EventTypes(t, chatRec.Body.String()), "RUN_FINISHED", "RUN_ERROR")
	case <-time.After(10 * time.Second):
		t.Fatal("cancelled run did not terminate within 10s")
	}
}
