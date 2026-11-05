package biz

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

// mockTenantRepo 捕获仓储调用并按用例配置返回值：biz 只验证用例编排与
// 领域校验，不触真实存储。
type mockTenantRepo struct {
	TenantRepo

	getID     string
	getCalls  int
	getErr    error
	getOut    *Tenant
	listed    bool
	created   *Tenant
	updated   *Tenant
	deleted   []string
	disabled  []string
	restored  []string
	createErr error
	updateErr error
	deleteErr error
	statusErr error
}

func (m *mockTenantRepo) Get(_ context.Context, _ Identity, id string) (*Tenant, error) {
	m.getCalls++
	m.getID = id
	if m.getErr != nil {
		return nil, m.getErr
	}
	if m.getOut != nil {
		return m.getOut, nil
	}
	return &Tenant{ID: id}, nil
}

func (m *mockTenantRepo) List(context.Context, Identity, *dynq.Query) (*TenantList, error) {
	m.listed = true
	return &TenantList{}, nil
}

func (m *mockTenantRepo) CreateSuper(_ context.Context, _ Identity, t *Tenant) (*Tenant, error) {
	m.created = t
	if m.createErr != nil {
		return nil, m.createErr
	}
	out := *t
	out.ID = "t-0123456789abcd"
	return &out, nil
}

func (m *mockTenantRepo) UpdateSuper(_ context.Context, _ Identity, t *Tenant) (*Tenant, error) {
	m.updated = t
	if m.updateErr != nil {
		return nil, m.updateErr
	}
	return t, nil
}

func (m *mockTenantRepo) DeleteSuper(_ context.Context, _ Identity, id string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.deleted = append(m.deleted, id)
	return nil
}

func (m *mockTenantRepo) DisableSuper(_ context.Context, _ Identity, id string) (*Tenant, error) {
	if m.statusErr != nil {
		return nil, m.statusErr
	}
	m.disabled = append(m.disabled, id)
	return &Tenant{ID: id, Status: TenantStatusDisabled}, nil
}

func (m *mockTenantRepo) RestoreSuper(_ context.Context, _ Identity, id string) (*Tenant, error) {
	if m.statusErr != nil {
		return nil, m.statusErr
	}
	m.restored = append(m.restored, id)
	return &Tenant{ID: id, Status: TenantStatusEnabled}, nil
}

// tenantAuth 是租户用例测试的操作者身份（平台租户 t-super-01）。
func tenantAuth() Identity {
	return Identity{TenantID: "t-super-01", UserID: 1}
}

func TestTenantUsecaseCreateDefaultsParentStatusAndLocale(t *testing.T) {
	repo := &mockTenantRepo{}
	uc := NewTenantUsecase(repo)

	out, err := uc.CreateTenantSuper(context.Background(), tenantAuth(), &Tenant{
		Status: TenantStatusUnspecified,
		Code:   "  demo  ",
		Name:   "  演示租户  ",
	})
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	if out.ID != "t-0123456789abcd" {
		t.Fatalf("created id = %q, want server-generated id", out.ID)
	}
	if repo.created == nil {
		t.Fatal("repo must be called")
	}
	if repo.created.ParentID == nil || *repo.created.ParentID != "t-super-01" {
		t.Fatalf("parent = %v, want caller tenant t-super-01", repo.created.ParentID)
	}
	if repo.created.Status != TenantStatusEnabled {
		t.Fatalf("status = %d, want enabled", repo.created.Status)
	}
	if repo.created.Code != "demo" || repo.created.Name != "演示租户" {
		t.Fatalf("row = (%q, %q), want trimmed (demo, 演示租户)", repo.created.Code, repo.created.Name)
	}
	if repo.created.DefaultLocale != DefaultTenantLocale {
		t.Fatalf("default locale = %q, want %q", repo.created.DefaultLocale, DefaultTenantLocale)
	}
	// 父级恒为调用方自身（结构父级与行归属一致），不查仓储。
	if repo.getCalls != 0 {
		t.Fatalf("parent lookup calls = %d, want 0 (parent must equal caller)", repo.getCalls)
	}
}

func TestTenantUsecaseCreateKeepsExplicitValues(t *testing.T) {
	repo := &mockTenantRepo{}
	uc := NewTenantUsecase(repo)
	parent := tenantAuth().TenantID
	contactName := "张三"
	contactPhone := "13800000000"
	expired := time.Unix(1800000000, 0).UTC()
	maxUsers := int32(1)
	zero := int32(0)

	out, err := uc.CreateTenantSuper(context.Background(), tenantAuth(), &Tenant{
		Status:        zero,
		Code:          "sub",
		Name:          "子租户",
		ParentID:      &parent,
		ContactName:   &contactName,
		ContactPhone:  &contactPhone,
		ExpiredAt:     &expired,
		MaxUserCount:  &maxUsers,
		DefaultLocale: "en-US",
	})
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	if out == nil || repo.created == nil {
		t.Fatal("repo must be called")
	}
	if repo.created.Status != TenantStatusDisabled {
		t.Fatalf("status = %d, want disabled (explicit 0)", repo.created.Status)
	}
	if repo.created.ParentID == nil || *repo.created.ParentID != parent {
		t.Fatalf("parent = %v, want %q", repo.created.ParentID, parent)
	}
	if repo.created.ContactName == nil || *repo.created.ContactName != contactName ||
		repo.created.ContactPhone == nil || *repo.created.ContactPhone != contactPhone {
		t.Fatalf("contact = (%v, %v), want (%q, %q)",
			repo.created.ContactName, repo.created.ContactPhone, contactName, contactPhone)
	}
	if repo.created.ExpiredAt == nil || !repo.created.ExpiredAt.Equal(expired) {
		t.Fatalf("expired_at = %v, want %v", repo.created.ExpiredAt, expired)
	}
	if repo.created.MaxUserCount == nil || *repo.created.MaxUserCount != maxUsers {
		t.Fatalf("max_user_count = %v, want %d", repo.created.MaxUserCount, maxUsers)
	}
	if repo.created.DefaultLocale != "en-US" {
		t.Fatalf("default locale = %q, want en-US", repo.created.DefaultLocale)
	}
}

func TestTenantUsecaseCreateAcceptsColumnWidthBoundaries(t *testing.T) {
	// code 32 / name 256 / contact_name 64 / contact_phone 32 /
	// default_locale 10（合法 BCP-47 形态）/ max_user_count 1 均在界内。
	repo := &mockTenantRepo{}
	uc := NewTenantUsecase(repo)
	contactName := strings.Repeat("a", 64)
	contactPhone := strings.Repeat("1", 32)
	maxUsers := int32(1)

	if _, err := uc.CreateTenantSuper(context.Background(), tenantAuth(), &Tenant{
		Status:       TenantStatusUnspecified,
		Code:         strings.Repeat("c", 32),
		Name:         strings.Repeat("n", 256),
		ContactName:  &contactName,
		ContactPhone: &contactPhone,
		MaxUserCount: &maxUsers,
		// 2 + 1 + 7 = 10 字符，合法 BCP-47 形态。
		DefaultLocale: "zh-abcde1",
	}); err != nil {
		t.Fatalf("create tenant at boundaries: %v", err)
	}
	if repo.created == nil {
		t.Fatal("repo must be called")
	}
}

func TestTenantUsecaseCreateRejectsInvalidInput(t *testing.T) {
	tooLongCode := strings.Repeat("c", 33)
	tooLongName := strings.Repeat("n", 257)
	tooLongContact := strings.Repeat("a", 65)
	tooLongPhone := strings.Repeat("1", 33)
	zero := int32(0)
	negative := int32(-1)
	badStatus := int32(2)
	cases := []struct {
		name string
		in   *Tenant
	}{
		{"nil", nil},
		{"blank code", &Tenant{Code: "  ", Name: "租户"}},
		{"code over width", &Tenant{Code: tooLongCode, Name: "租户"}},
		{"blank name", &Tenant{Code: "demo", Name: "  "}},
		{"name over width", &Tenant{Code: "demo", Name: tooLongName}},
		{"contact over width", &Tenant{Code: "demo", Name: "租户", ContactName: &tooLongContact}},
		{"phone over width", &Tenant{Code: "demo", Name: "租户", ContactPhone: &tooLongPhone}},
		{"locale over width", &Tenant{Code: "demo", Name: "租户", DefaultLocale: "zh-abcdefg1"}},
		{"locale malformed underscore", &Tenant{Code: "demo", Name: "租户", DefaultLocale: "zh_CN"}},
		{"locale malformed single letter", &Tenant{Code: "demo", Name: "租户", DefaultLocale: "x"}},
		{"locale malformed leading dash", &Tenant{Code: "demo", Name: "租户", DefaultLocale: "-en"}},
		{"max users zero", &Tenant{Code: "demo", Name: "租户", MaxUserCount: &zero}},
		{"max users negative", &Tenant{Code: "demo", Name: "租户", MaxUserCount: &negative}},
		{"invalid status", &Tenant{Code: "demo", Name: "租户", Status: badStatus}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &mockTenantRepo{}
			if _, err := NewTenantUsecase(repo).CreateTenantSuper(context.Background(), tenantAuth(), tc.in); !errors.Is(err, ErrTenantInvalidArgument) {
				t.Fatalf("err = %v, want ErrTenantInvalidArgument", err)
			}
			if repo.getCalls != 0 || repo.created != nil {
				t.Fatalf("repo must not be called: get=%d created=%+v", repo.getCalls, repo.created)
			}
		})
	}
}

func TestTenantUsecaseCreateRejectsForeignParent(t *testing.T) {
	// 结构父级必须等于调用方租户：指向其它可见租户（或任何非调用方值）
	// 一律 INVALID_ARGUMENT，不触达仓储、不写入。
	for _, parent := range []string{"t-test-02", "t-tree-outside"} {
		t.Run(parent, func(t *testing.T) {
			repo := &mockTenantRepo{}
			_, err := NewTenantUsecase(repo).CreateTenantSuper(context.Background(), tenantAuth(), &Tenant{
				Code: "demo", Name: "租户", ParentID: &parent,
			})
			if !errors.Is(err, ErrTenantInvalidArgument) {
				t.Fatalf("err = %v, want ErrTenantInvalidArgument", err)
			}
			if repo.created != nil || repo.getCalls != 0 {
				t.Fatalf("repo must not be called: created=%+v get=%d", repo.created, repo.getCalls)
			}
		})
	}
}

func TestTenantUsecaseCreateAcceptsExplicitCallerParent(t *testing.T) {
	// 显式 parent_id = 调用方租户与省略等价。
	repo := &mockTenantRepo{}
	parent := tenantAuth().TenantID
	if _, err := NewTenantUsecase(repo).CreateTenantSuper(context.Background(), tenantAuth(), &Tenant{
		Code: "demo", Name: "租户", ParentID: &parent,
	}); err != nil {
		t.Fatalf("create with caller parent: %v", err)
	}
	if repo.created == nil || repo.created.ParentID == nil || *repo.created.ParentID != parent {
		t.Fatalf("parent = %+v, want %q", repo.created, parent)
	}
}

func TestTenantUsecaseCreatePropagatesAlreadyExists(t *testing.T) {
	repo := &mockTenantRepo{createErr: ErrTenantAlreadyExists}
	_, err := NewTenantUsecase(repo).CreateTenantSuper(context.Background(), tenantAuth(), &Tenant{
		Code: "demo", Name: "租户",
	})
	if !errors.Is(err, ErrTenantAlreadyExists) {
		t.Fatalf("err = %v, want ErrTenantAlreadyExists", err)
	}
}

func TestTenantUsecaseGetRejectsBlankID(t *testing.T) {
	repo := &mockTenantRepo{}
	uc := NewTenantUsecase(repo)
	for _, id := range []string{"", "   "} {
		if _, err := uc.GetTenantSuper(context.Background(), tenantAuth(), id); !errors.Is(err, ErrTenantInvalidArgument) {
			t.Fatalf("get %q err = %v, want ErrTenantInvalidArgument", id, err)
		}
	}
	if repo.getCalls != 0 {
		t.Fatalf("repo get calls = %d, want 0", repo.getCalls)
	}
}

func TestTenantUsecaseGetPropagatesNotFound(t *testing.T) {
	repo := &mockTenantRepo{getErr: ErrTenantNotFound}
	_, err := NewTenantUsecase(repo).GetTenantSuper(context.Background(), tenantAuth(), "t-test-02")
	if !errors.Is(err, ErrTenantNotFound) {
		t.Fatalf("err = %v, want ErrTenantNotFound", err)
	}
}

func TestTenantUsecaseUpdateRejectsMissingIDStatus(t *testing.T) {
	one := int32(1)
	zero := int32(0)
	negative := int32(-1)
	tooLongName := strings.Repeat("n", 257)
	tooLongContact := strings.Repeat("a", 65)
	tooLongPhone := strings.Repeat("1", 33)
	cases := []struct {
		name string
		in   *Tenant
	}{
		{"nil", nil},
		{"blank id", &Tenant{ID: "  ", Status: one, Code: "demo", Name: "租户"}},
		{"missing status", &Tenant{ID: "t-test-02", Status: TenantStatusUnspecified, Code: "demo", Name: "租户"}},
		{"invalid status", &Tenant{ID: "t-test-02", Status: 2, Code: "demo", Name: "租户"}},
		{"name over width", &Tenant{ID: "t-test-02", Status: one, Code: "demo", Name: tooLongName}},
		{"contact over width", &Tenant{ID: "t-test-02", Status: one, Code: "demo", Name: "租户", ContactName: &tooLongContact}},
		{"phone over width", &Tenant{ID: "t-test-02", Status: one, Code: "demo", Name: "租户", ContactPhone: &tooLongPhone}},
		{"max users zero", &Tenant{ID: "t-test-02", Status: one, Code: "demo", Name: "租户", MaxUserCount: &zero}},
		{"max users negative", &Tenant{ID: "t-test-02", Status: one, Code: "demo", Name: "租户", MaxUserCount: &negative}},
		{"locale over width", &Tenant{ID: "t-test-02", Status: one, Code: "demo", Name: "租户", DefaultLocale: "zh-abcdefg1"}},
		{"locale malformed", &Tenant{ID: "t-test-02", Status: one, Code: "demo", Name: "租户", DefaultLocale: "zh_CN"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &mockTenantRepo{}
			if _, err := NewTenantUsecase(repo).UpdateTenantSuper(context.Background(), tenantAuth(), tc.in); !errors.Is(err, ErrTenantInvalidArgument) {
				t.Fatalf("err = %v, want ErrTenantInvalidArgument", err)
			}
			if repo.updated != nil || repo.getCalls != 0 {
				t.Fatalf("repo must not be called: updated=%+v get=%d", repo.updated, repo.getCalls)
			}
		})
	}
}

func TestTenantUsecaseUpdateAcceptsColumnWidthBoundaries(t *testing.T) {
	// name 256 / contact_name 64 / contact_phone 32 / default_locale 10
	//（合法 BCP-47）与 max_user_count 1 均在界内；code 是身份列，不在
	// 更新校验范围（输入被忽略）。
	repo := &mockTenantRepo{}
	uc := NewTenantUsecase(repo)
	one := int32(1)
	maxUsers := int32(1)
	contactName := strings.Repeat("a", 64)
	contactPhone := strings.Repeat("1", 32)

	if _, err := uc.UpdateTenantSuper(context.Background(), tenantAuth(), &Tenant{
		ID:            "t-test-02",
		Status:        one,
		Name:          strings.Repeat("n", 256),
		ContactName:   &contactName,
		ContactPhone:  &contactPhone,
		MaxUserCount:  &maxUsers,
		DefaultLocale: "zh-abcde1",
	}); err != nil {
		t.Fatalf("update tenant at boundaries: %v", err)
	}
	if repo.updated == nil {
		t.Fatal("repo must be called")
	}
	if len([]rune(repo.updated.Name)) != 256 {
		t.Fatalf("name width = %d, want 256", len([]rune(repo.updated.Name)))
	}
	if repo.updated.Code != "" {
		t.Fatalf("code = %q, want empty (ignored on update)", repo.updated.Code)
	}
}

func TestTenantUsecaseUpdateIgnoresInputCode(t *testing.T) {
	// code 是身份列（同 dict code / feature flag_key）：更新输入中的
	// code 被清除，不参与校验、不进入仓储写入。
	repo := &mockTenantRepo{}
	one := int32(1)
	if _, err := NewTenantUsecase(repo).UpdateTenantSuper(context.Background(), tenantAuth(), &Tenant{
		ID: "t-test-02", Status: one, Code: "hijacked", Name: "租户",
	}); err != nil {
		t.Fatalf("update tenant: %v", err)
	}
	if repo.updated == nil {
		t.Fatal("repo must be called")
	}
	if repo.updated.Code != "" {
		t.Fatalf("repo code = %q, want empty (input ignored)", repo.updated.Code)
	}
}

func TestTenantUsecaseUpdateSelfStatusGuard(t *testing.T) {
	one := int32(1)
	zero := int32(0)
	auth := tenantAuth()

	t.Run("rejects changed status", func(t *testing.T) {
		repo := &mockTenantRepo{getOut: &Tenant{ID: auth.TenantID, Status: TenantStatusEnabled}}
		_, err := NewTenantUsecase(repo).UpdateTenantSuper(context.Background(), auth, &Tenant{
			ID: auth.TenantID, Status: zero, Code: "default", Name: "默认租户",
		})
		if !errors.Is(err, ErrTenantSelfStatusChange) {
			t.Fatalf("err = %v, want ErrTenantSelfStatusChange", err)
		}
		if !errors.Is(err, ErrTenantInvalidArgument) {
			t.Fatalf("err = %v, want errors.Is ErrTenantInvalidArgument", err)
		}
		if repo.updated != nil {
			t.Fatalf("repo must not update: %+v", repo.updated)
		}
		if repo.getCalls != 1 {
			t.Fatalf("current status lookup calls = %d, want 1", repo.getCalls)
		}
	})

	t.Run("allows same status", func(t *testing.T) {
		repo := &mockTenantRepo{getOut: &Tenant{ID: auth.TenantID, Status: TenantStatusEnabled}}
		if _, err := NewTenantUsecase(repo).UpdateTenantSuper(context.Background(), auth, &Tenant{
			ID: auth.TenantID, Status: one, Code: "default", Name: "默认租户-新",
		}); err != nil {
			t.Fatalf("update own tenant with same status: %v", err)
		}
		if repo.updated == nil || repo.updated.Name != "默认租户-新" {
			t.Fatalf("repo updated = %+v, want name changed", repo.updated)
		}
	})

	t.Run("other tenant status unaffected", func(t *testing.T) {
		repo := &mockTenantRepo{}
		if _, err := NewTenantUsecase(repo).UpdateTenantSuper(context.Background(), auth, &Tenant{
			ID: "t-test-02", Status: zero, Code: "demo", Name: "演示租户",
		}); err != nil {
			t.Fatalf("update other tenant status: %v", err)
		}
		if repo.updated == nil || repo.getCalls != 0 {
			t.Fatalf("repo = (updated %+v, get %d), want update without status lookup", repo.updated, repo.getCalls)
		}
	})
}

func TestTenantUsecaseUpdateIgnoresParentAndTenantID(t *testing.T) {
	repo := &mockTenantRepo{}
	uc := NewTenantUsecase(repo)
	one := int32(1)
	forgedParent := "t-forged"
	forgedTenant := "t-forged-tenant"

	_, err := uc.UpdateTenantSuper(context.Background(), tenantAuth(), &Tenant{
		ID:            "t-test-02",
		Status:        one,
		Code:          "  demo  ",
		Name:          "  演示租户  ",
		ParentID:      &forgedParent,
		TenantID:      forgedTenant,
		DefaultLocale: "  ",
	})
	if err != nil {
		t.Fatalf("update tenant: %v", err)
	}
	if repo.updated == nil {
		t.Fatal("repo must be called")
	}
	if repo.updated.ParentID != nil || repo.updated.TenantID != "" || repo.updated.Code != "" {
		t.Fatalf("updated identity columns = (%v, %q, %q), want all dropped",
			repo.updated.ParentID, repo.updated.TenantID, repo.updated.Code)
	}
	if repo.updated.Name != "演示租户" {
		t.Fatalf("name = %q, want trimmed 演示租户", repo.updated.Name)
	}
	if repo.updated.DefaultLocale != DefaultTenantLocale {
		t.Fatalf("default locale = %q, want %q", repo.updated.DefaultLocale, DefaultTenantLocale)
	}
	if repo.getCalls != 0 {
		t.Fatalf("update must not perform parent lookup, get calls = %d", repo.getCalls)
	}
}

func TestTenantUsecaseUpdatePropagatesNotFoundAndConflict(t *testing.T) {
	one := int32(1)
	for _, repoErr := range []error{ErrTenantNotFound, ErrTenantAlreadyExists} {
		repo := &mockTenantRepo{updateErr: repoErr}
		_, err := NewTenantUsecase(repo).UpdateTenantSuper(context.Background(), tenantAuth(), &Tenant{
			ID: "t-test-02", Status: one, Code: "demo", Name: "租户",
		})
		if !errors.Is(err, repoErr) {
			t.Fatalf("err = %v, want %v", err, repoErr)
		}
	}
}

func TestTenantUsecaseDeleteDisableRestoreForbidOwnTenant(t *testing.T) {
	auth := tenantAuth()
	for name, call := range map[string]func(*TenantUsecase, Identity) error{
		"delete": func(uc *TenantUsecase, a Identity) error {
			return uc.DeleteTenantSuper(context.Background(), a, a.TenantID)
		},
		"disable": func(uc *TenantUsecase, a Identity) error {
			_, err := uc.DisableTenantSuper(context.Background(), a, a.TenantID)
			return err
		},
		"restore": func(uc *TenantUsecase, a Identity) error {
			_, err := uc.RestoreTenantSuper(context.Background(), a, a.TenantID)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			repo := &mockTenantRepo{}
			if err := call(NewTenantUsecase(repo), auth); !errors.Is(err, ErrTenantInvalidArgument) {
				t.Fatalf("err = %v, want ErrTenantInvalidArgument", err)
			}
			if len(repo.deleted)+len(repo.disabled)+len(repo.restored) != 0 {
				t.Fatalf("repo must not be called: %+v/%+v/%+v", repo.deleted, repo.disabled, repo.restored)
			}
		})
	}
}

func TestTenantUsecaseDeleteDisableRestoreRejectBlankID(t *testing.T) {
	repo := &mockTenantRepo{}
	uc := NewTenantUsecase(repo)
	if err := uc.DeleteTenantSuper(context.Background(), tenantAuth(), "  "); !errors.Is(err, ErrTenantInvalidArgument) {
		t.Fatalf("delete err = %v, want ErrTenantInvalidArgument", err)
	}
	if _, err := uc.DisableTenantSuper(context.Background(), tenantAuth(), ""); !errors.Is(err, ErrTenantInvalidArgument) {
		t.Fatalf("disable err = %v, want ErrTenantInvalidArgument", err)
	}
	if _, err := uc.RestoreTenantSuper(context.Background(), tenantAuth(), ""); !errors.Is(err, ErrTenantInvalidArgument) {
		t.Fatalf("restore err = %v, want ErrTenantInvalidArgument", err)
	}
	if len(repo.deleted)+len(repo.disabled)+len(repo.restored) != 0 {
		t.Fatal("repo must not be called for blank ids")
	}
}

func TestTenantUsecaseDeleteDisableRestoreForwardTarget(t *testing.T) {
	repo := &mockTenantRepo{}
	uc := NewTenantUsecase(repo)
	auth := tenantAuth()

	if err := uc.DeleteTenantSuper(context.Background(), auth, "t-test-02"); err != nil {
		t.Fatalf("delete tenant: %v", err)
	}
	disabled, err := uc.DisableTenantSuper(context.Background(), auth, "t-test-02")
	if err != nil {
		t.Fatalf("disable tenant: %v", err)
	}
	if disabled.Status != TenantStatusDisabled {
		t.Fatalf("disabled status = %d, want 0", disabled.Status)
	}
	restored, err := uc.RestoreTenantSuper(context.Background(), auth, "t-test-02")
	if err != nil {
		t.Fatalf("restore tenant: %v", err)
	}
	if restored.Status != TenantStatusEnabled {
		t.Fatalf("restored status = %d, want 1", restored.Status)
	}
	if len(repo.deleted) != 1 || repo.deleted[0] != "t-test-02" {
		t.Fatalf("deleted = %v, want [t-test-02]", repo.deleted)
	}
	if len(repo.disabled) != 1 || repo.disabled[0] != "t-test-02" {
		t.Fatalf("disabled = %v, want [t-test-02]", repo.disabled)
	}
	if len(repo.restored) != 1 || repo.restored[0] != "t-test-02" {
		t.Fatalf("restored = %v, want [t-test-02]", repo.restored)
	}
}

func TestTenantUsecaseDeleteDisableRestorePropagateErrors(t *testing.T) {
	auth := tenantAuth()
	t.Run("delete has children", func(t *testing.T) {
		repo := &mockTenantRepo{deleteErr: ErrTenantHasChildren}
		if err := NewTenantUsecase(repo).DeleteTenantSuper(context.Background(), auth, "t-test-02"); !errors.Is(err, ErrTenantHasChildren) {
			t.Fatalf("err = %v, want ErrTenantHasChildren", err)
		}
	})
	t.Run("disable not found", func(t *testing.T) {
		repo := &mockTenantRepo{statusErr: ErrTenantNotFound}
		if _, err := NewTenantUsecase(repo).DisableTenantSuper(context.Background(), auth, "t-test-02"); !errors.Is(err, ErrTenantNotFound) {
			t.Fatalf("err = %v, want ErrTenantNotFound", err)
		}
	})
	t.Run("restore not found", func(t *testing.T) {
		repo := &mockTenantRepo{statusErr: ErrTenantNotFound}
		if _, err := NewTenantUsecase(repo).RestoreTenantSuper(context.Background(), auth, "t-test-02"); !errors.Is(err, ErrTenantNotFound) {
			t.Fatalf("err = %v, want ErrTenantNotFound", err)
		}
	})
}

func TestTenantUsecaseListForwardsQuery(t *testing.T) {
	repo := &mockTenantRepo{}
	if _, err := NewTenantUsecase(repo).ListTenantsSuper(context.Background(), tenantAuth(), &dynq.Query{}); err != nil {
		t.Fatalf("list tenants: %v", err)
	}
	if !repo.listed {
		t.Fatal("repo list must be called")
	}
}
