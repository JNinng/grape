package pg_sqlc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"grape/internal/biz"
	"grape/pkg/redact"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/trace"
)

// 审计仓储集成测试：以真实 PostgreSQL 验证租户 RLS、
// 写入-查询往返（含脱敏后内容）、组合批量与分页边界。测试自建专属库
// grape_audit_test 并重建（DROP+CREATE+按 DeployFiles 部署清单全量
// 装载，含 schema/95_agent_audit.sql），不触碰其他数据库。设置以下环境
// 变量后运行，未设置时跳过：
//
//	GRAPE_AUDIT_TEST_ADMIN_DSN=postgres://postgresql:root@127.0.0.1:5432/postgres \
//	GRAPE_AUDIT_TEST_DSN=postgres://grape_u:grape_u_pwd@127.0.0.1:5432/grape_audit_test \
//	go test ./internal/data/pg_sqlc -run TestAuditRepo -count=1 -v
//
// 隔离口径：两表启用租户 RLS（部署默认），租户边界由 RLS 强制、读侧
// WHERE 的同源租户谓词作应用层兜底——RLS 被关闭时跨租户仍不可见
//（对齐「隔离正确性不押在 RLS 是否存在上」验收口径）。

const auditTestDatabase = "grape_audit_test"

// auditTenantA/B 是用例身份：tenant_id 列宽 varchar(16)，取值保持在
// 列宽内。
var (
	auditTenantA = biz.Identity{TenantID: "t-audit-a", UserID: 31}
	auditTenantB = biz.Identity{TenantID: "t-audit-b", UserID: 32}
)

// auditTestTraceID 是用例的固定链路 ID（合法非零 16 字节）。
var auditTestTraceID = [16]byte{0xaa, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}

// auditIdentityContext 注入认证身份；withTrace 为真时附加 SpanContext
// （模拟链路中间件注入，审计的 trace_id 从这里来）。
func auditIdentityContext(id biz.Identity, withTrace bool) context.Context {
	ctx := biz.WithIdentity(context.Background(), id)
	if !withTrace {
		return ctx
	}
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    auditTestTraceID,
		SpanID:     [8]byte{9, 9, 9, 9, 9, 9, 9, 9},
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	return trace.ContextWithSpanContext(ctx, sc)
}

// setupAuditTestDB 重建专属测试库、按序装载部署清单（含
// schema/95_agent_audit.sql）并返回以 grape_u 连接的存储门面。
func setupAuditTestDB(t *testing.T) *Data {
	t.Helper()
	adminDSN := os.Getenv("GRAPE_AUDIT_TEST_ADMIN_DSN")
	testDSN := os.Getenv("GRAPE_AUDIT_TEST_DSN")
	if adminDSN == "" || testDSN == "" {
		t.Skip("GRAPE_AUDIT_TEST_ADMIN_DSN / GRAPE_AUDIT_TEST_DSN 未设置，跳过审计集成测试")
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
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+auditTestDatabase+" WITH (FORCE)"); err != nil {
		t.Fatalf("drop test database: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+auditTestDatabase); err != nil {
		t.Fatalf("create test database: %v", err)
	}
	if err := admin.Close(ctx); err != nil {
		t.Fatalf("close admin: %v", err)
	}

	deployCfg, err := pgx.ParseConfig(adminDSN)
	if err != nil {
		t.Fatalf("parse admin dsn: %v", err)
	}
	deployDir, err := DeployDir()
	if err != nil {
		t.Fatalf("locate deploy dir: %v", err)
	}
	if err := ValidateDeployFiles(deployDir); err != nil {
		t.Fatalf("validate deploy files: %v", err)
	}
	deployCfg.Database = auditTestDatabase
	deployCfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	deployConn, err := pgx.ConnectConfig(ctx, deployCfg)
	if err != nil {
		t.Fatalf("connect deploy: %v", err)
	}
	defer deployConn.Close(ctx)
	// role.sql 的角色目录操作是集群级的，跨测试包并发部署必须互斥。
	release, err := AcquireDeployLock(ctx)
	if err != nil {
		t.Fatalf("acquire deploy lock: %v", err)
	}
	defer release()
	for _, f := range DeployFiles {
		sql, err := os.ReadFile(filepath.Join(deployDir, f))
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		deployed := ResolveDeployPasswords(string(sql), testGrapeUPassword, testTestUPassword)
		if _, err := deployConn.Exec(ctx, deployed); err != nil {
			t.Fatalf("deploy %s: %v", f, err)
		}
	}
	if _, err := deployConn.Exec(ctx, "GRANT CONNECT ON DATABASE "+auditTestDatabase+" TO grape_u, test_u"); err != nil {
		t.Fatalf("grant connect: %v", err)
	}

	pool, err := pgxpool.New(ctx, testDSN)
	if err != nil {
		t.Fatalf("connect grape_u: %v", err)
	}
	t.Cleanup(pool.Close)
	return &Data{db: pool}
}

// newAuditUsecaseForTest 以标准脱敏注册表构造用例（与生产装配同默认值）。
func newAuditUsecaseForTest(d *Data) *biz.AuditUsecase {
	return biz.NewAuditUsecase(NewAuditRepo(d), redact.NewStandardRegistry())
}

// recordAuditPair 是往返用例的固定写入：一次组合批量落工具调用与授权
// 确认两行，参数含敏感键，结果超长。
func recordAuditPair(t *testing.T, uc *biz.AuditUsecase, sessionID string) {
	t.Helper()
	err := uc.RecordToolCallWithConfirmation(auditIdentityContext(auditTenantA, true),
		&biz.ToolCallRecordInput{
			SessionID:        sessionID,
			ToolCallID:       "call-" + sessionID,
			ToolName:         "create_ticket",
			ArgumentsSummary: `{"password":"hunter2","title":"grape audit"}`,
			ResultSummary:    strings.Repeat("r", 1200),
			RetryCount:       2,
			DurationMS:       120,
			Status:           biz.ToolCallStatusSuccess,
		},
		&biz.ConfirmationRecordInput{
			ConfirmationID: "call-" + sessionID,
			SessionID:      sessionID,
			ToolCallID:     "call-" + sessionID,
			ToolName:       "create_ticket",
			Decision:       biz.ConfirmationDecisionApproved,
			Remember:       biz.ConfirmationRememberSession,
			Path:           biz.ConfirmationPathLive,
			DecidedBy:      auditTenantA.TenantID + ":31",
		})
	if err != nil {
		t.Fatalf("record audit pair for %s: %v", sessionID, err)
	}
}

func TestAuditRepo_write_then_read_round_trip_redacts_summaries(t *testing.T) {
	d := setupAuditTestDB(t)
	uc := newAuditUsecaseForTest(d)
	recordAuditPair(t, uc, "sess-rt")

	ctx := auditIdentityContext(auditTenantA, true)
	tool, err := uc.GetToolCall(ctx, "call-sess-rt")
	if err != nil {
		t.Fatalf("get tool call: %v", err)
	}
	// 敏感键被掩码替换、未命中键保留；租户与身份来自服务端。
	if strings.Contains(tool.ArgumentsSummary, "hunter2") {
		t.Fatalf("arguments summary leaks sensitive value: %q", tool.ArgumentsSummary)
	}
	if !strings.Contains(tool.ArgumentsSummary, `"title":"grape audit"`) {
		t.Fatalf("arguments summary lost unfiltered key: %q", tool.ArgumentsSummary)
	}
	if tool.TenantID != auditTenantA.TenantID || tool.UserID != auditTenantA.UserID {
		t.Fatalf("identity = (%s, %d), want server-side identity", tool.TenantID, tool.UserID)
	}
	// 链路事实来自 SpanContext。
	if tool.TraceID != "aa0102030405060708090a0b0c0d0e0f" {
		t.Fatalf("trace id = %q, want span trace id", tool.TraceID)
	}
	// 结果摘要被 retrieval 类别截断（1200 rune → 512 + "...(truncated)"）。
	if got := len([]rune(tool.ResultSummary)); got != 512+len("...(truncated)") {
		t.Fatalf("result summary = %d runes, want 512 + marker", got)
	}
	if !strings.HasSuffix(tool.ResultSummary, "...(truncated)") {
		t.Fatal("result summary lacks truncation marker")
	}
	if tool.RetryCount != 2 || tool.DurationMS != 120 || tool.Status != biz.ToolCallStatusSuccess {
		t.Fatalf("tool call fields = (%d, %d, %s), want (2, 120, success)", tool.RetryCount, tool.DurationMS, tool.Status)
	}
	if tool.SessionID != "sess-rt" || tool.ToolName != "create_ticket" {
		t.Fatalf("session/tool = (%s, %s), want (sess-rt, create_ticket)", tool.SessionID, tool.ToolName)
	}

	conf, err := uc.GetConfirmation(ctx, "call-sess-rt")
	if err != nil {
		t.Fatalf("get confirmation: %v", err)
	}
	if conf.Decision != biz.ConfirmationDecisionApproved ||
		conf.Remember != biz.ConfirmationRememberSession ||
		conf.Path != biz.ConfirmationPathLive ||
		conf.DecidedBy != "t-audit-a:31" {
		t.Fatalf("confirmation fields = (%s, %s, %s, %s), want (approved, session, live, t-audit-a:31)",
			conf.Decision, conf.Remember, conf.Path, conf.DecidedBy)
	}
	if conf.ToolCallID != "call-sess-rt" || conf.TraceID != tool.TraceID {
		t.Fatalf("confirmation linkage = (%s, %s), want tool call id and same trace", conf.ToolCallID, conf.TraceID)
	}
}

func TestAuditRepo_cross_tenant_invisible_under_enabled_rls(t *testing.T) {
	d := setupAuditTestDB(t)
	uc := newAuditUsecaseForTest(d)

	// 固定场景前提：部署默认启用并强制 RLS。
	var rowSecurity, forceSecurity bool
	err := d.db.QueryRow(context.Background(),
		`SELECT relrowsecurity, relforcerowsecurity FROM pg_class WHERE oid = 'public.agent_tool_call_log'::regclass`).Scan(&rowSecurity, &forceSecurity)
	if err != nil || !rowSecurity || !forceSecurity {
		t.Fatalf("agent_tool_call_log RLS = (%v, %v, %v), want enabled+forced", rowSecurity, forceSecurity, err)
	}
	if err := d.db.QueryRow(context.Background(),
		`SELECT relrowsecurity, relforcerowsecurity FROM pg_class WHERE oid = 'public.agent_confirmation_log'::regclass`).Scan(&rowSecurity, &forceSecurity); err != nil || !rowSecurity || !forceSecurity {
		t.Fatalf("agent_confirmation_log RLS = (%v, %v, %v), want enabled+forced", rowSecurity, forceSecurity, err)
	}

	recordAuditPair(t, uc, "sess-iso")
	recordAuditPair(t, uc, "sess-iso-2")

	// 租户 B：列表与计数不可见，点查等同不存在。
	ctxB := auditIdentityContext(auditTenantB, false)
	tools, err := uc.ListToolCalls(ctxB, &biz.AuditFilter{Limit: 50})
	if err != nil || len(tools.Items) != 0 || tools.Total != 0 {
		t.Fatalf("cross-tenant tool list = (%d items, %d total, %v), want empty", len(tools.Items), tools.Total, err)
	}
	confs, err := uc.ListConfirmations(ctxB, nil)
	if err != nil || len(confs.Items) != 0 || confs.Total != 0 {
		t.Fatalf("cross-tenant confirmation list = (%d items, %d total, %v), want empty", len(confs.Items), confs.Total, err)
	}
	if _, err := uc.GetToolCall(ctxB, "call-sess-iso"); !errors.Is(err, biz.ErrAuditNotFound) {
		t.Fatalf("cross-tenant GetToolCall = %v, want ErrAuditNotFound", err)
	}
	if _, err := uc.GetConfirmation(ctxB, "call-sess-iso"); !errors.Is(err, biz.ErrAuditNotFound) {
		t.Fatalf("cross-tenant GetConfirmation = %v, want ErrAuditNotFound", err)
	}
	// 租户 A 不受越权尝试影响：两对记录仍然完整可见。
	own, err := uc.ListToolCalls(auditIdentityContext(auditTenantA, false), nil)
	if err != nil || own.Total != 2 || len(own.Items) != 2 {
		t.Fatalf("owner list after foreign attempts = (%d, %d, %v), want 2", own.Total, len(own.Items), err)
	}
}

func TestAuditRepo_cross_tenant_invisible_while_rls_disabled(t *testing.T) {
	d := setupAuditTestDB(t)
	uc := newAuditUsecaseForTest(d)
	recordAuditPair(t, uc, "sess-norls")

	// 关闭 RLS（模拟误操作/降级部署）：应用层兜底（读侧同源租户谓词）
	// 必须独立兜住隔离。
	adminCfg, err := pgx.ParseConfig(os.Getenv("GRAPE_AUDIT_TEST_ADMIN_DSN"))
	if err != nil {
		t.Fatalf("parse admin dsn: %v", err)
	}
	adminCfg.Database = auditTestDatabase
	admin, err := pgx.ConnectConfig(context.Background(), adminCfg)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	for _, stmt := range []string{
		"ALTER TABLE public.agent_tool_call_log NO FORCE ROW LEVEL SECURITY",
		"ALTER TABLE public.agent_tool_call_log DISABLE ROW LEVEL SECURITY",
		"ALTER TABLE public.agent_confirmation_log NO FORCE ROW LEVEL SECURITY",
		"ALTER TABLE public.agent_confirmation_log DISABLE ROW LEVEL SECURITY",
	} {
		if _, err := admin.Exec(context.Background(), stmt); err != nil {
			t.Fatalf("disable rls (%s): %v", stmt, err)
		}
	}
	if err := admin.Close(context.Background()); err != nil {
		t.Fatalf("close admin: %v", err)
	}

	ctxB := auditIdentityContext(auditTenantB, false)
	if _, err := uc.GetToolCall(ctxB, "call-sess-norls"); !errors.Is(err, biz.ErrAuditNotFound) {
		t.Fatalf("GetToolCall with RLS disabled = %v, want ErrAuditNotFound (app-layer predicate must hold)", err)
	}
	tools, err := uc.ListToolCalls(ctxB, nil)
	if err != nil || tools.Total != 0 {
		t.Fatalf("ListToolCalls with RLS disabled = (%d total, %v), want 0", tools.Total, err)
	}
	confs, err := uc.ListConfirmations(ctxB, nil)
	if err != nil || confs.Total != 0 {
		t.Fatalf("ListConfirmations with RLS disabled = (%d total, %v), want 0", confs.Total, err)
	}
	// 属主仍可见：兜底谓词不改变本租户语义。
	own, err := uc.ListToolCalls(auditIdentityContext(auditTenantA, false), nil)
	if err != nil || own.Total != 1 {
		t.Fatalf("owner list with RLS disabled = (%d, %v), want 1", own.Total, err)
	}
}

func TestAuditRepo_batch_write_is_atomic_and_partial_allowed(t *testing.T) {
	d := setupAuditTestDB(t)
	uc := newAuditUsecaseForTest(d)
	ctx := auditIdentityContext(auditTenantA, false)

	// 组合批量：两行都在。
	recordAuditPair(t, uc, "sess-batch")
	if _, err := uc.GetToolCall(ctx, "call-sess-batch"); err != nil {
		t.Fatalf("batch tool row missing: %v", err)
	}
	if _, err := uc.GetConfirmation(ctx, "call-sess-batch"); err != nil {
		t.Fatalf("batch confirmation row missing: %v", err)
	}

	// 单边批量（仅确认，recovery 路径无对应工具调用行）合法。
	err := uc.RecordToolCallWithConfirmation(ctx, nil, &biz.ConfirmationRecordInput{
		ConfirmationID: "cf-only", SessionID: "sess-batch", ToolName: "write",
		Decision: biz.ConfirmationDecisionDenied, Path: biz.ConfirmationPathRecovery,
		DecidedBy: "t-audit-a:31",
	})
	if err != nil {
		t.Fatalf("confirmation-only batch: %v", err)
	}
	if _, err := uc.GetConfirmation(ctx, "cf-only"); err != nil {
		t.Fatalf("confirmation-only row missing: %v", err)
	}

	// 双空批量拒绝。
	if err := uc.RecordToolCallWithConfirmation(ctx, nil, nil); !errors.Is(err, biz.ErrAuditInvalidArgument) {
		t.Fatalf("empty batch = %v, want ErrAuditInvalidArgument", err)
	}

	// 原子性负例：确认行撞唯一约束时，同批的工具行必须一并回滚。
	err = uc.RecordToolCallWithConfirmation(ctx, &biz.ToolCallRecordInput{
		ToolCallID: "call-batch-rollback", ToolName: "write", Status: biz.ToolCallStatusSuccess,
	}, &biz.ConfirmationRecordInput{
		ConfirmationID: "call-sess-batch", SessionID: "sess-batch", ToolName: "write",
		Decision: biz.ConfirmationDecisionApproved, Path: biz.ConfirmationPathLive,
		DecidedBy: "t-audit-a:31",
	})
	if err == nil {
		t.Fatal("batch with duplicate confirmation id should fail, got nil")
	}
	if _, err := uc.GetToolCall(ctx, "call-batch-rollback"); !errors.Is(err, biz.ErrAuditNotFound) {
		t.Fatalf("tool row should be rolled back with failed batch, got %v", err)
	}
}

func TestAuditRepo_pagination_orders_by_time_desc_and_filters_session(t *testing.T) {
	d := setupAuditTestDB(t)
	uc := newAuditUsecaseForTest(d)
	ctx := auditIdentityContext(auditTenantA, false)

	// 五条记录分属两个会话；同秒写入以 id tie-break，顺序断言只依赖
	// 会话过滤与 limit/offset，不依赖行内时间戳精度。
	for i, session := range []string{"p-1", "p-1", "p-1", "p-2", "p-2"} {
		if err := uc.RecordToolCall(ctx, &biz.ToolCallRecordInput{
			SessionID:  session,
			ToolCallID: fmt.Sprintf("call-p-%02d", i),
			ToolName:   "echo", Status: biz.ToolCallStatusSuccess,
		}); err != nil {
			t.Fatalf("seed tool call: %v", err)
		}
	}

	list, err := uc.ListToolCalls(ctx, &biz.AuditFilter{SessionID: "p-1", Limit: 2})
	if err != nil {
		t.Fatalf("list page: %v", err)
	}
	if list.Total != 3 || len(list.Items) != 2 {
		t.Fatalf("page = (%d total, %d items), want (3, 2)", list.Total, len(list.Items))
	}
	seen := map[string]bool{}
	for _, item := range list.Items {
		if item.SessionID != "p-1" {
			t.Fatalf("page leaks other session %q", item.SessionID)
		}
		if seen[item.ToolCallID] {
			t.Fatalf("page repeats %q (unstable ordering)", item.ToolCallID)
		}
		seen[item.ToolCallID] = true
	}
	// 第三页只剩一行；越界页为空且不报错。
	last, err := uc.ListToolCalls(ctx, &biz.AuditFilter{SessionID: "p-1", Limit: 2, Offset: 2})
	if err != nil || len(last.Items) != 1 {
		t.Fatalf("last page = (%d items, %v), want 1", len(last.Items), err)
	}
	beyond, err := uc.ListToolCalls(ctx, &biz.AuditFilter{Limit: 10, Offset: 100})
	if err != nil || len(beyond.Items) != 0 {
		t.Fatalf("beyond page = (%d items, %v), want empty", len(beyond.Items), err)
	}
	// 无过滤时五条全量可见（本租户）。
	all, err := uc.ListToolCalls(ctx, &biz.AuditFilter{Limit: 10})
	if err != nil || all.Total != 5 {
		t.Fatalf("unfiltered list = (%d, %v), want 5", all.Total, err)
	}
}

func TestAuditRepo_duplicate_records_rejected_by_unique_index(t *testing.T) {
	d := setupAuditTestDB(t)
	uc := newAuditUsecaseForTest(d)
	ctx := auditIdentityContext(auditTenantA, false)

	in := &biz.ToolCallRecordInput{ToolCallID: "call-dup", ToolName: "echo", Status: biz.ToolCallStatusSuccess}
	if err := uc.RecordToolCall(ctx, in); err != nil {
		t.Fatalf("first record: %v", err)
	}
	err := uc.RecordToolCall(ctx, in)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("duplicate record = %v, want unique violation (23505)", err)
	}

	conf := &biz.ConfirmationRecordInput{
		ConfirmationID: "call-dup", SessionID: "s", ToolName: "echo",
		Decision: biz.ConfirmationDecisionApproved, Path: biz.ConfirmationPathLive, DecidedBy: "t-audit-a:31",
	}
	if err := uc.RecordConfirmation(ctx, conf); err != nil {
		t.Fatalf("first confirmation: %v", err)
	}
	if err := uc.RecordConfirmation(ctx, conf); !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("duplicate confirmation = %v, want unique violation (23505)", err)
	}
}

func TestAuditRepo_unauthenticated_calls_fail_closed(t *testing.T) {
	d := setupAuditTestDB(t)
	repo := NewAuditRepo(d)
	uc := biz.NewAuditUsecase(repo, nil)
	ctx := context.Background()

	if err := uc.RecordToolCall(ctx, &biz.ToolCallRecordInput{ToolCallID: "c", ToolName: "echo", Status: biz.ToolCallStatusSuccess}); !errors.Is(err, biz.ErrSessionInvalid) {
		t.Fatalf("unauthenticated write = %v, want ErrSessionInvalid", err)
	}
	if _, err := uc.ListToolCalls(ctx, nil); !errors.Is(err, biz.ErrSessionInvalid) {
		t.Fatalf("unauthenticated list = %v, want ErrSessionInvalid", err)
	}
	if _, err := uc.GetToolCall(ctx, "c"); !errors.Is(err, biz.ErrSessionInvalid) {
		t.Fatalf("unauthenticated get = %v, want ErrSessionInvalid", err)
	}
	// 仓储层同样拒绝（用例之外的直接调用不越身份门）。
	if err := repo.AppendToolCall(ctx, &biz.ToolCallAudit{ToolCallID: "c", ToolName: "echo", Status: biz.ToolCallStatusSuccess}); err == nil {
		t.Fatal("repo write without identity = nil error, want rejection")
	}
	if _, err := repo.ListToolCalls(ctx, &biz.AuditFilter{Limit: 1}); err == nil {
		t.Fatal("repo list without identity = nil error, want rejection")
	}
}
