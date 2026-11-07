package biz

import (
	"context"
	"errors"
	"testing"

	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

// mockAuditLogRepo 捕获仓储调用：biz 只验证用例编排与入参校验，不触真实
// 存储。审计日志只读、无业务规则，mock 仅回传固定行/信封。
type mockAuditLogRepo struct {
	loginGetID  int64
	loginListed bool
	operGetID   int64
	operListed  bool
	getErr      error
	insertErr   error
	loginList   *LoginLogList
	operList    *OperationLogList
	loginInsert *LoginLogInput
	operInsert  *OperationLogInput
	operAuth    Identity
}

func (m *mockAuditLogRepo) GetLoginLogByID(_ context.Context, _ Identity, id int64) (*LoginLog, error) {
	m.loginGetID = id
	if m.getErr != nil {
		return nil, m.getErr
	}
	return &LoginLog{ID: id, Username: "admin", LoginType: "password", IsSuccess: true}, nil
}

func (m *mockAuditLogRepo) ListLoginLogs(context.Context, Identity, *dynq.Query) (*LoginLogList, error) {
	m.loginListed = true
	return m.loginList, nil
}

func (m *mockAuditLogRepo) GetOperationLogByID(_ context.Context, _ Identity, id int64) (*OperationLog, error) {
	m.operGetID = id
	if m.getErr != nil {
		return nil, m.getErr
	}
	return &OperationLog{ID: id, Username: "admin", OperationType: "CREATE"}, nil
}

func (m *mockAuditLogRepo) ListOperationLogs(context.Context, Identity, *dynq.Query) (*OperationLogList, error) {
	m.operListed = true
	return m.operList, nil
}

func (m *mockAuditLogRepo) InsertLoginLog(_ context.Context, in *LoginLogInput) error {
	m.loginInsert = in
	return m.insertErr
}

func (m *mockAuditLogRepo) InsertOperationLog(_ context.Context, auth Identity, in *OperationLogInput) error {
	m.operAuth = auth
	m.operInsert = in
	return m.insertErr
}

func TestAuditLogUsecaseGetLoginLogForwardsPositiveID(t *testing.T) {
	repo := &mockAuditLogRepo{}
	uc := NewAuditLogUsecase(repo)

	out, err := uc.GetLoginLog(context.Background(), Identity{TenantID: "t-1", UserID: 7}, 3)
	if err != nil {
		t.Fatalf("get login log: %v", err)
	}
	if out.ID != 3 || repo.loginGetID != 3 {
		t.Fatalf("row/repo id = (%d, %d), want 3/3", out.ID, repo.loginGetID)
	}
}

func TestAuditLogUsecaseGetLoginLogRejectsNonPositiveID(t *testing.T) {
	repo := &mockAuditLogRepo{}
	uc := NewAuditLogUsecase(repo)

	_, err := uc.GetLoginLog(context.Background(), Identity{TenantID: "t-1", UserID: 7}, 0)
	if !errors.Is(err, ErrAuditLogInvalidArgument) {
		t.Fatalf("err = %v, want ErrAuditLogInvalidArgument", err)
	}
	if repo.loginGetID != 0 {
		t.Fatalf("repo must not be called, got id %d", repo.loginGetID)
	}
}

func TestAuditLogUsecaseGetLoginLogPropagatesNotFound(t *testing.T) {
	repo := &mockAuditLogRepo{getErr: ErrAuditLogNotFound}
	uc := NewAuditLogUsecase(repo)

	_, err := uc.GetLoginLog(context.Background(), Identity{TenantID: "t-1", UserID: 7}, 3)
	if !errors.Is(err, ErrAuditLogNotFound) {
		t.Fatalf("err = %v, want ErrAuditLogNotFound", err)
	}
}

func TestAuditLogUsecaseListLoginLogsForwardsQuery(t *testing.T) {
	repo := &mockAuditLogRepo{loginList: &LoginLogList{}}
	uc := NewAuditLogUsecase(repo)

	out, err := uc.ListLoginLogs(context.Background(), Identity{TenantID: "t-1", UserID: 7}, &dynq.Query{})
	if err != nil {
		t.Fatalf("list login logs: %v", err)
	}
	if !repo.loginListed || out == nil {
		t.Fatal("repo ListLoginLogs must be called and its envelope returned")
	}
}

func TestAuditLogUsecaseGetOperationLogForwardsPositiveID(t *testing.T) {
	repo := &mockAuditLogRepo{}
	uc := NewAuditLogUsecase(repo)

	out, err := uc.GetOperationLog(context.Background(), Identity{TenantID: "t-1", UserID: 7}, 5)
	if err != nil {
		t.Fatalf("get operation log: %v", err)
	}
	if out.ID != 5 || repo.operGetID != 5 {
		t.Fatalf("row/repo id = (%d, %d), want 5/5", out.ID, repo.operGetID)
	}
}

func TestAuditLogUsecaseGetOperationLogRejectsNonPositiveID(t *testing.T) {
	repo := &mockAuditLogRepo{}
	uc := NewAuditLogUsecase(repo)

	_, err := uc.GetOperationLog(context.Background(), Identity{TenantID: "t-1", UserID: 7}, -1)
	if !errors.Is(err, ErrAuditLogInvalidArgument) {
		t.Fatalf("err = %v, want ErrAuditLogInvalidArgument", err)
	}
	if repo.operGetID != 0 {
		t.Fatalf("repo must not be called, got id %d", repo.operGetID)
	}
}

func TestAuditLogUsecaseListOperationLogsForwardsQuery(t *testing.T) {
	repo := &mockAuditLogRepo{operList: &OperationLogList{}}
	uc := NewAuditLogUsecase(repo)

	out, err := uc.ListOperationLogs(context.Background(), Identity{TenantID: "t-1", UserID: 7}, &dynq.Query{})
	if err != nil {
		t.Fatalf("list operation logs: %v", err)
	}
	if !repo.operListed || out == nil {
		t.Fatal("repo ListOperationLogs must be called and its envelope returned")
	}
}

func TestAuditLogUsecaseRecordLoginDefaultsPasswordAndForwards(t *testing.T) {
	repo := &mockAuditLogRepo{}
	uc := NewAuditLogUsecase(repo)
	in := &LoginLogInput{TenantID: "t-1", Username: "admin", IsSuccess: true}

	if err := uc.RecordLogin(context.Background(), in); err != nil {
		t.Fatalf("record login: %v", err)
	}
	if repo.loginInsert != in || in.LoginType != "password" {
		t.Fatalf("insert = %+v (login type %q), want forwarded with password default", repo.loginInsert, in.LoginType)
	}
}

func TestAuditLogUsecaseRecordLoginRejectsMissingTenantOrUsername(t *testing.T) {
	repo := &mockAuditLogRepo{}
	uc := NewAuditLogUsecase(repo)

	for _, in := range []*LoginLogInput{
		{TenantID: "", Username: "admin"},
		{TenantID: "t-1", Username: ""},
	} {
		if err := uc.RecordLogin(context.Background(), in); !errors.Is(err, ErrAuditLogInvalidArgument) {
			t.Fatalf("err = %v, want ErrAuditLogInvalidArgument for %+v", err, in)
		}
	}
	if repo.loginInsert != nil {
		t.Fatal("repo must not be called for invalid input")
	}
}

func TestAuditLogUsecaseRecordOperationForwardsAuthenticatedIdentity(t *testing.T) {
	repo := &mockAuditLogRepo{}
	uc := NewAuditLogUsecase(repo)
	auth := Identity{TenantID: "t-1", UserID: 7}
	in := &OperationLogInput{Username: "admin", OperationType: "CREATE"}

	if err := uc.RecordOperation(context.Background(), auth, in); err != nil {
		t.Fatalf("record operation: %v", err)
	}
	if repo.operInsert != in || repo.operAuth != auth {
		t.Fatalf("forwarded = (%+v, %+v), want (%+v, %+v)", repo.operInsert, repo.operAuth, in, auth)
	}
}

func TestAuditLogUsecaseRecordOperationRejectsMissingIdentity(t *testing.T) {
	repo := &mockAuditLogRepo{}
	uc := NewAuditLogUsecase(repo)

	for _, auth := range []Identity{{TenantID: "", UserID: 7}, {TenantID: "t-1", UserID: 0}} {
		if err := uc.RecordOperation(context.Background(), auth, &OperationLogInput{OperationType: "CREATE"}); !errors.Is(err, ErrAuditLogInvalidArgument) {
			t.Fatalf("err = %v, want ErrAuditLogInvalidArgument for %+v", err, auth)
		}
	}
	if repo.operInsert != nil {
		t.Fatal("repo must not be called for invalid identity")
	}
}

func TestSessionUsecaseListSessionsForwardsQuery(t *testing.T) {
	repo := &fakeSessionRepo{}
	uc := NewSessionUsecase(repo)

	out, err := uc.ListSessions(context.Background(), Identity{TenantID: "t-1", UserID: 7}, &dynq.Query{})
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if out == nil {
		t.Fatal("repo List envelope must be returned")
	}
}

func TestSessionUsecaseListSessionsRejectsIncompleteIdentity(t *testing.T) {
	repo := &fakeSessionRepo{}
	uc := NewSessionUsecase(repo)

	_, err := uc.ListSessions(context.Background(), Identity{TenantID: "", UserID: 7}, &dynq.Query{})
	if !errors.Is(err, ErrSessionInvalidArgument) {
		t.Fatalf("err = %v, want ErrSessionInvalidArgument", err)
	}

	_, err = uc.ListSessions(context.Background(), Identity{TenantID: "t-1", UserID: 0}, &dynq.Query{})
	if !errors.Is(err, ErrSessionInvalidArgument) {
		t.Fatalf("err = %v, want ErrSessionInvalidArgument", err)
	}
}
