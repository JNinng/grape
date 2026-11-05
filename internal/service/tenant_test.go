package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	queryv1 "grape/api/query/v1"
	tenantv1 "grape/api/tenant/v1"
	"grape/internal/biz"

	"github.com/jninng/sqlc_gen_dynamic/dynq"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// fakeTenantRepo 捕获用例转发的租户 DO，供断言转换与身份透传。
type fakeTenantRepo struct {
	biz.TenantRepo

	gotAuth    biz.Identity
	callsCount int

	gotID    string
	getOut   *biz.Tenant
	created  *biz.Tenant
	updated  *biz.Tenant
	deleted  []string
	disabled []string
	restored []string
	list     *biz.TenantList
	repoErr  error
}

func (f *fakeTenantRepo) Get(_ context.Context, auth biz.Identity, id string) (*biz.Tenant, error) {
	f.callsCount++
	f.gotAuth = auth
	f.gotID = id
	if f.repoErr != nil {
		return nil, f.repoErr
	}
	if f.getOut != nil {
		return f.getOut, nil
	}
	return &biz.Tenant{ID: id, Status: biz.TenantStatusEnabled}, nil
}

func (f *fakeTenantRepo) List(_ context.Context, auth biz.Identity, _ *dynq.Query) (*biz.TenantList, error) {
	f.callsCount++
	f.gotAuth = auth
	return f.list, nil
}

func (f *fakeTenantRepo) CreateSuper(_ context.Context, auth biz.Identity, t *biz.Tenant) (*biz.Tenant, error) {
	f.callsCount++
	f.gotAuth = auth
	f.created = t
	out := *t
	out.ID = "t-0123456789abcd"
	return &out, nil
}

func (f *fakeTenantRepo) UpdateSuper(_ context.Context, auth biz.Identity, t *biz.Tenant) (*biz.Tenant, error) {
	f.callsCount++
	f.gotAuth = auth
	f.updated = t
	return t, nil
}

func (f *fakeTenantRepo) DeleteSuper(_ context.Context, auth biz.Identity, id string) error {
	f.callsCount++
	f.gotAuth = auth
	f.deleted = append(f.deleted, id)
	return nil
}

func (f *fakeTenantRepo) DisableSuper(_ context.Context, auth biz.Identity, id string) (*biz.Tenant, error) {
	f.callsCount++
	f.gotAuth = auth
	f.disabled = append(f.disabled, id)
	return &biz.Tenant{ID: id, Status: biz.TenantStatusDisabled}, nil
}

func (f *fakeTenantRepo) RestoreSuper(_ context.Context, auth biz.Identity, id string) (*biz.Tenant, error) {
	f.callsCount++
	f.gotAuth = auth
	f.restored = append(f.restored, id)
	return &biz.Tenant{ID: id, Status: biz.TenantStatusEnabled}, nil
}

func TestTenantServiceReadsIdentityFromContext(t *testing.T) {
	repo := &fakeTenantRepo{}
	svc := NewTenantService(biz.NewTenantUsecase(repo))
	auth := biz.Identity{TenantID: "t-super-01", UserID: 7}

	out, err := svc.GetTenantSuper(biz.WithIdentity(context.Background(), auth), &tenantv1.GetTenantSuperRequest{Id: "t-demo-01"})
	if err != nil {
		t.Fatalf("get tenant: %v", err)
	}
	if out.GetId() != "t-demo-01" {
		t.Fatalf("tenant id = %q, want t-demo-01", out.GetId())
	}
	if repo.callsCount != 1 || repo.gotAuth != auth || repo.gotID != "t-demo-01" {
		t.Fatalf("repo call = (%+v, %q, %d), want (%+v, t-demo-01, 1)", repo.gotAuth, repo.gotID, repo.callsCount, auth)
	}
}

func TestTenantServiceFailsClosedWithoutIdentity(t *testing.T) {
	repo := &fakeTenantRepo{}
	svc := NewTenantService(biz.NewTenantUsecase(repo))

	if _, err := svc.GetTenantSuper(context.Background(), &tenantv1.GetTenantSuperRequest{Id: "t-demo-01"}); !errors.Is(err, biz.ErrSessionInvalid) {
		t.Fatalf("err = %v, want ErrSessionInvalid", err)
	}
	if _, err := svc.CreateTenantSuper(context.Background(), &tenantv1.CreateTenantSuperRequest{
		Tenant: &tenantv1.Tenant{Code: "demo", Name: "租户"},
	}); !errors.Is(err, biz.ErrSessionInvalid) {
		t.Fatalf("create err = %v, want ErrSessionInvalid", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("repo must not be called without identity, calls = %d", repo.callsCount)
	}
}

func TestTenantServiceCreateDefaultsParentAndStatus(t *testing.T) {
	repo := &fakeTenantRepo{}
	svc := NewTenantService(biz.NewTenantUsecase(repo))
	auth := biz.Identity{TenantID: "t-super-01", UserID: 7}
	expired := time.Unix(1800000000, 0).UTC()
	maxUsers := int32(10)
	empty := ""

	out, err := svc.CreateTenantSuper(biz.WithIdentity(context.Background(), auth), &tenantv1.CreateTenantSuperRequest{
		Tenant: &tenantv1.Tenant{
			Code:          "  demo  ",
			Name:          "  演示租户  ",
			ContactName:   &empty,
			ExpiredAt:     timestamppb.New(expired),
			MaxUserCount:  &maxUsers,
			DefaultLocale: "",
			TenantId:      "t-forged",
		},
	})
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	if repo.created == nil {
		t.Fatal("repo must be called")
	}
	if repo.created.ParentID == nil || *repo.created.ParentID != "t-super-01" {
		t.Fatalf("parent = %v, want caller tenant t-super-01", repo.created.ParentID)
	}
	if repo.created.Status != biz.TenantStatusEnabled {
		t.Fatalf("status = %d, want enabled", repo.created.Status)
	}
	if repo.created.Code != "demo" || repo.created.Name != "演示租户" {
		t.Fatalf("row = (%q, %q), want trimmed (demo, 演示租户)", repo.created.Code, repo.created.Name)
	}
	if repo.created.TenantID != "" {
		t.Fatalf("tenant id = %q, want empty (forged value dropped)", repo.created.TenantID)
	}
	// 空串 contact_name 折 nil（与 config 的 optionalString 口径一致）。
	if repo.created.ContactName != nil {
		t.Fatalf("contact name = %v, want nil for empty string", repo.created.ContactName)
	}
	if repo.created.ExpiredAt == nil || !repo.created.ExpiredAt.Equal(expired) {
		t.Fatalf("expired_at = %v, want %v", repo.created.ExpiredAt, expired)
	}
	if repo.created.MaxUserCount == nil || *repo.created.MaxUserCount != maxUsers {
		t.Fatalf("max_user_count = %v, want %d", repo.created.MaxUserCount, maxUsers)
	}
	if repo.created.DefaultLocale != biz.DefaultTenantLocale {
		t.Fatalf("default locale = %q, want %q", repo.created.DefaultLocale, biz.DefaultTenantLocale)
	}
	if out.GetTenantId() != "" {
		t.Fatalf("out tenant id = %q, want empty (server value not echoed by fake)", out.GetTenantId())
	}
}

func TestTenantServiceCreateKeepsExplicitDisabled(t *testing.T) {
	zero := int32(0)
	repo := &fakeTenantRepo{}
	svc := NewTenantService(biz.NewTenantUsecase(repo))
	auth := biz.Identity{TenantID: "t-super-01", UserID: 7}

	if _, err := svc.CreateTenantSuper(biz.WithIdentity(context.Background(), auth), &tenantv1.CreateTenantSuperRequest{
		Tenant: &tenantv1.Tenant{Code: "demo", Name: "租户", Status: &zero},
	}); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	if repo.created == nil || repo.created.Status != biz.TenantStatusDisabled {
		t.Fatalf("repo status = %+v, want disabled", repo.created)
	}
}

func TestTenantServiceCreateRejectsExplicitSentinelStatus(t *testing.T) {
	// 只有 nil（未设置）映射未指定哨兵；显式 -1/2 等值不是合法输入，
	// 直接 INVALID_ARGUMENT 且不触达用例。
	for _, status := range []int32{-1, 2} {
		t.Run(fmt.Sprintf("status=%d", status), func(t *testing.T) {
			repo := &fakeTenantRepo{}
			svc := NewTenantService(biz.NewTenantUsecase(repo))
			auth := biz.Identity{TenantID: "t-super-01", UserID: 7}
			value := status

			_, err := svc.CreateTenantSuper(biz.WithIdentity(context.Background(), auth), &tenantv1.CreateTenantSuperRequest{
				Tenant: &tenantv1.Tenant{Code: "demo", Name: "租户", Status: &value},
			})
			if !errors.Is(err, biz.ErrTenantInvalidArgument) {
				t.Fatalf("err = %v, want ErrTenantInvalidArgument", err)
			}
			if repo.callsCount != 0 {
				t.Fatalf("repo calls = %d, want 0", repo.callsCount)
			}
		})
	}
}

func TestTenantServiceCreateRejectsMissingTenant(t *testing.T) {
	repo := &fakeTenantRepo{}
	svc := NewTenantService(biz.NewTenantUsecase(repo))
	auth := biz.Identity{TenantID: "t-super-01", UserID: 7}

	if _, err := svc.CreateTenantSuper(biz.WithIdentity(context.Background(), auth), &tenantv1.CreateTenantSuperRequest{}); !errors.Is(err, biz.ErrTenantInvalidArgument) {
		t.Fatalf("err = %v, want ErrTenantInvalidArgument", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("repo must not be called, calls = %d", repo.callsCount)
	}
}

func TestTenantServiceUpdateRequiresStatus(t *testing.T) {
	repo := &fakeTenantRepo{}
	svc := NewTenantService(biz.NewTenantUsecase(repo))
	auth := biz.Identity{TenantID: "t-super-01", UserID: 7}

	_, err := svc.UpdateTenantSuper(biz.WithIdentity(context.Background(), auth), &tenantv1.UpdateTenantSuperRequest{
		Tenant: &tenantv1.Tenant{Id: "t-demo-01", Code: "demo", Name: "租户"},
	})
	if !errors.Is(err, biz.ErrTenantInvalidArgument) {
		t.Fatalf("err = %v, want ErrTenantInvalidArgument", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("repo must not be called, calls = %d", repo.callsCount)
	}
}

func TestTenantServiceUpdateRejectsInvalidStatus(t *testing.T) {
	// 更新路径 status 必填且只接受 0/1：显式 -1（哨兵）与 2 在 service
	// 入口即 InvalidArgument（用例层复核），仓储零调用。
	for _, status := range []int32{-1, 2} {
		t.Run(fmt.Sprintf("status=%d", status), func(t *testing.T) {
			repo := &fakeTenantRepo{}
			svc := NewTenantService(biz.NewTenantUsecase(repo))
			auth := biz.Identity{TenantID: "t-super-01", UserID: 7}
			value := status

			_, err := svc.UpdateTenantSuper(biz.WithIdentity(context.Background(), auth), &tenantv1.UpdateTenantSuperRequest{
				Tenant: &tenantv1.Tenant{Id: "t-demo-01", Code: "demo", Name: "租户", Status: &value},
			})
			if !errors.Is(err, biz.ErrTenantInvalidArgument) {
				t.Fatalf("err = %v, want ErrTenantInvalidArgument", err)
			}
			if repo.callsCount != 0 {
				t.Fatalf("repo calls = %d, want 0", repo.callsCount)
			}
		})
	}
}

func TestTenantServiceUpdateDropsIdentityColumns(t *testing.T) {
	// code（身份列）、parent_id（结构列）与 tenant_id（归属列）创建后
	// 不可变：更新输入在传输层即被丢弃（不映射进 DO）。
	repo := &fakeTenantRepo{}
	svc := NewTenantService(biz.NewTenantUsecase(repo))
	auth := biz.Identity{TenantID: "t-super-01", UserID: 7}
	one := int32(1)
	forgedParent := "t-forged-parent"

	_, err := svc.UpdateTenantSuper(biz.WithIdentity(context.Background(), auth), &tenantv1.UpdateTenantSuperRequest{
		Tenant: &tenantv1.Tenant{
			Id:        "t-demo-01",
			Status:    &one,
			Code:      "hijacked",
			Name:      "租户",
			ParentId:  &forgedParent,
			TenantId:  "t-forged-tenant",
			ExpiredAt: nil,
		},
	})
	if err != nil {
		t.Fatalf("update tenant: %v", err)
	}
	if repo.updated == nil {
		t.Fatal("repo must be called")
	}
	if repo.updated.Code != "" || repo.updated.ParentID != nil || repo.updated.TenantID != "" {
		t.Fatalf("updated identity columns = (%q, %v, %q), want all dropped",
			repo.updated.Code, repo.updated.ParentID, repo.updated.TenantID)
	}
	if repo.updated.ExpiredAt != nil {
		t.Fatalf("updated expired_at = %v, want nil (unset clears)", repo.updated.ExpiredAt)
	}
}

func TestTenantServiceListReturnsEnvelope(t *testing.T) {
	total := int64(1)
	parent := "t-super-01"
	repo := &fakeTenantRepo{list: &biz.TenantList{
		Items: []*biz.Tenant{{
			ID: "t-demo-01", Status: biz.TenantStatusEnabled, Code: "demo", Name: "演示租户",
			ParentID: &parent, TenantID: "t-super-01",
		}},
		Total:      &total,
		HasMore:    true,
		NextCursor: "cur",
	}}
	svc := NewTenantService(biz.NewTenantUsecase(repo))
	auth := biz.Identity{TenantID: "t-super-01", UserID: 7}
	page := int32(2)

	out, err := svc.ListTenantsSuper(biz.WithIdentity(context.Background(), auth), &tenantv1.ListTenantsSuperRequest{
		Query: &queryv1.Query{Pagination: &queryv1.Pagination{Page: &page}},
	})
	if err != nil {
		t.Fatalf("list tenants: %v", err)
	}
	if len(out.GetItems()) != 1 || out.GetItems()[0].GetId() != "t-demo-01" {
		t.Fatalf("items = %+v, want single id t-demo-01", out.GetItems())
	}
	if out.GetItems()[0].GetParentId() != parent || out.GetItems()[0].GetTenantId() != "t-super-01" {
		t.Fatalf("row parent/tenant = (%q, %q), want (%q, t-super-01)",
			out.GetItems()[0].GetParentId(), out.GetItems()[0].GetTenantId(), parent)
	}
	if out.GetTotal() != 1 || !out.GetHasMore() || out.GetNextCursor() != "cur" || out.GetPage() != 2 {
		t.Fatalf("envelope = (%d, %v, %q, %d), want (1, true, cur, 2)",
			out.GetTotal(), out.GetHasMore(), out.GetNextCursor(), out.GetPage())
	}
}

func TestTenantServiceListRejectsInvalidQuery(t *testing.T) {
	repo := &fakeTenantRepo{}
	svc := NewTenantService(biz.NewTenantUsecase(repo))
	auth := biz.Identity{TenantID: "t-super-01", UserID: 7}
	page := int32(1)
	after := "cur"

	_, err := svc.ListTenantsSuper(biz.WithIdentity(context.Background(), auth), &tenantv1.ListTenantsSuperRequest{
		Query: &queryv1.Query{Pagination: &queryv1.Pagination{Page: &page, After: &after}},
	})
	if !errors.Is(err, biz.ErrTenantInvalidArgument) {
		t.Fatalf("err = %v, want ErrTenantInvalidArgument", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("repo calls = %d, want 0", repo.callsCount)
	}
}

func TestTenantServiceGetMapsTimestampsAndOptionals(t *testing.T) {
	created := time.Unix(1700000000, 0).UTC()
	updated := time.Unix(1700000100, 0).UTC()
	deleted := time.Unix(1700000200, 0).UTC()
	expired := time.Unix(1800000000, 0).UTC()
	parent := "t-super-01"
	contactName := "张三"
	contactPhone := "13800000000"
	maxUsers := int32(50)
	repo := &fakeTenantRepo{getOut: &biz.Tenant{
		ID: "t-demo-01", Status: biz.TenantStatusEnabled, Code: "demo", Name: "演示租户",
		ParentID: &parent, ContactName: &contactName, ContactPhone: &contactPhone,
		ExpiredAt: &expired, MaxUserCount: &maxUsers, DefaultLocale: "zh-CN",
		TenantID:  "t-super-01",
		CreatedBy: 1, UpdatedBy: 2,
		CreatedAt: created, UpdatedAt: &updated, DeletedAt: &deleted,
	}}
	svc := NewTenantService(biz.NewTenantUsecase(repo))

	out, err := svc.GetTenantSuper(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-super-01", UserID: 7}),
		&tenantv1.GetTenantSuperRequest{Id: "t-demo-01"})
	if err != nil {
		t.Fatalf("get tenant: %v", err)
	}
	if out.GetCreatedAt().AsTime() != created || out.GetUpdatedAt().AsTime() != updated || out.GetDeletedAt().AsTime() != deleted {
		t.Fatalf("timestamps = (%v, %v, %v), want (%v, %v, %v)",
			out.GetCreatedAt().AsTime(), out.GetUpdatedAt().AsTime(), out.GetDeletedAt().AsTime(), created, updated, deleted)
	}
	if out.GetExpiredAt().AsTime() != expired {
		t.Fatalf("expired_at = %v, want %v", out.GetExpiredAt().AsTime(), expired)
	}
	if out.GetParentId() != parent || out.GetContactName() != contactName || out.GetContactPhone() != contactPhone {
		t.Fatalf("optionals = (%q, %q, %q), want (%q, %q, %q)",
			out.GetParentId(), out.GetContactName(), out.GetContactPhone(), parent, contactName, contactPhone)
	}
	if out.GetMaxUserCount() != maxUsers {
		t.Fatalf("max_user_count = %d, want %d", out.GetMaxUserCount(), maxUsers)
	}
	if out.GetStatus() != biz.TenantStatusEnabled || out.GetTenantId() != "t-super-01" {
		t.Fatalf("status/tenant = (%d, %q), want (1, t-super-01)", out.GetStatus(), out.GetTenantId())
	}
}

func TestTenantServiceDeleteForwardsID(t *testing.T) {
	repo := &fakeTenantRepo{}
	svc := NewTenantService(biz.NewTenantUsecase(repo))
	auth := biz.Identity{TenantID: "t-super-01", UserID: 7}

	if _, err := svc.DeleteTenantSuper(biz.WithIdentity(context.Background(), auth), &tenantv1.DeleteTenantSuperRequest{Id: "t-demo-01"}); err != nil {
		t.Fatalf("delete tenant: %v", err)
	}
	if len(repo.deleted) != 1 || repo.deleted[0] != "t-demo-01" {
		t.Fatalf("deleted = %v, want [t-demo-01]", repo.deleted)
	}
}

func TestTenantServiceDisableRestoreForwardID(t *testing.T) {
	repo := &fakeTenantRepo{}
	svc := NewTenantService(biz.NewTenantUsecase(repo))
	auth := biz.Identity{TenantID: "t-super-01", UserID: 7}

	disabled, err := svc.DisableTenantSuper(biz.WithIdentity(context.Background(), auth), &tenantv1.DisableTenantSuperRequest{Id: "t-demo-01"})
	if err != nil {
		t.Fatalf("disable tenant: %v", err)
	}
	if disabled.GetStatus() != biz.TenantStatusDisabled {
		t.Fatalf("disabled status = %d, want 0", disabled.GetStatus())
	}
	restored, err := svc.RestoreTenantSuper(biz.WithIdentity(context.Background(), auth), &tenantv1.RestoreTenantSuperRequest{Id: "t-demo-01"})
	if err != nil {
		t.Fatalf("restore tenant: %v", err)
	}
	if restored.GetStatus() != biz.TenantStatusEnabled {
		t.Fatalf("restored status = %d, want 1", restored.GetStatus())
	}
	if len(repo.disabled) != 1 || repo.disabled[0] != "t-demo-01" {
		t.Fatalf("disabled = %v, want [t-demo-01]", repo.disabled)
	}
	if len(repo.restored) != 1 || repo.restored[0] != "t-demo-01" {
		t.Fatalf("restored = %v, want [t-demo-01]", repo.restored)
	}
}

func TestTenantServiceNilRequestsDoNotPanic(t *testing.T) {
	// 生成代码的 getter 对 nil 接收者安全：缺请求体统一落到入参校验，
	// 而不是 nil 解引用 panic。
	repo := &fakeTenantRepo{}
	svc := NewTenantService(biz.NewTenantUsecase(repo))
	auth := biz.Identity{TenantID: "t-super-01", UserID: 7}
	ctx := biz.WithIdentity(context.Background(), auth)

	if _, err := svc.CreateTenantSuper(ctx, nil); !errors.Is(err, biz.ErrTenantInvalidArgument) {
		t.Fatalf("create nil req err = %v, want ErrTenantInvalidArgument", err)
	}
	if _, err := svc.UpdateTenantSuper(ctx, nil); !errors.Is(err, biz.ErrTenantInvalidArgument) {
		t.Fatalf("update nil req err = %v, want ErrTenantInvalidArgument", err)
	}
	if _, err := svc.GetTenantSuper(ctx, nil); !errors.Is(err, biz.ErrTenantInvalidArgument) {
		t.Fatalf("get nil req err = %v, want ErrTenantInvalidArgument", err)
	}
	if _, err := svc.DeleteTenantSuper(ctx, nil); !errors.Is(err, biz.ErrTenantInvalidArgument) {
		t.Fatalf("delete nil req err = %v, want ErrTenantInvalidArgument", err)
	}
	if _, err := svc.DisableTenantSuper(ctx, nil); !errors.Is(err, biz.ErrTenantInvalidArgument) {
		t.Fatalf("disable nil req err = %v, want ErrTenantInvalidArgument", err)
	}
	if _, err := svc.RestoreTenantSuper(ctx, nil); !errors.Is(err, biz.ErrTenantInvalidArgument) {
		t.Fatalf("restore nil req err = %v, want ErrTenantInvalidArgument", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("repo calls = %d, want 0", repo.callsCount)
	}
}
