package biz

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

// mockConfigRepo 捕获仓储调用并按用例配置返回值：biz 只验证用例编排与
// 领域校验，不触真实存储。
type mockConfigRepo struct {
	ConfigRepo

	getID    int64
	listed   bool
	created  *Config
	updated  *Config
	deleted  []int64
	repoErr  error
	createID int64
}

// fakePlatformTenantGuard 返回预设的平台判定结论与错误并记录调用次数：
// 全局配置写路径的信任边界在用例层验证，不触真实存储。
type fakePlatformTenantGuard struct {
	platform bool
	err      error
	calls    int
}

func (f *fakePlatformTenantGuard) IsPlatformTenant(context.Context, Identity) (bool, error) {
	f.calls++
	return f.platform, f.err
}

// newTestConfigUsecase 构造默认放行（平台租户）的全局配置用例：既有用例
// 关注入参校验与仓储编排，平台边界由专门用例覆盖。
func newTestConfigUsecase(repo ConfigRepo) *ConfigUsecase {
	return NewConfigUsecase(repo, &fakePlatformTenantGuard{platform: true})
}

func (m *mockConfigRepo) Get(_ context.Context, _ Identity, id int64) (*Config, error) {
	m.getID = id
	if m.repoErr != nil {
		return nil, m.repoErr
	}
	return &Config{ID: id, Status: ConfigStatusEnabled, Name: "cfg", ConfigKey: "k", ValueType: "string"}, nil
}

func (m *mockConfigRepo) List(context.Context, Identity, *dynq.Query) (*ConfigList, error) {
	m.listed = true
	return &ConfigList{}, nil
}

func (m *mockConfigRepo) CreateSuper(_ context.Context, _ Identity, c *Config) (*Config, error) {
	m.created = c
	if m.repoErr != nil {
		return nil, m.repoErr
	}
	out := *c
	out.ID = 100
	return &out, nil
}

func (m *mockConfigRepo) UpdateSuper(_ context.Context, _ Identity, c *Config) (*Config, error) {
	m.updated = c
	if m.repoErr != nil {
		return nil, m.repoErr
	}
	return c, nil
}

func (m *mockConfigRepo) DeleteSuper(_ context.Context, _ Identity, id int64) error {
	if m.repoErr != nil {
		return m.repoErr
	}
	m.deleted = append(m.deleted, id)
	return nil
}

// mockTenantConfigRepo 同型替身，供租户配置用例断言。
type mockTenantConfigRepo struct {
	TenantConfigRepo

	getID   int64
	listed  bool
	created *TenantConfig
	updated *TenantConfig
	deleted []int64
	repoErr error
}

func (m *mockTenantConfigRepo) Get(_ context.Context, _ Identity, id int64) (*TenantConfig, error) {
	m.getID = id
	if m.repoErr != nil {
		return nil, m.repoErr
	}
	return &TenantConfig{ID: id, Status: ConfigStatusEnabled, Name: "cfg", ConfigKey: "k", ValueType: "string", TenantID: "t-1"}, nil
}

func (m *mockTenantConfigRepo) List(context.Context, Identity, *dynq.Query) (*TenantConfigList, error) {
	m.listed = true
	return &TenantConfigList{}, nil
}

func (m *mockTenantConfigRepo) Create(_ context.Context, _ Identity, c *TenantConfig) (*TenantConfig, error) {
	m.created = c
	if m.repoErr != nil {
		return nil, m.repoErr
	}
	out := *c
	out.ID = 200
	out.TenantID = "t-1"
	return &out, nil
}

func (m *mockTenantConfigRepo) Update(_ context.Context, _ Identity, c *TenantConfig) (*TenantConfig, error) {
	m.updated = c
	if m.repoErr != nil {
		return nil, m.repoErr
	}
	return c, nil
}

func (m *mockTenantConfigRepo) Delete(_ context.Context, _ Identity, id int64) error {
	if m.repoErr != nil {
		return m.repoErr
	}
	m.deleted = append(m.deleted, id)
	return nil
}

func TestConfigUsecaseCreateConfigSuperDefaultsUnspecifiedStatusToEnabled(t *testing.T) {
	repo := &mockConfigRepo{}
	uc := newTestConfigUsecase(repo)

	out, err := uc.CreateConfigSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&Config{Name: "超时", ConfigKey: "session.timeout", ValueType: "integer", Status: ConfigStatusUnspecified})
	if err != nil {
		t.Fatalf("create config: %v", err)
	}
	if out.Status != ConfigStatusEnabled {
		t.Fatalf("status = %d, want enabled", out.Status)
	}
	if repo.created == nil || repo.created.ConfigKey != "session.timeout" {
		t.Fatalf("repo created = %+v, want key session.timeout", repo.created)
	}
}

func TestConfigUsecaseCreateConfigSuperKeepsExplicitDisabled(t *testing.T) {
	// 显式 status=0 是合法的禁用取值，不得并入「未指定缺省启用」分支。
	repo := &mockConfigRepo{}
	uc := newTestConfigUsecase(repo)

	_, err := uc.CreateConfigSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&Config{Name: "paused", ConfigKey: "paused", ValueType: "string", Status: ConfigStatusDisabled})
	if err != nil {
		t.Fatalf("create config: %v", err)
	}
	if repo.created == nil || repo.created.Status != ConfigStatusDisabled {
		t.Fatalf("repo created status = %+v, want disabled", repo.created)
	}
}

func TestConfigUsecaseCreateConfigSuperRejectsBlankName(t *testing.T) {
	repo := &mockConfigRepo{}
	uc := newTestConfigUsecase(repo)

	_, err := uc.CreateConfigSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&Config{Name: "   ", ConfigKey: "k", ValueType: "string"})
	if !errors.Is(err, ErrConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrConfigInvalidArgument", err)
	}
	if repo.created != nil {
		t.Fatalf("repo must not be called on invalid input, got %+v", repo.created)
	}
}

func TestConfigUsecaseCreateConfigSuperRejectsNameOverColumnWidth(t *testing.T) {
	repo := &mockConfigRepo{}
	uc := newTestConfigUsecase(repo)

	_, err := uc.CreateConfigSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&Config{Name: strings.Repeat("名", 129), ConfigKey: "k", ValueType: "string"})
	if !errors.Is(err, ErrConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrConfigInvalidArgument", err)
	}
}

func TestConfigUsecaseCreateConfigSuperAcceptsNameAtColumnWidth(t *testing.T) {
	repo := &mockConfigRepo{}
	uc := newTestConfigUsecase(repo)

	_, err := uc.CreateConfigSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&Config{Name: strings.Repeat("名", 128), ConfigKey: "k", ValueType: "string"})
	if err != nil {
		t.Fatalf("create config with 128-rune name: %v", err)
	}
}

func TestConfigUsecaseCreateConfigSuperRejectsBlankConfigKey(t *testing.T) {
	repo := &mockConfigRepo{}
	uc := newTestConfigUsecase(repo)

	_, err := uc.CreateConfigSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&Config{Name: "cfg", ConfigKey: "  ", ValueType: "string"})
	if !errors.Is(err, ErrConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrConfigInvalidArgument", err)
	}
}

func TestConfigUsecaseCreateConfigSuperRejectsConfigKeyOverColumnWidth(t *testing.T) {
	repo := &mockConfigRepo{}
	uc := newTestConfigUsecase(repo)

	_, err := uc.CreateConfigSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&Config{Name: "cfg", ConfigKey: strings.Repeat("k", 129), ValueType: "string"})
	if !errors.Is(err, ErrConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrConfigInvalidArgument", err)
	}
}

func TestConfigUsecaseCreateConfigSuperRejectsUnknownValueType(t *testing.T) {
	repo := &mockConfigRepo{}
	uc := newTestConfigUsecase(repo)

	_, err := uc.CreateConfigSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&Config{Name: "cfg", ConfigKey: "k", ValueType: "object"})
	if !errors.Is(err, ErrConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrConfigInvalidArgument", err)
	}
	if repo.created != nil {
		t.Fatalf("repo must not be called on invalid input, got %+v", repo.created)
	}
}

func TestConfigValueTypeWhiteListContainsExactlyDeclaredValues(t *testing.T) {
	// 白名单是 schema 注释声明的五个值：缺一不可、多一不可。断言字面量
	// 集合而非遍历生产常量（后者属自证循环）。
	want := []string{"string", "integer", "boolean", "float", "json"}
	if !reflect.DeepEqual(ConfigValueTypeWhiteList, want) {
		t.Fatalf("value type whitelist = %v, want %v", ConfigValueTypeWhiteList, want)
	}
}

func TestConfigUsecaseCreateConfigSuperAcceptsEachWhiteListValueType(t *testing.T) {
	for _, vt := range []string{"string", "integer", "boolean", "float", "json"} {
		repo := &mockConfigRepo{}
		uc := newTestConfigUsecase(repo)

		_, err := uc.CreateConfigSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1},
			&Config{Name: "cfg", ConfigKey: "k", ValueType: vt})
		if err != nil {
			t.Fatalf("create config with value_type %q: %v", vt, err)
		}
	}
}

func TestConfigUsecaseCreateConfigSuperRejectsValueTypeOverColumnWidth(t *testing.T) {
	repo := &mockConfigRepo{}
	uc := newTestConfigUsecase(repo)

	_, err := uc.CreateConfigSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&Config{Name: "cfg", ConfigKey: "k", ValueType: strings.Repeat("s", 33)})
	if !errors.Is(err, ErrConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrConfigInvalidArgument", err)
	}
}

func TestConfigUsecaseCreateConfigSuperRejectsInvalidStatus(t *testing.T) {
	repo := &mockConfigRepo{}
	uc := newTestConfigUsecase(repo)

	_, err := uc.CreateConfigSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&Config{Name: "cfg", ConfigKey: "k", ValueType: "string", Status: 2})
	if !errors.Is(err, ErrConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrConfigInvalidArgument", err)
	}
}

func TestConfigUsecaseUpdateConfigSuperRejectsInvalidStatus(t *testing.T) {
	// 更新路径 status 必填且只认 0/1；未指定哨兵在更新输入里非法。
	repo := &mockConfigRepo{}
	uc := newTestConfigUsecase(repo)

	_, err := uc.UpdateConfigSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&Config{ID: 1, Name: "cfg", Status: ConfigStatusUnspecified})
	if !errors.Is(err, ErrConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrConfigInvalidArgument", err)
	}
}

func TestConfigUsecaseUpdateConfigSuperRejectsMissingID(t *testing.T) {
	repo := &mockConfigRepo{}
	uc := newTestConfigUsecase(repo)

	_, err := uc.UpdateConfigSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&Config{ID: 0, Name: "cfg", Status: ConfigStatusEnabled})
	if !errors.Is(err, ErrConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrConfigInvalidArgument", err)
	}
}

func TestConfigUsecaseUpdateConfigSuperRejectsNil(t *testing.T) {
	repo := &mockConfigRepo{}
	uc := newTestConfigUsecase(repo)

	_, err := uc.UpdateConfigSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1}, nil)
	if !errors.Is(err, ErrConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrConfigInvalidArgument", err)
	}
}

func TestConfigUsecaseUpdateConfigSuperRejectsBlankName(t *testing.T) {
	repo := &mockConfigRepo{}
	uc := newTestConfigUsecase(repo)

	_, err := uc.UpdateConfigSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&Config{ID: 1, Name: "  ", Status: ConfigStatusEnabled})
	if !errors.Is(err, ErrConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrConfigInvalidArgument", err)
	}
	if repo.updated != nil {
		t.Fatalf("repo must not be called on invalid input, got %+v", repo.updated)
	}
}

func TestConfigUsecaseUpdateConfigSuperRejectsRemarkOverColumnWidth(t *testing.T) {
	repo := &mockConfigRepo{}
	uc := newTestConfigUsecase(repo)

	remark := strings.Repeat("注", 513)
	_, err := uc.UpdateConfigSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&Config{ID: 1, Name: "cfg", Status: ConfigStatusEnabled, Remark: &remark})
	if !errors.Is(err, ErrConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrConfigInvalidArgument", err)
	}
	if repo.updated != nil {
		t.Fatalf("repo must not be called on invalid input, got %+v", repo.updated)
	}
}

func TestConfigUsecaseUpdateConfigSuperIgnoresConfigKeyAndValueType(t *testing.T) {
	// config_key/value_type 是身份/值语义列：更新路径不参与校验，
	// 空值也不构成非法入参（服务端不写这两列）。
	repo := &mockConfigRepo{}
	uc := newTestConfigUsecase(repo)

	_, err := uc.UpdateConfigSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&Config{ID: 1, Name: "cfg", Status: ConfigStatusEnabled})
	if err != nil {
		t.Fatalf("update config without config_key/value_type: %v", err)
	}
	if repo.updated == nil {
		t.Fatal("repo UpdateSuper must be called")
	}
}

func TestConfigUsecaseGetConfigRejectsNonPositiveID(t *testing.T) {
	repo := &mockConfigRepo{}
	uc := newTestConfigUsecase(repo)

	_, err := uc.GetConfig(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 0)
	if !errors.Is(err, ErrConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrConfigInvalidArgument", err)
	}
	if repo.getID != 0 {
		t.Fatalf("repo must not be called, got id %d", repo.getID)
	}
}

func TestConfigUsecaseDeleteConfigSuperRejectsNonPositiveID(t *testing.T) {
	repo := &mockConfigRepo{}
	uc := newTestConfigUsecase(repo)

	err := uc.DeleteConfigSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1}, -1)
	if !errors.Is(err, ErrConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrConfigInvalidArgument", err)
	}
	if len(repo.deleted) != 0 {
		t.Fatalf("repo must not be called, got %+v", repo.deleted)
	}
}

func TestConfigUsecaseGetConfigPropagatesNotFound(t *testing.T) {
	repo := &mockConfigRepo{repoErr: ErrConfigNotFound}
	uc := newTestConfigUsecase(repo)

	_, err := uc.GetConfig(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 9)
	if !errors.Is(err, ErrConfigNotFound) {
		t.Fatalf("err = %v, want ErrConfigNotFound", err)
	}
}

func TestConfigUsecaseCreateConfigSuperPropagatesAlreadyExists(t *testing.T) {
	repo := &mockConfigRepo{repoErr: ErrConfigAlreadyExists}
	uc := newTestConfigUsecase(repo)

	_, err := uc.CreateConfigSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&Config{Name: "cfg", ConfigKey: "dup", ValueType: "string"})
	if !errors.Is(err, ErrConfigAlreadyExists) {
		t.Fatalf("err = %v, want ErrConfigAlreadyExists", err)
	}
}

func TestConfigUsecaseUpdateConfigSuperPropagatesNotFound(t *testing.T) {
	repo := &mockConfigRepo{repoErr: ErrConfigNotFound}
	uc := newTestConfigUsecase(repo)

	_, err := uc.UpdateConfigSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&Config{ID: 9, Name: "cfg", Status: ConfigStatusEnabled})
	if !errors.Is(err, ErrConfigNotFound) {
		t.Fatalf("err = %v, want ErrConfigNotFound", err)
	}
}

func TestConfigUsecaseDeleteConfigSuperPropagatesNotFound(t *testing.T) {
	repo := &mockConfigRepo{repoErr: ErrConfigNotFound}
	uc := newTestConfigUsecase(repo)

	err := uc.DeleteConfigSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 9)
	if !errors.Is(err, ErrConfigNotFound) {
		t.Fatalf("err = %v, want ErrConfigNotFound", err)
	}
}

func TestConfigUsecaseListConfigsForwardsQuery(t *testing.T) {
	repo := &mockConfigRepo{}
	uc := newTestConfigUsecase(repo)

	_, err := uc.ListConfigs(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &dynq.Query{})
	if err != nil {
		t.Fatalf("list configs: %v", err)
	}
	if !repo.listed {
		t.Fatal("repo List must be called")
	}
}

// configWriteCall 是一条全局配置写路径的调用尝试，并断言仓储是否被触达。
type configWriteCall struct {
	name       string
	call       func(*ConfigUsecase) error
	repoCalled func(*mockConfigRepo) bool
}

// configWriteCalls 枚举全局配置的三条写路径（创建/更新/删除），供平台
// 信任边界用例复用。
func configWriteCalls() []configWriteCall {
	return []configWriteCall{
		{
			name: "create",
			call: func(uc *ConfigUsecase) error {
				_, err := uc.CreateConfigSuper(context.Background(), Identity{TenantID: "t-demo-01", UserID: 7},
					&Config{Name: "cfg", ConfigKey: "test.platform", ValueType: "string"})
				return err
			},
			repoCalled: func(r *mockConfigRepo) bool { return r.created != nil },
		},
		{
			name: "update",
			call: func(uc *ConfigUsecase) error {
				_, err := uc.UpdateConfigSuper(context.Background(), Identity{TenantID: "t-demo-01", UserID: 7},
					&Config{ID: 1, Name: "cfg", Status: ConfigStatusEnabled})
				return err
			},
			repoCalled: func(r *mockConfigRepo) bool { return r.updated != nil },
		},
		{
			name: "delete",
			call: func(uc *ConfigUsecase) error {
				return uc.DeleteConfigSuper(context.Background(), Identity{TenantID: "t-demo-01", UserID: 7}, 1)
			},
			repoCalled: func(r *mockConfigRepo) bool { return len(r.deleted) != 0 },
		},
	}
}

// TestConfigUsecaseWritesRequirePlatformTenant 验证全局配置写路径的平台
// 信任边界：非平台（根）租户返回 ErrConfigPlatformRequired（403）且仓储
// 未被调用；平台租户正常写入。
func TestConfigUsecaseWritesRequirePlatformTenant(t *testing.T) {
	for _, tc := range configWriteCalls() {
		t.Run(tc.name+"/non-platform", func(t *testing.T) {
			repo := &mockConfigRepo{}
			guard := &fakePlatformTenantGuard{platform: false}
			uc := NewConfigUsecase(repo, guard)

			if err := tc.call(uc); !errors.Is(err, ErrConfigPlatformRequired) {
				t.Fatalf("err = %v, want ErrConfigPlatformRequired", err)
			}
			if guard.calls != 1 {
				t.Fatalf("guard calls = %d, want 1", guard.calls)
			}
			if tc.repoCalled(repo) {
				t.Fatal("repo must not be called for non-platform tenant")
			}
		})
		t.Run(tc.name+"/platform", func(t *testing.T) {
			repo := &mockConfigRepo{}
			guard := &fakePlatformTenantGuard{platform: true}
			uc := NewConfigUsecase(repo, guard)

			if err := tc.call(uc); err != nil {
				t.Fatalf("platform write: %v", err)
			}
			if guard.calls != 1 {
				t.Fatalf("guard calls = %d, want 1", guard.calls)
			}
			if !tc.repoCalled(repo) {
				t.Fatal("repo must be called for platform tenant")
			}
		})
	}
}

// TestConfigUsecaseWritePropagatesPlatformGuardError 验证守卫自身错误原样
// 透传（不吞错、不改写为 403），且不触达仓储。
func TestConfigUsecaseWritePropagatesPlatformGuardError(t *testing.T) {
	guardErr := errors.New("guard lookup failed")
	for _, tc := range configWriteCalls() {
		t.Run(tc.name, func(t *testing.T) {
			repo := &mockConfigRepo{}
			uc := NewConfigUsecase(repo, &fakePlatformTenantGuard{err: guardErr})

			if err := tc.call(uc); !errors.Is(err, guardErr) {
				t.Fatalf("err = %v, want guard error", err)
			}
			if tc.repoCalled(repo) {
				t.Fatal("repo must not be called when guard fails")
			}
		})
	}
}

// TestConfigUsecaseReadsDoNotCheckPlatformTenant 验证读路径不设平台守卫：
// 非平台租户照常读取（全局配置读取本身即全局口径）。
func TestConfigUsecaseReadsDoNotCheckPlatformTenant(t *testing.T) {
	repo := &mockConfigRepo{}
	guard := &fakePlatformTenantGuard{platform: false}
	uc := NewConfigUsecase(repo, guard)
	ctx := context.Background()
	auth := Identity{TenantID: "t-demo-01", UserID: 7}

	if _, err := uc.GetConfig(ctx, auth, 1); err != nil {
		t.Fatalf("get config: %v", err)
	}
	if _, err := uc.ListConfigs(ctx, auth, &dynq.Query{}); err != nil {
		t.Fatalf("list configs: %v", err)
	}
	if guard.calls != 0 {
		t.Fatalf("guard calls = %d, want 0 on read paths", guard.calls)
	}
}

func TestTenantConfigUsecaseCreateTenantConfigDefaultsUnspecifiedStatusToEnabled(t *testing.T) {
	repo := &mockTenantConfigRepo{}
	uc := NewTenantConfigUsecase(repo)

	out, err := uc.CreateTenantConfig(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&TenantConfig{Name: "超时", ConfigKey: "session.timeout", ValueType: "integer", Status: ConfigStatusUnspecified})
	if err != nil {
		t.Fatalf("create tenant config: %v", err)
	}
	if out.Status != ConfigStatusEnabled {
		t.Fatalf("status = %d, want enabled", out.Status)
	}
	if out.TenantID != "t-1" {
		t.Fatalf("tenant id = %q, want t-1", out.TenantID)
	}
}

func TestTenantConfigUsecaseCreateTenantConfigRejectsBlankConfigKey(t *testing.T) {
	repo := &mockTenantConfigRepo{}
	uc := NewTenantConfigUsecase(repo)

	_, err := uc.CreateTenantConfig(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&TenantConfig{Name: "cfg", ConfigKey: "  ", ValueType: "string"})
	if !errors.Is(err, ErrTenantConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrTenantConfigInvalidArgument", err)
	}
	if repo.created != nil {
		t.Fatalf("repo must not be called on invalid input, got %+v", repo.created)
	}
}

func TestTenantConfigUsecaseCreateTenantConfigRejectsUnknownValueType(t *testing.T) {
	repo := &mockTenantConfigRepo{}
	uc := NewTenantConfigUsecase(repo)

	_, err := uc.CreateTenantConfig(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&TenantConfig{Name: "cfg", ConfigKey: "k", ValueType: "object"})
	if !errors.Is(err, ErrTenantConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrTenantConfigInvalidArgument", err)
	}
}

func TestTenantConfigUsecaseCreateTenantConfigRejectsNameOverColumnWidth(t *testing.T) {
	repo := &mockTenantConfigRepo{}
	uc := NewTenantConfigUsecase(repo)

	_, err := uc.CreateTenantConfig(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&TenantConfig{Name: strings.Repeat("名", 129), ConfigKey: "k", ValueType: "string"})
	if !errors.Is(err, ErrTenantConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrTenantConfigInvalidArgument", err)
	}
}

func TestTenantConfigUsecaseUpdateTenantConfigRejectsInvalidStatus(t *testing.T) {
	repo := &mockTenantConfigRepo{}
	uc := NewTenantConfigUsecase(repo)

	_, err := uc.UpdateTenantConfig(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&TenantConfig{ID: 1, Name: "cfg", Status: ConfigStatusUnspecified})
	if !errors.Is(err, ErrTenantConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrTenantConfigInvalidArgument", err)
	}
}

func TestTenantConfigUsecaseUpdateTenantConfigRejectsMissingID(t *testing.T) {
	repo := &mockTenantConfigRepo{}
	uc := NewTenantConfigUsecase(repo)

	_, err := uc.UpdateTenantConfig(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&TenantConfig{ID: 0, Name: "cfg", Status: ConfigStatusEnabled})
	if !errors.Is(err, ErrTenantConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrTenantConfigInvalidArgument", err)
	}
}

func TestTenantConfigUsecaseUpdateTenantConfigIgnoresConfigKeyAndValueType(t *testing.T) {
	// 身份/值语义列与归属列在更新路径不参与校验（服务端不写）。
	repo := &mockTenantConfigRepo{}
	uc := NewTenantConfigUsecase(repo)

	_, err := uc.UpdateTenantConfig(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&TenantConfig{ID: 1, Name: "cfg", Status: ConfigStatusEnabled})
	if err != nil {
		t.Fatalf("update tenant config without config_key/value_type: %v", err)
	}
	if repo.updated == nil {
		t.Fatal("repo Update must be called")
	}
}

func TestTenantConfigUsecaseGetTenantConfigRejectsNonPositiveID(t *testing.T) {
	repo := &mockTenantConfigRepo{}
	uc := NewTenantConfigUsecase(repo)

	_, err := uc.GetTenantConfig(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 0)
	if !errors.Is(err, ErrTenantConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrTenantConfigInvalidArgument", err)
	}
	if repo.getID != 0 {
		t.Fatalf("repo must not be called, got id %d", repo.getID)
	}
}

func TestTenantConfigUsecaseDeleteTenantConfigRejectsNonPositiveID(t *testing.T) {
	repo := &mockTenantConfigRepo{}
	uc := NewTenantConfigUsecase(repo)

	err := uc.DeleteTenantConfig(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 0)
	if !errors.Is(err, ErrTenantConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrTenantConfigInvalidArgument", err)
	}
	if len(repo.deleted) != 0 {
		t.Fatalf("repo must not be called, got %+v", repo.deleted)
	}
}

func TestTenantConfigUsecaseGetTenantConfigPropagatesNotFound(t *testing.T) {
	repo := &mockTenantConfigRepo{repoErr: ErrTenantConfigNotFound}
	uc := NewTenantConfigUsecase(repo)

	_, err := uc.GetTenantConfig(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 9)
	if !errors.Is(err, ErrTenantConfigNotFound) {
		t.Fatalf("err = %v, want ErrTenantConfigNotFound", err)
	}
}

func TestTenantConfigUsecaseCreateTenantConfigPropagatesAlreadyExists(t *testing.T) {
	repo := &mockTenantConfigRepo{repoErr: ErrTenantConfigAlreadyExists}
	uc := NewTenantConfigUsecase(repo)

	_, err := uc.CreateTenantConfig(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&TenantConfig{Name: "cfg", ConfigKey: "dup", ValueType: "string"})
	if !errors.Is(err, ErrTenantConfigAlreadyExists) {
		t.Fatalf("err = %v, want ErrTenantConfigAlreadyExists", err)
	}
}

func TestTenantConfigUsecaseDeleteTenantConfigPropagatesNotFound(t *testing.T) {
	repo := &mockTenantConfigRepo{repoErr: ErrTenantConfigNotFound}
	uc := NewTenantConfigUsecase(repo)

	err := uc.DeleteTenantConfig(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 9)
	if !errors.Is(err, ErrTenantConfigNotFound) {
		t.Fatalf("err = %v, want ErrTenantConfigNotFound", err)
	}
}

func TestTenantConfigUsecaseListTenantConfigsForwardsQuery(t *testing.T) {
	repo := &mockTenantConfigRepo{}
	uc := NewTenantConfigUsecase(repo)

	_, err := uc.ListTenantConfigs(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &dynq.Query{})
	if err != nil {
		t.Fatalf("list tenant configs: %v", err)
	}
	if !repo.listed {
		t.Fatal("repo List must be called")
	}
}

func TestConfigUsecaseCreateConfigSuperRejectsNil(t *testing.T) {
	repo := &mockConfigRepo{}
	uc := newTestConfigUsecase(repo)

	_, err := uc.CreateConfigSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1}, nil)
	if !errors.Is(err, ErrConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrConfigInvalidArgument", err)
	}
	if repo.created != nil {
		t.Fatalf("repo must not be called on nil input, got %+v", repo.created)
	}
}

func TestConfigUsecaseCreateConfigSuperAcceptsConfigKeyAtColumnWidth(t *testing.T) {
	repo := &mockConfigRepo{}
	uc := newTestConfigUsecase(repo)

	_, err := uc.CreateConfigSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&Config{Name: "cfg", ConfigKey: strings.Repeat("k", 128), ValueType: "string"})
	if err != nil {
		t.Fatalf("create config with 128-rune config_key: %v", err)
	}
}

func TestConfigUsecaseCreateConfigSuperAcceptsRemarkAtColumnWidth(t *testing.T) {
	repo := &mockConfigRepo{}
	uc := newTestConfigUsecase(repo)

	remark := strings.Repeat("注", 512)
	_, err := uc.CreateConfigSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&Config{Name: "cfg", ConfigKey: "k", ValueType: "string", Remark: &remark})
	if err != nil {
		t.Fatalf("create config with 512-rune remark: %v", err)
	}
}

func TestConfigUsecaseCreateConfigSuperTrimsNameAndConfigKey(t *testing.T) {
	// 列宽校验按 trim 后长度，落库也必须是 trim 后值（防 22001 / 500）；
	// value 是有效配置内容，原样透传（含首尾空白）。
	repo := &mockConfigRepo{}
	uc := newTestConfigUsecase(repo)
	value := "  raw-value  "

	_, err := uc.CreateConfigSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&Config{Name: "  超时  ", ConfigKey: "  session.timeout  ", ValueType: "string", Value: &value})
	if err != nil {
		t.Fatalf("create config: %v", err)
	}
	if repo.created == nil || repo.created.Name != "超时" || repo.created.ConfigKey != "session.timeout" {
		t.Fatalf("repo created = (%q, %q), want trimmed (超时, session.timeout)", repo.created.Name, repo.created.ConfigKey)
	}
	if repo.created.Value == nil || *repo.created.Value != "  raw-value  " {
		t.Fatalf("repo value = %+v, want raw value unchanged", repo.created.Value)
	}
}

func TestConfigUsecaseUpdateConfigSuperTrimsName(t *testing.T) {
	// name 按 trim 后落库；value 是有效配置内容，原样透传（含首尾空白）。
	repo := &mockConfigRepo{}
	uc := newTestConfigUsecase(repo)
	value := "  raw-value  "

	_, err := uc.UpdateConfigSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&Config{ID: 1, Name: "  新名  ", Status: ConfigStatusEnabled, Value: &value})
	if err != nil {
		t.Fatalf("update config: %v", err)
	}
	if repo.updated == nil || repo.updated.Name != "新名" {
		t.Fatalf("repo updated name = %+v, want trimmed 新名", repo.updated)
	}
	if repo.updated.Value == nil || *repo.updated.Value != "  raw-value  " {
		t.Fatalf("repo value = %+v, want raw value unchanged", repo.updated.Value)
	}
}

func TestConfigUsecaseCreateConfigSuperRejectsRemarkOverColumnWidth(t *testing.T) {
	// 创建路径与更新路径共用 remark 列宽约束（README 声明的 512 上限）。
	repo := &mockConfigRepo{}
	uc := newTestConfigUsecase(repo)

	remark := strings.Repeat("注", 513)
	_, err := uc.CreateConfigSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&Config{Name: "cfg", ConfigKey: "k", ValueType: "string", Remark: &remark})
	if !errors.Is(err, ErrConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrConfigInvalidArgument", err)
	}
	if repo.created != nil {
		t.Fatalf("repo must not be called on invalid input, got %+v", repo.created)
	}
}

func TestTenantConfigUsecaseCreateTenantConfigKeepsExplicitDisabled(t *testing.T) {
	repo := &mockTenantConfigRepo{}
	uc := NewTenantConfigUsecase(repo)

	_, err := uc.CreateTenantConfig(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&TenantConfig{Name: "paused", ConfigKey: "paused", ValueType: "string", Status: ConfigStatusDisabled})
	if err != nil {
		t.Fatalf("create tenant config: %v", err)
	}
	if repo.created == nil || repo.created.Status != ConfigStatusDisabled {
		t.Fatalf("repo created status = %+v, want disabled", repo.created)
	}
}

func TestTenantConfigUsecaseCreateTenantConfigRejectsInvalidStatus(t *testing.T) {
	repo := &mockTenantConfigRepo{}
	uc := NewTenantConfigUsecase(repo)

	_, err := uc.CreateTenantConfig(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&TenantConfig{Name: "cfg", ConfigKey: "k", ValueType: "string", Status: 2})
	if !errors.Is(err, ErrTenantConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrTenantConfigInvalidArgument", err)
	}
}

func TestTenantConfigUsecaseCreateTenantConfigRejectsNil(t *testing.T) {
	repo := &mockTenantConfigRepo{}
	uc := NewTenantConfigUsecase(repo)

	_, err := uc.CreateTenantConfig(context.Background(), Identity{TenantID: "t-1", UserID: 1}, nil)
	if !errors.Is(err, ErrTenantConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrTenantConfigInvalidArgument", err)
	}
	if repo.created != nil {
		t.Fatalf("repo must not be called on nil input, got %+v", repo.created)
	}
}

func TestTenantConfigUsecaseCreateTenantConfigRejectsBlankName(t *testing.T) {
	repo := &mockTenantConfigRepo{}
	uc := NewTenantConfigUsecase(repo)

	_, err := uc.CreateTenantConfig(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&TenantConfig{Name: "   ", ConfigKey: "k", ValueType: "string"})
	if !errors.Is(err, ErrTenantConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrTenantConfigInvalidArgument", err)
	}
}

func TestTenantConfigUsecaseCreateTenantConfigAcceptsNameAtColumnWidth(t *testing.T) {
	repo := &mockTenantConfigRepo{}
	uc := NewTenantConfigUsecase(repo)

	_, err := uc.CreateTenantConfig(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&TenantConfig{Name: strings.Repeat("名", 128), ConfigKey: "k", ValueType: "string"})
	if err != nil {
		t.Fatalf("create tenant config with 128-rune name: %v", err)
	}
}

func TestTenantConfigUsecaseCreateTenantConfigAcceptsConfigKeyAtColumnWidth(t *testing.T) {
	repo := &mockTenantConfigRepo{}
	uc := NewTenantConfigUsecase(repo)

	_, err := uc.CreateTenantConfig(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&TenantConfig{Name: "cfg", ConfigKey: strings.Repeat("k", 128), ValueType: "string"})
	if err != nil {
		t.Fatalf("create tenant config with 128-rune config_key: %v", err)
	}
}

func TestTenantConfigUsecaseCreateTenantConfigRejectsConfigKeyOverColumnWidth(t *testing.T) {
	repo := &mockTenantConfigRepo{}
	uc := NewTenantConfigUsecase(repo)

	_, err := uc.CreateTenantConfig(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&TenantConfig{Name: "cfg", ConfigKey: strings.Repeat("k", 129), ValueType: "string"})
	if !errors.Is(err, ErrTenantConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrTenantConfigInvalidArgument", err)
	}
}

func TestTenantConfigUsecaseCreateTenantConfigAcceptsRemarkAtColumnWidth(t *testing.T) {
	repo := &mockTenantConfigRepo{}
	uc := NewTenantConfigUsecase(repo)

	remark := strings.Repeat("注", 512)
	_, err := uc.CreateTenantConfig(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&TenantConfig{Name: "cfg", ConfigKey: "k", ValueType: "string", Remark: &remark})
	if err != nil {
		t.Fatalf("create tenant config with 512-rune remark: %v", err)
	}
}

func TestTenantConfigUsecaseCreateTenantConfigRejectsRemarkOverColumnWidth(t *testing.T) {
	repo := &mockTenantConfigRepo{}
	uc := NewTenantConfigUsecase(repo)

	remark := strings.Repeat("注", 513)
	_, err := uc.CreateTenantConfig(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&TenantConfig{Name: "cfg", ConfigKey: "k", ValueType: "string", Remark: &remark})
	if !errors.Is(err, ErrTenantConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrTenantConfigInvalidArgument", err)
	}
}

func TestTenantConfigUsecaseCreateTenantConfigRejectsValueTypeOverColumnWidth(t *testing.T) {
	repo := &mockTenantConfigRepo{}
	uc := NewTenantConfigUsecase(repo)

	_, err := uc.CreateTenantConfig(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&TenantConfig{Name: "cfg", ConfigKey: "k", ValueType: strings.Repeat("s", 33)})
	if !errors.Is(err, ErrTenantConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrTenantConfigInvalidArgument", err)
	}
}

func TestTenantConfigUsecaseCreateTenantConfigRejectsValueTypeAtColumnWidthWhenNotWhiteListed(t *testing.T) {
	// value_type 同时受列宽与白名单约束：恰 32 rune 的非白名单值同样拒绝
	//（边界两侧都不存在「长度合法即放行」的口子）。
	repo := &mockTenantConfigRepo{}
	uc := NewTenantConfigUsecase(repo)

	_, err := uc.CreateTenantConfig(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&TenantConfig{Name: "cfg", ConfigKey: "k", ValueType: strings.Repeat("s", 32)})
	if !errors.Is(err, ErrTenantConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrTenantConfigInvalidArgument", err)
	}
}

func TestTenantConfigUsecaseCreateTenantConfigTrimsNameAndConfigKey(t *testing.T) {
	repo := &mockTenantConfigRepo{}
	uc := NewTenantConfigUsecase(repo)
	value := "  raw-value  "

	_, err := uc.CreateTenantConfig(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&TenantConfig{Name: "  超时  ", ConfigKey: "  session.timeout  ", ValueType: "string", Value: &value})
	if err != nil {
		t.Fatalf("create tenant config: %v", err)
	}
	if repo.created == nil || repo.created.Name != "超时" || repo.created.ConfigKey != "session.timeout" {
		t.Fatalf("repo created = (%q, %q), want trimmed (超时, session.timeout)", repo.created.Name, repo.created.ConfigKey)
	}
	if repo.created.Value == nil || *repo.created.Value != "  raw-value  " {
		t.Fatalf("repo value = %+v, want raw value unchanged", repo.created.Value)
	}
}

func TestTenantConfigUsecaseUpdateTenantConfigRejectsNil(t *testing.T) {
	repo := &mockTenantConfigRepo{}
	uc := NewTenantConfigUsecase(repo)

	_, err := uc.UpdateTenantConfig(context.Background(), Identity{TenantID: "t-1", UserID: 1}, nil)
	if !errors.Is(err, ErrTenantConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrTenantConfigInvalidArgument", err)
	}
}

func TestTenantConfigUsecaseUpdateTenantConfigRejectsBlankName(t *testing.T) {
	repo := &mockTenantConfigRepo{}
	uc := NewTenantConfigUsecase(repo)

	_, err := uc.UpdateTenantConfig(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&TenantConfig{ID: 1, Name: "  ", Status: ConfigStatusEnabled})
	if !errors.Is(err, ErrTenantConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrTenantConfigInvalidArgument", err)
	}
	if repo.updated != nil {
		t.Fatalf("repo must not be called on invalid input, got %+v", repo.updated)
	}
}

func TestTenantConfigUsecaseUpdateTenantConfigTrimsName(t *testing.T) {
	// name 按 trim 后落库；value 是有效配置内容，原样透传（含首尾空白）。
	repo := &mockTenantConfigRepo{}
	uc := NewTenantConfigUsecase(repo)
	value := "  raw-value  "

	_, err := uc.UpdateTenantConfig(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&TenantConfig{ID: 1, Name: "  新名  ", Status: ConfigStatusEnabled, Value: &value})
	if err != nil {
		t.Fatalf("update tenant config: %v", err)
	}
	if repo.updated == nil || repo.updated.Name != "新名" {
		t.Fatalf("repo updated name = %+v, want trimmed 新名", repo.updated)
	}
	if repo.updated.Value == nil || *repo.updated.Value != "  raw-value  " {
		t.Fatalf("repo value = %+v, want raw value unchanged", repo.updated.Value)
	}
}

func TestTenantConfigUsecaseUpdateTenantConfigPropagatesNotFound(t *testing.T) {
	repo := &mockTenantConfigRepo{repoErr: ErrTenantConfigNotFound}
	uc := NewTenantConfigUsecase(repo)

	_, err := uc.UpdateTenantConfig(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&TenantConfig{ID: 9, Name: "cfg", Status: ConfigStatusEnabled})
	if !errors.Is(err, ErrTenantConfigNotFound) {
		t.Fatalf("err = %v, want ErrTenantConfigNotFound", err)
	}
}

// mockFeatureFlagRepo 捕获功能开关仓储调用并按用例配置返回值。
type mockFeatureFlagRepo struct {
	FeatureFlagRepo

	getID   int64
	listed  bool
	created *FeatureFlag
	updated *FeatureFlag
	deleted []int64
	repoErr error
}

func (m *mockFeatureFlagRepo) Get(_ context.Context, _ Identity, id int64) (*FeatureFlag, error) {
	m.getID = id
	if m.repoErr != nil {
		return nil, m.repoErr
	}
	return &FeatureFlag{ID: id, Status: ConfigStatusEnabled, Name: "flag", FlagKey: "k", TenantID: "t-1"}, nil
}

func (m *mockFeatureFlagRepo) List(context.Context, Identity, *dynq.Query) (*FeatureFlagList, error) {
	m.listed = true
	return &FeatureFlagList{}, nil
}

func (m *mockFeatureFlagRepo) Create(_ context.Context, _ Identity, f *FeatureFlag) (*FeatureFlag, error) {
	m.created = f
	if m.repoErr != nil {
		return nil, m.repoErr
	}
	out := *f
	out.ID = 300
	out.TenantID = "t-1"
	return &out, nil
}

func (m *mockFeatureFlagRepo) Update(_ context.Context, _ Identity, f *FeatureFlag) (*FeatureFlag, error) {
	m.updated = f
	if m.repoErr != nil {
		return nil, m.repoErr
	}
	return f, nil
}

func (m *mockFeatureFlagRepo) Delete(_ context.Context, _ Identity, id int64) error {
	if m.repoErr != nil {
		return m.repoErr
	}
	m.deleted = append(m.deleted, id)
	return nil
}

// mockTranslationRepo 捕获翻译仓储调用并按用例配置返回值。
type mockTranslationRepo struct {
	TranslationRepo

	getID   int64
	listed  bool
	created *Translation
	updated *Translation
	deleted []int64
	repoErr error
}

func (m *mockTranslationRepo) Get(_ context.Context, _ Identity, id int64) (*Translation, error) {
	m.getID = id
	if m.repoErr != nil {
		return nil, m.repoErr
	}
	return &Translation{ID: id, Status: ConfigStatusEnabled, TableName: "sys_menu", RowID: 1,
		ColumnName: "name", Locale: "en-US", Value: "v", TenantID: "t-1"}, nil
}

func (m *mockTranslationRepo) List(context.Context, Identity, *dynq.Query) (*TranslationList, error) {
	m.listed = true
	return &TranslationList{}, nil
}

func (m *mockTranslationRepo) Create(_ context.Context, _ Identity, t *Translation) (*Translation, error) {
	m.created = t
	if m.repoErr != nil {
		return nil, m.repoErr
	}
	out := *t
	out.ID = 400
	out.TenantID = "t-1"
	return &out, nil
}

func (m *mockTranslationRepo) Update(_ context.Context, _ Identity, t *Translation) (*Translation, error) {
	m.updated = t
	if m.repoErr != nil {
		return nil, m.repoErr
	}
	return t, nil
}

func (m *mockTranslationRepo) Delete(_ context.Context, _ Identity, id int64) error {
	if m.repoErr != nil {
		return m.repoErr
	}
	m.deleted = append(m.deleted, id)
	return nil
}

func TestFeatureFlagUsecaseCreateDefaultsUnspecifiedStatusToEnabled(t *testing.T) {
	repo := &mockFeatureFlagRepo{}
	uc := NewFeatureFlagUsecase(repo)

	out, err := uc.CreateFeatureFlag(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&FeatureFlag{Status: ConfigStatusUnspecified, Name: "  导出  ", FlagKey: "  export_enabled  ",
			IsEnabled: true, TenantID: "t-forged"})
	if err != nil {
		t.Fatalf("create feature flag: %v", err)
	}
	if out.Status != ConfigStatusEnabled {
		t.Fatalf("status = %d, want enabled", out.Status)
	}
	if repo.created == nil || repo.created.Name != "导出" || repo.created.FlagKey != "export_enabled" {
		t.Fatalf("repo created = (%+v), want trimmed (导出, export_enabled)", repo.created)
	}
	if repo.created.TenantID != "" {
		t.Fatalf("repo created tenant id = %q, want empty (forged value dropped)", repo.created.TenantID)
	}
	if !repo.created.IsEnabled {
		t.Fatal("repo created is_enabled = false, want true")
	}
}

func TestFeatureFlagUsecaseCreateKeepsExplicitDisabled(t *testing.T) {
	repo := &mockFeatureFlagRepo{}
	uc := NewFeatureFlagUsecase(repo)

	_, err := uc.CreateFeatureFlag(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&FeatureFlag{Name: "paused", FlagKey: "paused", Status: ConfigStatusDisabled})
	if err != nil {
		t.Fatalf("create feature flag: %v", err)
	}
	if repo.created == nil || repo.created.Status != ConfigStatusDisabled {
		t.Fatalf("repo created status = %+v, want disabled", repo.created)
	}
}

func TestFeatureFlagUsecaseCreateRejectsInvalidInput(t *testing.T) {
	base := func() *FeatureFlag {
		return &FeatureFlag{Name: "flag", FlagKey: "k", Status: ConfigStatusEnabled}
	}
	cases := []struct {
		name string
		in   *FeatureFlag
	}{
		{name: "nil", in: nil},
		{name: "blank name", in: &FeatureFlag{Name: "   ", FlagKey: "k"}},
		{name: "name over width", in: &FeatureFlag{Name: strings.Repeat("名", 129), FlagKey: "k"}},
		{name: "blank flag key", in: &FeatureFlag{Name: "flag", FlagKey: "  "}},
		{name: "flag key over width", in: &FeatureFlag{Name: "flag", FlagKey: strings.Repeat("k", 129)}},
		{name: "status invalid", in: func() *FeatureFlag {
			f := base()
			f.Status = 2
			return f
		}()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &mockFeatureFlagRepo{}
			uc := NewFeatureFlagUsecase(repo)
			_, err := uc.CreateFeatureFlag(context.Background(), Identity{TenantID: "t-1", UserID: 1}, tc.in)
			if !errors.Is(err, ErrFeatureFlagInvalidArgument) {
				t.Fatalf("err = %v, want ErrFeatureFlagInvalidArgument", err)
			}
			if repo.created != nil {
				t.Fatalf("repo must not be called on invalid input, got %+v", repo.created)
			}
		})
	}
}

func TestFeatureFlagUsecaseCreateRejectsTextOverColumnWidth(t *testing.T) {
	description := strings.Repeat("描", 513)
	remark := strings.Repeat("注", 513)
	for _, tc := range []struct {
		name string
		in   *FeatureFlag
	}{
		{name: "description 513", in: &FeatureFlag{Name: "flag", FlagKey: "k", Description: &description}},
		{name: "remark 513", in: &FeatureFlag{Name: "flag", FlagKey: "k", Remark: &remark}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &mockFeatureFlagRepo{}
			uc := NewFeatureFlagUsecase(repo)
			_, err := uc.CreateFeatureFlag(context.Background(), Identity{TenantID: "t-1", UserID: 1}, tc.in)
			if !errors.Is(err, ErrFeatureFlagInvalidArgument) {
				t.Fatalf("err = %v, want ErrFeatureFlagInvalidArgument", err)
			}
			if repo.created != nil {
				t.Fatalf("repo must not be called on invalid input, got %+v", repo.created)
			}
		})
	}
}

func TestFeatureFlagUsecaseCreateAcceptsColumnWidthBoundaries(t *testing.T) {
	description := strings.Repeat("描", 512)
	remark := strings.Repeat("注", 512)
	repo := &mockFeatureFlagRepo{}
	uc := NewFeatureFlagUsecase(repo)

	_, err := uc.CreateFeatureFlag(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&FeatureFlag{Name: strings.Repeat("名", 128), FlagKey: strings.Repeat("k", 128),
			Description: &description, Remark: &remark})
	if err != nil {
		t.Fatalf("create feature flag at column width boundaries: %v", err)
	}
}

func TestFeatureFlagUsecaseUpdateIgnoresIdentityColumns(t *testing.T) {
	repo := &mockFeatureFlagRepo{}
	uc := NewFeatureFlagUsecase(repo)

	_, err := uc.UpdateFeatureFlag(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&FeatureFlag{ID: 1, Status: ConfigStatusEnabled, Name: "  新名  ", FlagKey: "hacked", TenantID: "t-forged"})
	if err != nil {
		t.Fatalf("update feature flag: %v", err)
	}
	if repo.updated == nil || repo.updated.Name != "新名" {
		t.Fatalf("repo updated name = %+v, want trimmed 新名", repo.updated)
	}
	if repo.updated.FlagKey != "" || repo.updated.TenantID != "" {
		t.Fatalf("repo updated identity columns = (%q, %q), want dropped", repo.updated.FlagKey, repo.updated.TenantID)
	}
}

func TestFeatureFlagUsecaseUpdateRejectsInvalidInput(t *testing.T) {
	remark := strings.Repeat("注", 513)
	cases := []struct {
		name string
		in   *FeatureFlag
	}{
		{name: "nil", in: nil},
		{name: "missing id", in: &FeatureFlag{ID: 0, Name: "flag", Status: ConfigStatusEnabled}},
		{name: "unspecified status", in: &FeatureFlag{ID: 1, Name: "flag", Status: ConfigStatusUnspecified}},
		{name: "status invalid", in: &FeatureFlag{ID: 1, Name: "flag", Status: 2}},
		{name: "blank name", in: &FeatureFlag{ID: 1, Name: "  ", Status: ConfigStatusEnabled}},
		{name: "remark over width", in: &FeatureFlag{ID: 1, Name: "flag", Status: ConfigStatusEnabled, Remark: &remark}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &mockFeatureFlagRepo{}
			uc := NewFeatureFlagUsecase(repo)
			_, err := uc.UpdateFeatureFlag(context.Background(), Identity{TenantID: "t-1", UserID: 1}, tc.in)
			if !errors.Is(err, ErrFeatureFlagInvalidArgument) {
				t.Fatalf("err = %v, want ErrFeatureFlagInvalidArgument", err)
			}
			if repo.updated != nil {
				t.Fatalf("repo must not be called on invalid input, got %+v", repo.updated)
			}
		})
	}
}

func TestFeatureFlagUsecaseGetAndDeleteRejectNonPositiveID(t *testing.T) {
	repo := &mockFeatureFlagRepo{}
	uc := NewFeatureFlagUsecase(repo)

	if _, err := uc.GetFeatureFlag(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 0); !errors.Is(err, ErrFeatureFlagInvalidArgument) {
		t.Fatalf("get err = %v, want ErrFeatureFlagInvalidArgument", err)
	}
	if err := uc.DeleteFeatureFlag(context.Background(), Identity{TenantID: "t-1", UserID: 1}, -1); !errors.Is(err, ErrFeatureFlagInvalidArgument) {
		t.Fatalf("delete err = %v, want ErrFeatureFlagInvalidArgument", err)
	}
	if len(repo.deleted) != 0 {
		t.Fatalf("repo must not be called, got %+v", repo.deleted)
	}
}

func TestFeatureFlagUsecasePropagatesRepoErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		call func(*FeatureFlagUsecase) error
	}{
		{name: "get not found", err: ErrFeatureFlagNotFound, call: func(uc *FeatureFlagUsecase) error {
			_, err := uc.GetFeatureFlag(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 9)
			return err
		}},
		{name: "create already exists", err: ErrFeatureFlagAlreadyExists, call: func(uc *FeatureFlagUsecase) error {
			_, err := uc.CreateFeatureFlag(context.Background(), Identity{TenantID: "t-1", UserID: 1},
				&FeatureFlag{Name: "flag", FlagKey: "dup", Status: ConfigStatusEnabled})
			return err
		}},
		{name: "update not found", err: ErrFeatureFlagNotFound, call: func(uc *FeatureFlagUsecase) error {
			_, err := uc.UpdateFeatureFlag(context.Background(), Identity{TenantID: "t-1", UserID: 1},
				&FeatureFlag{ID: 9, Name: "flag", Status: ConfigStatusEnabled})
			return err
		}},
		{name: "delete not found", err: ErrFeatureFlagNotFound, call: func(uc *FeatureFlagUsecase) error {
			return uc.DeleteFeatureFlag(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 9)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			uc := NewFeatureFlagUsecase(&mockFeatureFlagRepo{repoErr: tc.err})
			if err := tc.call(uc); !errors.Is(err, tc.err) {
				t.Fatalf("err = %v, want %v", err, tc.err)
			}
		})
	}
}

func TestFeatureFlagUsecaseListForwardsQuery(t *testing.T) {
	repo := &mockFeatureFlagRepo{}
	uc := NewFeatureFlagUsecase(repo)

	if _, err := uc.ListFeatureFlags(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &dynq.Query{}); err != nil {
		t.Fatalf("list feature flags: %v", err)
	}
	if !repo.listed {
		t.Fatal("repo List must be called")
	}
}

func TestTranslationUsecaseCreateDefaultsStatusAndTrimsColumns(t *testing.T) {
	repo := &mockTranslationRepo{}
	uc := NewTranslationUsecase(repo)

	out, err := uc.CreateTranslation(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&Translation{Status: ConfigStatusUnspecified, TableName: "  sys_menu  ", RowID: 201,
			ColumnName: "  name  ", Locale: "  en-US  ", Value: "  Hello  ", TenantID: "t-forged"})
	if err != nil {
		t.Fatalf("create translation: %v", err)
	}
	if out.Status != ConfigStatusEnabled {
		t.Fatalf("status = %d, want enabled", out.Status)
	}
	if repo.created == nil {
		t.Fatal("repo Create must be called")
	}
	if repo.created.TableName != "sys_menu" || repo.created.ColumnName != "name" ||
		repo.created.Locale != "en-US" || repo.created.Value != "Hello" {
		t.Fatalf("repo created = (%q, %q, %q, %q), want trimmed identity and value",
			repo.created.TableName, repo.created.ColumnName, repo.created.Locale, repo.created.Value)
	}
	if repo.created.TenantID != "" {
		t.Fatalf("repo created tenant id = %q, want empty (forged value dropped)", repo.created.TenantID)
	}
}

func TestTranslationUsecaseCreateKeepsExplicitDisabled(t *testing.T) {
	repo := &mockTranslationRepo{}
	uc := NewTranslationUsecase(repo)

	_, err := uc.CreateTranslation(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&Translation{Status: ConfigStatusDisabled, TableName: "sys_menu", RowID: 1,
			ColumnName: "name", Locale: "en-US", Value: "v"})
	if err != nil {
		t.Fatalf("create translation: %v", err)
	}
	if repo.created == nil || repo.created.Status != ConfigStatusDisabled {
		t.Fatalf("repo created status = %+v, want disabled", repo.created)
	}
}

func TestTranslationUsecaseCreateRejectsInvalidInput(t *testing.T) {
	base := func() *Translation {
		return &Translation{TableName: "sys_menu", RowID: 1, ColumnName: "name", Locale: "en-US", Value: "v"}
	}
	cases := []struct {
		name string
		in   *Translation
	}{
		{name: "nil", in: nil},
		{name: "blank value", in: func() *Translation { tr := base(); tr.Value = "   "; return tr }()},
		{name: "blank table name", in: func() *Translation { tr := base(); tr.TableName = "  "; return tr }()},
		{name: "table name over width", in: func() *Translation {
			tr := base()
			tr.TableName = strings.Repeat("t", 65)
			return tr
		}()},
		{name: "blank column name", in: func() *Translation { tr := base(); tr.ColumnName = "  "; return tr }()},
		{name: "column name over width", in: func() *Translation {
			tr := base()
			tr.ColumnName = strings.Repeat("c", 65)
			return tr
		}()},
		{name: "row id zero", in: func() *Translation { tr := base(); tr.RowID = 0; return tr }()},
		{name: "row id negative", in: func() *Translation { tr := base(); tr.RowID = -1; return tr }()},
		{name: "locale malformed", in: func() *Translation { tr := base(); tr.Locale = "en_US"; return tr }()},
		{name: "locale single letter", in: func() *Translation { tr := base(); tr.Locale = "e"; return tr }()},
		{name: "locale over width", in: func() *Translation { tr := base(); tr.Locale = "zh-Hant-CNX"; return tr }()},
		{name: "status invalid", in: func() *Translation { tr := base(); tr.Status = 2; return tr }()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &mockTranslationRepo{}
			uc := NewTranslationUsecase(repo)
			_, err := uc.CreateTranslation(context.Background(), Identity{TenantID: "t-1", UserID: 1}, tc.in)
			if !errors.Is(err, ErrTranslationInvalidArgument) {
				t.Fatalf("err = %v, want ErrTranslationInvalidArgument", err)
			}
			if repo.created != nil {
				t.Fatalf("repo must not be called on invalid input, got %+v", repo.created)
			}
		})
	}
}

func TestTranslationUsecaseCreateAcceptsLocaleFormsAndColumnWidths(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US", "ja-JP", "ko-KR", "zh-Hant-CN"} {
		t.Run(locale, func(t *testing.T) {
			repo := &mockTranslationRepo{}
			uc := NewTranslationUsecase(repo)

			_, err := uc.CreateTranslation(context.Background(), Identity{TenantID: "t-1", UserID: 1},
				&Translation{TableName: strings.Repeat("t", 64), RowID: 1,
					ColumnName: strings.Repeat("c", 64), Locale: locale, Value: "v"})
			if err != nil {
				t.Fatalf("create translation with locale %q: %v", locale, err)
			}
		})
	}
}

func TestTranslationUsecaseCreateRejectsRemarkOverColumnWidth(t *testing.T) {
	remark := strings.Repeat("注", 513)
	repo := &mockTranslationRepo{}
	uc := NewTranslationUsecase(repo)

	_, err := uc.CreateTranslation(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&Translation{TableName: "sys_menu", RowID: 1, ColumnName: "name", Locale: "en-US", Value: "v", Remark: &remark})
	if !errors.Is(err, ErrTranslationInvalidArgument) {
		t.Fatalf("err = %v, want ErrTranslationInvalidArgument", err)
	}
	if repo.created != nil {
		t.Fatalf("repo must not be called on invalid input, got %+v", repo.created)
	}
}

func TestTranslationUsecaseAcceptsRemarkAtColumnWidth(t *testing.T) {
	remark := strings.Repeat("注", 512)
	t.Run("create", func(t *testing.T) {
		repo := &mockTranslationRepo{}
		uc := NewTranslationUsecase(repo)

		_, err := uc.CreateTranslation(context.Background(), Identity{TenantID: "t-1", UserID: 1},
			&Translation{TableName: "sys_menu", RowID: 1, ColumnName: "name", Locale: "en-US",
				Value: "v", Remark: &remark})
		if err != nil {
			t.Fatalf("create translation with 512-rune remark: %v", err)
		}
		if repo.created == nil || repo.created.Remark == nil || *repo.created.Remark != remark {
			t.Fatalf("repo created remark = %+v, want 512-rune remark", repo.created)
		}
	})
	t.Run("update", func(t *testing.T) {
		repo := &mockTranslationRepo{}
		uc := NewTranslationUsecase(repo)

		_, err := uc.UpdateTranslation(context.Background(), Identity{TenantID: "t-1", UserID: 1},
			&Translation{ID: 1, Status: ConfigStatusEnabled, Value: "v", Remark: &remark})
		if err != nil {
			t.Fatalf("update translation with 512-rune remark: %v", err)
		}
		if repo.updated == nil || repo.updated.Remark == nil || *repo.updated.Remark != remark {
			t.Fatalf("repo updated remark = %+v, want 512-rune remark", repo.updated)
		}
	})
}

func TestTranslationUsecaseUpdateIgnoresIdentityColumns(t *testing.T) {
	repo := &mockTranslationRepo{}
	uc := NewTranslationUsecase(repo)

	_, err := uc.UpdateTranslation(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&Translation{ID: 1, Status: ConfigStatusEnabled, Value: "  New  ",
			TableName: "hacked", RowID: 999, ColumnName: "hacked", Locale: "xx-XX", TenantID: "t-forged"})
	if err != nil {
		t.Fatalf("update translation: %v", err)
	}
	if repo.updated == nil {
		t.Fatal("repo Update must be called")
	}
	if repo.updated.Value != "New" {
		t.Fatalf("repo updated value = %q, want trimmed New", repo.updated.Value)
	}
	if repo.updated.TableName != "" || repo.updated.RowID != 0 ||
		repo.updated.ColumnName != "" || repo.updated.Locale != "" || repo.updated.TenantID != "" {
		t.Fatalf("repo updated identity columns = (%q, %d, %q, %q, %q), want dropped",
			repo.updated.TableName, repo.updated.RowID, repo.updated.ColumnName, repo.updated.Locale, repo.updated.TenantID)
	}
}

func TestTranslationUsecaseUpdateRejectsInvalidInput(t *testing.T) {
	remark := strings.Repeat("注", 513)
	cases := []struct {
		name string
		in   *Translation
	}{
		{name: "nil", in: nil},
		{name: "missing id", in: &Translation{ID: 0, Status: ConfigStatusEnabled, Value: "v"}},
		{name: "unspecified status", in: &Translation{ID: 1, Status: ConfigStatusUnspecified, Value: "v"}},
		{name: "status invalid", in: &Translation{ID: 1, Status: 2, Value: "v"}},
		{name: "blank value", in: &Translation{ID: 1, Status: ConfigStatusEnabled, Value: "   "}},
		{name: "remark over width", in: &Translation{ID: 1, Status: ConfigStatusEnabled, Value: "v", Remark: &remark}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &mockTranslationRepo{}
			uc := NewTranslationUsecase(repo)
			_, err := uc.UpdateTranslation(context.Background(), Identity{TenantID: "t-1", UserID: 1}, tc.in)
			if !errors.Is(err, ErrTranslationInvalidArgument) {
				t.Fatalf("err = %v, want ErrTranslationInvalidArgument", err)
			}
			if repo.updated != nil {
				t.Fatalf("repo must not be called on invalid input, got %+v", repo.updated)
			}
		})
	}
}

func TestTranslationUsecaseGetAndDeleteRejectNonPositiveID(t *testing.T) {
	repo := &mockTranslationRepo{}
	uc := NewTranslationUsecase(repo)

	if _, err := uc.GetTranslation(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 0); !errors.Is(err, ErrTranslationInvalidArgument) {
		t.Fatalf("get err = %v, want ErrTranslationInvalidArgument", err)
	}
	if err := uc.DeleteTranslation(context.Background(), Identity{TenantID: "t-1", UserID: 1}, -1); !errors.Is(err, ErrTranslationInvalidArgument) {
		t.Fatalf("delete err = %v, want ErrTranslationInvalidArgument", err)
	}
	if len(repo.deleted) != 0 {
		t.Fatalf("repo must not be called, got %+v", repo.deleted)
	}
}

func TestTranslationUsecasePropagatesRepoErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		call func(*TranslationUsecase) error
	}{
		{name: "get not found", err: ErrTranslationNotFound, call: func(uc *TranslationUsecase) error {
			_, err := uc.GetTranslation(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 9)
			return err
		}},
		{name: "create already exists", err: ErrTranslationAlreadyExists, call: func(uc *TranslationUsecase) error {
			_, err := uc.CreateTranslation(context.Background(), Identity{TenantID: "t-1", UserID: 1},
				&Translation{TableName: "sys_menu", RowID: 1, ColumnName: "name", Locale: "en-US", Value: "v"})
			return err
		}},
		{name: "update not found", err: ErrTranslationNotFound, call: func(uc *TranslationUsecase) error {
			_, err := uc.UpdateTranslation(context.Background(), Identity{TenantID: "t-1", UserID: 1},
				&Translation{ID: 9, Status: ConfigStatusEnabled, Value: "v"})
			return err
		}},
		{name: "delete not found", err: ErrTranslationNotFound, call: func(uc *TranslationUsecase) error {
			return uc.DeleteTranslation(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 9)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			uc := NewTranslationUsecase(&mockTranslationRepo{repoErr: tc.err})
			if err := tc.call(uc); !errors.Is(err, tc.err) {
				t.Fatalf("err = %v, want %v", err, tc.err)
			}
		})
	}
}

func TestTranslationUsecaseListForwardsQuery(t *testing.T) {
	repo := &mockTranslationRepo{}
	uc := NewTranslationUsecase(repo)

	if _, err := uc.ListTranslations(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &dynq.Query{}); err != nil {
		t.Fatalf("list translations: %v", err)
	}
	if !repo.listed {
		t.Fatal("repo List must be called")
	}
}
