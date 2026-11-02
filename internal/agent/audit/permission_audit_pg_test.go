package audit_test

// 授权终态观察转写器的 PG 集成测试：以真实 PostgreSQL（本套件专属库
// grape_agent_permission_test）验证 allow 层级工具调用经授权终态观察
// 挂点落一条 auto 授权行——真实 biz.AuditUsecase + audit 仓储写入
// agent_confirmation_log，RLS 租户边界生效。环境变量门控（未设置时
// 跳过），形态与会话/确认集成测试一致（DeployFiles 全清单部署 + 跨包
// 部署锁互斥）：
//
//	GRAPE_PERMISSION_TEST_ADMIN_DSN=postgres://postgresql:root@127.0.0.1:5432/postgres \
//	go test ./internal/agent/audit -run TestPGPermissionAudit -count=1 -v
//
// 外部测试包（audit_test）形态：用例经完整 Stack（根包生产装配路径）
// 驱动，不触达本包私有符号。

import (
	"context"
	"testing"

	agent "grape/internal/agent"
	"grape/internal/agent/internal/testsupport"
	"grape/internal/agent/tools"
	"grape/internal/biz"
	"grape/internal/conf"
	"grape/internal/data/pg_sqlc"
	"grape/pkg/redact"

	"github.com/jninng/trpc_agent_go_ack/acktest"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// permissionSuite 是本套件专属测试库定位；其余测试库绝不触碰。
var permissionSuite = testsupport.SuiteDatabase{
	EnvVar:         "GRAPE_PERMISSION_TEST_ADMIN_DSN",
	Database:       "grape_agent_permission_test",
	GrapeUPassword: "grape_u_pwd",
	SkipMessage:    "GRAPE_PERMISSION_TEST_ADMIN_DSN not set, skip permission audit integration tests",
}

// permissionSetupDatabase 重建专属测试库并按 DeployFiles 全清单部署，
// 返回 grape_u DSN（形态与确认集成测试一致）。
func permissionSetupDatabase(t *testing.T) string {
	t.Helper()
	return testsupport.SetupSuiteDatabase(t, permissionSuite)
}

// permissionSessionService 经生产装配函数构造会话服务（postgres 后端）。
func permissionSessionService(t *testing.T, grapeUDSN string) (session.Service, func()) {
	t.Helper()
	c := &conf.Agent{AppName: testsupport.AppName}
	d := &conf.Data{Database: &conf.Data_Database{Master: &conf.Data_Database_Slot{Source: grapeUDSN}}}
	sessionSvc, cleanup, err := agent.NewSessionService(c, d)
	if err != nil {
		t.Fatalf("build session service: %v", err)
	}
	return sessionSvc, cleanup
}

// TestPGPermissionAudit_when_allow_tier_tool_runs_records_auto_row 验证
// allow 层级（get_current_user）的授权行落库：真实 biz.AuditUsecase 经
// 观察挂点把自动放行终态写入 agent_confirmation_log——decision=auto、
// path=auto、confirmation_id 即 tool_call_id、decided_by 为属主 userKey；
// 与同次执行的工具调用行同租户可查（RLS 生效，跨租户不可见）。
func TestPGPermissionAudit_when_allow_tier_tool_runs_records_auto_row(t *testing.T) {
	dsn := permissionSetupDatabase(t)
	const (
		threadID = "thread-permission-auto"
		runID    = "run-permission-auto"
		callID   = "call-permission-auto"
	)
	repo := testsupport.NewFakeUserRepo(&biz.User{ID: testsupport.OwnerID, Status: biz.UserStatusEnabled,
		Name: "alice", Nickname: "Alice", Locale: "zh-CN"})

	svc, cleanupSvc := permissionSessionService(t, dsn)
	defer cleanupSvc()
	// 审计面走生产装配：真实数据层（专属测试库）+ 标准脱敏注册表。
	// system 槽与 master 同源（NewData 装配期校验两槽，与生产 conf 形态
	// 一致——过渡期两槽指向同一实例）。
	data, cleanupData, err := pg_sqlc.NewData(&conf.Data{Database: &conf.Data_Database{
		Master: &conf.Data_Database_Slot{Source: dsn},
		System: &conf.Data_Database_Slot{Source: dsn},
	}})
	if err != nil {
		t.Fatalf("build data: %v", err)
	}
	defer cleanupData()
	hook := biz.NewAuditUsecase(pg_sqlc.NewAuditRepo(data), redact.NewStandardRegistry())

	llm := acktest.NewScriptedModel("permission-audit-model",
		acktest.Step{ToolCalls: []acktest.ToolCallSpec{{ID: callID, Name: tools.GetCurrentUserToolName, Args: `{}`}}},
	)
	stack, err := agent.NewStackWithOptions(testsupport.StackConf(), nil, svc, agent.NewVectorStore(nil),
		biz.NewUserUsecase(repo), nil, &testsupport.SearchTestEmbedder{Dims: 4},
		agent.WithModelOverride(llm), agent.WithAuditHook(hook))
	if err != nil {
		t.Fatalf("build stack: %v", err)
	}
	t.Cleanup(stack.Close)

	h := testsupport.IdentityHandler(stack.Handler(), testsupport.Tenant, testsupport.OwnerID)
	chat := testsupport.StartChat(h, threadID, runID, "who am i")
	testsupport.ChatEvents(t, chat)

	uc := biz.NewAuditUsecase(pg_sqlc.NewAuditRepo(data), redact.NewStandardRegistry())
	owner := biz.Identity{TenantID: testsupport.Tenant, UserID: testsupport.OwnerID}
	list, err := uc.ListConfirmations(biz.WithIdentity(context.Background(), owner), &biz.AuditFilter{SessionID: threadID, Limit: 10})
	if err != nil {
		t.Fatalf("list confirmations: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("permission audit rows = %d, want 1", len(list.Items))
	}
	row := list.Items[0]
	if row.Decision != biz.ConfirmationDecisionAuto || row.Path != biz.ConfirmationPathAuto {
		t.Fatalf("decision/path = %s/%s, want auto/auto", row.Decision, row.Path)
	}
	if row.ConfirmationID != callID || row.ToolCallID != callID || row.ToolName != tools.GetCurrentUserToolName {
		t.Fatalf("row ids/name = %s/%s/%s, want %s/%s/%s",
			row.ConfirmationID, row.ToolCallID, row.ToolName, callID, callID, tools.GetCurrentUserToolName)
	}
	if row.SessionID != threadID {
		t.Fatalf("row sessionId = %q, want %q", row.SessionID, threadID)
	}
	if row.UserID != testsupport.OwnerID {
		t.Fatalf("row userId = %d, want %d (owner identity from run ctx)", row.UserID, testsupport.OwnerID)
	}
	if want := testsupport.OwnerUserKey(t); row.DecidedBy != want {
		t.Fatalf("row decidedBy = %q, want owner userKey %q", row.DecidedBy, want)
	}

	// 同次执行的工具调用行同租户可查：一次 allow 调用恰一条工具行 +
	// 一条授权行（观察挂点与执行终态 wrapper 是两个独立写入点）。
	tool, err := uc.GetToolCall(biz.WithIdentity(context.Background(), owner), callID)
	if err != nil {
		t.Fatalf("get tool call: %v", err)
	}
	if tool.Status != biz.ToolCallStatusSuccess || tool.ToolName != tools.GetCurrentUserToolName {
		t.Fatalf("tool row = %s/%s, want success/%s", tool.Status, tool.ToolName, tools.GetCurrentUserToolName)
	}

	// RLS 租户边界：其他租户查不到该授权行。
	other, err := uc.ListConfirmations(biz.WithIdentity(context.Background(),
		biz.Identity{TenantID: "t-perm-other", UserID: testsupport.OwnerID}), &biz.AuditFilter{SessionID: threadID, Limit: 10})
	if err != nil {
		t.Fatalf("list confirmations cross tenant: %v", err)
	}
	if len(other.Items) != 0 {
		t.Fatalf("cross tenant rows = %d, want 0 (RLS)", len(other.Items))
	}
}
