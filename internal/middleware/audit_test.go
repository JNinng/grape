package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	userv1 "grape/api/user/v1"
	"grape/internal/biz"

	"github.com/go-kratos/kratos/v3/transport"
	"github.com/jninng/sqlc_gen_dynamic/dynq"
	"google.golang.org/protobuf/types/known/structpb"
)

// fakeAuditRepo 捕获操作日志写入；读方法本测试不触达。
type fakeAuditRepo struct {
	inserted  *biz.OperationLogInput
	auth      biz.Identity
	insertErr error
}

func (f *fakeAuditRepo) GetLoginLogByID(context.Context, biz.Identity, int64) (*biz.LoginLog, error) {
	return nil, nil
}
func (f *fakeAuditRepo) ListLoginLogs(context.Context, biz.Identity, *dynq.Query) (*biz.LoginLogList, error) {
	return &biz.LoginLogList{}, nil
}
func (f *fakeAuditRepo) InsertLoginLog(context.Context, *biz.LoginLogInput) error { return nil }
func (f *fakeAuditRepo) GetOperationLogByID(context.Context, biz.Identity, int64) (*biz.OperationLog, error) {
	return nil, nil
}
func (f *fakeAuditRepo) ListOperationLogs(context.Context, biz.Identity, *dynq.Query) (*biz.OperationLogList, error) {
	return &biz.OperationLogList{}, nil
}
func (f *fakeAuditRepo) InsertOperationLog(_ context.Context, auth biz.Identity, in *biz.OperationLogInput) error {
	f.auth = auth
	f.inserted = in
	return f.insertErr
}

func TestOperationAuditMetaOfMapsWritesAndSkipsReads(t *testing.T) {
	cases := []struct {
		operation string
		want      operationAuditMeta
		gated     bool
	}{
		{"/user.v1.UserService/CreateUser", operationAuditMeta{OperationType: "CREATE", TargetTable: "sys_user"}, true},
		{"/user.v1.UserService/ListUsers", operationAuditMeta{}, false},
		{"/user.v1.UserService/GetUser", operationAuditMeta{}, false},
		{"/audit.v1.SessionAdminService/RevokeSession", operationAuditMeta{OperationType: "DELETE", TargetTable: "sys_session"}, true},
		{"/audit.v1.SessionAdminService/ListSessions", operationAuditMeta{}, false},
		{"/config.v1.TenantConfigService/UpdateTenantConfig", operationAuditMeta{OperationType: "UPDATE", TargetTable: "sys_tenant_config"}, true},
		{"/config.v1.ConfigService/CreateConfigSuper", operationAuditMeta{OperationType: "CREATE", TargetTable: "sys_config"}, true},
		{"/dict.v1.DictService/UpdateDictData", operationAuditMeta{OperationType: "UPDATE", TargetTable: "sys_dict_data"}, true},
		{"/dict.v1.DictService/CreateDictType", operationAuditMeta{OperationType: "CREATE", TargetTable: "sys_dict_type"}, true},
		{"/org.v1.UserGroupService/AddUserGroupMembers", operationAuditMeta{OperationType: "UPDATE", TargetTable: "sys_user_group"}, true},
		{"/agent.v1.AgentService/RunAgent", operationAuditMeta{}, false},
	}
	for _, tc := range cases {
		got, gated := operationAuditMetaOf(tc.operation)
		if gated != tc.gated || got != tc.want {
			t.Errorf("%s => (%+v, %v), want (%+v, %v)", tc.operation, got, gated, tc.want, tc.gated)
		}
	}
}

func TestOperationAuditMiddlewareRecordsMutationWithRequestFacts(t *testing.T) {
	repo := &fakeAuditRepo{}
	mw := NewOperationAuditMiddleware(biz.NewAuditLogUsecase(repo), func(context.Context, biz.Identity) (string, error) {
		return "admin", nil
	})
	handler := func(context.Context, any) (any, error) { return "ok", nil }

	req := httptest.NewRequest(http.MethodPost, "/v1/users", nil)
	req.RemoteAddr = "10.0.0.8:4321"
	req.Header.Set("User-Agent", "test-agent")
	req.Header.Set("x-request-id", "req-1")
	ctx := biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7})
	ctx = transport.NewServerContext(ctx, &fakeTransporter{
		operation: "/user.v1.UserService/CreateUser",
		header:    fakeHeader{},
		req:       req,
	})

	if _, err := mw(handler)(ctx, nil); err != nil {
		t.Fatalf("handler: %v", err)
	}
	got := repo.inserted
	if got == nil || repo.auth.UserID != 7 {
		t.Fatalf("inserted = %+v (auth %+v), want operation log for user 7", got, repo.auth)
	}
	if got.Username != "admin" || got.OperationType != "CREATE" || got.TargetTable == nil || *got.TargetTable != "sys_user" {
		t.Fatalf("operation facts = %+v, want admin/CREATE/sys_user", got)
	}
	if got.APIPath == nil || *got.APIPath != "/v1/users" || got.HTTPMethod == nil || *got.HTTPMethod != http.MethodPost {
		t.Fatalf("request facts = %+v, want POST /v1/users", got)
	}
	if got.IPAddress == nil || *got.IPAddress != "10.0.0.8" || got.UserAgent == nil || *got.UserAgent != "test-agent" {
		t.Fatalf("peer facts = %+v, want 10.0.0.8/test-agent", got)
	}
	if got.RequestID == nil || *got.RequestID != "req-1" || got.ResultCode == nil || *got.ResultCode != "200" {
		t.Fatalf("trace facts = %+v, want req-1/200", got)
	}
}

func TestOperationAuditMiddlewareRecordsPermissionDenied(t *testing.T) {
	repo := &fakeAuditRepo{}
	mw := NewOperationAuditMiddleware(biz.NewAuditLogUsecase(repo), nil)
	handler := func(context.Context, any) (any, error) { return nil, biz.ErrPermissionDenied }

	req := httptest.NewRequest(http.MethodDelete, "/v1/users/9", nil)
	ctx := biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7})
	ctx = transport.NewServerContext(ctx, &fakeTransporter{
		operation: "/user.v1.UserService/DeleteUser",
		header:    fakeHeader{},
		req:       req,
	})

	if _, err := mw(handler)(ctx, nil); !errors.Is(err, biz.ErrPermissionDenied) {
		t.Fatalf("err = %v, want ErrPermissionDenied", err)
	}
	got := repo.inserted
	if got == nil || got.ResultCode == nil || *got.ResultCode != "403" {
		t.Fatalf("result code = %+v, want 403", got)
	}
	if got.ResultMsg == nil || *got.ResultMsg == "" {
		t.Fatal("result message must be populated on error")
	}
}

func TestOperationAuditMiddlewareSkipsReadOperations(t *testing.T) {
	repo := &fakeAuditRepo{}
	mw := NewOperationAuditMiddleware(biz.NewAuditLogUsecase(repo), nil)
	handler := func(context.Context, any) (any, error) { return "ok", nil }

	req := httptest.NewRequest(http.MethodPost, "/v1/users/list", nil)
	ctx := biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7})
	ctx = transport.NewServerContext(ctx, &fakeTransporter{
		operation: "/user.v1.UserService/ListUsers",
		header:    fakeHeader{},
		req:       req,
	})

	if _, err := mw(handler)(ctx, nil); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if repo.inserted != nil {
		t.Fatalf("read operation must not be audited, got %+v", repo.inserted)
	}
}

func TestOperationAuditSnapshotExtractsCreateReply(t *testing.T) {
	req := &userv1.CreateUserRequest{
		User:     &userv1.User{Name: "alice"},
		Password: "secret",
	}
	reply := &userv1.User{Id: 42, Name: "alice", Nickname: "Alice"}
	snapshot := operationAuditSnapshot(operationAuditMeta{OperationType: "CREATE", TargetTable: "sys_user"}, req, reply)

	if snapshot.TargetID != "42" {
		t.Fatalf("target id = %q, want 42", snapshot.TargetID)
	}
	if !strings.Contains(string(snapshot.NewValue), `"alice"`) {
		t.Fatalf("new value = %s, want reply entity", snapshot.NewValue)
	}
	if strings.Contains(string(snapshot.NewValue), "secret") {
		t.Fatalf("new value = %s, must not contain request password", snapshot.NewValue)
	}
}

func TestOperationAuditSnapshotFallsBackToRequestAndRedactsPassword(t *testing.T) {
	req := &userv1.SetUserPasswordRequest{Id: 7, Password: "secret"}
	snapshot := operationAuditSnapshot(operationAuditMeta{OperationType: "UPDATE", TargetTable: "sys_user"}, req, nil)

	if snapshot.TargetID != "7" {
		t.Fatalf("target id = %q, want 7", snapshot.TargetID)
	}
	got := string(snapshot.NewValue)
	if !strings.Contains(got, `"password":"***"`) || strings.Contains(got, "secret") {
		t.Fatalf("new value = %s, want masked password", got)
	}
	same := string(operationAuditSnapshot(operationAuditMeta{OperationType: "UPDATE", TargetTable: "sys_user"}, req, nil).NewValue)
	if same != got {
		t.Fatalf("masked snapshot = %s, want deterministic %s", same, got)
	}
}

func TestOperationAuditSnapshotDeleteKeepsTargetWithoutNewValue(t *testing.T) {
	req := &userv1.DeleteUserRequest{Id: 9}
	snapshot := operationAuditSnapshot(operationAuditMeta{OperationType: "DELETE", TargetTable: "sys_user"}, req, nil)

	if snapshot.TargetID != "9" {
		t.Fatalf("target id = %q, want 9", snapshot.TargetID)
	}
	if snapshot.NewValue != nil {
		t.Fatalf("new value = %s, want nil for delete", snapshot.NewValue)
	}
}

func TestOperationAuditTargetIDFindsNestedEntityID(t *testing.T) {
	req := &userv1.UpdateUserRequest{User: &userv1.User{Id: 11, Name: "bob"}}
	if got := operationAuditTargetID("sys_user", nil, req); got != "11" {
		t.Fatalf("target id = %q, want nested user id 11", got)
	}
}

func TestAuditMessageJSONRedactsConfigValueOnlyForConfigTables(t *testing.T) {
	value, err := structpb.NewStruct(map[string]any{"name": "smtp", "value": "top-secret"})
	if err != nil {
		t.Fatalf("build struct: %v", err)
	}

	configJSON := string(auditMessageJSON("sys_config", value))
	if !strings.Contains(configJSON, `"value":"sha256:`) || strings.Contains(configJSON, "top-secret") || !strings.Contains(configJSON, `"smtp"`) {
		t.Fatalf("config snapshot = %s, want value hashed and name kept", configJSON)
	}
	if again := string(auditMessageJSON("sys_config", value)); again != configJSON {
		t.Fatalf("hashed snapshot = %s, want deterministic %s", again, configJSON)
	}

	translationJSON := string(auditMessageJSON("sys_translation", value))
	if !strings.Contains(translationJSON, "top-secret") {
		t.Fatalf("translation snapshot = %s, want value kept", translationJSON)
	}
}

func TestAuditMessageJSONMasksPasswordAndHashesOtherSecrets(t *testing.T) {
	value, err := structpb.NewStruct(map[string]any{
		"name":     "alice",
		"password": "secret",
		"token":    "token-secret",
	})
	if err != nil {
		t.Fatalf("build struct: %v", err)
	}

	got := string(auditMessageJSON("sys_user", value))
	if !strings.Contains(got, `"password":"***"`) || strings.Contains(got, "secret\"") {
		t.Fatalf("password field = %s, want fixed mask", got)
	}
	if !strings.Contains(got, `"token":"sha256:`) || strings.Contains(got, "token-secret") {
		t.Fatalf("token field = %s, want hash", got)
	}
	if !strings.Contains(got, `"alice"`) {
		t.Fatalf("snapshot = %s, want non-sensitive fields kept", got)
	}
	if again := string(auditMessageJSON("sys_user", value)); again != got {
		t.Fatalf("hashed snapshot = %s, want deterministic %s", again, got)
	}
}
