package service

import (
	"context"
	"errors"
	"testing"

	auditv1 "grape/api/audit/v1"
	queryv1 "grape/api/query/v1"
	"grape/internal/biz"

	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

// fakeSessionAdminRepo 捕获用例转发的会话管理面查询与撤销。
type fakeSessionAdminRepo struct {
	biz.SessionRepo

	gotAuth    biz.Identity
	callsCount int
	revokedID  int64

	list *biz.SessionAdminList
}

func (f *fakeSessionAdminRepo) List(_ context.Context, auth biz.Identity, _ *dynq.Query) (*biz.SessionAdminList, error) {
	f.callsCount++
	f.gotAuth = auth
	return f.list, nil
}

func (f *fakeSessionAdminRepo) Revoke(_ context.Context, auth biz.Identity, id int64) error {
	f.callsCount++
	f.gotAuth = auth
	f.revokedID = id
	return nil
}

func TestSessionAdminServiceListSessionsReturnsEnvelope(t *testing.T) {
	total := int64(1)
	repo := &fakeSessionAdminRepo{list: &biz.SessionAdminList{
		Items:      []*biz.Session{{ID: 5, UserID: 7, TenantID: "t-1", Status: biz.SessionStatusEnabled, IsRevoked: false}},
		Total:      &total,
		HasMore:    true,
		NextCursor: "cur",
	}}
	svc := NewSessionAdminService(biz.NewSessionUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	out, err := svc.ListSessions(biz.WithIdentity(context.Background(), auth), &auditv1.ListSessionsRequest{
		Query: &queryv1.Query{},
	})
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(out.GetItems()) != 1 || out.GetItems()[0].GetId() != 5 || out.GetItems()[0].GetIsRevoked() {
		t.Fatalf("items = %+v, want single active session id 5", out.GetItems())
	}
	if out.GetTotal() != 1 || !out.GetHasMore() || out.GetNextCursor() != "cur" {
		t.Fatalf("envelope = (%d, %v, %q), want (1, true, cur)", out.GetTotal(), out.GetHasMore(), out.GetNextCursor())
	}
	if repo.callsCount != 1 || repo.gotAuth != auth {
		t.Fatalf("usecase auth = %+v (calls %d), want %+v once", repo.gotAuth, repo.callsCount, auth)
	}
}

func TestSessionAdminServiceListSessionsRejectsInvalidQuery(t *testing.T) {
	repo := &fakeSessionAdminRepo{}
	svc := NewSessionAdminService(biz.NewSessionUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}
	page := int32(1)
	after := "cur"

	_, err := svc.ListSessions(biz.WithIdentity(context.Background(), auth), &auditv1.ListSessionsRequest{
		Query: &queryv1.Query{Pagination: &queryv1.Pagination{Page: &page, After: &after}},
	})
	if !errors.Is(err, biz.ErrSessionInvalidArgument) {
		t.Fatalf("err = %v, want ErrSessionInvalidArgument", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("repo calls = %d, want 0", repo.callsCount)
	}
}

func TestSessionAdminServiceRevokeSessionForwardsID(t *testing.T) {
	repo := &fakeSessionAdminRepo{}
	svc := NewSessionAdminService(biz.NewSessionUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	_, err := svc.RevokeSession(biz.WithIdentity(context.Background(), auth), &auditv1.RevokeSessionRequest{Id: 9})
	if err != nil {
		t.Fatalf("revoke session: %v", err)
	}
	if repo.revokedID != 9 || repo.gotAuth != auth {
		t.Fatalf("revoke = (id %d, auth %+v), want (9, %+v)", repo.revokedID, repo.gotAuth, auth)
	}
}

func TestSessionAdminServiceRevokeSessionRejectsNonPositiveID(t *testing.T) {
	repo := &fakeSessionAdminRepo{}
	svc := NewSessionAdminService(biz.NewSessionUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	_, err := svc.RevokeSession(biz.WithIdentity(context.Background(), auth), &auditv1.RevokeSessionRequest{Id: 0})
	if !errors.Is(err, biz.ErrSessionInvalidArgument) {
		t.Fatalf("err = %v, want ErrSessionInvalidArgument", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("repo calls = %d, want 0", repo.callsCount)
	}
}

func TestSessionAdminServiceFailsClosedWithoutIdentity(t *testing.T) {
	repo := &fakeSessionAdminRepo{}
	svc := NewSessionAdminService(biz.NewSessionUsecase(repo))

	_, err := svc.ListSessions(context.Background(), &auditv1.ListSessionsRequest{})
	if !errors.Is(err, biz.ErrSessionInvalid) {
		t.Fatalf("err = %v, want ErrSessionInvalid", err)
	}

	_, err = svc.RevokeSession(context.Background(), &auditv1.RevokeSessionRequest{Id: 9})
	if !errors.Is(err, biz.ErrSessionInvalid) {
		t.Fatalf("err = %v, want ErrSessionInvalid", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("repo must not be called without identity, calls = %d", repo.callsCount)
	}
}
