package pg_sqlc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"grape/internal/biz"

	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

// 审计日志与会话管理面集成测试（真实 PostgreSQL，未设 GRAPE_TEST_* DSN 时
// 跳过）：覆盖 sys_login_log / sys_operation_log 只读查询与 sys_session 管理
// 面 List（令牌哈希不出现、跨租户隔离）。setupTestDB 每次重建数据库；两表
// append-only，种子即全部事实。

// auditTenantSeeded 是种子日志所在的租户（test_seed.sql）。
const auditTenantSeeded = "t-test-01"

// auditLogQuery 构造按用户名精确过滤的动态查询（两表同名语义列）。
func auditLogQuery(username string) *dynq.Query {
	return &dynq.Query{
		Where: []dynq.Condition{{Field: "username", Op: "eq", Value: username}},
	}
}

func TestAuditLogRepoGetLoginLogByIDReturnsSeededRow(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	repo := NewAuditLogRepo(d)

	out, err := repo.GetLoginLogByID(context.Background(), dictAuth(auditTenantSeeded), 3)
	if err != nil {
		t.Fatalf("get login log 3: %v", err)
	}
	if out.Username != "lisi" || out.IsSuccess {
		t.Fatalf("row = (%s, success %v), want (lisi, false)", out.Username, out.IsSuccess)
	}
	if out.FailureReason == nil || *out.FailureReason == "" {
		t.Fatalf("failure_reason = %+v, want non-empty", out.FailureReason)
	}
	if out.LoginAt.IsZero() {
		t.Fatal("login_at must be populated")
	}
}

func TestAuditLogRepoGetLoginLogByIDAcrossTenantReturnsNotFound(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	repo := NewAuditLogRepo(d)

	// 种子日志 5 属 t-test-02（zhaoliu）。
	_, err := repo.GetLoginLogByID(context.Background(), dictAuth(auditTenantSeeded), 5)
	if !errors.Is(err, biz.ErrAuditLogNotFound) {
		t.Fatalf("err = %v, want ErrAuditLogNotFound", err)
	}
}

func TestAuditLogRepoGetLoginLogByIDMissingRowReturnsNotFound(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	repo := NewAuditLogRepo(d)

	_, err := repo.GetLoginLogByID(context.Background(), dictAuth(dictTenantSeeded), 999999)
	if !errors.Is(err, biz.ErrAuditLogNotFound) {
		t.Fatalf("err = %v, want ErrAuditLogNotFound", err)
	}
}

func TestAuditLogRepoListLoginLogsFiltersByUsernameAndIsolatesTenant(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	repo := NewAuditLogRepo(d)

	out, err := repo.ListLoginLogs(context.Background(), dictAuth(auditTenantSeeded), auditLogQuery("lisi"))
	if err != nil {
		t.Fatalf("list login logs: %v", err)
	}
	// 种子中 lisi 有两条（一败一成）。
	if len(out.Items) != 2 || out.Total == nil || *out.Total != 2 {
		t.Fatalf("items/total = %d/%+v, want 2/2", len(out.Items), out.Total)
	}

	empty, err := repo.ListLoginLogs(context.Background(), dictAuth(dictTenantEmpty), auditLogQuery("lisi"))
	if err != nil {
		t.Fatalf("list login logs in empty tenant: %v", err)
	}
	if len(empty.Items) != 0 {
		t.Fatalf("items = %+v, want empty across tenant", empty.Items)
	}
}

func TestAuditLogRepoListLoginLogsRejectsUnknownField(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	repo := NewAuditLogRepo(d)

	q := &dynq.Query{Where: []dynq.Condition{{Field: "noSuchField", Op: "eq", Value: "x"}}}
	_, err := repo.ListLoginLogs(context.Background(), dictAuth(dictTenantSeeded), q)
	if !errors.Is(err, biz.ErrAuditLogInvalidArgument) {
		t.Fatalf("err = %v, want ErrAuditLogInvalidArgument", err)
	}
}

func TestAuditLogRepoGetOperationLogByIDReturnsSnapshots(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	repo := NewAuditLogRepo(d)

	// 种子操作日志 2：UPDATE sys_role，old/new 快照均为 JSON 对象。
	out, err := repo.GetOperationLogByID(context.Background(), dictAuth(auditTenantSeeded), 2)
	if err != nil {
		t.Fatalf("get operation log 2: %v", err)
	}
	if out.OperationType != "UPDATE" || out.TargetTable == nil || *out.TargetTable != "sys_role" {
		t.Fatalf("row = (%s, target %+v), want (UPDATE, sys_role)", out.OperationType, out.TargetTable)
	}
	if string(out.OldValue) == "" || string(out.NewValue) == "" {
		t.Fatalf("snapshots = (%q, %q), want both populated", out.OldValue, out.NewValue)
	}
	if out.DurationMs == nil || *out.DurationMs != 30 {
		t.Fatalf("duration_ms = %+v, want 30", out.DurationMs)
	}
}

func TestAuditLogRepoGetOperationLogByIDAcrossTenantReturnsNotFound(t *testing.T) {
	t.Parallel()
	// 种子操作日志全部属 t-test-01：无种子租户点查等同不存在。
	d := setupTestDB(t)
	repo := NewAuditLogRepo(d)

	_, err := repo.GetOperationLogByID(context.Background(), dictAuth(dictTenantEmpty), 1)
	if !errors.Is(err, biz.ErrAuditLogNotFound) {
		t.Fatalf("err = %v, want ErrAuditLogNotFound", err)
	}
}

func TestAuditLogRepoGetOperationLogByIDNullSnapshotStaysNil(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	repo := NewAuditLogRepo(d)

	// 种子操作日志 1：old_value 为 NULL，DO 侧保持 nil（非空字节串）。
	out, err := repo.GetOperationLogByID(context.Background(), dictAuth(auditTenantSeeded), 1)
	if err != nil {
		t.Fatalf("get operation log 1: %v", err)
	}
	if out.OldValue != nil {
		t.Fatalf("old_value = %q, want nil for NULL snapshot", out.OldValue)
	}
	if out.NewValue == nil {
		t.Fatal("new_value must stay populated")
	}
}

func TestAuditLogRepoListOperationLogsCountsTotalIndependentOfPage(t *testing.T) {
	t.Parallel()
	// 种子操作日志共 5 条；第 2 页（页大小 3）的 total 不得随翻页位置递减。
	d := setupTestDB(t)
	repo := NewAuditLogRepo(d)
	page := 2
	pageSize := 3

	q := &dynq.Query{Pagination: &dynq.Pagination{Page: &page, PageSize: &pageSize}}
	out, err := repo.ListOperationLogs(context.Background(), dictAuth(auditTenantSeeded), q)
	if err != nil {
		t.Fatalf("list operation logs page 2: %v", err)
	}
	if len(out.Items) != 2 {
		t.Fatalf("page 2 items = %d, want 2", len(out.Items))
	}
	if out.Total == nil || *out.Total != 5 {
		t.Fatalf("total = %+v, want 5", out.Total)
	}
}

func TestAuditLogRepoListOperationLogsCursorPageKeepsTotalUnshifted(t *testing.T) {
	t.Parallel()
	// 游标模式下 total 与翻页位置无关：第二页（After 游标）的 total 仍为
	// 全集 5，而非剩余行数。
	d := setupTestDB(t)
	repo := NewAuditLogRepo(d)
	pageSize := 2
	includeTotal := true

	first := &dynq.Query{Pagination: &dynq.Pagination{PageSize: &pageSize, IncludeTotal: &includeTotal}}
	out, err := repo.ListOperationLogs(context.Background(), dictAuth(auditTenantSeeded), first)
	if err != nil {
		t.Fatalf("list first cursor page: %v", err)
	}
	if len(out.Items) != 2 || out.NextCursor == "" {
		t.Fatalf("first page = (%d items, cursor %q), want 2 items with a cursor", len(out.Items), out.NextCursor)
	}

	after := out.NextCursor
	second := &dynq.Query{Pagination: &dynq.Pagination{After: &after, PageSize: &pageSize, IncludeTotal: &includeTotal}}
	secondOut, err := repo.ListOperationLogs(context.Background(), dictAuth(auditTenantSeeded), second)
	if err != nil {
		t.Fatalf("list second cursor page: %v", err)
	}
	if len(secondOut.Items) != 2 {
		t.Fatalf("second page items = %d, want 2", len(secondOut.Items))
	}
	if secondOut.Total == nil || *secondOut.Total != 5 {
		t.Fatalf("second page total = %+v, want 5", secondOut.Total)
	}
}

func TestSessionAdminListCursorPageKeepsTotalUnshifted(t *testing.T) {
	t.Parallel()
	// 会话行经 newID() 发号，id 超出 JS 安全整数：本用例验证大主键游标
	// 翻页时 total 与翻页位置无关（dynq 以十进制字符串精确解码）。
	d := setupTestDB(t)
	repo := NewSessionRepo(d)
	auth := dictAuth(dictTenantSeeded)

	for i := 0; i < 3; i++ {
		if _, err := repo.Issue(context.Background(), auth, &biz.IssueSessionInput{
			AccessTokenHash:  fmt.Sprintf("cursor-total-access-%d", i),
			RefreshTokenHash: fmt.Sprintf("cursor-total-refresh-%d", i),
			AccessTTL:        30 * time.Minute,
			RefreshTTL:       7 * 24 * time.Hour,
		}); err != nil {
			t.Fatalf("issue session %d: %v", i, err)
		}
	}

	pageSize := 2
	includeTotal := true
	first := &dynq.Query{Pagination: &dynq.Pagination{PageSize: &pageSize, IncludeTotal: &includeTotal}}
	out, err := repo.List(context.Background(), auth, first)
	if err != nil {
		t.Fatalf("list first cursor page: %v", err)
	}
	if len(out.Items) != 2 || out.NextCursor == "" {
		t.Fatalf("first page = (%d items, cursor %q), want 2 items with a cursor", len(out.Items), out.NextCursor)
	}

	after := out.NextCursor
	second := &dynq.Query{Pagination: &dynq.Pagination{After: &after, PageSize: &pageSize, IncludeTotal: &includeTotal}}
	secondOut, err := repo.List(context.Background(), auth, second)
	if err != nil {
		t.Fatalf("list second cursor page: %v", err)
	}
	if len(secondOut.Items) != 1 {
		t.Fatalf("second page items = %d, want 1", len(secondOut.Items))
	}
	if secondOut.Total == nil || *secondOut.Total != 3 {
		t.Fatalf("second page total = %+v, want 3", secondOut.Total)
	}
}

func TestSessionAdminListReturnsTheJustIssuedSessionFirst(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	repo := NewSessionRepo(d)
	auth := dictAuth(dictTenantSeeded)
	now := time.Now()

	issued, err := repo.Issue(context.Background(), auth, &biz.IssueSessionInput{
		AccessTokenHash:  "aaaa-bbb-access-hash-1",
		RefreshTokenHash: "aaaa-bbb-refresh-hash-1",
		AccessTTL:        30 * time.Minute,
		RefreshTTL:       7 * 24 * time.Hour,
		DeviceInfo:       strPtrOf("Windows Chrome"),
		IPAddress:        strPtrOf("192.168.1.100"),
	})
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}

	// 默认排序 updated_at DESC：刚签发的会话在首页。
	page := 1
	pageSize := 10
	q := &dynq.Query{Pagination: &dynq.Pagination{Page: &page, PageSize: &pageSize}}
	out, err := repo.List(context.Background(), auth, q)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(out.Items) == 0 || out.Items[0].ID != issued.ID {
		t.Fatalf("first item = %+v, want the just-issued session %d", out.Items, issued.ID)
	}
	row := out.Items[0]
	if row.UserID != auth.UserID || row.IsRevoked {
		t.Fatalf("row = (user %d, revoked %v), want (%d, false)", row.UserID, row.IsRevoked, auth.UserID)
	}
	if row.DeviceInfo == nil || *row.DeviceInfo != "Windows Chrome" {
		t.Fatalf("device_info = %+v, want Windows Chrome", row.DeviceInfo)
	}
	if row.ExpiresAt.Before(now) {
		t.Fatalf("expires_at %v must be in the future", row.ExpiresAt)
	}
	if out.Total == nil || *out.Total != 1 {
		t.Fatalf("total = %+v, want 1", out.Total)
	}
}

func TestSessionAdminListFiltersByUserAndIsolatesTenant(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	repo := NewSessionRepo(d)
	auth := dictAuth(dictTenantSeeded)

	// token_hash 是全局唯一索引：每个会话用独立哈希。
	for i, uid := range []int64{2, 2, 3} {
		if _, err := repo.Issue(context.Background(), biz.Identity{TenantID: auth.TenantID, UserID: uid}, &biz.IssueSessionInput{
			AccessTokenHash:  fmt.Sprintf("hash-for-user-%d-access", i),
			RefreshTokenHash: fmt.Sprintf("hash-for-user-%d-refresh", i),
			AccessTTL:        30 * time.Minute,
			RefreshTTL:       7 * 24 * time.Hour,
		}); err != nil {
			t.Fatalf("issue session for user %d: %v", uid, err)
		}
	}

	q := &dynq.Query{Where: []dynq.Condition{{Field: "userId", Op: "eq", Value: int64(2)}}}
	out, err := repo.List(context.Background(), auth, q)
	if err != nil {
		t.Fatalf("list sessions filtered by user: %v", err)
	}
	if len(out.Items) != 2 || out.Total == nil || *out.Total != 2 {
		t.Fatalf("items/total = %d/%+v, want 2/2", len(out.Items), out.Total)
	}

	// 同一查询在无种子租户内必须为空集（RLS 隔离），不报错。
	empty, err := repo.List(context.Background(), dictAuth(dictTenantEmpty), q)
	if err != nil {
		t.Fatalf("list sessions in empty tenant: %v", err)
	}
	if len(empty.Items) != 0 {
		t.Fatalf("items = %+v, want empty across tenant", empty.Items)
	}
}

func TestSessionAdminListRejectsUnknownField(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	repo := NewSessionRepo(d)

	q := &dynq.Query{Where: []dynq.Condition{{Field: "tokenHash", Op: "eq", Value: "x"}}}
	_, err := repo.List(context.Background(), dictAuth(dictTenantSeeded), q)
	if !errors.Is(err, biz.ErrSessionInvalidArgument) {
		t.Fatalf("err = %v, want ErrSessionInvalidArgument for @internal column query", err)
	}
}

// strPtrOf 是集成测试的可空字符串辅助。
func strPtrOf(s string) *string { return &s }

func TestAuditLogRepoInsertLoginLogRoundTrip(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	repo := NewAuditLogRepo(d)
	ctx := context.Background()
	const username = "audit-write-login"

	reason := "invalid credentials"
	err := repo.InsertLoginLog(ctx, &biz.LoginLogInput{
		TenantID:      auditTenantSeeded,
		UserID:        99,
		Username:      username,
		IPAddress:     strPtrOf("10.0.0.8"),
		UserAgent:     strPtrOf("test-agent"),
		LoginType:     "password",
		IsSuccess:     false,
		FailureReason: &reason,
	})
	if err != nil {
		t.Fatalf("insert login log: %v", err)
	}

	out, err := repo.ListLoginLogs(ctx, dictAuth(auditTenantSeeded), auditLogQuery(username))
	if err != nil {
		t.Fatalf("list login logs: %v", err)
	}
	if len(out.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(out.Items))
	}
	row := out.Items[0]
	if row.UserID != 99 || row.Username != username || row.IsSuccess {
		t.Fatalf("row = (%d, %s, success %v), want (99, %s, false)", row.UserID, row.Username, row.IsSuccess, username)
	}
	if row.IPAddress == nil || *row.IPAddress != "10.0.0.8" || row.FailureReason == nil || *row.FailureReason != reason {
		t.Fatalf("row facts = %+v, want ip/failure reason", row)
	}

	other, err := repo.ListLoginLogs(ctx, dictAuth("t-test-02"), auditLogQuery(username))
	if err != nil {
		t.Fatalf("list across tenant: %v", err)
	}
	if len(other.Items) != 0 {
		t.Fatalf("items across tenant = %+v, want empty", other.Items)
	}
}

func TestAuditLogRepoInsertOperationLogRoundTrip(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	repo := NewAuditLogRepo(d)
	ctx := context.Background()
	auth := dictAuth(auditTenantSeeded)
	const username = "audit-write-operation"

	err := repo.InsertOperationLog(ctx, auth, &biz.OperationLogInput{
		Username:      username,
		OperationType: "UPDATE",
		TargetTable:   strPtrOf("sys_user"),
		TargetID:      strPtrOf("9"),
		OldValue:      json.RawMessage(`{"name":"old"}`),
		NewValue:      json.RawMessage(`{"name":"new"}`),
		IPAddress:     strPtrOf("10.0.0.9"),
		UserAgent:     strPtrOf("test-agent"),
		APIPath:       strPtrOf("/v1/users/9"),
		HTTPMethod:    strPtrOf("PUT"),
		DurationMs:    int32Ptr(12),
		ResultCode:    strPtrOf("200"),
		RequestID:     strPtrOf("req-audit"),
	})
	if err != nil {
		t.Fatalf("insert operation log: %v", err)
	}

	out, err := repo.ListOperationLogs(ctx, auth, auditLogQuery(username))
	if err != nil {
		t.Fatalf("list operation logs: %v", err)
	}
	if len(out.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(out.Items))
	}
	row := out.Items[0]
	if row.Username != username || row.OperationType != "UPDATE" || row.UserID != auth.UserID || row.CreatedBy != auth.UserID {
		t.Fatalf("row = %+v, want operator/UPDATE for auth %+v", row, auth)
	}
	if row.TargetTable == nil || *row.TargetTable != "sys_user" || row.APIPath == nil || *row.APIPath != "/v1/users/9" {
		t.Fatalf("row target facts = %+v, want sys_user / /v1/users/9", row)
	}
	if row.DurationMs == nil || *row.DurationMs != 12 || row.RequestID == nil || *row.RequestID != "req-audit" {
		t.Fatalf("row request facts = %+v, want 12ms / req-audit", row)
	}
	if !strings.Contains(string(row.OldValue), `"old"`) || !strings.Contains(string(row.NewValue), `"new"`) {
		t.Fatalf("row snapshots = %s / %s, want old/new JSON", row.OldValue, row.NewValue)
	}

	other, err := repo.ListOperationLogs(ctx, dictAuth("t-test-02"), auditLogQuery(username))
	if err != nil {
		t.Fatalf("list across tenant: %v", err)
	}
	if len(other.Items) != 0 {
		t.Fatalf("items across tenant = %+v, want empty", other.Items)
	}
}
