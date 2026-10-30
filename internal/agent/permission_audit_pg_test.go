package agent

// 授权终态观察转写器的 PG 集成测试：以真实 PostgreSQL（本套件专属库
// grape_agent_permission_test）验证 allow 层级工具调用经授权终态观察
// 挂点落一条 auto 授权行——真实 biz.AuditUsecase + audit 仓储写入
// agent_confirmation_log，RLS 租户边界生效。环境变量门控（未设置时
// 跳过），形态与会话/确认集成测试一致（DeployFiles 全清单部署 + 跨包
// 部署锁互斥）：
//
//	GRAPE_PERMISSION_TEST_ADMIN_DSN=postgres://postgresql:root@127.0.0.1:5432/postgres \
//	go test ./internal/agent -run TestPGPermissionAudit -count=1 -v

import (
	"context"
	"net/url"
	"os"
	"testing"

	"grape/internal/biz"
	"grape/internal/conf"
	"grape/internal/data/pg_sqlc"
	"grape/pkg/redact"

	"github.com/jackc/pgx/v5"
	"github.com/jninng/trpc_agent_go_ack/acktest"
)

// permissionTestDatabase 是本套件专属测试库；其余测试库绝不触碰。
const permissionTestDatabase = "grape_agent_permission_test"

// permissionSetupDatabase 重建专属测试库并按 DeployFiles 全清单部署，
// 返回 grape_u DSN（形态与确认集成测试一致）。
func permissionSetupDatabase(t *testing.T) string {
	t.Helper()
	adminDSN := os.Getenv("GRAPE_PERMISSION_TEST_ADMIN_DSN")
	if adminDSN == "" {
		t.Skip("GRAPE_PERMISSION_TEST_ADMIN_DSN not set, skip permission audit integration tests")
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
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+permissionTestDatabase+" WITH (FORCE)"); err != nil {
		t.Fatalf("drop test database: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+permissionTestDatabase); err != nil {
		t.Fatalf("create test database: %v", err)
	}

	deployDir, err := pg_sqlc.DeployDir()
	if err != nil {
		t.Fatalf("locate deploy dir: %v", err)
	}
	if err := pg_sqlc.ValidateDeployFiles(deployDir); err != nil {
		t.Fatalf("validate deploy files: %v", err)
	}
	deployCfg := *cfg
	deployCfg.Database = permissionTestDatabase
	deployCfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	deployConn, err := pgx.ConnectConfig(ctx, &deployCfg)
	if err != nil {
		t.Fatalf("connect deploy: %v", err)
	}
	defer deployConn.Close(ctx)
	// role.sql 的角色目录操作是集群级的，跨测试包并发部署必须互斥。
	release, err := pg_sqlc.AcquireDeployLock(ctx)
	if err != nil {
		t.Fatalf("acquire deploy lock: %v", err)
	}
	defer release()
	for _, name := range pg_sqlc.DeployFiles {
		sql, err := os.ReadFile(deployDir + "/" + name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		deployed := pg_sqlc.ResolveDeployPasswords(string(sql), confirmGrapeUPassword, "test_u_pwd")
		if _, err := deployConn.Exec(ctx, deployed); err != nil {
			t.Fatalf("deploy %s: %v", name, err)
		}
	}
	if _, err := deployConn.Exec(ctx,
		"GRANT CONNECT ON DATABASE "+permissionTestDatabase+" TO grape_u, test_u"); err != nil {
		t.Fatalf("grant connect: %v", err)
	}

	u, err := url.Parse(adminDSN)
	if err != nil {
		t.Fatalf("parse admin dsn: %v", err)
	}
	u.User = url.UserPassword("grape_u", confirmGrapeUPassword)
	u.Path = "/" + permissionTestDatabase
	return u.String()
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
	repo := newFakeUserRepo(&biz.User{ID: confirmTestOwnerID, Status: biz.UserStatusEnabled,
		Name: "alice", Nickname: "Alice", Locale: "zh-CN"})

	svc, cleanupSvc := confirmSessionService(t, dsn)
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
		acktest.Step{ToolCalls: []acktest.ToolCallSpec{{ID: callID, Name: getCurrentUserToolName, Args: `{}`}}},
	)
	stack, err := NewStackWithOptions(confirmStackConf(), nil, svc, NewVectorStore(nil),
		biz.NewUserUsecase(repo), nil, &searchTestEmbedder{dims: 4},
		WithModelOverride(llm), WithAuditHook(hook))
	if err != nil {
		t.Fatalf("build stack: %v", err)
	}
	t.Cleanup(stack.Close)

	h := identityHandler(stack.Handler(), confirmTestTenant, confirmTestOwnerID)
	chat := startChat(h, threadID, runID, "who am i")
	chatEvents(t, chat)

	uc := biz.NewAuditUsecase(pg_sqlc.NewAuditRepo(data), redact.NewStandardRegistry())
	owner := biz.Identity{TenantID: confirmTestTenant, UserID: confirmTestOwnerID}
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
	if row.ConfirmationID != callID || row.ToolCallID != callID || row.ToolName != getCurrentUserToolName {
		t.Fatalf("row ids/name = %s/%s/%s, want %s/%s/%s",
			row.ConfirmationID, row.ToolCallID, row.ToolName, callID, callID, getCurrentUserToolName)
	}
	if row.SessionID != threadID {
		t.Fatalf("row sessionId = %q, want %q", row.SessionID, threadID)
	}
	if row.UserID != confirmTestOwnerID {
		t.Fatalf("row userId = %d, want %d (owner identity from run ctx)", row.UserID, confirmTestOwnerID)
	}
	if want := ownerUserKey(t); row.DecidedBy != want {
		t.Fatalf("row decidedBy = %q, want owner userKey %q", row.DecidedBy, want)
	}

	// 同次执行的工具调用行同租户可查：一次 allow 调用恰一条工具行 +
	// 一条授权行（观察挂点与执行终态 wrapper 是两个独立写入点）。
	tool, err := uc.GetToolCall(biz.WithIdentity(context.Background(), owner), callID)
	if err != nil {
		t.Fatalf("get tool call: %v", err)
	}
	if tool.Status != biz.ToolCallStatusSuccess || tool.ToolName != getCurrentUserToolName {
		t.Fatalf("tool row = %s/%s, want success/%s", tool.Status, tool.ToolName, getCurrentUserToolName)
	}

	// RLS 租户边界：其他租户查不到该授权行。
	other, err := uc.ListConfirmations(biz.WithIdentity(context.Background(),
		biz.Identity{TenantID: "t-perm-other", UserID: confirmTestOwnerID}), &biz.AuditFilter{SessionID: threadID, Limit: 10})
	if err != nil {
		t.Fatalf("list confirmations cross tenant: %v", err)
	}
	if len(other.Items) != 0 {
		t.Fatalf("cross tenant rows = %d, want 0 (RLS)", len(other.Items))
	}
}
