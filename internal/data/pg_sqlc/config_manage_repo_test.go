package pg_sqlc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"grape/internal/biz"
	"grape/internal/data/pg_sqlc/deploy"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

// 配置族仓储集成测试（真实 PostgreSQL，未设 GRAPE_TEST_ADMIN_DSN /
// GRAPE_TEST_DSN 时跳过）：覆盖 sys_config（平台级，无 RLS，经受控写函数）
// 与 sys_tenant_config（租户表，RLS）的 CRUD、唯一键与软删语义、动态查询
// 与租户边界。setupTestDB 每次重建数据库；种子见 sqlc/seed/ 与 test_seed.sql。

const (
	// configTenantSeeded 是种子全局/租户配置所在的租户。
	configTenantSeeded = "t-super-01"
	// configTenantOther 是种子租户配置 3-4 所在的租户，供跨租户断言使用。
	configTenantOther = "t-test-02"
)

// configAuth 返回指定租户的操作者身份；配置表无 @scope，操作者仅落入
// 审计列（全局表由函数校验认证事务纪律，租户表由 RLS 承担边界）。
func configAuth(tenant string) biz.Identity {
	return biz.Identity{TenantID: tenant, UserID: 1}
}

// mustCreateConfig 创建全局配置，失败即终止用例。
func mustCreateConfig(t *testing.T, d *Data, auth biz.Identity, c *biz.Config) *biz.Config {
	t.Helper()
	out, err := NewConfigRepo(d).CreateSuper(context.Background(), auth, c)
	if err != nil {
		t.Fatalf("create config %s: %v", c.ConfigKey, err)
	}
	return out
}

// mustCreateTenantConfig 创建租户配置，失败即终止用例。
func mustCreateTenantConfig(t *testing.T, d *Data, auth biz.Identity, c *biz.TenantConfig) *biz.TenantConfig {
	t.Helper()
	out, err := NewTenantConfigRepo(d).Create(context.Background(), auth, c)
	if err != nil {
		t.Fatalf("create tenant config %s: %v", c.ConfigKey, err)
	}
	return out
}

// configKeyQuery 构造按 config_key 精确过滤的动态查询。
func configKeyQuery(key string) *dynq.Query {
	return &dynq.Query{
		Where: []dynq.Condition{{Field: "configKey", Op: "eq", Value: key}},
	}
}

func TestConfigRepo_get_seeded_row(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	out, err := NewConfigRepo(d).Get(context.Background(), configAuth(configTenantSeeded), 1)
	if err != nil {
		t.Fatalf("get config 1: %v", err)
	}
	if out.ConfigKey != "session.timeout" || out.ValueType != "integer" {
		t.Fatalf("row = (%s, %s), want (session.timeout, integer)", out.ConfigKey, out.ValueType)
	}
	if out.Value == nil || *out.Value != "1800" {
		t.Fatalf("value = %+v, want 1800", out.Value)
	}
}

func TestConfigRepo_get_is_global_across_tenants(t *testing.T) {
	t.Parallel()
	// sys_config 无 tenant_id 与 RLS：任意认证租户都读到同一份全局配置。
	d := setupTestDB(t)

	out, err := NewConfigRepo(d).Get(context.Background(), configAuth("t-config-none"), 1)
	if err != nil {
		t.Fatalf("get global config from other tenant: %v", err)
	}
	if out.ConfigKey != "session.timeout" {
		t.Fatalf("config key = %s, want session.timeout", out.ConfigKey)
	}
}

func TestConfigRepo_create_super_writes_via_controlled_function(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := configAuth(configTenantSeeded)

	created := mustCreateConfig(t, d, auth, &biz.Config{
		Status: biz.ConfigStatusEnabled, Name: "新增配置", ConfigKey: "test.new", ValueType: "string",
	})
	if created.ID == 0 || created.CreatedBy != auth.UserID || created.UpdatedBy != auth.UserID {
		t.Fatalf("created = (%d, by %d/%d), want positive id and operator audit columns",
			created.ID, created.CreatedBy, created.UpdatedBy)
	}

	got, err := NewConfigRepo(d).Get(context.Background(), auth, created.ID)
	if err != nil {
		t.Fatalf("re-read created config: %v", err)
	}
	if got.ConfigKey != "test.new" || got.Name != "新增配置" {
		t.Fatalf("row = (%s, %s), want (test.new, 新增配置)", got.ConfigKey, got.Name)
	}
}

// assertPermissionDenied 断言直写被运行角色权限拒绝（SQLSTATE 42501）。
// 只判错误码不判报错文案：服务端 lc_messages 本地化后文案随语言变化
// （如中文「权限不够」），42501 已等价表达 permission denied。
func assertPermissionDenied(t *testing.T, err error) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("direct write err = %v, want permission denied (42501)", err)
	}
}

func TestConfigRepo_direct_insert_denied_for_runtime_role(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := configAuth(configTenantSeeded)

	// role.sql 对 grape_u REVOKE sys_config 写授权：直写被权限拒绝
	// （42501），受控写函数是唯一写路径。
	err := d.IdentityTx(context.Background(), pgAuth(auth), func(s *IdentityTx) error {
		_, err := s.Tx().Exec(context.Background(),
			`INSERT INTO public.sys_config (id, status, "name", config_key, value_type, created_by, updated_by)
			 VALUES ($1, 1, 'smuggled', 'test.smuggled', 'string', 1, 1)`, newID())
		return err
	})
	assertPermissionDenied(t, err)
}

func TestConfigRepo_direct_update_denied_for_runtime_role(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := configAuth(configTenantSeeded)

	err := d.IdentityTx(context.Background(), pgAuth(auth), func(s *IdentityTx) error {
		_, err := s.Tx().Exec(context.Background(),
			`UPDATE public.sys_config SET "name" = 'smuggled' WHERE id = 1`)
		return err
	})
	assertPermissionDenied(t, err)
}

func TestConfigRepo_direct_delete_denied_for_runtime_role(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := configAuth(configTenantSeeded)

	err := d.IdentityTx(context.Background(), pgAuth(auth), func(s *IdentityTx) error {
		_, err := s.Tx().Exec(context.Background(),
			`DELETE FROM public.sys_config WHERE id = 1`)
		return err
	})
	assertPermissionDenied(t, err)
}

func TestConfigRepo_controlled_functions_deny_without_tenant(t *testing.T) {
	t.Parallel()
	// 三个受控写函数以 current_setting('app.tenant_id', true) 校验认证事务
	// 纪律：裸连接（不经 IdentityTx）未设置该变量时逐一拒绝，且不产生任何
	// 写入（新建行不存在、种子行未被篡改或软删）。
	d := setupTestDB(t)
	ctx := context.Background()

	var before int
	if err := d.db.QueryRow(ctx, "SELECT count(*) FROM public.sys_config").Scan(&before); err != nil {
		t.Fatalf("count configs before: %v", err)
	}
	var seededName, seededConfigKey, seededValueType string
	var seededStatus int16
	var seededValue, seededRemark *string
	var seededCreatedBy, seededUpdatedBy int64
	var seededUpdatedAt time.Time
	var seededDeleted bool
	if err := d.db.QueryRow(ctx,
		`SELECT "name", status, config_key, value_type, value, remark, created_by, updated_by, updated_at,
		        deleted_at IS NOT NULL
		 FROM public.sys_config WHERE id = 1`).
		Scan(&seededName, &seededStatus, &seededConfigKey, &seededValueType, &seededValue, &seededRemark,
			&seededCreatedBy, &seededUpdatedBy, &seededUpdatedAt, &seededDeleted); err != nil {
		t.Fatalf("read seeded config before: %v", err)
	}

	newRowID := newID()
	cases := []struct {
		name string
		sql  string
		args []any
	}{
		{
			name: "create",
			sql: `SELECT * FROM public.fn_config_create($1::int8, 1::int2, 'x'::varchar, 'test.no-tenant'::varchar,
			                                           'v'::text, 'string'::varchar, NULL::varchar, 1::int8)`,
			args: []any{newRowID},
		},
		{
			name: "update",
			sql:  `SELECT * FROM public.fn_config_update(1::int8, 0::int2, 'hacked'::varchar, 'hacked'::text, 'hacked'::varchar, 1::int8)`,
		},
		{
			name: "delete",
			sql:  `SELECT public.fn_config_delete(1::int8, 1::int8)`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := d.db.Exec(ctx, tc.sql, tc.args...)
			if err == nil || !strings.Contains(err.Error(), "config write requires app.tenant_id") {
				t.Fatalf("err = %v, want app.tenant_id rejection", err)
			}
		})
	}

	var after int
	if err := d.db.QueryRow(ctx, "SELECT count(*) FROM public.sys_config").Scan(&after); err != nil {
		t.Fatalf("count configs after: %v", err)
	}
	if after != before {
		t.Fatalf("config count after rejected calls = %d, want unchanged %d", after, before)
	}
	var created bool
	if err := d.db.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM public.sys_config WHERE id = $1)", newRowID).Scan(&created); err != nil {
		t.Fatalf("check rejected create: %v", err)
	}
	if created {
		t.Fatalf("rejected create wrote row %d", newRowID)
	}
	var gotName, gotConfigKey, gotValueType string
	var gotStatus int16
	var gotValue, gotRemark *string
	var gotCreatedBy, gotUpdatedBy int64
	var gotUpdatedAt time.Time
	var gotDeleted bool
	if err := d.db.QueryRow(ctx,
		`SELECT "name", status, config_key, value_type, value, remark, created_by, updated_by, updated_at,
		        deleted_at IS NOT NULL
		 FROM public.sys_config WHERE id = 1`).
		Scan(&gotName, &gotStatus, &gotConfigKey, &gotValueType, &gotValue, &gotRemark,
			&gotCreatedBy, &gotUpdatedBy, &gotUpdatedAt, &gotDeleted); err != nil {
		t.Fatalf("read seeded config after: %v", err)
	}
	if gotName != seededName || gotStatus != seededStatus ||
		gotConfigKey != seededConfigKey || gotValueType != seededValueType ||
		!sameOptionalString(gotValue, seededValue) || !sameOptionalString(gotRemark, seededRemark) ||
		gotCreatedBy != seededCreatedBy || gotUpdatedBy != seededUpdatedBy ||
		!gotUpdatedAt.Equal(seededUpdatedAt) || gotDeleted != seededDeleted {
		t.Fatalf("seeded row changed: got (%q, status %d, key %q, type %q, value %+v, remark %+v, by %d/%d, at %v, deleted %v), "+
			"want (%q, status %d, key %q, type %q, value %+v, remark %+v, by %d/%d, at %v, deleted %v)",
			gotName, gotStatus, gotConfigKey, gotValueType, gotValue, gotRemark, gotCreatedBy, gotUpdatedBy, gotUpdatedAt, gotDeleted,
			seededName, seededStatus, seededConfigKey, seededValueType, seededValue, seededRemark, seededCreatedBy, seededUpdatedBy, seededUpdatedAt, seededDeleted)
	}
}

// sameOptionalString 比较两个可空字符串的指针内容（nil 与空串不同）。
func sameOptionalString(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// assertRowLevelSecurityDenied 断言裸连接写入被 RLS 的 WITH CHECK 拒绝
// （SQLSTATE 42501：row-level security policy 不满足）。
func assertRowLevelSecurityDenied(t *testing.T, err error) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("err = %v, want row-level security denial (42501)", err)
	}
}

// backdateUpdatedAt 以部署超级用户把指定表的 updated_at 回拨为
// CURRENT_TIMESTAMP - 10 秒并返回新值：审计列用例以已知旧时间做比较，
// 不依赖真实睡眠（sys_config 对运行角色无直写权限，回拨必须走超级用户
// 面；tenant 表同样走该面以保持单一写法）。表名只取测试内字面量。
func backdateUpdatedAt(t *testing.T, table string, id int64) time.Time {
	t.Helper()
	cfg, err := pgx.ParseConfig(os.Getenv("GRAPE_TEST_ADMIN_DSN"))
	if err != nil {
		t.Fatalf("parse admin dsn: %v", err)
	}
	cfg.Database = deploy.DatabaseNameFor(t.Name())
	conn, err := pgx.ConnectConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	defer func() {
		if err := conn.Close(context.Background()); err != nil {
			t.Fatalf("close admin: %v", err)
		}
	}()
	var out time.Time
	stmt := fmt.Sprintf(
		`UPDATE %s SET updated_at = CURRENT_TIMESTAMP - interval '10 seconds' WHERE id = $1 RETURNING updated_at`, table)
	if err := conn.QueryRow(context.Background(), stmt, id).Scan(&out); err != nil {
		t.Fatalf("backdate %s id %d updated_at: %v", table, id, err)
	}
	return out
}

func TestConfigRepo_create_duplicate_key_returns_already_exists(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	_, err := NewConfigRepo(d).CreateSuper(context.Background(), configAuth(configTenantSeeded), &biz.Config{
		Status: biz.ConfigStatusEnabled, Name: "重复", ConfigKey: "session.timeout", ValueType: "integer",
	})
	if !errors.Is(err, biz.ErrConfigAlreadyExists) {
		t.Fatalf("err = %v, want ErrConfigAlreadyExists", err)
	}
}

func TestConfigRepo_create_key_reusable_after_soft_delete(t *testing.T) {
	t.Parallel()
	// 唯一约束限定未删除行间：软删后同 key 可复用。
	d := setupTestDB(t)
	auth := configAuth(configTenantSeeded)
	repo := NewConfigRepo(d)

	first := mustCreateConfig(t, d, auth, &biz.Config{
		Status: biz.ConfigStatusEnabled, Name: "临时", ConfigKey: "test.reuse", ValueType: "string",
	})
	if err := repo.DeleteSuper(context.Background(), auth, first.ID); err != nil {
		t.Fatalf("soft delete config: %v", err)
	}

	second, err := repo.CreateSuper(context.Background(), auth, &biz.Config{
		Status: biz.ConfigStatusEnabled, Name: "复用", ConfigKey: "test.reuse", ValueType: "string",
	})
	if err != nil {
		t.Fatalf("reuse key after soft delete: %v", err)
	}
	if second.ID == first.ID {
		t.Fatalf("reused row id = %d, want a new id", second.ID)
	}
}

func TestConfigRepo_update_covers_mutable_fields_only(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := configAuth(configTenantSeeded)
	repo := NewConfigRepo(d)

	created := mustCreateConfig(t, d, auth, &biz.Config{
		Status: biz.ConfigStatusEnabled, Name: "旧名", ConfigKey: "test.upd", ValueType: "string",
	})
	value := "new-value"
	remark := "新备注"
	updated, err := repo.UpdateSuper(context.Background(), auth, &biz.Config{
		ID: created.ID, Status: biz.ConfigStatusDisabled, Name: "新名", Value: &value, Remark: &remark,
	})
	if err != nil {
		t.Fatalf("update config: %v", err)
	}
	if updated.Name != "新名" || updated.Status != biz.ConfigStatusDisabled {
		t.Fatalf("row = (%s, status %d), want (新名, disabled)", updated.Name, updated.Status)
	}
	if updated.Value == nil || *updated.Value != "new-value" || updated.Remark == nil || *updated.Remark != "新备注" {
		t.Fatalf("row value/remark = (%+v, %+v), want (new-value, 新备注)", updated.Value, updated.Remark)
	}
	if updated.ConfigKey != "test.upd" || updated.ValueType != "string" {
		t.Fatalf("identity columns = (%s, %s), want unchanged (test.upd, string)", updated.ConfigKey, updated.ValueType)
	}
}

func TestConfigRepo_update_missing_row_returns_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	_, err := NewConfigRepo(d).UpdateSuper(context.Background(), configAuth(configTenantSeeded), &biz.Config{
		ID: 999999, Status: biz.ConfigStatusEnabled, Name: "x",
	})
	if !errors.Is(err, biz.ErrConfigNotFound) {
		t.Fatalf("err = %v, want ErrConfigNotFound", err)
	}
}

func TestConfigRepo_delete_missing_row_returns_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	err := NewConfigRepo(d).DeleteSuper(context.Background(), configAuth(configTenantSeeded), 999999)
	if !errors.Is(err, biz.ErrConfigNotFound) {
		t.Fatalf("err = %v, want ErrConfigNotFound", err)
	}
}

func TestConfigRepo_delete_hides_row_from_get(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := configAuth(configTenantSeeded)
	repo := NewConfigRepo(d)

	created := mustCreateConfig(t, d, auth, &biz.Config{
		Status: biz.ConfigStatusEnabled, Name: "待删", ConfigKey: "test.del", ValueType: "string",
	})
	if err := repo.DeleteSuper(context.Background(), auth, created.ID); err != nil {
		t.Fatalf("soft delete config: %v", err)
	}
	if _, err := repo.Get(context.Background(), auth, created.ID); !errors.Is(err, biz.ErrConfigNotFound) {
		t.Fatalf("err = %v, want ErrConfigNotFound after soft delete", err)
	}
}

func TestConfigRepo_list_filters_and_counts_total(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	out, err := NewConfigRepo(d).List(context.Background(), configAuth(configTenantSeeded), configKeyQuery("session.timeout"))
	if err != nil {
		t.Fatalf("list configs: %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].ID != 1 {
		t.Fatalf("items = %+v, want single seeded config 1", out.Items)
	}
	if out.Total == nil || *out.Total != 1 {
		t.Fatalf("total = %+v, want 1", out.Total)
	}
}

func TestConfigRepo_list_page_total_independent_of_page(t *testing.T) {
	t.Parallel()
	// 种子全局配置共 13 条（含模型目录默认键与 oss.* 四键）；第 2 页
	// （页大小 2）的 total 不得随翻页位置递减。
	d := setupTestDB(t)
	page := 2
	pageSize := 2

	q := &dynq.Query{Pagination: &dynq.Pagination{Page: &page, PageSize: &pageSize}}
	out, err := NewConfigRepo(d).List(context.Background(), configAuth(configTenantSeeded), q)
	if err != nil {
		t.Fatalf("list configs page 2: %v", err)
	}
	if len(out.Items) != 2 {
		t.Fatalf("page 2 items = %d, want 2", len(out.Items))
	}
	if out.Total == nil || *out.Total != 13 {
		t.Fatalf("total = %+v, want 13", out.Total)
	}
}

func TestConfigRepo_list_rejects_unknown_field(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	q := &dynq.Query{Where: []dynq.Condition{{Field: "noSuchField", Op: "eq", Value: "x"}}}
	_, err := NewConfigRepo(d).List(context.Background(), configAuth(configTenantSeeded), q)
	if !errors.Is(err, biz.ErrConfigInvalidArgument) {
		t.Fatalf("err = %v, want ErrConfigInvalidArgument", err)
	}
}

func TestTenantConfigRepo_create_lands_session_tenant(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := configAuth(configTenantOther)

	created := mustCreateTenantConfig(t, d, auth, &biz.TenantConfig{
		Status: biz.ConfigStatusEnabled, Name: "新增租户配置", ConfigKey: "test.tenant-new", ValueType: "string",
	})
	if created.TenantID != auth.TenantID {
		t.Fatalf("tenant id = %q, want %q", created.TenantID, auth.TenantID)
	}
	if created.CreatedBy != auth.UserID || created.UpdatedBy != auth.UserID {
		t.Fatalf("audit by = %d/%d, want %d", created.CreatedBy, created.UpdatedBy, auth.UserID)
	}

	got, err := NewTenantConfigRepo(d).Get(context.Background(), auth, created.ID)
	if err != nil {
		t.Fatalf("re-read tenant config: %v", err)
	}
	if got.ConfigKey != "test.tenant-new" || got.TenantID != auth.TenantID {
		t.Fatalf("row = (%s, %s), want (test.tenant-new, %s)", got.ConfigKey, got.TenantID, auth.TenantID)
	}
}

func TestTenantConfigRepo_get_seeded_row(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	out, err := NewTenantConfigRepo(d).Get(context.Background(), configAuth(configTenantSeeded), 1)
	if err != nil {
		t.Fatalf("get tenant config 1: %v", err)
	}
	if out.ConfigKey != "tenant.logo" || out.TenantID != configTenantSeeded {
		t.Fatalf("row = (%s, %s), want (tenant.logo, %s)", out.ConfigKey, out.TenantID, configTenantSeeded)
	}
}

func TestTenantConfigRepo_get_across_tenant_returns_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	_, err := NewTenantConfigRepo(d).Get(context.Background(), configAuth(configTenantOther), 1)
	if !errors.Is(err, biz.ErrTenantConfigNotFound) {
		t.Fatalf("err = %v, want ErrTenantConfigNotFound", err)
	}
}

func TestTenantConfigRepo_create_duplicate_key_returns_already_exists(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	// 种子租户配置 1 的 key 是 tenant.logo。
	_, err := NewTenantConfigRepo(d).Create(context.Background(), configAuth(configTenantSeeded), &biz.TenantConfig{
		Status: biz.ConfigStatusEnabled, Name: "重复", ConfigKey: "tenant.logo", ValueType: "string",
	})
	if !errors.Is(err, biz.ErrTenantConfigAlreadyExists) {
		t.Fatalf("err = %v, want ErrTenantConfigAlreadyExists", err)
	}
}

func TestTenantConfigRepo_create_same_key_allowed_in_other_tenant(t *testing.T) {
	t.Parallel()
	// 唯一约束按 (tenant_id, config_key)：跨租户同 key 合法。
	d := setupTestDB(t)

	out, err := NewTenantConfigRepo(d).Create(context.Background(), configAuth("t-config-none"), &biz.TenantConfig{
		Status: biz.ConfigStatusEnabled, Name: "跨租户同 key", ConfigKey: "tenant.logo", ValueType: "string",
	})
	if err != nil {
		t.Fatalf("create same key in other tenant: %v", err)
	}
	if out.TenantID != "t-config-none" {
		t.Fatalf("tenant id = %q, want t-config-none", out.TenantID)
	}
}

func TestTenantConfigRepo_create_key_reusable_after_soft_delete(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := configAuth(configTenantOther)
	repo := NewTenantConfigRepo(d)

	first := mustCreateTenantConfig(t, d, auth, &biz.TenantConfig{
		Status: biz.ConfigStatusEnabled, Name: "临时", ConfigKey: "test.tenant-reuse", ValueType: "string",
	})
	if err := repo.Delete(context.Background(), auth, first.ID); err != nil {
		t.Fatalf("soft delete tenant config: %v", err)
	}

	second, err := repo.Create(context.Background(), auth, &biz.TenantConfig{
		Status: biz.ConfigStatusEnabled, Name: "复用", ConfigKey: "test.tenant-reuse", ValueType: "string",
	})
	if err != nil {
		t.Fatalf("reuse key after soft delete: %v", err)
	}
	if second.ID == first.ID {
		t.Fatalf("reused row id = %d, want a new id", second.ID)
	}
}

func TestTenantConfigRepo_update_covers_mutable_fields_only(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := configAuth(configTenantOther)
	repo := NewTenantConfigRepo(d)

	created := mustCreateTenantConfig(t, d, auth, &biz.TenantConfig{
		Status: biz.ConfigStatusEnabled, Name: "旧名", ConfigKey: "test.tenant-upd", ValueType: "integer",
	})
	value := "42"
	remark := "新备注"
	updated, err := repo.Update(context.Background(), auth, &biz.TenantConfig{
		ID: created.ID, Status: biz.ConfigStatusDisabled, Name: "新名", Value: &value, Remark: &remark,
	})
	if err != nil {
		t.Fatalf("update tenant config: %v", err)
	}
	if updated.Name != "新名" || updated.Status != biz.ConfigStatusDisabled {
		t.Fatalf("row = (%s, status %d), want (新名, disabled)", updated.Name, updated.Status)
	}
	if updated.Value == nil || *updated.Value != "42" || updated.Remark == nil || *updated.Remark != "新备注" {
		t.Fatalf("row value/remark = (%+v, %+v), want (42, 新备注)", updated.Value, updated.Remark)
	}
	if updated.ConfigKey != "test.tenant-upd" || updated.ValueType != "integer" || updated.TenantID != auth.TenantID {
		t.Fatalf("identity columns = (%s, %s, %s), want unchanged",
			updated.ConfigKey, updated.ValueType, updated.TenantID)
	}
}

func TestTenantConfigRepo_update_across_tenant_returns_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	// 种子租户配置 1 属 t-super-01；t-test-02 视角等同不存在。
	_, err := NewTenantConfigRepo(d).Update(context.Background(), configAuth(configTenantOther), &biz.TenantConfig{
		ID: 1, Status: biz.ConfigStatusEnabled, Name: "x",
	})
	if !errors.Is(err, biz.ErrTenantConfigNotFound) {
		t.Fatalf("err = %v, want ErrTenantConfigNotFound", err)
	}
}

func TestTenantConfigRepo_delete_across_tenant_returns_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	err := NewTenantConfigRepo(d).Delete(context.Background(), configAuth(configTenantOther), 1)
	if !errors.Is(err, biz.ErrTenantConfigNotFound) {
		t.Fatalf("err = %v, want ErrTenantConfigNotFound", err)
	}
	// 目标行在本租户内保持可读。
	if _, err := NewTenantConfigRepo(d).Get(context.Background(), configAuth(configTenantSeeded), 1); err != nil {
		t.Fatalf("target row must stay readable in its own tenant: %v", err)
	}
}

func TestTenantConfigRepo_delete_hides_row_from_get(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := configAuth(configTenantOther)
	repo := NewTenantConfigRepo(d)

	created := mustCreateTenantConfig(t, d, auth, &biz.TenantConfig{
		Status: biz.ConfigStatusEnabled, Name: "待删", ConfigKey: "test.tenant-del", ValueType: "string",
	})
	if err := repo.Delete(context.Background(), auth, created.ID); err != nil {
		t.Fatalf("soft delete tenant config: %v", err)
	}
	if _, err := repo.Get(context.Background(), auth, created.ID); !errors.Is(err, biz.ErrTenantConfigNotFound) {
		t.Fatalf("err = %v, want ErrTenantConfigNotFound after soft delete", err)
	}
}

func TestTenantConfigRepo_list_isolates_tenant(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	repo := NewTenantConfigRepo(d)

	out, err := repo.List(context.Background(), configAuth(configTenantSeeded), configKeyQuery("tenant.display-name"))
	if err != nil {
		t.Fatalf("list tenant configs: %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].ID != 2 {
		t.Fatalf("items = %+v, want single seeded config 2", out.Items)
	}
	if out.Items[0].TenantID != configTenantSeeded {
		t.Fatalf("tenant id = %q, want %q (backfilled from identity)", out.Items[0].TenantID, configTenantSeeded)
	}

	// 同一查询在其他租户内命中该租户自己的行（id 3），不是跨租户行。
	other, err := repo.List(context.Background(), configAuth(configTenantOther), configKeyQuery("tenant.display-name"))
	if err != nil {
		t.Fatalf("list tenant configs in other tenant: %v", err)
	}
	if len(other.Items) != 1 || other.Items[0].ID != 3 {
		t.Fatalf("items = %+v, want single seeded config 3", other.Items)
	}
	if other.Items[0].TenantID != configTenantOther {
		t.Fatalf("tenant id = %q, want %q (backfilled from identity)", other.Items[0].TenantID, configTenantOther)
	}

	// 未知租户（无种子）为空集，不报错。
	empty, err := repo.List(context.Background(), configAuth("t-config-none"), configKeyQuery("tenant.display-name"))
	if err != nil {
		t.Fatalf("list tenant configs in empty tenant: %v", err)
	}
	if len(empty.Items) != 0 {
		t.Fatalf("items = %+v, want empty across tenant", empty.Items)
	}
}

func TestTenantConfigRepo_list_rejects_hidden_and_unknown_fields(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	repo := NewTenantConfigRepo(d)
	cases := map[string]*dynq.Query{
		"unknown where field":       {Where: []dynq.Condition{{Field: "noSuchField", Op: "eq", Value: "x"}}},
		"hidden tenantId in where":  {Where: []dynq.Condition{{Field: "tenantId", Op: "eq", Value: configTenantSeeded}}},
		"hidden tenantId in fields": {Fields: []string{"tenantId"}},
	}
	for name, q := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := repo.List(context.Background(), configAuth(configTenantSeeded), q)
			if !errors.Is(err, biz.ErrTenantConfigInvalidArgument) {
				t.Fatalf("err = %v, want ErrTenantConfigInvalidArgument", err)
			}
		})
	}
}

func TestConfigRepo_list_orders_by_requested_sort(t *testing.T) {
	t.Parallel()
	// 动态协议的 sort 生效：按 id 降序取前 3 条为种子 13/12/11。
	d := setupTestDB(t)
	pageSize := 3

	q := &dynq.Query{
		Sort:       []dynq.SortItem{{Field: "id", Order: "desc"}},
		Pagination: &dynq.Pagination{PageSize: &pageSize},
	}
	out, err := NewConfigRepo(d).List(context.Background(), configAuth(configTenantSeeded), q)
	if err != nil {
		t.Fatalf("list configs sorted: %v", err)
	}
	if len(out.Items) != 3 || out.Items[0].ID != 13 || out.Items[1].ID != 12 || out.Items[2].ID != 11 {
		t.Fatalf("items = %+v, want descending ids 13/12/11", out.Items)
	}
}

func TestTenantConfigRepo_list_orders_by_requested_sort(t *testing.T) {
	t.Parallel()
	// 租户内动态 sort 生效（t-test-02 种子租户配置 3/4，按 id 降序）。
	d := setupTestDB(t)

	q := &dynq.Query{Sort: []dynq.SortItem{{Field: "id", Order: "desc"}}}
	out, err := NewTenantConfigRepo(d).List(context.Background(), configAuth(configTenantOther), q)
	if err != nil {
		t.Fatalf("list tenant configs sorted: %v", err)
	}
	if len(out.Items) != 2 || out.Items[0].ID != 4 || out.Items[1].ID != 3 {
		t.Fatalf("items = %+v, want descending ids 4/3", out.Items)
	}
}

func TestConfigRepo_update_soft_deleted_row_returns_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := configAuth(configTenantSeeded)
	repo := NewConfigRepo(d)

	created := mustCreateConfig(t, d, auth, &biz.Config{
		Status: biz.ConfigStatusEnabled, Name: "已删", ConfigKey: "test.deleted-upd", ValueType: "string",
	})
	if err := repo.DeleteSuper(context.Background(), auth, created.ID); err != nil {
		t.Fatalf("soft delete config: %v", err)
	}
	_, err := repo.UpdateSuper(context.Background(), auth, &biz.Config{
		ID: created.ID, Status: biz.ConfigStatusEnabled, Name: "复活",
	})
	if !errors.Is(err, biz.ErrConfigNotFound) {
		t.Fatalf("err = %v, want ErrConfigNotFound", err)
	}
}

func TestConfigRepo_delete_soft_deleted_row_returns_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := configAuth(configTenantSeeded)
	repo := NewConfigRepo(d)

	created := mustCreateConfig(t, d, auth, &biz.Config{
		Status: biz.ConfigStatusEnabled, Name: "已删", ConfigKey: "test.deleted-del", ValueType: "string",
	})
	if err := repo.DeleteSuper(context.Background(), auth, created.ID); err != nil {
		t.Fatalf("soft delete config: %v", err)
	}
	if err := repo.DeleteSuper(context.Background(), auth, created.ID); !errors.Is(err, biz.ErrConfigNotFound) {
		t.Fatalf("err = %v, want ErrConfigNotFound on repeated delete", err)
	}
}

func TestConfigRepo_list_excludes_soft_deleted_rows(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := configAuth(configTenantSeeded)
	repo := NewConfigRepo(d)

	created := mustCreateConfig(t, d, auth, &biz.Config{
		Status: biz.ConfigStatusEnabled, Name: "隐藏", ConfigKey: "test.hidden", ValueType: "string",
	})
	if err := repo.DeleteSuper(context.Background(), auth, created.ID); err != nil {
		t.Fatalf("soft delete config: %v", err)
	}
	out, err := repo.List(context.Background(), auth, configKeyQuery("test.hidden"))
	if err != nil {
		t.Fatalf("list configs: %v", err)
	}
	if len(out.Items) != 0 {
		t.Fatalf("items = %+v, want empty (soft-deleted rows excluded)", out.Items)
	}
	if out.Total == nil || *out.Total != 0 {
		t.Fatalf("total = %+v, want 0 (Count excludes soft-deleted rows)", out.Total)
	}
}

func TestConfigRepo_update_and_delete_refresh_audit_columns(t *testing.T) {
	t.Parallel()
	// updated_by 记操作者；updated_at 由 trg_touch_updated_at 在 Update 与
	// 软删路径刷新。基线用超管把 updated_at 回拨 10 秒得到已知旧时间，
	// 不依赖真实睡眠（timestamp(0) 精度下时间先后确定可判）。
	d := setupTestDB(t)
	owner := configAuth(configTenantSeeded)
	repo := NewConfigRepo(d)

	created := mustCreateConfig(t, d, owner, &biz.Config{
		Status: biz.ConfigStatusEnabled, Name: "审计", ConfigKey: "test.audit", ValueType: "string",
	})
	baseline := backdateUpdatedAt(t, "public.sys_config", created.ID)

	updater := biz.Identity{TenantID: configTenantSeeded, UserID: 2}
	updated, err := repo.UpdateSuper(context.Background(), updater, &biz.Config{
		ID: created.ID, Status: biz.ConfigStatusEnabled, Name: "审计后",
	})
	if err != nil {
		t.Fatalf("update config: %v", err)
	}
	if updated.UpdatedBy != updater.UserID {
		t.Fatalf("updated_by = %d, want %d", updated.UpdatedBy, updater.UserID)
	}
	if updated.UpdatedAt == nil || !updated.UpdatedAt.After(baseline) {
		t.Fatalf("updated_at = %v, want refreshed after %v", updated.UpdatedAt, baseline)
	}

	baseline = backdateUpdatedAt(t, "public.sys_config", created.ID)
	deleter := biz.Identity{TenantID: configTenantSeeded, UserID: 3}
	if err := repo.DeleteSuper(context.Background(), deleter, created.ID); err != nil {
		t.Fatalf("soft delete config: %v", err)
	}
	var gotBy int64
	var gotDeletedAt, gotUpdatedAt time.Time
	err = d.IdentityTx(context.Background(), pgAuth(owner), func(s *IdentityTx) error {
		return s.Tx().QueryRow(context.Background(),
			`SELECT updated_by, deleted_at, updated_at FROM public.sys_config WHERE id = $1`,
			created.ID).Scan(&gotBy, &gotDeletedAt, &gotUpdatedAt)
	})
	if err != nil {
		t.Fatalf("read audit columns after delete: %v", err)
	}
	if gotBy != deleter.UserID {
		t.Fatalf("updated_by after delete = %d, want %d", gotBy, deleter.UserID)
	}
	if !gotDeletedAt.After(baseline) {
		t.Fatalf("deleted_at = %v, want after %v", gotDeletedAt, baseline)
	}
	if !gotUpdatedAt.After(baseline) {
		t.Fatalf("updated_at after delete = %v, want refreshed after %v", gotUpdatedAt, baseline)
	}
}

func TestTenantConfigRepo_update_soft_deleted_row_returns_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := configAuth(configTenantOther)
	repo := NewTenantConfigRepo(d)

	created := mustCreateTenantConfig(t, d, auth, &biz.TenantConfig{
		Status: biz.ConfigStatusEnabled, Name: "已删", ConfigKey: "test.tenant-deleted-upd", ValueType: "string",
	})
	if err := repo.Delete(context.Background(), auth, created.ID); err != nil {
		t.Fatalf("soft delete tenant config: %v", err)
	}
	_, err := repo.Update(context.Background(), auth, &biz.TenantConfig{
		ID: created.ID, Status: biz.ConfigStatusEnabled, Name: "复活",
	})
	if !errors.Is(err, biz.ErrTenantConfigNotFound) {
		t.Fatalf("err = %v, want ErrTenantConfigNotFound", err)
	}
}

func TestTenantConfigRepo_delete_soft_deleted_row_returns_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := configAuth(configTenantOther)
	repo := NewTenantConfigRepo(d)

	created := mustCreateTenantConfig(t, d, auth, &biz.TenantConfig{
		Status: biz.ConfigStatusEnabled, Name: "已删", ConfigKey: "test.tenant-deleted-del", ValueType: "string",
	})
	if err := repo.Delete(context.Background(), auth, created.ID); err != nil {
		t.Fatalf("soft delete tenant config: %v", err)
	}
	if err := repo.Delete(context.Background(), auth, created.ID); !errors.Is(err, biz.ErrTenantConfigNotFound) {
		t.Fatalf("err = %v, want ErrTenantConfigNotFound on repeated delete", err)
	}
}

func TestTenantConfigRepo_list_excludes_soft_deleted_rows(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := configAuth(configTenantOther)
	repo := NewTenantConfigRepo(d)

	created := mustCreateTenantConfig(t, d, auth, &biz.TenantConfig{
		Status: biz.ConfigStatusEnabled, Name: "隐藏", ConfigKey: "test.tenant-hidden", ValueType: "string",
	})
	if err := repo.Delete(context.Background(), auth, created.ID); err != nil {
		t.Fatalf("soft delete tenant config: %v", err)
	}
	out, err := repo.List(context.Background(), auth, configKeyQuery("test.tenant-hidden"))
	if err != nil {
		t.Fatalf("list tenant configs: %v", err)
	}
	if len(out.Items) != 0 {
		t.Fatalf("items = %+v, want empty (soft-deleted rows excluded)", out.Items)
	}
	if out.Total == nil || *out.Total != 0 {
		t.Fatalf("total = %+v, want 0 (Count excludes soft-deleted rows)", out.Total)
	}
}

func TestTenantConfigRepo_update_and_delete_refresh_audit_columns(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	owner := configAuth(configTenantOther)
	repo := NewTenantConfigRepo(d)

	created := mustCreateTenantConfig(t, d, owner, &biz.TenantConfig{
		Status: biz.ConfigStatusEnabled, Name: "审计", ConfigKey: "test.tenant-audit", ValueType: "string",
	})
	baseline := backdateUpdatedAt(t, "public.sys_tenant_config", created.ID)

	updater := biz.Identity{TenantID: configTenantOther, UserID: 2}
	updated, err := repo.Update(context.Background(), updater, &biz.TenantConfig{
		ID: created.ID, Status: biz.ConfigStatusEnabled, Name: "审计后",
	})
	if err != nil {
		t.Fatalf("update tenant config: %v", err)
	}
	if updated.UpdatedBy != updater.UserID {
		t.Fatalf("updated_by = %d, want %d", updated.UpdatedBy, updater.UserID)
	}
	if updated.UpdatedAt == nil || !updated.UpdatedAt.After(baseline) {
		t.Fatalf("updated_at = %v, want refreshed after %v", updated.UpdatedAt, baseline)
	}

	baseline = backdateUpdatedAt(t, "public.sys_tenant_config", created.ID)
	deleter := biz.Identity{TenantID: configTenantOther, UserID: 3}
	if err := repo.Delete(context.Background(), deleter, created.ID); err != nil {
		t.Fatalf("soft delete tenant config: %v", err)
	}
	var gotBy int64
	var gotDeletedAt, gotUpdatedAt time.Time
	err = d.IdentityTx(context.Background(), pgAuth(owner), func(s *IdentityTx) error {
		return s.Tx().QueryRow(context.Background(),
			`SELECT updated_by, deleted_at, updated_at FROM public.sys_tenant_config WHERE id = $1`,
			created.ID).Scan(&gotBy, &gotDeletedAt, &gotUpdatedAt)
	})
	if err != nil {
		t.Fatalf("read audit columns after delete: %v", err)
	}
	if gotBy != deleter.UserID {
		t.Fatalf("updated_by after delete = %d, want %d", gotBy, deleter.UserID)
	}
	if !gotDeletedAt.After(baseline) {
		t.Fatalf("deleted_at = %v, want after %v", gotDeletedAt, baseline)
	}
	if !gotUpdatedAt.After(baseline) {
		t.Fatalf("updated_at after delete = %v, want refreshed after %v", gotUpdatedAt, baseline)
	}
}

func TestTenantConfigRepo_rls_denies_without_tenant(t *testing.T) {
	t.Parallel()
	// 绕过门面的裸连接上不设 app.tenant_id：RLS 默认全拒。
	d := setupTestDB(t)

	var n int
	if err := d.db.QueryRow(context.Background(), "SELECT count(*) FROM public.sys_tenant_config").Scan(&n); err != nil {
		t.Fatalf("query: %v", err)
	}
	if n != 0 {
		t.Fatalf("bare connection without tenant saw %d tenant config rows, want 0", n)
	}
}

func TestTenantConfigRepo_writes_denied_without_tenant(t *testing.T) {
	t.Parallel()
	// 写侧 fail-closed：裸连接不设 app.tenant_id 时 INSERT 违反 WITH CHECK
	// 报错，UPDATE/DELETE 看不到行（RETURNING 无行 = pgx.ErrNoRows）；
	// 目标种子行保持原值。
	d := setupTestDB(t)
	ctx := context.Background()
	repo := NewTenantConfigRepo(d)
	auth := configAuth(configTenantSeeded)

	before, err := repo.Get(ctx, auth, 1)
	if err != nil {
		t.Fatalf("read seeded tenant config before: %v", err)
	}

	_, err = d.db.Exec(ctx,
		`INSERT INTO public.sys_tenant_config (id, tenant_id, status, "name", config_key, value_type, created_by, updated_by)
		 VALUES ($1, 't-super-01', 1, 'smuggled', 'test.smuggled', 'string', 1, 1)`, newID())
	assertRowLevelSecurityDenied(t, err)

	var id int64
	if err := d.db.QueryRow(ctx,
		`UPDATE public.sys_tenant_config SET "name" = 'hacked' WHERE id = 1 RETURNING id`).Scan(&id); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("update err = %v, want pgx.ErrNoRows", err)
	}
	if err := d.db.QueryRow(ctx,
		`DELETE FROM public.sys_tenant_config WHERE id = 1 RETURNING id`).Scan(&id); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("delete err = %v, want pgx.ErrNoRows", err)
	}

	after, err := repo.Get(ctx, auth, 1)
	if err != nil {
		t.Fatalf("read seeded tenant config after: %v", err)
	}
	if after.Name != before.Name || after.Status != before.Status ||
		!sameOptionalString(after.Value, before.Value) || !sameOptionalString(after.Remark, before.Remark) ||
		after.UpdatedBy != before.UpdatedBy {
		t.Fatalf("seeded row changed: before %+v, after %+v", before, after)
	}
}

// mustCreateFeatureFlag 创建功能开关，失败即终止用例。
func mustCreateFeatureFlag(t *testing.T, d *Data, auth biz.Identity, f *biz.FeatureFlag) *biz.FeatureFlag {
	t.Helper()
	out, err := NewFeatureFlagRepo(d).Create(context.Background(), auth, f)
	if err != nil {
		t.Fatalf("create feature flag %s: %v", f.FlagKey, err)
	}
	return out
}

// mustCreateTranslation 创建翻译，失败即终止用例。
func mustCreateTranslation(t *testing.T, d *Data, auth biz.Identity, tr *biz.Translation) *biz.Translation {
	t.Helper()
	out, err := NewTranslationRepo(d).Create(context.Background(), auth, tr)
	if err != nil {
		t.Fatalf("create translation %s: %v", tr.TableName, err)
	}
	return out
}

// featureFlagKeyQuery 构造按 flag_key 精确过滤的动态查询。
func featureFlagKeyQuery(key string) *dynq.Query {
	return &dynq.Query{Where: []dynq.Condition{{Field: "flagKey", Op: "eq", Value: key}}}
}

// translationTableQuery 构造按 table_name 精确过滤的动态查询。
func translationTableQuery(table string) *dynq.Query {
	return &dynq.Query{Where: []dynq.Condition{{Field: "tableName", Op: "eq", Value: table}}}
}

func TestFeatureFlagRepo_get_seeded_row(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	out, err := NewFeatureFlagRepo(d).Get(context.Background(), configAuth(configTenantSeeded), 1)
	if err != nil {
		t.Fatalf("get feature flag 1: %v", err)
	}
	if out.FlagKey != "export_enabled" || !out.IsEnabled || out.TenantID != configTenantSeeded {
		t.Fatalf("row = (%s, enabled %v, %s), want (export_enabled, true, %s)",
			out.FlagKey, out.IsEnabled, out.TenantID, configTenantSeeded)
	}
}

func TestFeatureFlagRepo_get_across_tenant_returns_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	_, err := NewFeatureFlagRepo(d).Get(context.Background(), configAuth(configTenantOther), 1)
	if !errors.Is(err, biz.ErrFeatureFlagNotFound) {
		t.Fatalf("err = %v, want ErrFeatureFlagNotFound", err)
	}
}

func TestFeatureFlagRepo_create_lands_session_tenant(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := configAuth(configTenantOther)

	created := mustCreateFeatureFlag(t, d, auth, &biz.FeatureFlag{
		Status: biz.ConfigStatusEnabled, Name: "新增开关", FlagKey: "test.new-flag", IsEnabled: true,
	})
	if created.TenantID != auth.TenantID {
		t.Fatalf("tenant id = %q, want %q", created.TenantID, auth.TenantID)
	}
	if created.CreatedBy != auth.UserID || created.UpdatedBy != auth.UserID {
		t.Fatalf("audit by = %d/%d, want %d", created.CreatedBy, created.UpdatedBy, auth.UserID)
	}
	if !created.IsEnabled {
		t.Fatal("is_enabled = false, want true")
	}
}

func TestFeatureFlagRepo_create_invalid_status_maps_to_invalid_argument(t *testing.T) {
	t.Parallel()
	// 绕过用例校验直接把非法 status 写库：库层 CHECK（23514）兜底映射为
	// InvalidArgument，不穿透为原始驱动错误。
	d := setupTestDB(t)

	_, err := NewFeatureFlagRepo(d).Create(context.Background(), configAuth(configTenantSeeded), &biz.FeatureFlag{
		Status: 2, Name: "非法状态", FlagKey: "test.bad-status",
	})
	if !errors.Is(err, biz.ErrFeatureFlagInvalidArgument) {
		t.Fatalf("err = %v, want ErrFeatureFlagInvalidArgument", err)
	}
}

func TestFeatureFlagRepo_create_duplicate_key_returns_already_exists(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	_, err := NewFeatureFlagRepo(d).Create(context.Background(), configAuth(configTenantSeeded), &biz.FeatureFlag{
		Status: biz.ConfigStatusEnabled, Name: "重复", FlagKey: "export_enabled",
	})
	if !errors.Is(err, biz.ErrFeatureFlagAlreadyExists) {
		t.Fatalf("err = %v, want ErrFeatureFlagAlreadyExists", err)
	}
}

func TestFeatureFlagRepo_create_same_key_allowed_in_other_tenant(t *testing.T) {
	t.Parallel()
	// 唯一约束按 (tenant_id, flag_key)：跨租户同 key 合法。
	d := setupTestDB(t)

	out, err := NewFeatureFlagRepo(d).Create(context.Background(), configAuth("t-config-none"), &biz.FeatureFlag{
		Status: biz.ConfigStatusEnabled, Name: "跨租户同 key", FlagKey: "export_enabled",
	})
	if err != nil {
		t.Fatalf("create same key in other tenant: %v", err)
	}
	if out.TenantID != "t-config-none" {
		t.Fatalf("tenant id = %q, want t-config-none", out.TenantID)
	}
}

func TestFeatureFlagRepo_create_key_reusable_after_soft_delete(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := configAuth(configTenantOther)
	repo := NewFeatureFlagRepo(d)

	first := mustCreateFeatureFlag(t, d, auth, &biz.FeatureFlag{
		Status: biz.ConfigStatusEnabled, Name: "临时", FlagKey: "test.flag-reuse",
	})
	if err := repo.Delete(context.Background(), auth, first.ID); err != nil {
		t.Fatalf("soft delete feature flag: %v", err)
	}

	second, err := repo.Create(context.Background(), auth, &biz.FeatureFlag{
		Status: biz.ConfigStatusEnabled, Name: "复用", FlagKey: "test.flag-reuse",
	})
	if err != nil {
		t.Fatalf("reuse key after soft delete: %v", err)
	}
	if second.ID == first.ID {
		t.Fatalf("reused row id = %d, want a new id", second.ID)
	}
}

func TestFeatureFlagRepo_update_covers_mutable_fields_only(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := configAuth(configTenantOther)
	repo := NewFeatureFlagRepo(d)

	created := mustCreateFeatureFlag(t, d, auth, &biz.FeatureFlag{
		Status: biz.ConfigStatusEnabled, Name: "旧名", FlagKey: "test.flag-upd",
	})
	baseline := backdateUpdatedAt(t, "public.sys_feature_flag", created.ID)
	description := "新描述"
	remark := "新备注"
	updated, err := repo.Update(context.Background(), auth, &biz.FeatureFlag{
		ID: created.ID, Status: biz.ConfigStatusDisabled, Name: "新名",
		Description: &description, IsEnabled: true, Remark: &remark,
	})
	if err != nil {
		t.Fatalf("update feature flag: %v", err)
	}
	if updated.Name != "新名" || updated.Status != biz.ConfigStatusDisabled || !updated.IsEnabled {
		t.Fatalf("row = (%s, status %d, enabled %v), want (新名, disabled, true)",
			updated.Name, updated.Status, updated.IsEnabled)
	}
	if updated.UpdatedBy != auth.UserID {
		t.Fatalf("updated_by = %d, want %d", updated.UpdatedBy, auth.UserID)
	}
	if updated.UpdatedAt == nil || !updated.UpdatedAt.After(baseline) {
		t.Fatalf("updated_at = %v, want refreshed after %v", updated.UpdatedAt, baseline)
	}
	if updated.Description == nil || *updated.Description != description ||
		updated.Remark == nil || *updated.Remark != remark {
		t.Fatalf("row description/remark = (%+v, %+v), want (%s, %s)",
			updated.Description, updated.Remark, description, remark)
	}
	if updated.FlagKey != "test.flag-upd" || updated.TenantID != auth.TenantID {
		t.Fatalf("identity columns = (%s, %s), want unchanged", updated.FlagKey, updated.TenantID)
	}
}

func TestFeatureFlagRepo_update_omits_is_enabled_to_false(t *testing.T) {
	t.Parallel()
	// Update 是覆盖语义：未设置 IsEnabled（DO 零值 false）即落库 false，
	// 不沿用旧值；重读确认持久化结果。
	d := setupTestDB(t)
	auth := configAuth(configTenantOther)
	repo := NewFeatureFlagRepo(d)

	created := mustCreateFeatureFlag(t, d, auth, &biz.FeatureFlag{
		Status: biz.ConfigStatusEnabled, Name: "开启", FlagKey: "test.flag-omit-enabled", IsEnabled: true,
	})
	updated, err := repo.Update(context.Background(), auth, &biz.FeatureFlag{
		ID: created.ID, Status: biz.ConfigStatusEnabled, Name: "关闭",
	})
	if err != nil {
		t.Fatalf("update feature flag: %v", err)
	}
	if updated.IsEnabled {
		t.Fatal("updated is_enabled = true, want false when omitted")
	}
	got, err := repo.Get(context.Background(), auth, created.ID)
	if err != nil {
		t.Fatalf("re-read feature flag: %v", err)
	}
	if got.IsEnabled {
		t.Fatal("persisted is_enabled = true, want false when omitted")
	}
}

func TestFeatureFlagRepo_update_across_tenant_returns_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	_, err := NewFeatureFlagRepo(d).Update(context.Background(), configAuth(configTenantOther), &biz.FeatureFlag{
		ID: 1, Status: biz.ConfigStatusEnabled, Name: "x",
	})
	if !errors.Is(err, biz.ErrFeatureFlagNotFound) {
		t.Fatalf("err = %v, want ErrFeatureFlagNotFound", err)
	}
}

func TestFeatureFlagRepo_delete_across_tenant_returns_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	err := NewFeatureFlagRepo(d).Delete(context.Background(), configAuth(configTenantOther), 1)
	if !errors.Is(err, biz.ErrFeatureFlagNotFound) {
		t.Fatalf("err = %v, want ErrFeatureFlagNotFound", err)
	}
	if _, err := NewFeatureFlagRepo(d).Get(context.Background(), configAuth(configTenantSeeded), 1); err != nil {
		t.Fatalf("target row must stay readable in its own tenant: %v", err)
	}
}

func TestFeatureFlagRepo_get_soft_deleted_row_returns_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := configAuth(configTenantOther)
	repo := NewFeatureFlagRepo(d)

	created := mustCreateFeatureFlag(t, d, auth, &biz.FeatureFlag{
		Status: biz.ConfigStatusEnabled, Name: "已删", FlagKey: "test.flag-deleted",
	})
	if err := repo.Delete(context.Background(), auth, created.ID); err != nil {
		t.Fatalf("soft delete feature flag: %v", err)
	}
	if _, err := repo.Get(context.Background(), auth, created.ID); !errors.Is(err, biz.ErrFeatureFlagNotFound) {
		t.Fatalf("get err = %v, want ErrFeatureFlagNotFound after soft delete", err)
	}
}

func TestFeatureFlagRepo_update_soft_deleted_row_returns_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := configAuth(configTenantOther)
	repo := NewFeatureFlagRepo(d)

	created := mustCreateFeatureFlag(t, d, auth, &biz.FeatureFlag{
		Status: biz.ConfigStatusEnabled, Name: "已删", FlagKey: "test.flag-deleted-upd",
	})
	if err := repo.Delete(context.Background(), auth, created.ID); err != nil {
		t.Fatalf("soft delete feature flag: %v", err)
	}
	_, err := repo.Update(context.Background(), auth, &biz.FeatureFlag{
		ID: created.ID, Status: biz.ConfigStatusEnabled, Name: "复活",
	})
	if !errors.Is(err, biz.ErrFeatureFlagNotFound) {
		t.Fatalf("err = %v, want ErrFeatureFlagNotFound", err)
	}
}

func TestFeatureFlagRepo_delete_soft_deleted_row_returns_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := configAuth(configTenantOther)
	repo := NewFeatureFlagRepo(d)

	created := mustCreateFeatureFlag(t, d, auth, &biz.FeatureFlag{
		Status: biz.ConfigStatusEnabled, Name: "已删", FlagKey: "test.flag-deleted-del",
	})
	if err := repo.Delete(context.Background(), auth, created.ID); err != nil {
		t.Fatalf("soft delete feature flag: %v", err)
	}
	if err := repo.Delete(context.Background(), auth, created.ID); !errors.Is(err, biz.ErrFeatureFlagNotFound) {
		t.Fatalf("err = %v, want ErrFeatureFlagNotFound on repeated delete", err)
	}
}

func TestFeatureFlagRepo_update_and_delete_refresh_audit_columns(t *testing.T) {
	t.Parallel()
	// 软删路径同样刷新 updated_by/updated_at：基线用超管回拨 updated_at
	// 得到已知旧时间，不依赖真实睡眠。
	d := setupTestDB(t)
	owner := configAuth(configTenantOther)
	repo := NewFeatureFlagRepo(d)

	created := mustCreateFeatureFlag(t, d, owner, &biz.FeatureFlag{
		Status: biz.ConfigStatusEnabled, Name: "审计", FlagKey: "test.flag-audit",
	})
	baseline := backdateUpdatedAt(t, "public.sys_feature_flag", created.ID)

	deleter := biz.Identity{TenantID: configTenantOther, UserID: 3}
	if err := repo.Delete(context.Background(), deleter, created.ID); err != nil {
		t.Fatalf("soft delete feature flag: %v", err)
	}
	var gotBy int64
	var gotDeletedAt, gotUpdatedAt time.Time
	err := d.IdentityTx(context.Background(), pgAuth(owner), func(s *IdentityTx) error {
		return s.Tx().QueryRow(context.Background(),
			`SELECT updated_by, deleted_at, updated_at FROM public.sys_feature_flag WHERE id = $1`,
			created.ID).Scan(&gotBy, &gotDeletedAt, &gotUpdatedAt)
	})
	if err != nil {
		t.Fatalf("read audit columns after delete: %v", err)
	}
	if gotBy != deleter.UserID {
		t.Fatalf("updated_by after delete = %d, want %d", gotBy, deleter.UserID)
	}
	if !gotDeletedAt.After(baseline) {
		t.Fatalf("deleted_at = %v, want after %v", gotDeletedAt, baseline)
	}
	if !gotUpdatedAt.After(baseline) {
		t.Fatalf("updated_at after delete = %v, want refreshed after %v", gotUpdatedAt, baseline)
	}
}

func TestFeatureFlagRepo_delete_missing_row_returns_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	err := NewFeatureFlagRepo(d).Delete(context.Background(), configAuth(configTenantSeeded), 999999)
	if !errors.Is(err, biz.ErrFeatureFlagNotFound) {
		t.Fatalf("err = %v, want ErrFeatureFlagNotFound", err)
	}
}

func TestFeatureFlagRepo_list_filters_and_counts_total(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	out, err := NewFeatureFlagRepo(d).List(context.Background(), configAuth(configTenantSeeded), featureFlagKeyQuery("api_access"))
	if err != nil {
		t.Fatalf("list feature flags: %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].ID != 2 {
		t.Fatalf("items = %+v, want single seeded feature flag 2", out.Items)
	}
	if out.Total == nil || *out.Total != 1 {
		t.Fatalf("total = %+v, want 1", out.Total)
	}
}

func TestFeatureFlagRepo_list_isolates_tenant(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	repo := NewFeatureFlagRepo(d)

	seeded, err := repo.List(context.Background(), configAuth(configTenantSeeded), featureFlagKeyQuery("export_enabled"))
	if err != nil {
		t.Fatalf("list feature flags: %v", err)
	}
	if len(seeded.Items) != 1 || seeded.Items[0].ID != 1 {
		t.Fatalf("items = %+v, want single seeded flag 1", seeded.Items)
	}
	if seeded.Items[0].TenantID != configTenantSeeded {
		t.Fatalf("tenant id = %q, want %q (backfilled from identity)", seeded.Items[0].TenantID, configTenantSeeded)
	}

	other, err := repo.List(context.Background(), configAuth(configTenantOther), featureFlagKeyQuery("export_enabled"))
	if err != nil {
		t.Fatalf("list feature flags in other tenant: %v", err)
	}
	if len(other.Items) != 1 || other.Items[0].ID != 5 {
		t.Fatalf("items = %+v, want single seeded flag 5", other.Items)
	}
	if other.Items[0].TenantID != configTenantOther {
		t.Fatalf("tenant id = %q, want %q (backfilled from identity)", other.Items[0].TenantID, configTenantOther)
	}

	empty, err := repo.List(context.Background(), configAuth("t-config-none"), featureFlagKeyQuery("export_enabled"))
	if err != nil {
		t.Fatalf("list feature flags in empty tenant: %v", err)
	}
	if len(empty.Items) != 0 {
		t.Fatalf("items = %+v, want empty across tenant", empty.Items)
	}
}

func TestFeatureFlagRepo_list_excludes_soft_deleted_rows(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := configAuth(configTenantOther)
	repo := NewFeatureFlagRepo(d)

	created := mustCreateFeatureFlag(t, d, auth, &biz.FeatureFlag{
		Status: biz.ConfigStatusEnabled, Name: "隐藏", FlagKey: "test.flag-hidden",
	})
	if err := repo.Delete(context.Background(), auth, created.ID); err != nil {
		t.Fatalf("soft delete feature flag: %v", err)
	}
	out, err := repo.List(context.Background(), auth, featureFlagKeyQuery("test.flag-hidden"))
	if err != nil {
		t.Fatalf("list feature flags: %v", err)
	}
	if len(out.Items) != 0 {
		t.Fatalf("items = %+v, want empty (soft-deleted rows excluded)", out.Items)
	}
	if out.Total == nil || *out.Total != 0 {
		t.Fatalf("total = %+v, want 0", out.Total)
	}
}

func TestFeatureFlagRepo_list_orders_and_paginates(t *testing.T) {
	t.Parallel()
	// t-test-02 种子开关为 5/6/7/8：按 id 降序第 1 页 8/7，第 2 页 6/5；
	// 共 4 条且 total 不随翻页位置递减。
	d := setupTestDB(t)
	pageSize := 2
	page := 2

	q := &dynq.Query{
		Sort:       []dynq.SortItem{{Field: "id", Order: "desc"}},
		Pagination: &dynq.Pagination{Page: &page, PageSize: &pageSize},
	}
	out, err := NewFeatureFlagRepo(d).List(context.Background(), configAuth(configTenantOther), q)
	if err != nil {
		t.Fatalf("list feature flags page 2: %v", err)
	}
	if len(out.Items) != 2 || out.Items[0].ID != 6 || out.Items[1].ID != 5 {
		t.Fatalf("page 2 items = %+v, want ids 6/5", out.Items)
	}
	if out.Total == nil || *out.Total != 4 {
		t.Fatalf("total = %+v, want 4", out.Total)
	}
}

func TestFeatureFlagRepo_list_cursor_page_keeps_total_unshifted(t *testing.T) {
	t.Parallel()
	// 游标模式：首屏（页大小 2，id 降序）为 8/7 并给出 NextCursor；次屏
	// 为 6/5。两屏 total 均为全集 4，不随游标收窄。
	d := setupTestDB(t)
	repo := NewFeatureFlagRepo(d)
	pageSize := 2
	includeTotal := true

	first := &dynq.Query{
		Sort:       []dynq.SortItem{{Field: "id", Order: "desc"}},
		Pagination: &dynq.Pagination{PageSize: &pageSize, IncludeTotal: &includeTotal},
	}
	out, err := repo.List(context.Background(), configAuth(configTenantOther), first)
	if err != nil {
		t.Fatalf("list first cursor page: %v", err)
	}
	if len(out.Items) != 2 || out.Items[0].ID != 8 || out.Items[1].ID != 7 {
		t.Fatalf("first page items = %+v, want ids 8/7", out.Items)
	}
	if out.NextCursor == "" {
		t.Fatal("first page next cursor is empty, want a cursor")
	}
	if out.Total == nil || *out.Total != 4 {
		t.Fatalf("first page total = %+v, want 4", out.Total)
	}

	after := out.NextCursor
	second := &dynq.Query{
		Sort:       []dynq.SortItem{{Field: "id", Order: "desc"}},
		Pagination: &dynq.Pagination{After: &after, PageSize: &pageSize, IncludeTotal: &includeTotal},
	}
	secondOut, err := repo.List(context.Background(), configAuth(configTenantOther), second)
	if err != nil {
		t.Fatalf("list second cursor page: %v", err)
	}
	if len(secondOut.Items) != 2 || secondOut.Items[0].ID != 6 || secondOut.Items[1].ID != 5 {
		t.Fatalf("second page items = %+v, want ids 6/5", secondOut.Items)
	}
	if secondOut.Total == nil || *secondOut.Total != 4 {
		t.Fatalf("second page total = %+v, want 4 (not narrowed by cursor)", secondOut.Total)
	}
}

func TestFeatureFlagRepo_list_rejects_hidden_and_unknown_fields(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	repo := NewFeatureFlagRepo(d)
	cases := map[string]*dynq.Query{
		"unknown where field":       {Where: []dynq.Condition{{Field: "noSuchField", Op: "eq", Value: "x"}}},
		"hidden tenantId in where":  {Where: []dynq.Condition{{Field: "tenantId", Op: "eq", Value: configTenantSeeded}}},
		"hidden tenantId in fields": {Fields: []string{"tenantId"}},
	}
	for name, q := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := repo.List(context.Background(), configAuth(configTenantSeeded), q)
			if !errors.Is(err, biz.ErrFeatureFlagInvalidArgument) {
				t.Fatalf("err = %v, want ErrFeatureFlagInvalidArgument", err)
			}
		})
	}
}

func TestFeatureFlagRepo_rls_denies_without_tenant(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	var n int
	if err := d.db.QueryRow(context.Background(), "SELECT count(*) FROM public.sys_feature_flag").Scan(&n); err != nil {
		t.Fatalf("query: %v", err)
	}
	if n != 0 {
		t.Fatalf("bare connection without tenant saw %d feature flag rows, want 0", n)
	}
}

func TestFeatureFlagRepo_writes_denied_without_tenant(t *testing.T) {
	t.Parallel()
	// 写侧 fail-closed：INSERT 违反 RLS WITH CHECK 报错，UPDATE/DELETE 因
	// 行不可见返回 pgx.ErrNoRows；目标种子行保持原值。
	d := setupTestDB(t)
	ctx := context.Background()
	repo := NewFeatureFlagRepo(d)
	auth := configAuth(configTenantSeeded)

	before, err := repo.Get(ctx, auth, 1)
	if err != nil {
		t.Fatalf("read seeded feature flag before: %v", err)
	}

	_, err = d.db.Exec(ctx,
		`INSERT INTO public.sys_feature_flag (id, tenant_id, status, "name", flag_key, is_enabled, created_by, updated_by)
		 VALUES ($1, 't-super-01', 1, 'smuggled', 'test.smuggled', false, 1, 1)`, newID())
	assertRowLevelSecurityDenied(t, err)

	var id int64
	if err := d.db.QueryRow(ctx,
		`UPDATE public.sys_feature_flag SET "name" = 'hacked' WHERE id = 1 RETURNING id`).Scan(&id); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("update err = %v, want pgx.ErrNoRows", err)
	}
	if err := d.db.QueryRow(ctx,
		`DELETE FROM public.sys_feature_flag WHERE id = 1 RETURNING id`).Scan(&id); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("delete err = %v, want pgx.ErrNoRows", err)
	}

	after, err := repo.Get(ctx, auth, 1)
	if err != nil {
		t.Fatalf("read seeded feature flag after: %v", err)
	}
	if after.Name != before.Name || after.Status != before.Status ||
		after.FlagKey != before.FlagKey || after.IsEnabled != before.IsEnabled ||
		!sameOptionalString(after.Remark, before.Remark) || after.UpdatedBy != before.UpdatedBy {
		t.Fatalf("seeded row changed: before %+v, after %+v", before, after)
	}
}

func TestTranslationRepo_get_seeded_row(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	out, err := NewTranslationRepo(d).Get(context.Background(), configAuth(configTenantSeeded), 1)
	if err != nil {
		t.Fatalf("get translation 1: %v", err)
	}
	if out.TableName != "sys_menu" || out.RowID != 201 || out.ColumnName != "name" ||
		out.Locale != "en-US" || out.Value != "System Management" {
		t.Fatalf("row = (%s, %d, %s, %s, %s), want seeded menu translation",
			out.TableName, out.RowID, out.ColumnName, out.Locale, out.Value)
	}
}

func TestTranslationRepo_get_across_tenant_returns_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	_, err := NewTranslationRepo(d).Get(context.Background(), configAuth(configTenantOther), 1)
	if !errors.Is(err, biz.ErrTranslationNotFound) {
		t.Fatalf("err = %v, want ErrTranslationNotFound", err)
	}
}

func TestTranslationRepo_create_lands_session_tenant(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := configAuth(configTenantOther)

	created := mustCreateTranslation(t, d, auth, &biz.Translation{
		Status: biz.ConfigStatusEnabled, TableName: "sys_dict_data", RowID: 99,
		ColumnName: "label", Locale: "zh-CN", Value: "测试翻译",
	})
	if created.TenantID != auth.TenantID {
		t.Fatalf("tenant id = %q, want %q", created.TenantID, auth.TenantID)
	}
	if created.CreatedBy != auth.UserID || created.UpdatedBy != auth.UserID {
		t.Fatalf("audit by = %d/%d, want %d", created.CreatedBy, created.UpdatedBy, auth.UserID)
	}
	if created.Value != "测试翻译" {
		t.Fatalf("value = %q, want 测试翻译", created.Value)
	}
}

func TestTranslationRepo_create_locale_over_column_width_maps_to_invalid_argument(t *testing.T) {
	t.Parallel()
	// 绕过用例校验写入超列宽 locale：库层 22001 兜底映射为
	// InvalidArgument，不穿透为原始驱动错误。
	d := setupTestDB(t)

	_, err := NewTranslationRepo(d).Create(context.Background(), configAuth(configTenantSeeded), &biz.Translation{
		Status: biz.ConfigStatusEnabled, TableName: "sys_menu", RowID: 1,
		ColumnName: "name", Locale: "zh-Hant-CNX", Value: "v",
	})
	if !errors.Is(err, biz.ErrTranslationInvalidArgument) {
		t.Fatalf("err = %v, want ErrTranslationInvalidArgument", err)
	}
}

func TestTranslationRepo_create_duplicate_identity_returns_already_exists(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	// 种子翻译 1 的身份四元组是 (sys_menu, 201, name, en-US)。
	_, err := NewTranslationRepo(d).Create(context.Background(), configAuth(configTenantSeeded), &biz.Translation{
		Status: biz.ConfigStatusEnabled, TableName: "sys_menu", RowID: 201,
		ColumnName: "name", Locale: "en-US", Value: "重复",
	})
	if !errors.Is(err, biz.ErrTranslationAlreadyExists) {
		t.Fatalf("err = %v, want ErrTranslationAlreadyExists", err)
	}
}

func TestTranslationRepo_create_same_identity_allowed_in_other_tenant(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	out, err := NewTranslationRepo(d).Create(context.Background(), configAuth("t-config-none"), &biz.Translation{
		Status: biz.ConfigStatusEnabled, TableName: "sys_menu", RowID: 201,
		ColumnName: "name", Locale: "en-US", Value: "Other Tenant",
	})
	if err != nil {
		t.Fatalf("create same identity in other tenant: %v", err)
	}
	if out.TenantID != "t-config-none" {
		t.Fatalf("tenant id = %q, want t-config-none", out.TenantID)
	}
}

func TestTranslationRepo_create_identity_reusable_after_soft_delete(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := configAuth(configTenantOther)
	repo := NewTranslationRepo(d)

	first := mustCreateTranslation(t, d, auth, &biz.Translation{
		Status: biz.ConfigStatusEnabled, TableName: "sys_menu", RowID: 202,
		ColumnName: "name", Locale: "en-US", Value: "临时",
	})
	if err := repo.Delete(context.Background(), auth, first.ID); err != nil {
		t.Fatalf("soft delete translation: %v", err)
	}

	second, err := repo.Create(context.Background(), auth, &biz.Translation{
		Status: biz.ConfigStatusEnabled, TableName: "sys_menu", RowID: 202,
		ColumnName: "name", Locale: "en-US", Value: "复用",
	})
	if err != nil {
		t.Fatalf("reuse identity after soft delete: %v", err)
	}
	if second.ID == first.ID {
		t.Fatalf("reused row id = %d, want a new id", second.ID)
	}
}

func TestTranslationRepo_update_covers_mutable_fields_only(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := configAuth(configTenantOther)
	repo := NewTranslationRepo(d)

	created := mustCreateTranslation(t, d, auth, &biz.Translation{
		Status: biz.ConfigStatusEnabled, TableName: "sys_menu", RowID: 203,
		ColumnName: "name", Locale: "en-US", Value: "旧翻译",
	})
	baseline := backdateUpdatedAt(t, "public.sys_translation", created.ID)
	remark := "新备注"
	updated, err := repo.Update(context.Background(), auth, &biz.Translation{
		ID: created.ID, Status: biz.ConfigStatusDisabled, Value: "新翻译", Remark: &remark,
	})
	if err != nil {
		t.Fatalf("update translation: %v", err)
	}
	if updated.Value != "新翻译" || updated.Status != biz.ConfigStatusDisabled {
		t.Fatalf("row = (%s, status %d), want (新翻译, disabled)", updated.Value, updated.Status)
	}
	if updated.UpdatedBy != auth.UserID {
		t.Fatalf("updated_by = %d, want %d", updated.UpdatedBy, auth.UserID)
	}
	if updated.UpdatedAt == nil || !updated.UpdatedAt.After(baseline) {
		t.Fatalf("updated_at = %v, want refreshed after %v", updated.UpdatedAt, baseline)
	}
	if updated.Remark == nil || *updated.Remark != remark {
		t.Fatalf("remark = %+v, want %s", updated.Remark, remark)
	}
	if updated.TableName != "sys_menu" || updated.RowID != 203 ||
		updated.ColumnName != "name" || updated.Locale != "en-US" || updated.TenantID != auth.TenantID {
		t.Fatalf("identity columns = (%s, %d, %s, %s, %s), want unchanged",
			updated.TableName, updated.RowID, updated.ColumnName, updated.Locale, updated.TenantID)
	}
}

func TestTranslationRepo_update_across_tenant_returns_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	_, err := NewTranslationRepo(d).Update(context.Background(), configAuth(configTenantOther), &biz.Translation{
		ID: 1, Status: biz.ConfigStatusEnabled, Value: "x",
	})
	if !errors.Is(err, biz.ErrTranslationNotFound) {
		t.Fatalf("err = %v, want ErrTranslationNotFound", err)
	}
}

func TestTranslationRepo_delete_across_tenant_returns_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	err := NewTranslationRepo(d).Delete(context.Background(), configAuth(configTenantOther), 1)
	if !errors.Is(err, biz.ErrTranslationNotFound) {
		t.Fatalf("err = %v, want ErrTranslationNotFound", err)
	}
	if _, err := NewTranslationRepo(d).Get(context.Background(), configAuth(configTenantSeeded), 1); err != nil {
		t.Fatalf("target row must stay readable in its own tenant: %v", err)
	}
}

func TestTranslationRepo_get_soft_deleted_row_returns_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := configAuth(configTenantOther)
	repo := NewTranslationRepo(d)

	created := mustCreateTranslation(t, d, auth, &biz.Translation{
		Status: biz.ConfigStatusEnabled, TableName: "sys_menu", RowID: 204,
		ColumnName: "name", Locale: "en-US", Value: "已删",
	})
	if err := repo.Delete(context.Background(), auth, created.ID); err != nil {
		t.Fatalf("soft delete translation: %v", err)
	}
	if _, err := repo.Get(context.Background(), auth, created.ID); !errors.Is(err, biz.ErrTranslationNotFound) {
		t.Fatalf("get err = %v, want ErrTranslationNotFound after soft delete", err)
	}
}

func TestTranslationRepo_update_soft_deleted_row_returns_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := configAuth(configTenantOther)
	repo := NewTranslationRepo(d)

	created := mustCreateTranslation(t, d, auth, &biz.Translation{
		Status: biz.ConfigStatusEnabled, TableName: "sys_menu", RowID: 204,
		ColumnName: "name", Locale: "en-US", Value: "已删",
	})
	if err := repo.Delete(context.Background(), auth, created.ID); err != nil {
		t.Fatalf("soft delete translation: %v", err)
	}
	_, err := repo.Update(context.Background(), auth, &biz.Translation{
		ID: created.ID, Status: biz.ConfigStatusEnabled, Value: "复活",
	})
	if !errors.Is(err, biz.ErrTranslationNotFound) {
		t.Fatalf("err = %v, want ErrTranslationNotFound", err)
	}
}

func TestTranslationRepo_delete_soft_deleted_row_returns_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := configAuth(configTenantOther)
	repo := NewTranslationRepo(d)

	created := mustCreateTranslation(t, d, auth, &biz.Translation{
		Status: biz.ConfigStatusEnabled, TableName: "sys_menu", RowID: 204,
		ColumnName: "name", Locale: "en-US", Value: "已删",
	})
	if err := repo.Delete(context.Background(), auth, created.ID); err != nil {
		t.Fatalf("soft delete translation: %v", err)
	}
	if err := repo.Delete(context.Background(), auth, created.ID); !errors.Is(err, biz.ErrTranslationNotFound) {
		t.Fatalf("err = %v, want ErrTranslationNotFound on repeated delete", err)
	}
}

func TestTranslationRepo_update_and_delete_refresh_audit_columns(t *testing.T) {
	t.Parallel()
	// 更新与软删路径刷新 updated_by/updated_at：基线用超管回拨
	// updated_at，不依赖真实睡眠。
	d := setupTestDB(t)
	owner := configAuth(configTenantOther)
	repo := NewTranslationRepo(d)

	created := mustCreateTranslation(t, d, owner, &biz.Translation{
		Status: biz.ConfigStatusEnabled, TableName: "sys_menu", RowID: 206,
		ColumnName: "name", Locale: "en-US", Value: "审计",
	})
	baseline := backdateUpdatedAt(t, "public.sys_translation", created.ID)
	remark := "新备注"
	updated, err := repo.Update(context.Background(), owner, &biz.Translation{
		ID: created.ID, Status: biz.ConfigStatusDisabled, Value: "审计后", Remark: &remark,
	})
	if err != nil {
		t.Fatalf("update translation: %v", err)
	}
	if updated.UpdatedBy != owner.UserID {
		t.Fatalf("updated_by = %d, want %d", updated.UpdatedBy, owner.UserID)
	}
	if updated.UpdatedAt == nil || !updated.UpdatedAt.After(baseline) {
		t.Fatalf("updated_at = %v, want refreshed after %v", updated.UpdatedAt, baseline)
	}

	baseline = backdateUpdatedAt(t, "public.sys_translation", created.ID)
	deleter := biz.Identity{TenantID: configTenantOther, UserID: 3}
	if err := repo.Delete(context.Background(), deleter, created.ID); err != nil {
		t.Fatalf("soft delete translation: %v", err)
	}
	var gotBy int64
	var gotDeletedAt, gotUpdatedAt time.Time
	err = d.IdentityTx(context.Background(), pgAuth(owner), func(s *IdentityTx) error {
		return s.Tx().QueryRow(context.Background(),
			`SELECT updated_by, deleted_at, updated_at FROM public.sys_translation WHERE id = $1`,
			created.ID).Scan(&gotBy, &gotDeletedAt, &gotUpdatedAt)
	})
	if err != nil {
		t.Fatalf("read audit columns after delete: %v", err)
	}
	if gotBy != deleter.UserID {
		t.Fatalf("updated_by after delete = %d, want %d", gotBy, deleter.UserID)
	}
	if !gotDeletedAt.After(baseline) {
		t.Fatalf("deleted_at = %v, want after %v", gotDeletedAt, baseline)
	}
	if !gotUpdatedAt.After(baseline) {
		t.Fatalf("updated_at after delete = %v, want refreshed after %v", gotUpdatedAt, baseline)
	}
}

func TestTranslationRepo_delete_missing_row_returns_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	err := NewTranslationRepo(d).Delete(context.Background(), configAuth(configTenantSeeded), 999999)
	if !errors.Is(err, biz.ErrTranslationNotFound) {
		t.Fatalf("err = %v, want ErrTranslationNotFound", err)
	}
}

func TestTranslationRepo_list_filters_and_counts_total(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	// 种子翻译中来源表为 sys_config 的共 7 条（id 57-63）。
	out, err := NewTranslationRepo(d).List(context.Background(), configAuth(configTenantSeeded), translationTableQuery("sys_config"))
	if err != nil {
		t.Fatalf("list translations: %v", err)
	}
	if len(out.Items) != 7 {
		t.Fatalf("items = %d, want 7", len(out.Items))
	}
	for _, item := range out.Items {
		if item.TenantID != configTenantSeeded {
			t.Fatalf("tenant id = %q, want %q (backfilled from identity)", item.TenantID, configTenantSeeded)
		}
	}
	if out.Total == nil || *out.Total != 7 {
		t.Fatalf("total = %+v, want 7", out.Total)
	}
}

func TestTranslationRepo_list_isolates_tenant(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	repo := NewTranslationRepo(d)

	out, err := repo.List(context.Background(), configAuth(configTenantOther), translationTableQuery("sys_menu"))
	if err != nil {
		t.Fatalf("list translations in other tenant: %v", err)
	}
	if len(out.Items) != 0 {
		t.Fatalf("items = %+v, want empty across tenant", out.Items)
	}
}

func TestTranslationRepo_list_excludes_soft_deleted_rows(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := configAuth(configTenantOther)
	repo := NewTranslationRepo(d)

	created := mustCreateTranslation(t, d, auth, &biz.Translation{
		Status: biz.ConfigStatusEnabled, TableName: "sys_menu", RowID: 205,
		ColumnName: "name", Locale: "en-US", Value: "隐藏",
	})
	if err := repo.Delete(context.Background(), auth, created.ID); err != nil {
		t.Fatalf("soft delete translation: %v", err)
	}
	out, err := repo.List(context.Background(), auth, translationTableQuery("sys_menu"))
	if err != nil {
		t.Fatalf("list translations: %v", err)
	}
	if len(out.Items) != 0 {
		t.Fatalf("items = %+v, want empty (soft-deleted rows excluded)", out.Items)
	}
	if out.Total == nil || *out.Total != 0 {
		t.Fatalf("total = %+v, want 0", out.Total)
	}
}

func TestTranslationRepo_list_orders_and_paginates(t *testing.T) {
	t.Parallel()
	// 种子中来源表为 sys_config 的翻译共 7 条（id 57-63）：按 id 降序
	// 第 2 页（页大小 2）为 61/60，total 7。按来源表过滤，避免菜单翻译
	// 数量变化影响本用例。
	d := setupTestDB(t)
	pageSize := 2
	page := 2

	q := &dynq.Query{
		Where:      []dynq.Condition{{Field: "tableName", Op: "eq", Value: "sys_config"}},
		Sort:       []dynq.SortItem{{Field: "id", Order: "desc"}},
		Pagination: &dynq.Pagination{Page: &page, PageSize: &pageSize},
	}
	out, err := NewTranslationRepo(d).List(context.Background(), configAuth(configTenantSeeded), q)
	if err != nil {
		t.Fatalf("list translations page 2: %v", err)
	}
	if len(out.Items) != 2 || out.Items[0].ID != 61 || out.Items[1].ID != 60 {
		t.Fatalf("page 2 items = %+v, want ids 61/60", out.Items)
	}
	if out.Total == nil || *out.Total != 7 {
		t.Fatalf("total = %+v, want 7", out.Total)
	}
}

func TestTranslationRepo_list_cursor_page_keeps_total_unshifted(t *testing.T) {
	t.Parallel()
	// 游标模式：来源表为 sys_config 的翻译 7 条（id 57-63），首屏
	// （页大小 2，id 降序）为 63/62 并给出 NextCursor；次屏为 61/60。
	// 两屏 total 均为 7，不随游标收窄。
	d := setupTestDB(t)
	repo := NewTranslationRepo(d)
	pageSize := 2
	includeTotal := true
	where := []dynq.Condition{{Field: "tableName", Op: "eq", Value: "sys_config"}}

	first := &dynq.Query{
		Where:      where,
		Sort:       []dynq.SortItem{{Field: "id", Order: "desc"}},
		Pagination: &dynq.Pagination{PageSize: &pageSize, IncludeTotal: &includeTotal},
	}
	out, err := repo.List(context.Background(), configAuth(configTenantSeeded), first)
	if err != nil {
		t.Fatalf("list first cursor page: %v", err)
	}
	if len(out.Items) != 2 || out.Items[0].ID != 63 || out.Items[1].ID != 62 {
		t.Fatalf("first page items = %+v, want ids 63/62", out.Items)
	}
	if out.NextCursor == "" {
		t.Fatal("first page next cursor is empty, want a cursor")
	}
	if out.Total == nil || *out.Total != 7 {
		t.Fatalf("first page total = %+v, want 7", out.Total)
	}

	after := out.NextCursor
	second := &dynq.Query{
		Where:      where,
		Sort:       []dynq.SortItem{{Field: "id", Order: "desc"}},
		Pagination: &dynq.Pagination{After: &after, PageSize: &pageSize, IncludeTotal: &includeTotal},
	}
	secondOut, err := repo.List(context.Background(), configAuth(configTenantSeeded), second)
	if err != nil {
		t.Fatalf("list second cursor page: %v", err)
	}
	if len(secondOut.Items) != 2 || secondOut.Items[0].ID != 61 || secondOut.Items[1].ID != 60 {
		t.Fatalf("second page items = %+v, want ids 61/60", secondOut.Items)
	}
	if secondOut.Total == nil || *secondOut.Total != 7 {
		t.Fatalf("second page total = %+v, want 7 (not narrowed by cursor)", secondOut.Total)
	}
}

func TestTranslationRepo_list_rejects_hidden_and_unknown_fields(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	repo := NewTranslationRepo(d)
	cases := map[string]*dynq.Query{
		"unknown where field":       {Where: []dynq.Condition{{Field: "noSuchField", Op: "eq", Value: "x"}}},
		"hidden tenantId in where":  {Where: []dynq.Condition{{Field: "tenantId", Op: "eq", Value: configTenantSeeded}}},
		"hidden tenantId in fields": {Fields: []string{"tenantId"}},
	}
	for name, q := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := repo.List(context.Background(), configAuth(configTenantSeeded), q)
			if !errors.Is(err, biz.ErrTranslationInvalidArgument) {
				t.Fatalf("err = %v, want ErrTranslationInvalidArgument", err)
			}
		})
	}
}

func TestTranslationRepo_rls_denies_without_tenant(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	var n int
	if err := d.db.QueryRow(context.Background(), "SELECT count(*) FROM public.sys_translation").Scan(&n); err != nil {
		t.Fatalf("query: %v", err)
	}
	if n != 0 {
		t.Fatalf("bare connection without tenant saw %d translation rows, want 0", n)
	}
}

func TestTranslationRepo_writes_denied_without_tenant(t *testing.T) {
	t.Parallel()
	// 写侧 fail-closed：INSERT 违反 RLS WITH CHECK 报错，UPDATE/DELETE 因
	// 行不可见返回 pgx.ErrNoRows；目标种子行保持原值。
	d := setupTestDB(t)
	ctx := context.Background()
	repo := NewTranslationRepo(d)
	auth := configAuth(configTenantSeeded)

	before, err := repo.Get(ctx, auth, 1)
	if err != nil {
		t.Fatalf("read seeded translation before: %v", err)
	}

	_, err = d.db.Exec(ctx,
		`INSERT INTO public.sys_translation (id, tenant_id, status, table_name, row_id, column_name, locale, value, created_by, updated_by)
		 VALUES ($1, 't-super-01', 1, 'sys_menu', 999999, 'name', 'en-US', 'x', 1, 1)`, newID())
	assertRowLevelSecurityDenied(t, err)

	var id int64
	if err := d.db.QueryRow(ctx,
		`UPDATE public.sys_translation SET value = 'hacked' WHERE id = 1 RETURNING id`).Scan(&id); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("update err = %v, want pgx.ErrNoRows", err)
	}
	if err := d.db.QueryRow(ctx,
		`DELETE FROM public.sys_translation WHERE id = 1 RETURNING id`).Scan(&id); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("delete err = %v, want pgx.ErrNoRows", err)
	}

	after, err := repo.Get(ctx, auth, 1)
	if err != nil {
		t.Fatalf("read seeded translation after: %v", err)
	}
	if after.TableName != before.TableName || after.RowID != before.RowID ||
		after.ColumnName != before.ColumnName || after.Locale != before.Locale ||
		after.Value != before.Value || after.Status != before.Status ||
		!sameOptionalString(after.Remark, before.Remark) || after.UpdatedBy != before.UpdatedBy {
		t.Fatalf("seeded row changed: before %+v, after %+v", before, after)
	}
}

// TestPlatformTenantGuard_classifies_seed_tenants 验证平台判定读调用方
// 自身租户行（RLS 放行 id = app.tenant_id）：树根（parent_id IS NULL）为
// 平台租户；直接子租户与不存在的租户均为 false（fail-closed，不探测
// 树外租户）。
func TestPlatformTenantGuard_classifies_seed_tenants(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	guard := NewPlatformTenantGuard(d)
	ctx := context.Background()

	for _, tc := range []struct {
		tenant string
		want   bool
	}{
		{tenant: configTenantSeeded, want: true}, // t-super-01：树根
		{tenant: configTenantOther, want: false}, // t-test-02：直接子租户
		{tenant: "t-config-none", want: false},   // 不存在的租户
	} {
		got, err := guard.IsPlatformTenant(ctx, configAuth(tc.tenant))
		if err != nil {
			t.Fatalf("is platform tenant %s: %v", tc.tenant, err)
		}
		if got != tc.want {
			t.Fatalf("is platform tenant %s = %v, want %v", tc.tenant, got, tc.want)
		}
	}
}

// TestPlatformTenantGuard_soft_deleted_tenant_is_not_platform 验证调用方
// 租户行被软删后平台判定 false（fail-closed）：身份仍可构造，但全局配置
// 写路径必须拒绝，不能因历史行身份残留而放行。
func TestPlatformTenantGuard_soft_deleted_tenant_is_not_platform(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	ctx := context.Background()
	root := configAuth(configTenantSeeded)

	// 以平台租户身份软删一个无子租户的种子租户，再以该租户身份判定。
	if err := NewTenantRepo(d).DeleteSuper(ctx, root, "t-test-01"); err != nil {
		t.Fatalf("soft delete seed tenant: %v", err)
	}
	got, err := NewPlatformTenantGuard(d).IsPlatformTenant(ctx, configAuth("t-test-01"))
	if err != nil {
		t.Fatalf("is platform tenant after soft delete: %v", err)
	}
	if got {
		t.Fatal("soft-deleted tenant classified as platform tenant, want false")
	}
}

// TestConfigUsecase_global_write_requires_platform_tenant 验证全局配置写
// 路径的平台信任边界在真实存储上成立：直接子租户（t-test-02）经完整用例
// 写全局配置返回 ErrConfigPlatformRequired（403），新建行不落库、更新与
// 删除不触碰种子行；树根（t-super-01）正常写入。
func TestConfigUsecase_global_write_requires_platform_tenant(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	ctx := context.Background()
	uc := biz.NewConfigUsecase(NewConfigRepo(d), NewPlatformTenantGuard(d))
	repo := NewConfigRepo(d)

	deniedAuth := configAuth(configTenantOther)
	_, err := uc.CreateConfigSuper(ctx, deniedAuth, &biz.Config{
		Status: biz.ConfigStatusEnabled, Name: "子租户越权", ConfigKey: "test.platform-denied", ValueType: "string",
	})
	if !errors.Is(err, biz.ErrConfigPlatformRequired) {
		t.Fatalf("create err = %v, want ErrConfigPlatformRequired", err)
	}
	denied, err := repo.List(ctx, deniedAuth, configKeyQuery("test.platform-denied"))
	if err != nil {
		t.Fatalf("list rejected config: %v", err)
	}
	if len(denied.Items) != 0 || denied.Total == nil || *denied.Total != 0 {
		t.Fatalf("rejected create wrote rows: %+v", denied)
	}

	// 更新/删除同样被拒，种子行 1 保持可读且未被软删或改名。
	before, err := repo.Get(ctx, configAuth(configTenantSeeded), 1)
	if err != nil {
		t.Fatalf("read seeded config before: %v", err)
	}
	if _, err := uc.UpdateConfigSuper(ctx, deniedAuth, &biz.Config{
		ID: 1, Status: biz.ConfigStatusDisabled, Name: "hacked",
	}); !errors.Is(err, biz.ErrConfigPlatformRequired) {
		t.Fatalf("update err = %v, want ErrConfigPlatformRequired", err)
	}
	if err := uc.DeleteConfigSuper(ctx, deniedAuth, 1); !errors.Is(err, biz.ErrConfigPlatformRequired) {
		t.Fatalf("delete err = %v, want ErrConfigPlatformRequired", err)
	}
	after, err := repo.Get(ctx, configAuth(configTenantSeeded), 1)
	if err != nil {
		t.Fatalf("read seeded config after: %v", err)
	}
	if after.Name != before.Name || after.Status != before.Status ||
		!sameOptionalString(after.Value, before.Value) ||
		after.DeletedAt != nil || after.UpdatedBy != before.UpdatedBy {
		t.Fatalf("seeded row changed: before %+v, after %+v", before, after)
	}

	// 树根租户写路径正常。
	rootAuth := configAuth(configTenantSeeded)
	created, err := uc.CreateConfigSuper(ctx, rootAuth, &biz.Config{
		Status: biz.ConfigStatusEnabled, Name: "平台配置", ConfigKey: "test.platform-allowed", ValueType: "string",
	})
	if err != nil {
		t.Fatalf("platform create: %v", err)
	}
	got, err := repo.Get(ctx, rootAuth, created.ID)
	if err != nil {
		t.Fatalf("re-read platform config: %v", err)
	}
	if got.ConfigKey != "test.platform-allowed" || got.Name != "平台配置" {
		t.Fatalf("row = (%s, %s), want (test.platform-allowed, 平台配置)", got.ConfigKey, got.Name)
	}
}
