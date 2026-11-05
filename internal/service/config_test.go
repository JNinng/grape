package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	configv1 "grape/api/config/v1"
	queryv1 "grape/api/query/v1"
	"grape/internal/biz"

	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

// fakePlatformTenantGuard 恒判平台租户：服务层用例关注 DTO 转换与请求
// 校验，平台信任边界由 biz/data 用例覆盖。
type fakePlatformTenantGuard struct{}

func (fakePlatformTenantGuard) IsPlatformTenant(context.Context, biz.Identity) (bool, error) {
	return true, nil
}

// newConfigUsecase 构造放行平台守卫的全局配置用例。
func newConfigUsecase(repo biz.ConfigRepo) *biz.ConfigUsecase {
	return biz.NewConfigUsecase(repo, fakePlatformTenantGuard{})
}

// fakeConfigRepo 捕获用例转发的全局配置 DO，供断言转换与身份透传。
type fakeConfigRepo struct {
	biz.ConfigRepo

	gotAuth    biz.Identity
	callsCount int

	gotID      int64
	created    *biz.Config
	updated    *biz.Config
	deletedIDs []int64
	list       *biz.ConfigList
}

func (f *fakeConfigRepo) Get(_ context.Context, auth biz.Identity, id int64) (*biz.Config, error) {
	f.callsCount++
	f.gotAuth = auth
	f.gotID = id
	return &biz.Config{ID: id, Name: "cfg", ConfigKey: "k", ValueType: "string", Status: biz.ConfigStatusEnabled}, nil
}

func (f *fakeConfigRepo) List(_ context.Context, auth biz.Identity, _ *dynq.Query) (*biz.ConfigList, error) {
	f.callsCount++
	f.gotAuth = auth
	return f.list, nil
}

func (f *fakeConfigRepo) CreateSuper(_ context.Context, auth biz.Identity, c *biz.Config) (*biz.Config, error) {
	f.callsCount++
	f.gotAuth = auth
	f.created = c
	out := *c
	out.ID = 9
	return &out, nil
}

func (f *fakeConfigRepo) UpdateSuper(_ context.Context, auth biz.Identity, c *biz.Config) (*biz.Config, error) {
	f.callsCount++
	f.gotAuth = auth
	f.updated = c
	return c, nil
}

func (f *fakeConfigRepo) DeleteSuper(_ context.Context, auth biz.Identity, id int64) error {
	f.callsCount++
	f.gotAuth = auth
	f.deletedIDs = append(f.deletedIDs, id)
	return nil
}

// fakeTenantConfigRepo 同型替身，供租户配置服务断言。
type fakeTenantConfigRepo struct {
	biz.TenantConfigRepo

	gotAuth    biz.Identity
	callsCount int

	gotID      int64
	created    *biz.TenantConfig
	updated    *biz.TenantConfig
	deletedIDs []int64
	list       *biz.TenantConfigList
}

func (f *fakeTenantConfigRepo) Get(_ context.Context, auth biz.Identity, id int64) (*biz.TenantConfig, error) {
	f.callsCount++
	f.gotAuth = auth
	f.gotID = id
	return &biz.TenantConfig{ID: id, Name: "cfg", ConfigKey: "k", ValueType: "string", Status: biz.ConfigStatusEnabled, TenantID: auth.TenantID}, nil
}

func (f *fakeTenantConfigRepo) List(_ context.Context, auth biz.Identity, _ *dynq.Query) (*biz.TenantConfigList, error) {
	f.callsCount++
	f.gotAuth = auth
	return f.list, nil
}

func (f *fakeTenantConfigRepo) Create(_ context.Context, auth biz.Identity, c *biz.TenantConfig) (*biz.TenantConfig, error) {
	f.callsCount++
	f.gotAuth = auth
	f.created = c
	out := *c
	out.ID = 10
	out.TenantID = auth.TenantID
	return &out, nil
}

func (f *fakeTenantConfigRepo) Update(_ context.Context, auth biz.Identity, c *biz.TenantConfig) (*biz.TenantConfig, error) {
	f.callsCount++
	f.gotAuth = auth
	f.updated = c
	out := *c
	out.TenantID = auth.TenantID
	return &out, nil
}

func (f *fakeTenantConfigRepo) Delete(_ context.Context, auth biz.Identity, id int64) error {
	f.callsCount++
	f.gotAuth = auth
	f.deletedIDs = append(f.deletedIDs, id)
	return nil
}

func TestConfigServiceReadsIdentityFromContext(t *testing.T) {
	repo := &fakeConfigRepo{}
	svc := NewConfigService(newConfigUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	out, err := svc.GetConfig(biz.WithIdentity(context.Background(), auth), &configv1.GetConfigRequest{Id: 5})
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if out.GetId() != 5 {
		t.Fatalf("config id = %d, want 5", out.GetId())
	}
	if repo.callsCount != 1 || repo.gotAuth != auth {
		t.Fatalf("usecase auth = %+v (calls %d), want %+v once", repo.gotAuth, repo.callsCount, auth)
	}
}

func TestConfigServiceFailsClosedWithoutIdentity(t *testing.T) {
	repo := &fakeConfigRepo{}
	svc := NewConfigService(newConfigUsecase(repo))

	_, err := svc.GetConfig(context.Background(), &configv1.GetConfigRequest{Id: 5})
	if !errors.Is(err, biz.ErrSessionInvalid) {
		t.Fatalf("err = %v, want ErrSessionInvalid", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("repo must not be called without identity, calls = %d", repo.callsCount)
	}
}

func TestConfigServiceCreateConfigSuperDefaultsMissingStatusToEnabled(t *testing.T) {
	// optional status 缺失走未指定哨兵路径（用例落缺省启用），而非 0=禁用；
	// 仓储捕获的是用例落定后的值。
	repo := &fakeConfigRepo{}
	svc := NewConfigService(newConfigUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	_, err := svc.CreateConfigSuper(biz.WithIdentity(context.Background(), auth), &configv1.CreateConfigSuperRequest{
		Config: &configv1.Config{Name: "超时", ConfigKey: "session.timeout", ValueType: "integer"},
	})
	if err != nil {
		t.Fatalf("create config: %v", err)
	}
	if repo.created == nil || repo.created.Status != biz.ConfigStatusEnabled {
		t.Fatalf("repo status = %+v, want enabled", repo.created)
	}
}

func TestConfigServiceCreateConfigSuperKeepsExplicitDisabled(t *testing.T) {
	zero := int32(0)
	repo := &fakeConfigRepo{}
	svc := NewConfigService(newConfigUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	_, err := svc.CreateConfigSuper(biz.WithIdentity(context.Background(), auth), &configv1.CreateConfigSuperRequest{
		Config: &configv1.Config{Name: "paused", ConfigKey: "paused", ValueType: "string", Status: &zero},
	})
	if err != nil {
		t.Fatalf("create config: %v", err)
	}
	if repo.created == nil || repo.created.Status != biz.ConfigStatusDisabled {
		t.Fatalf("repo status = %+v, want disabled", repo.created)
	}
}

func TestConfigServiceUpdateConfigSuperRequiresStatus(t *testing.T) {
	repo := &fakeConfigRepo{}
	svc := NewConfigService(newConfigUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	_, err := svc.UpdateConfigSuper(biz.WithIdentity(context.Background(), auth), &configv1.UpdateConfigSuperRequest{
		Config: &configv1.Config{Id: 1, Name: "cfg"},
	})
	if !errors.Is(err, biz.ErrConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrConfigInvalidArgument", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("repo must not be called, calls = %d", repo.callsCount)
	}
}

func TestConfigServiceUpdateConfigSuperDropsIdentityColumns(t *testing.T) {
	// config_key/value_type 是身份/值语义列：更新输入中被忽略，转发的
	// DO 不携带它们。
	repo := &fakeConfigRepo{}
	svc := NewConfigService(newConfigUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}
	one := int32(1)

	_, err := svc.UpdateConfigSuper(biz.WithIdentity(context.Background(), auth), &configv1.UpdateConfigSuperRequest{
		Config: &configv1.Config{Id: 1, Name: "cfg", ConfigKey: "hacked", ValueType: "json", Status: &one},
	})
	if err != nil {
		t.Fatalf("update config: %v", err)
	}
	if repo.updated == nil {
		t.Fatal("repo must be called")
	}
	if repo.updated.ConfigKey != "" || repo.updated.ValueType != "" {
		t.Fatalf("repo updated = (%q, %q), want identity columns dropped", repo.updated.ConfigKey, repo.updated.ValueType)
	}
	if repo.updated.Name != "cfg" {
		t.Fatalf("repo updated name = %q, want cfg", repo.updated.Name)
	}
}

func TestConfigServiceListConfigsReturnsEnvelope(t *testing.T) {
	total := int64(1)
	repo := &fakeConfigRepo{list: &biz.ConfigList{
		Items:      []*biz.Config{{ID: 5, Name: "cfg", ConfigKey: "k", ValueType: "string", Status: biz.ConfigStatusEnabled, CreatedAt: time.Unix(1700000000, 0).UTC()}},
		Total:      &total,
		HasMore:    true,
		NextCursor: "cur",
	}}
	svc := NewConfigService(newConfigUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}
	page := int32(2)

	out, err := svc.ListConfigs(biz.WithIdentity(context.Background(), auth), &configv1.ListConfigsRequest{
		Query: &queryv1.Query{Pagination: &queryv1.Pagination{Page: &page}},
	})
	if err != nil {
		t.Fatalf("list configs: %v", err)
	}
	if len(out.GetItems()) != 1 || out.GetItems()[0].GetId() != 5 {
		t.Fatalf("items = %+v, want single id 5", out.GetItems())
	}
	if out.GetTotal() != 1 {
		t.Fatalf("total = %d, want 1", out.GetTotal())
	}
	if !out.GetHasMore() || out.GetNextCursor() != "cur" {
		t.Fatalf("has_more/next_cursor = %v/%q, want true/cur", out.GetHasMore(), out.GetNextCursor())
	}
	if out.GetPage() != 2 {
		t.Fatalf("page = %d, want 2", out.GetPage())
	}
}

func TestConfigServiceListConfigsRejectsInvalidQuery(t *testing.T) {
	// page 与 cursor 互斥由传输层协议校验拒绝，仓储不得被触达。
	repo := &fakeConfigRepo{}
	svc := NewConfigService(newConfigUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}
	page := int32(1)
	after := "cur"

	_, err := svc.ListConfigs(biz.WithIdentity(context.Background(), auth), &configv1.ListConfigsRequest{
		Query: &queryv1.Query{Pagination: &queryv1.Pagination{Page: &page, After: &after}},
	})
	if !errors.Is(err, biz.ErrConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrConfigInvalidArgument", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("repo calls = %d, want 0", repo.callsCount)
	}
}

func TestConfigServiceGetConfigMapsTimestamps(t *testing.T) {
	created := time.Unix(1700000000, 0).UTC()
	updated := time.Unix(1700000100, 0).UTC()
	svc := NewConfigService(newConfigUsecase(&tsFakeConfigRepo{created: created, updated: &updated}))

	out, err := svc.GetConfig(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}), &configv1.GetConfigRequest{Id: 3})
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if out.GetCreatedAt().AsTime() != created {
		t.Fatalf("created_at = %v, want %v", out.GetCreatedAt().AsTime(), created)
	}
	if out.GetUpdatedAt().AsTime() != updated {
		t.Fatalf("updated_at = %v, want %v", out.GetUpdatedAt().AsTime(), updated)
	}
}

// tsFakeConfigRepo 返回携带精确时间戳的全局配置，供 DTO 时间映射断言。
type tsFakeConfigRepo struct {
	fakeConfigRepo
	created time.Time
	updated *time.Time
}

func (f *tsFakeConfigRepo) Get(context.Context, biz.Identity, int64) (*biz.Config, error) {
	return &biz.Config{ID: 3, Name: "cfg", ConfigKey: "k", ValueType: "string", Status: biz.ConfigStatusEnabled,
		CreatedAt: f.created, UpdatedAt: f.updated}, nil
}

func TestConfigServiceDeleteConfigSuperForwardsID(t *testing.T) {
	repo := &fakeConfigRepo{}
	svc := NewConfigService(newConfigUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	_, err := svc.DeleteConfigSuper(biz.WithIdentity(context.Background(), auth), &configv1.DeleteConfigSuperRequest{Id: 12})
	if err != nil {
		t.Fatalf("delete config: %v", err)
	}
	if len(repo.deletedIDs) != 1 || repo.deletedIDs[0] != 12 {
		t.Fatalf("deleted = %+v, want [12]", repo.deletedIDs)
	}
}

func TestTenantConfigServiceReadsIdentityFromContext(t *testing.T) {
	repo := &fakeTenantConfigRepo{}
	svc := NewTenantConfigService(biz.NewTenantConfigUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	out, err := svc.GetTenantConfig(biz.WithIdentity(context.Background(), auth), &configv1.GetTenantConfigRequest{Id: 5})
	if err != nil {
		t.Fatalf("get tenant config: %v", err)
	}
	if out.GetId() != 5 || out.GetTenantId() != "t-1" {
		t.Fatalf("tenant config = (%d, %q), want (5, t-1)", out.GetId(), out.GetTenantId())
	}
	if repo.callsCount != 1 || repo.gotAuth != auth {
		t.Fatalf("usecase auth = %+v (calls %d), want %+v once", repo.gotAuth, repo.callsCount, auth)
	}
}

func TestTenantConfigServiceFailsClosedWithoutIdentity(t *testing.T) {
	repo := &fakeTenantConfigRepo{}
	svc := NewTenantConfigService(biz.NewTenantConfigUsecase(repo))

	_, err := svc.GetTenantConfig(context.Background(), &configv1.GetTenantConfigRequest{Id: 5})
	if !errors.Is(err, biz.ErrSessionInvalid) {
		t.Fatalf("err = %v, want ErrSessionInvalid", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("repo must not be called without identity, calls = %d", repo.callsCount)
	}
}

func TestTenantConfigServiceCreateTenantConfigIgnoresTenantID(t *testing.T) {
	// tenant_id 只读：请求中的值不进入 DO，仓储按认证身份落值。
	repo := &fakeTenantConfigRepo{}
	svc := NewTenantConfigService(biz.NewTenantConfigUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	out, err := svc.CreateTenantConfig(biz.WithIdentity(context.Background(), auth), &configv1.CreateTenantConfigRequest{
		TenantConfig: &configv1.TenantConfig{Name: "超时", ConfigKey: "session.timeout", ValueType: "integer", TenantId: "t-forged"},
	})
	if err != nil {
		t.Fatalf("create tenant config: %v", err)
	}
	if repo.created == nil || repo.created.TenantID != "" {
		t.Fatalf("repo created tenant id = %+v, want empty (forged value dropped)", repo.created)
	}
	if repo.created.Status != biz.ConfigStatusEnabled {
		t.Fatalf("repo created status = %d, want enabled", repo.created.Status)
	}
	if out.GetTenantId() != "t-1" {
		t.Fatalf("out tenant id = %q, want t-1", out.GetTenantId())
	}
}

func TestTenantConfigServiceUpdateTenantConfigRequiresStatus(t *testing.T) {
	repo := &fakeTenantConfigRepo{}
	svc := NewTenantConfigService(biz.NewTenantConfigUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	_, err := svc.UpdateTenantConfig(biz.WithIdentity(context.Background(), auth), &configv1.UpdateTenantConfigRequest{
		TenantConfig: &configv1.TenantConfig{Id: 1, Name: "cfg"},
	})
	if !errors.Is(err, biz.ErrTenantConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrTenantConfigInvalidArgument", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("repo must not be called, calls = %d", repo.callsCount)
	}
}

func TestTenantConfigServiceUpdateTenantConfigDropsIdentityColumns(t *testing.T) {
	repo := &fakeTenantConfigRepo{}
	svc := NewTenantConfigService(biz.NewTenantConfigUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}
	one := int32(1)

	_, err := svc.UpdateTenantConfig(biz.WithIdentity(context.Background(), auth), &configv1.UpdateTenantConfigRequest{
		TenantConfig: &configv1.TenantConfig{
			Id: 1, Name: "cfg", ConfigKey: "hacked", ValueType: "json", TenantId: "t-forged", Status: &one,
		},
	})
	if err != nil {
		t.Fatalf("update tenant config: %v", err)
	}
	if repo.updated == nil {
		t.Fatal("repo must be called")
	}
	if repo.updated.ConfigKey != "" || repo.updated.ValueType != "" || repo.updated.TenantID != "" {
		t.Fatalf("repo updated = (%q, %q, %q), want identity columns dropped",
			repo.updated.ConfigKey, repo.updated.ValueType, repo.updated.TenantID)
	}
	if repo.updated.Name != "cfg" {
		t.Fatalf("repo updated name = %q, want cfg", repo.updated.Name)
	}
}

func TestTenantConfigServiceListTenantConfigsReturnsEnvelope(t *testing.T) {
	total := int64(1)
	repo := &fakeTenantConfigRepo{list: &biz.TenantConfigList{
		Items:      []*biz.TenantConfig{{ID: 5, Name: "cfg", ConfigKey: "k", ValueType: "string", Status: biz.ConfigStatusEnabled, TenantID: "t-1"}},
		Total:      &total,
		HasMore:    false,
		NextCursor: "",
	}}
	svc := NewTenantConfigService(biz.NewTenantConfigUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	out, err := svc.ListTenantConfigs(biz.WithIdentity(context.Background(), auth), &configv1.ListTenantConfigsRequest{})
	if err != nil {
		t.Fatalf("list tenant configs: %v", err)
	}
	if len(out.GetItems()) != 1 || out.GetItems()[0].GetTenantId() != "t-1" {
		t.Fatalf("items = %+v, want single tenant row", out.GetItems())
	}
	if out.GetTotal() != 1 {
		t.Fatalf("total = %d, want 1", out.GetTotal())
	}
}

func TestTenantConfigServiceListTenantConfigsRejectsInvalidQuery(t *testing.T) {
	repo := &fakeTenantConfigRepo{}
	svc := NewTenantConfigService(biz.NewTenantConfigUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}
	page := int32(1)
	after := "cur"

	_, err := svc.ListTenantConfigs(biz.WithIdentity(context.Background(), auth), &configv1.ListTenantConfigsRequest{
		Query: &queryv1.Query{Pagination: &queryv1.Pagination{Page: &page, After: &after}},
	})
	if !errors.Is(err, biz.ErrTenantConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrTenantConfigInvalidArgument", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("repo calls = %d, want 0", repo.callsCount)
	}
}

func TestTenantConfigServiceDeleteTenantConfigForwardsID(t *testing.T) {
	repo := &fakeTenantConfigRepo{}
	svc := NewTenantConfigService(biz.NewTenantConfigUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	_, err := svc.DeleteTenantConfig(biz.WithIdentity(context.Background(), auth), &configv1.DeleteTenantConfigRequest{Id: 12})
	if err != nil {
		t.Fatalf("delete tenant config: %v", err)
	}
	if len(repo.deletedIDs) != 1 || repo.deletedIDs[0] != 12 {
		t.Fatalf("deleted = %+v, want [12]", repo.deletedIDs)
	}
}

func TestConfigServiceCreateConfigSuperRejectsMissingConfig(t *testing.T) {
	repo := &fakeConfigRepo{}
	svc := NewConfigService(newConfigUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	_, err := svc.CreateConfigSuper(biz.WithIdentity(context.Background(), auth), &configv1.CreateConfigSuperRequest{})
	if !errors.Is(err, biz.ErrConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrConfigInvalidArgument", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("repo must not be called, calls = %d", repo.callsCount)
	}
}

func TestConfigServiceUpdateConfigSuperRejectsMissingConfig(t *testing.T) {
	repo := &fakeConfigRepo{}
	svc := NewConfigService(newConfigUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	_, err := svc.UpdateConfigSuper(biz.WithIdentity(context.Background(), auth), &configv1.UpdateConfigSuperRequest{})
	if !errors.Is(err, biz.ErrConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrConfigInvalidArgument", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("repo must not be called, calls = %d", repo.callsCount)
	}
}

func TestTenantConfigServiceCreateTenantConfigRejectsMissingConfig(t *testing.T) {
	repo := &fakeTenantConfigRepo{}
	svc := NewTenantConfigService(biz.NewTenantConfigUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	_, err := svc.CreateTenantConfig(biz.WithIdentity(context.Background(), auth), &configv1.CreateTenantConfigRequest{})
	if !errors.Is(err, biz.ErrTenantConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrTenantConfigInvalidArgument", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("repo must not be called, calls = %d", repo.callsCount)
	}
}

func TestTenantConfigServiceUpdateTenantConfigRejectsMissingConfig(t *testing.T) {
	repo := &fakeTenantConfigRepo{}
	svc := NewTenantConfigService(biz.NewTenantConfigUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	_, err := svc.UpdateTenantConfig(biz.WithIdentity(context.Background(), auth), &configv1.UpdateTenantConfigRequest{})
	if !errors.Is(err, biz.ErrTenantConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrTenantConfigInvalidArgument", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("repo must not be called, calls = %d", repo.callsCount)
	}
}

func TestConfigServiceCreateConfigSuperPreservesExplicitEmptyValue(t *testing.T) {
	// value 的空串是有效配置值（与「未设置」不同），不得折成 NULL。
	empty := ""
	repo := &fakeConfigRepo{}
	svc := NewConfigService(newConfigUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	_, err := svc.CreateConfigSuper(biz.WithIdentity(context.Background(), auth), &configv1.CreateConfigSuperRequest{
		Config: &configv1.Config{Name: "空值", ConfigKey: "test.empty", ValueType: "string", Value: &empty},
	})
	if err != nil {
		t.Fatalf("create config: %v", err)
	}
	if repo.created == nil || repo.created.Value == nil || *repo.created.Value != "" {
		t.Fatalf("repo value = %+v, want explicit empty string", repo.created)
	}
}

func TestConfigServiceCreateConfigSuperPreservesWhitespaceOnlyValue(t *testing.T) {
	// value 是原样透传的配置内容（不做 trim/空白折叠）：纯空白串与空串、
	// 未设置是三种不同输入，服务端不得归一化。
	blank := "   "
	repo := &fakeConfigRepo{}
	svc := NewConfigService(newConfigUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	_, err := svc.CreateConfigSuper(biz.WithIdentity(context.Background(), auth), &configv1.CreateConfigSuperRequest{
		Config: &configv1.Config{Name: "空白值", ConfigKey: "test.blank", ValueType: "string", Value: &blank},
	})
	if err != nil {
		t.Fatalf("create config: %v", err)
	}
	if repo.created == nil || repo.created.Value == nil || *repo.created.Value != "   " {
		t.Fatalf("repo value = %+v, want whitespace-only value unchanged", repo.created)
	}
}

func TestConfigServiceUpdateConfigSuperPreservesWhitespaceOnlyValue(t *testing.T) {
	// value 原样透传口径与创建一致：更新时纯空白串同样不归一化。
	blank := "   "
	repo := &fakeConfigRepo{}
	svc := NewConfigService(newConfigUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}
	one := int32(1)

	_, err := svc.UpdateConfigSuper(biz.WithIdentity(context.Background(), auth), &configv1.UpdateConfigSuperRequest{
		Config: &configv1.Config{Id: 1, Name: "空白值", Status: &one, Value: &blank},
	})
	if err != nil {
		t.Fatalf("update config: %v", err)
	}
	if repo.updated == nil || repo.updated.Value == nil || *repo.updated.Value != "   " {
		t.Fatalf("repo value = %+v, want whitespace-only value unchanged", repo.updated)
	}
}

func TestTenantConfigServicePreservesWhitespaceOnlyValue(t *testing.T) {
	blank := "   "
	auth := biz.Identity{TenantID: "t-1", UserID: 7}
	one := int32(1)

	t.Run("create", func(t *testing.T) {
		repo := &fakeTenantConfigRepo{}
		svc := NewTenantConfigService(biz.NewTenantConfigUsecase(repo))

		_, err := svc.CreateTenantConfig(biz.WithIdentity(context.Background(), auth), &configv1.CreateTenantConfigRequest{
			TenantConfig: &configv1.TenantConfig{Name: "空白值", ConfigKey: "test.blank", ValueType: "string", Value: &blank},
		})
		if err != nil {
			t.Fatalf("create tenant config: %v", err)
		}
		if repo.created == nil || repo.created.Value == nil || *repo.created.Value != "   " {
			t.Fatalf("repo value = %+v, want whitespace-only value unchanged", repo.created)
		}
	})
	t.Run("update", func(t *testing.T) {
		repo := &fakeTenantConfigRepo{}
		svc := NewTenantConfigService(biz.NewTenantConfigUsecase(repo))

		_, err := svc.UpdateTenantConfig(biz.WithIdentity(context.Background(), auth), &configv1.UpdateTenantConfigRequest{
			TenantConfig: &configv1.TenantConfig{Id: 1, Name: "空白值", Status: &one, Value: &blank},
		})
		if err != nil {
			t.Fatalf("update tenant config: %v", err)
		}
		if repo.updated == nil || repo.updated.Value == nil || *repo.updated.Value != "   " {
			t.Fatalf("repo value = %+v, want whitespace-only value unchanged", repo.updated)
		}
	})
}

func TestConfigServiceCreateConfigSuperKeepsUnsetValueNil(t *testing.T) {
	repo := &fakeConfigRepo{}
	svc := NewConfigService(newConfigUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	_, err := svc.CreateConfigSuper(biz.WithIdentity(context.Background(), auth), &configv1.CreateConfigSuperRequest{
		Config: &configv1.Config{Name: "未设置", ConfigKey: "test.unset", ValueType: "string"},
	})
	if err != nil {
		t.Fatalf("create config: %v", err)
	}
	if repo.created == nil || repo.created.Value != nil {
		t.Fatalf("repo value = %+v, want nil", repo.created)
	}
}

func TestConfigServiceUpdateConfigSuperPreservesExplicitEmptyValue(t *testing.T) {
	empty := ""
	repo := &fakeConfigRepo{}
	svc := NewConfigService(newConfigUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}
	one := int32(1)

	_, err := svc.UpdateConfigSuper(biz.WithIdentity(context.Background(), auth), &configv1.UpdateConfigSuperRequest{
		Config: &configv1.Config{Id: 1, Name: "清空", Status: &one, Value: &empty},
	})
	if err != nil {
		t.Fatalf("update config: %v", err)
	}
	if repo.updated == nil || repo.updated.Value == nil || *repo.updated.Value != "" {
		t.Fatalf("repo value = %+v, want explicit empty string", repo.updated)
	}
}

func TestTenantConfigServiceCreateTenantConfigPreservesExplicitEmptyValue(t *testing.T) {
	empty := ""
	repo := &fakeTenantConfigRepo{}
	svc := NewTenantConfigService(biz.NewTenantConfigUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	_, err := svc.CreateTenantConfig(biz.WithIdentity(context.Background(), auth), &configv1.CreateTenantConfigRequest{
		TenantConfig: &configv1.TenantConfig{Name: "空值", ConfigKey: "test.empty", ValueType: "string", Value: &empty},
	})
	if err != nil {
		t.Fatalf("create tenant config: %v", err)
	}
	if repo.created == nil || repo.created.Value == nil || *repo.created.Value != "" {
		t.Fatalf("repo value = %+v, want explicit empty string", repo.created)
	}
}

// fakeFeatureFlagRepo 捕获用例转发的功能开关 DO，供断言转换与身份透传。
type fakeFeatureFlagRepo struct {
	biz.FeatureFlagRepo

	gotAuth    biz.Identity
	callsCount int

	gotID      int64
	created    *biz.FeatureFlag
	updated    *biz.FeatureFlag
	deletedIDs []int64
	list       *biz.FeatureFlagList
}

func (f *fakeFeatureFlagRepo) Get(_ context.Context, auth biz.Identity, id int64) (*biz.FeatureFlag, error) {
	f.callsCount++
	f.gotAuth = auth
	f.gotID = id
	return &biz.FeatureFlag{ID: id, Name: "flag", FlagKey: "k", Status: biz.ConfigStatusEnabled, TenantID: auth.TenantID}, nil
}

func (f *fakeFeatureFlagRepo) List(_ context.Context, auth biz.Identity, _ *dynq.Query) (*biz.FeatureFlagList, error) {
	f.callsCount++
	f.gotAuth = auth
	return f.list, nil
}

func (f *fakeFeatureFlagRepo) Create(_ context.Context, auth biz.Identity, in *biz.FeatureFlag) (*biz.FeatureFlag, error) {
	f.callsCount++
	f.gotAuth = auth
	f.created = in
	out := *in
	out.ID = 9
	out.TenantID = auth.TenantID
	return &out, nil
}

func (f *fakeFeatureFlagRepo) Update(_ context.Context, auth biz.Identity, in *biz.FeatureFlag) (*biz.FeatureFlag, error) {
	f.callsCount++
	f.gotAuth = auth
	f.updated = in
	out := *in
	out.TenantID = auth.TenantID
	return &out, nil
}

func (f *fakeFeatureFlagRepo) Delete(_ context.Context, auth biz.Identity, id int64) error {
	f.callsCount++
	f.gotAuth = auth
	f.deletedIDs = append(f.deletedIDs, id)
	return nil
}

// fakeTranslationRepo 捕获用例转发的翻译 DO，供断言转换与身份透传。
type fakeTranslationRepo struct {
	biz.TranslationRepo

	gotAuth    biz.Identity
	callsCount int

	gotID      int64
	created    *biz.Translation
	updated    *biz.Translation
	deletedIDs []int64
	list       *biz.TranslationList
}

func (f *fakeTranslationRepo) Get(_ context.Context, auth biz.Identity, id int64) (*biz.Translation, error) {
	f.callsCount++
	f.gotAuth = auth
	f.gotID = id
	return &biz.Translation{ID: id, Status: biz.ConfigStatusEnabled, TableName: "sys_menu", RowID: 1,
		ColumnName: "name", Locale: "en-US", Value: "v", TenantID: auth.TenantID}, nil
}

func (f *fakeTranslationRepo) List(_ context.Context, auth biz.Identity, _ *dynq.Query) (*biz.TranslationList, error) {
	f.callsCount++
	f.gotAuth = auth
	return f.list, nil
}

func (f *fakeTranslationRepo) Create(_ context.Context, auth biz.Identity, in *biz.Translation) (*biz.Translation, error) {
	f.callsCount++
	f.gotAuth = auth
	f.created = in
	out := *in
	out.ID = 10
	out.TenantID = auth.TenantID
	return &out, nil
}

func (f *fakeTranslationRepo) Update(_ context.Context, auth biz.Identity, in *biz.Translation) (*biz.Translation, error) {
	f.callsCount++
	f.gotAuth = auth
	f.updated = in
	out := *in
	out.TenantID = auth.TenantID
	return &out, nil
}

func (f *fakeTranslationRepo) Delete(_ context.Context, auth biz.Identity, id int64) error {
	f.callsCount++
	f.gotAuth = auth
	f.deletedIDs = append(f.deletedIDs, id)
	return nil
}

func TestFeatureFlagServiceReadsIdentityFromContext(t *testing.T) {
	repo := &fakeFeatureFlagRepo{}
	svc := NewFeatureFlagService(biz.NewFeatureFlagUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	out, err := svc.GetFeatureFlag(biz.WithIdentity(context.Background(), auth), &configv1.GetFeatureFlagRequest{Id: 5})
	if err != nil {
		t.Fatalf("get feature flag: %v", err)
	}
	if out.GetId() != 5 || out.GetTenantId() != "t-1" {
		t.Fatalf("feature flag = (%d, %q), want (5, t-1)", out.GetId(), out.GetTenantId())
	}
	if repo.callsCount != 1 || repo.gotAuth != auth {
		t.Fatalf("usecase auth = %+v (calls %d), want %+v once", repo.gotAuth, repo.callsCount, auth)
	}
}

func TestFeatureFlagServiceFailsClosedWithoutIdentity(t *testing.T) {
	repo := &fakeFeatureFlagRepo{}
	svc := NewFeatureFlagService(biz.NewFeatureFlagUsecase(repo))

	_, err := svc.GetFeatureFlag(context.Background(), &configv1.GetFeatureFlagRequest{Id: 5})
	if !errors.Is(err, biz.ErrSessionInvalid) {
		t.Fatalf("err = %v, want ErrSessionInvalid", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("repo must not be called without identity, calls = %d", repo.callsCount)
	}
}

func TestFeatureFlagServiceCreateDefaultsMissingStatusAndIsEnabled(t *testing.T) {
	// status 缺失走未指定哨兵路径（用例落缺省启用）；is_enabled 缺失即
	// false（optional bool 的缺省语义），不得折成 true。
	repo := &fakeFeatureFlagRepo{}
	svc := NewFeatureFlagService(biz.NewFeatureFlagUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	_, err := svc.CreateFeatureFlag(biz.WithIdentity(context.Background(), auth), &configv1.CreateFeatureFlagRequest{
		FeatureFlag: &configv1.FeatureFlag{Name: "导出", FlagKey: "export_enabled"},
	})
	if err != nil {
		t.Fatalf("create feature flag: %v", err)
	}
	if repo.created == nil || repo.created.Status != biz.ConfigStatusEnabled {
		t.Fatalf("repo status = %+v, want enabled", repo.created)
	}
	if repo.created.IsEnabled {
		t.Fatal("repo is_enabled = true, want false when optional field unset")
	}
}

func TestFeatureFlagServiceCreateKeepsExplicitIsEnabledValues(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("enabled=%v", enabled), func(t *testing.T) {
			repo := &fakeFeatureFlagRepo{}
			svc := NewFeatureFlagService(biz.NewFeatureFlagUsecase(repo))
			auth := biz.Identity{TenantID: "t-1", UserID: 7}
			value := enabled

			_, err := svc.CreateFeatureFlag(biz.WithIdentity(context.Background(), auth), &configv1.CreateFeatureFlagRequest{
				FeatureFlag: &configv1.FeatureFlag{Name: "flag", FlagKey: "k", IsEnabled: &value},
			})
			if err != nil {
				t.Fatalf("create feature flag: %v", err)
			}
			if repo.created == nil || repo.created.IsEnabled != enabled {
				t.Fatalf("repo is_enabled = %+v, want %v", repo.created, enabled)
			}
		})
	}
}

func TestFeatureFlagServiceCreateIgnoresTenantID(t *testing.T) {
	repo := &fakeFeatureFlagRepo{}
	svc := NewFeatureFlagService(biz.NewFeatureFlagUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	out, err := svc.CreateFeatureFlag(biz.WithIdentity(context.Background(), auth), &configv1.CreateFeatureFlagRequest{
		FeatureFlag: &configv1.FeatureFlag{Name: "flag", FlagKey: "k", TenantId: "t-forged"},
	})
	if err != nil {
		t.Fatalf("create feature flag: %v", err)
	}
	if repo.created == nil || repo.created.TenantID != "" {
		t.Fatalf("repo created tenant id = %+v, want empty (forged value dropped)", repo.created)
	}
	if out.GetTenantId() != "t-1" {
		t.Fatalf("out tenant id = %q, want t-1", out.GetTenantId())
	}
}

func TestFeatureFlagServiceUpdateRequiresStatus(t *testing.T) {
	repo := &fakeFeatureFlagRepo{}
	svc := NewFeatureFlagService(biz.NewFeatureFlagUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	_, err := svc.UpdateFeatureFlag(biz.WithIdentity(context.Background(), auth), &configv1.UpdateFeatureFlagRequest{
		FeatureFlag: &configv1.FeatureFlag{Id: 1, Name: "flag"},
	})
	if !errors.Is(err, biz.ErrFeatureFlagInvalidArgument) {
		t.Fatalf("err = %v, want ErrFeatureFlagInvalidArgument", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("repo must not be called, calls = %d", repo.callsCount)
	}
}

func TestFeatureFlagServiceUpdateDefaultsUnsetIsEnabledToFalse(t *testing.T) {
	// 更新是覆盖语义：optional is_enabled 未设置即 false，不得沿用旧值。
	repo := &fakeFeatureFlagRepo{}
	svc := NewFeatureFlagService(biz.NewFeatureFlagUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}
	one := int32(1)

	_, err := svc.UpdateFeatureFlag(biz.WithIdentity(context.Background(), auth), &configv1.UpdateFeatureFlagRequest{
		FeatureFlag: &configv1.FeatureFlag{Id: 1, Name: "flag", Status: &one},
	})
	if err != nil {
		t.Fatalf("update feature flag: %v", err)
	}
	if repo.updated == nil || repo.updated.IsEnabled {
		t.Fatalf("repo updated is_enabled = %+v, want false when optional field unset", repo.updated)
	}
}

func TestFeatureFlagServiceUpdateDropsIdentityColumns(t *testing.T) {
	repo := &fakeFeatureFlagRepo{}
	svc := NewFeatureFlagService(biz.NewFeatureFlagUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}
	one := int32(1)
	enabled := true

	_, err := svc.UpdateFeatureFlag(biz.WithIdentity(context.Background(), auth), &configv1.UpdateFeatureFlagRequest{
		FeatureFlag: &configv1.FeatureFlag{Id: 1, Name: "flag", FlagKey: "hacked", TenantId: "t-forged",
			Status: &one, IsEnabled: &enabled},
	})
	if err != nil {
		t.Fatalf("update feature flag: %v", err)
	}
	if repo.updated == nil {
		t.Fatal("repo must be called")
	}
	if repo.updated.FlagKey != "" || repo.updated.TenantID != "" {
		t.Fatalf("repo updated = (%q, %q), want identity columns dropped", repo.updated.FlagKey, repo.updated.TenantID)
	}
	if !repo.updated.IsEnabled {
		t.Fatal("repo updated is_enabled = false, want true")
	}
}

func TestFeatureFlagServiceListReturnsEnvelope(t *testing.T) {
	total := int64(1)
	repo := &fakeFeatureFlagRepo{list: &biz.FeatureFlagList{
		Items:      []*biz.FeatureFlag{{ID: 5, Name: "flag", FlagKey: "k", Status: biz.ConfigStatusEnabled, TenantID: "t-1"}},
		Total:      &total,
		HasMore:    true,
		NextCursor: "cur",
	}}
	svc := NewFeatureFlagService(biz.NewFeatureFlagUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}
	page := int32(2)

	out, err := svc.ListFeatureFlags(biz.WithIdentity(context.Background(), auth), &configv1.ListFeatureFlagsRequest{
		Query: &queryv1.Query{Pagination: &queryv1.Pagination{Page: &page}},
	})
	if err != nil {
		t.Fatalf("list feature flags: %v", err)
	}
	if len(out.GetItems()) != 1 || out.GetItems()[0].GetId() != 5 {
		t.Fatalf("items = %+v, want single id 5", out.GetItems())
	}
	if out.GetTotal() != 1 || !out.GetHasMore() || out.GetNextCursor() != "cur" || out.GetPage() != 2 {
		t.Fatalf("envelope = (%d, %v, %q, %d), want (1, true, cur, 2)",
			out.GetTotal(), out.GetHasMore(), out.GetNextCursor(), out.GetPage())
	}
}

func TestFeatureFlagServiceListRejectsInvalidQuery(t *testing.T) {
	repo := &fakeFeatureFlagRepo{}
	svc := NewFeatureFlagService(biz.NewFeatureFlagUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}
	page := int32(1)
	after := "cur"

	_, err := svc.ListFeatureFlags(biz.WithIdentity(context.Background(), auth), &configv1.ListFeatureFlagsRequest{
		Query: &queryv1.Query{Pagination: &queryv1.Pagination{Page: &page, After: &after}},
	})
	if !errors.Is(err, biz.ErrFeatureFlagInvalidArgument) {
		t.Fatalf("err = %v, want ErrFeatureFlagInvalidArgument", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("repo calls = %d, want 0", repo.callsCount)
	}
}

func TestFeatureFlagServiceRejectsMissingBodies(t *testing.T) {
	repo := &fakeFeatureFlagRepo{}
	svc := NewFeatureFlagService(biz.NewFeatureFlagUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}
	ctx := biz.WithIdentity(context.Background(), auth)

	if _, err := svc.CreateFeatureFlag(ctx, &configv1.CreateFeatureFlagRequest{}); !errors.Is(err, biz.ErrFeatureFlagInvalidArgument) {
		t.Fatalf("create err = %v, want ErrFeatureFlagInvalidArgument", err)
	}
	if _, err := svc.UpdateFeatureFlag(ctx, &configv1.UpdateFeatureFlagRequest{}); !errors.Is(err, biz.ErrFeatureFlagInvalidArgument) {
		t.Fatalf("update err = %v, want ErrFeatureFlagInvalidArgument", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("repo calls = %d, want 0", repo.callsCount)
	}
}

func TestFeatureFlagServiceDeleteForwardsID(t *testing.T) {
	repo := &fakeFeatureFlagRepo{}
	svc := NewFeatureFlagService(biz.NewFeatureFlagUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	_, err := svc.DeleteFeatureFlag(biz.WithIdentity(context.Background(), auth), &configv1.DeleteFeatureFlagRequest{Id: 12})
	if err != nil {
		t.Fatalf("delete feature flag: %v", err)
	}
	if len(repo.deletedIDs) != 1 || repo.deletedIDs[0] != 12 {
		t.Fatalf("deleted = %+v, want [12]", repo.deletedIDs)
	}
}

func TestTranslationServiceReadsIdentityFromContext(t *testing.T) {
	repo := &fakeTranslationRepo{}
	svc := NewTranslationService(biz.NewTranslationUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	out, err := svc.GetTranslation(biz.WithIdentity(context.Background(), auth), &configv1.GetTranslationRequest{Id: 5})
	if err != nil {
		t.Fatalf("get translation: %v", err)
	}
	if out.GetId() != 5 || out.GetTenantId() != "t-1" {
		t.Fatalf("translation = (%d, %q), want (5, t-1)", out.GetId(), out.GetTenantId())
	}
	if repo.callsCount != 1 || repo.gotAuth != auth {
		t.Fatalf("usecase auth = %+v (calls %d), want %+v once", repo.gotAuth, repo.callsCount, auth)
	}
}

func TestTranslationServiceFailsClosedWithoutIdentity(t *testing.T) {
	repo := &fakeTranslationRepo{}
	svc := NewTranslationService(biz.NewTranslationUsecase(repo))

	_, err := svc.GetTranslation(context.Background(), &configv1.GetTranslationRequest{Id: 5})
	if !errors.Is(err, biz.ErrSessionInvalid) {
		t.Fatalf("err = %v, want ErrSessionInvalid", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("repo must not be called without identity, calls = %d", repo.callsCount)
	}
}

func TestTranslationServiceCreateMapsFieldsAndIgnoresTenant(t *testing.T) {
	repo := &fakeTranslationRepo{}
	svc := NewTranslationService(biz.NewTranslationUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	out, err := svc.CreateTranslation(biz.WithIdentity(context.Background(), auth), &configv1.CreateTranslationRequest{
		Translation: &configv1.Translation{TableName: "sys_menu", RowId: 201, ColumnName: "name",
			Locale: "en-US", Value: "  Hello  ", TenantId: "t-forged"},
	})
	if err != nil {
		t.Fatalf("create translation: %v", err)
	}
	if repo.created == nil {
		t.Fatal("repo must be called")
	}
	if repo.created.Status != biz.ConfigStatusEnabled {
		t.Fatalf("repo status = %d, want enabled", repo.created.Status)
	}
	if repo.created.TenantID != "" {
		t.Fatalf("repo created tenant id = %q, want empty (forged value dropped)", repo.created.TenantID)
	}
	// value 在传输层原样透传，trim 与必填校验在用例层落地；到达仓储的
	// 是用例落定后的 trim 值。
	if repo.created.Value != "Hello" {
		t.Fatalf("repo created value = %q, want trimmed Hello", repo.created.Value)
	}
	if out.GetTenantId() != "t-1" {
		t.Fatalf("out tenant id = %q, want t-1", out.GetTenantId())
	}
}

func TestTranslationServiceCreateKeepsExplicitDisabled(t *testing.T) {
	zero := int32(0)
	repo := &fakeTranslationRepo{}
	svc := NewTranslationService(biz.NewTranslationUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	_, err := svc.CreateTranslation(biz.WithIdentity(context.Background(), auth), &configv1.CreateTranslationRequest{
		Translation: &configv1.Translation{TableName: "sys_menu", RowId: 1, ColumnName: "name",
			Locale: "en-US", Value: "v", Status: &zero},
	})
	if err != nil {
		t.Fatalf("create translation: %v", err)
	}
	if repo.created == nil || repo.created.Status != biz.ConfigStatusDisabled {
		t.Fatalf("repo status = %+v, want disabled", repo.created)
	}
}

func TestTranslationServiceUpdateRequiresStatus(t *testing.T) {
	repo := &fakeTranslationRepo{}
	svc := NewTranslationService(biz.NewTranslationUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	_, err := svc.UpdateTranslation(biz.WithIdentity(context.Background(), auth), &configv1.UpdateTranslationRequest{
		Translation: &configv1.Translation{Id: 1, Value: "v"},
	})
	if !errors.Is(err, biz.ErrTranslationInvalidArgument) {
		t.Fatalf("err = %v, want ErrTranslationInvalidArgument", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("repo must not be called, calls = %d", repo.callsCount)
	}
}

func TestTranslationServiceUpdateDropsIdentityColumns(t *testing.T) {
	repo := &fakeTranslationRepo{}
	svc := NewTranslationService(biz.NewTranslationUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}
	one := int32(1)

	_, err := svc.UpdateTranslation(biz.WithIdentity(context.Background(), auth), &configv1.UpdateTranslationRequest{
		Translation: &configv1.Translation{Id: 1, Status: &one, Value: "  New  ",
			TableName: "hacked", RowId: 999, ColumnName: "hacked", Locale: "xx-XX", TenantId: "t-forged"},
	})
	if err != nil {
		t.Fatalf("update translation: %v", err)
	}
	if repo.updated == nil {
		t.Fatal("repo must be called")
	}
	if repo.updated.TableName != "" || repo.updated.RowID != 0 ||
		repo.updated.ColumnName != "" || repo.updated.Locale != "" || repo.updated.TenantID != "" {
		t.Fatalf("repo updated = (%q, %d, %q, %q, %q), want identity columns dropped",
			repo.updated.TableName, repo.updated.RowID, repo.updated.ColumnName, repo.updated.Locale, repo.updated.TenantID)
	}
	if repo.updated.Value != "New" {
		t.Fatalf("repo updated value = %q, want trimmed New", repo.updated.Value)
	}
}

func TestTranslationServiceListReturnsEnvelope(t *testing.T) {
	total := int64(1)
	repo := &fakeTranslationRepo{list: &biz.TranslationList{
		Items: []*biz.Translation{{ID: 5, Status: biz.ConfigStatusEnabled, TableName: "sys_menu", RowID: 1,
			ColumnName: "name", Locale: "en-US", Value: "v", TenantID: "t-1"}},
		Total: &total,
	}}
	svc := NewTranslationService(biz.NewTranslationUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	out, err := svc.ListTranslations(biz.WithIdentity(context.Background(), auth), &configv1.ListTranslationsRequest{})
	if err != nil {
		t.Fatalf("list translations: %v", err)
	}
	if len(out.GetItems()) != 1 || out.GetItems()[0].GetTenantId() != "t-1" {
		t.Fatalf("items = %+v, want single tenant row", out.GetItems())
	}
	if out.GetTotal() != 1 {
		t.Fatalf("total = %d, want 1", out.GetTotal())
	}
}

func TestTranslationServiceListRejectsInvalidQuery(t *testing.T) {
	repo := &fakeTranslationRepo{}
	svc := NewTranslationService(biz.NewTranslationUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}
	page := int32(1)
	after := "cur"

	_, err := svc.ListTranslations(biz.WithIdentity(context.Background(), auth), &configv1.ListTranslationsRequest{
		Query: &queryv1.Query{Pagination: &queryv1.Pagination{Page: &page, After: &after}},
	})
	if !errors.Is(err, biz.ErrTranslationInvalidArgument) {
		t.Fatalf("err = %v, want ErrTranslationInvalidArgument", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("repo calls = %d, want 0", repo.callsCount)
	}
}

func TestTranslationServiceRejectsMissingBodies(t *testing.T) {
	repo := &fakeTranslationRepo{}
	svc := NewTranslationService(biz.NewTranslationUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}
	ctx := biz.WithIdentity(context.Background(), auth)

	if _, err := svc.CreateTranslation(ctx, &configv1.CreateTranslationRequest{}); !errors.Is(err, biz.ErrTranslationInvalidArgument) {
		t.Fatalf("create err = %v, want ErrTranslationInvalidArgument", err)
	}
	if _, err := svc.UpdateTranslation(ctx, &configv1.UpdateTranslationRequest{}); !errors.Is(err, biz.ErrTranslationInvalidArgument) {
		t.Fatalf("update err = %v, want ErrTranslationInvalidArgument", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("repo calls = %d, want 0", repo.callsCount)
	}
}

func TestTranslationServiceDeleteForwardsID(t *testing.T) {
	repo := &fakeTranslationRepo{}
	svc := NewTranslationService(biz.NewTranslationUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	_, err := svc.DeleteTranslation(biz.WithIdentity(context.Background(), auth), &configv1.DeleteTranslationRequest{Id: 12})
	if err != nil {
		t.Fatalf("delete translation: %v", err)
	}
	if len(repo.deletedIDs) != 1 || repo.deletedIDs[0] != 12 {
		t.Fatalf("deleted = %+v, want [12]", repo.deletedIDs)
	}
}

func TestTenantConfigServiceUpdateTenantConfigPreservesExplicitEmptyValue(t *testing.T) {
	empty := ""
	repo := &fakeTenantConfigRepo{}
	svc := NewTenantConfigService(biz.NewTenantConfigUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}
	one := int32(1)

	_, err := svc.UpdateTenantConfig(biz.WithIdentity(context.Background(), auth), &configv1.UpdateTenantConfigRequest{
		TenantConfig: &configv1.TenantConfig{Id: 1, Name: "清空", Status: &one, Value: &empty},
	})
	if err != nil {
		t.Fatalf("update tenant config: %v", err)
	}
	if repo.updated == nil || repo.updated.Value == nil || *repo.updated.Value != "" {
		t.Fatalf("repo value = %+v, want explicit empty string", repo.updated)
	}
}

// configFamilyCreate 是配置族某个创建端点的测试入口：以指定 status 发起
// 创建，并暴露仓储调用次数与用例落定后的状态。
type configFamilyCreate struct {
	name    string
	invalid error
	build   func(status *int32) (call func() error, repoCalls func() int, createdStatus func() int32)
}

// configFamilyCreates 枚举配置族四个创建端点（全局/租户/功能开关/翻译）：
// status 归一化由四者共用，越界取值与 nil 缺省口径必须一致。
func configFamilyCreates(auth biz.Identity) []configFamilyCreate {
	ctx := biz.WithIdentity(context.Background(), auth)
	return []configFamilyCreate{
		{
			name:    "global config",
			invalid: biz.ErrConfigInvalidArgument,
			build: func(status *int32) (func() error, func() int, func() int32) {
				repo := &fakeConfigRepo{}
				svc := NewConfigService(newConfigUsecase(repo))
				return func() error {
						_, err := svc.CreateConfigSuper(ctx, &configv1.CreateConfigSuperRequest{
							Config: &configv1.Config{Name: "cfg", ConfigKey: "k", ValueType: "string", Status: status},
						})
						return err
					}, func() int { return repo.callsCount }, func() int32 {
						if repo.created == nil {
							return -1
						}
						return repo.created.Status
					}
			},
		},
		{
			name:    "tenant config",
			invalid: biz.ErrTenantConfigInvalidArgument,
			build: func(status *int32) (func() error, func() int, func() int32) {
				repo := &fakeTenantConfigRepo{}
				svc := NewTenantConfigService(biz.NewTenantConfigUsecase(repo))
				return func() error {
						_, err := svc.CreateTenantConfig(ctx, &configv1.CreateTenantConfigRequest{
							TenantConfig: &configv1.TenantConfig{Name: "cfg", ConfigKey: "k", ValueType: "string", Status: status},
						})
						return err
					}, func() int { return repo.callsCount }, func() int32 {
						if repo.created == nil {
							return -1
						}
						return repo.created.Status
					}
			},
		},
		{
			name:    "feature flag",
			invalid: biz.ErrFeatureFlagInvalidArgument,
			build: func(status *int32) (func() error, func() int, func() int32) {
				repo := &fakeFeatureFlagRepo{}
				svc := NewFeatureFlagService(biz.NewFeatureFlagUsecase(repo))
				return func() error {
						_, err := svc.CreateFeatureFlag(ctx, &configv1.CreateFeatureFlagRequest{
							FeatureFlag: &configv1.FeatureFlag{Name: "flag", FlagKey: "k", Status: status},
						})
						return err
					}, func() int { return repo.callsCount }, func() int32 {
						if repo.created == nil {
							return -1
						}
						return repo.created.Status
					}
			},
		},
		{
			name:    "translation",
			invalid: biz.ErrTranslationInvalidArgument,
			build: func(status *int32) (func() error, func() int, func() int32) {
				repo := &fakeTranslationRepo{}
				svc := NewTranslationService(biz.NewTranslationUsecase(repo))
				return func() error {
						_, err := svc.CreateTranslation(ctx, &configv1.CreateTranslationRequest{
							Translation: &configv1.Translation{TableName: "sys_menu", RowId: 1, ColumnName: "name",
								Locale: "en-US", Value: "v", Status: status},
						})
						return err
					}, func() int { return repo.callsCount }, func() int32 {
						if repo.created == nil {
							return -1
						}
						return repo.created.Status
					}
			},
		},
	}
}

// TestConfigFamilyCreateRejectsExplicitUnspecifiedStatus 验证显式 -1（哨兵
// 形态）与 2 等越界值不是合法请求值：四个创建端点一律返回各自域的
// InvalidArgument，且不触达用例/仓储。
func TestConfigFamilyCreateRejectsExplicitUnspecifiedStatus(t *testing.T) {
	minusOne := int32(-1)
	two := int32(2)
	for _, tc := range configFamilyCreates(biz.Identity{TenantID: "t-1", UserID: 7}) {
		for _, status := range []struct {
			name  string
			value *int32
		}{{name: "-1", value: &minusOne}, {name: "2", value: &two}} {
			t.Run(tc.name+"/"+status.name, func(t *testing.T) {
				call, repoCalls, _ := tc.build(status.value)
				if err := call(); !errors.Is(err, tc.invalid) {
					t.Fatalf("err = %v, want %v", err, tc.invalid)
				}
				if calls := repoCalls(); calls != 0 {
					t.Fatalf("repo calls = %d, want 0", calls)
				}
			})
		}
	}
}

// TestConfigFamilyCreateDefaultsMissingStatusToEnabled 验证 nil（未设置）才
// 走未指定哨兵：四个创建端点落库缺省启用（status=1），不得并入 0=禁用。
func TestConfigFamilyCreateDefaultsMissingStatusToEnabled(t *testing.T) {
	for _, tc := range configFamilyCreates(biz.Identity{TenantID: "t-1", UserID: 7}) {
		t.Run(tc.name, func(t *testing.T) {
			call, repoCalls, createdStatus := tc.build(nil)
			if err := call(); err != nil {
				t.Fatalf("create with unset status: %v", err)
			}
			if calls := repoCalls(); calls != 1 {
				t.Fatalf("repo calls = %d, want 1", calls)
			}
			if got := createdStatus(); got != biz.ConfigStatusEnabled {
				t.Fatalf("created status = %d, want enabled", got)
			}
		})
	}
}
