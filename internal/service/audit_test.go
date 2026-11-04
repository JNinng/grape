package service

import (
	"context"
	"errors"
	"testing"

	auditv1 "grape/api/audit/v1"
	queryv1 "grape/api/query/v1"
	"grape/internal/biz"

	"github.com/jninng/sqlc_gen_dynamic/dynq"
	"google.golang.org/protobuf/types/known/structpb"
)

// fakeAuditLogRepo 捕获用例转发的查询，供断言身份透传与信封转换。
type fakeAuditLogRepo struct {
	biz.AuditLogRepo

	gotAuth    biz.Identity
	callsCount int
	getID      int64

	loginList *biz.LoginLogList
	operList  *biz.OperationLogList
	loginRow  *biz.LoginLog
	operRow   *biz.OperationLog
}

func (f *fakeAuditLogRepo) GetLoginLogByID(_ context.Context, auth biz.Identity, id int64) (*biz.LoginLog, error) {
	f.callsCount++
	f.gotAuth = auth
	f.getID = id
	return f.loginRow, nil
}

func (f *fakeAuditLogRepo) ListLoginLogs(_ context.Context, auth biz.Identity, _ *dynq.Query) (*biz.LoginLogList, error) {
	f.callsCount++
	f.gotAuth = auth
	return f.loginList, nil
}

func (f *fakeAuditLogRepo) GetOperationLogByID(_ context.Context, auth biz.Identity, id int64) (*biz.OperationLog, error) {
	f.callsCount++
	f.gotAuth = auth
	f.getID = id
	return f.operRow, nil
}

func (f *fakeAuditLogRepo) ListOperationLogs(_ context.Context, auth biz.Identity, _ *dynq.Query) (*biz.OperationLogList, error) {
	f.callsCount++
	f.gotAuth = auth
	return f.operList, nil
}

func TestAuditServiceReadsIdentityFromContext(t *testing.T) {
	repo := &fakeAuditLogRepo{loginRow: &biz.LoginLog{ID: 3, Username: "admin", LoginType: "password", IsSuccess: true}}
	svc := NewAuditService(biz.NewAuditLogUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	out, err := svc.GetLoginLog(biz.WithIdentity(context.Background(), auth), &auditv1.GetLoginLogRequest{Id: 3})
	if err != nil {
		t.Fatalf("get login log: %v", err)
	}
	if out.GetId() != 3 || !out.GetIsSuccess() {
		t.Fatalf("row = (%d, %v), want (3, true)", out.GetId(), out.GetIsSuccess())
	}
	if repo.callsCount != 1 || repo.gotAuth != auth {
		t.Fatalf("usecase auth = %+v (calls %d), want %+v once", repo.gotAuth, repo.callsCount, auth)
	}
}

func TestAuditServiceFailsClosedWithoutIdentity(t *testing.T) {
	repo := &fakeAuditLogRepo{}
	svc := NewAuditService(biz.NewAuditLogUsecase(repo))

	_, err := svc.GetOperationLog(context.Background(), &auditv1.GetOperationLogRequest{Id: 3})
	if !errors.Is(err, biz.ErrSessionInvalid) {
		t.Fatalf("err = %v, want ErrSessionInvalid", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("repo must not be called without identity, calls = %d", repo.callsCount)
	}
}

func TestAuditServiceListLoginLogsReturnsEnvelope(t *testing.T) {
	total := int64(4)
	repo := &fakeAuditLogRepo{loginList: &biz.LoginLogList{
		Items:      []*biz.LoginLog{{ID: 1, Username: "admin", LoginType: "password", IsSuccess: false, FailureReason: strPtr("wrong password")}},
		Total:      &total,
		HasMore:    true,
		NextCursor: "cur",
	}}
	svc := NewAuditService(biz.NewAuditLogUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}
	page := int32(2)

	out, err := svc.ListLoginLogs(biz.WithIdentity(context.Background(), auth), &auditv1.ListLoginLogsRequest{
		Query: &queryv1.Query{Pagination: &queryv1.Pagination{Page: &page}},
	})
	if err != nil {
		t.Fatalf("list login logs: %v", err)
	}
	if len(out.GetItems()) != 1 || out.GetItems()[0].GetFailureReason() != "wrong password" {
		t.Fatalf("items = %+v, want single failed login", out.GetItems())
	}
	if out.GetTotal() != 4 || !out.GetHasMore() || out.GetNextCursor() != "cur" || out.GetPage() != 2 {
		t.Fatalf("envelope = (%d, %v, %q, %d), want (4, true, cur, 2)", out.GetTotal(), out.GetHasMore(), out.GetNextCursor(), out.GetPage())
	}
}

func TestAuditServiceListLoginLogsRejectsInvalidQuery(t *testing.T) {
	repo := &fakeAuditLogRepo{}
	svc := NewAuditService(biz.NewAuditLogUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}
	page := int32(1)
	after := "cur"

	_, err := svc.ListLoginLogs(biz.WithIdentity(context.Background(), auth), &auditv1.ListLoginLogsRequest{
		Query: &queryv1.Query{Pagination: &queryv1.Pagination{Page: &page, After: &after}},
	})
	if !errors.Is(err, biz.ErrAuditLogInvalidArgument) {
		t.Fatalf("err = %v, want ErrAuditLogInvalidArgument", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("repo calls = %d, want 0", repo.callsCount)
	}
}

func TestAuditServiceGetOperationLogDecodesJSONSnapshots(t *testing.T) {
	repo := &fakeAuditLogRepo{operRow: &biz.OperationLog{
		ID:            5,
		Username:      "admin",
		OperationType: "UPDATE",
		OldValue:      []byte(`{"data_scope":1}`),
		NewValue:      []byte(`{"data_scope":7}`),
	}}
	svc := NewAuditService(biz.NewAuditLogUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	out, err := svc.GetOperationLog(biz.WithIdentity(context.Background(), auth), &auditv1.GetOperationLogRequest{Id: 5})
	if err != nil {
		t.Fatalf("get operation log: %v", err)
	}
	if out.GetOldValue().GetFields()["data_scope"].GetNumberValue() != 1 {
		t.Fatalf("old_value = %+v, want data_scope 1", out.GetOldValue())
	}
	if out.GetNewValue().GetFields()["data_scope"].GetNumberValue() != 7 {
		t.Fatalf("new_value = %+v, want data_scope 7", out.GetNewValue())
	}
}

func TestAuditServiceGetOperationLogDegradesNonObjectSnapshotToUnset(t *testing.T) {
	// 历史脏数据（快照非 JSON 对象）只降级字段不炸整行。
	repo := &fakeAuditLogRepo{operRow: &biz.OperationLog{
		ID:            6,
		Username:      "admin",
		OperationType: "UPDATE",
		OldValue:      []byte(`[1,2,3]`),
	}}
	svc := NewAuditService(biz.NewAuditLogUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	out, err := svc.GetOperationLog(biz.WithIdentity(context.Background(), auth), &auditv1.GetOperationLogRequest{Id: 6})
	if err != nil {
		t.Fatalf("get operation log with array snapshot: %v", err)
	}
	if out.GetOldValue() != nil {
		t.Fatalf("old_value = %+v, want unset for non-object snapshot", out.GetOldValue())
	}
}

// TestAuditServiceOperationLogSnapshotContract 是 structpb 往返的契约锚：
// 前端消费 old/new_value 的唯一形态是 JSON 对象。
func TestAuditServiceOperationLogSnapshotContract(t *testing.T) {
	raw := []byte(`{"name":"zhangsan","dept_id":2}`)
	value := map[string]any{"name": "zhangsan", "dept_id": float64(2)}
	want, err := structpb.NewStruct(value)
	if err != nil {
		t.Fatalf("build struct: %v", err)
	}
	if got := jsonRawToStruct(raw); !protoStructEqual(got, want) {
		t.Fatalf("struct = %+v, want %+v", got, want)
	}
	if jsonRawToStruct(nil) != nil {
		t.Fatal("nil snapshot must map to unset")
	}
}

// protoStructEqual 比较 structpb.Struct 的 JSON 等价性（测试辅助，不依赖
// proto 反射）。
func protoStructEqual(a, b *structpb.Struct) bool {
	aj, aerr := a.MarshalJSON()
	bj, berr := b.MarshalJSON()
	if aerr != nil || berr != nil {
		return false
	}
	return string(aj) == string(bj)
}

// strPtr 是可空字符串测试辅助。
func strPtr(s string) *string { return &s }
