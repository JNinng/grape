package pg_sqlc

import (
	"context"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"grape/internal/biz"
	"grape/internal/data/pg_sqlc/deploy"

	"github.com/jackc/pgx/v5"
	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

// 租户仓储集成测试（真实 PostgreSQL，未设 GRAPE_TEST_* DSN 时跳过）：覆盖
// sys_tenant 租户树 RLS 边界、CRUD、唯一键与软删语义、子租户删除守卫、
// 目标状态写与动态查询。setupTestDB 每次重建数据库；种子树根为 t-super-01，
// 其子租户为 t-test-01/t-test-02。

const (
	// tenantTreeRoot 是种子租户树的根（平台租户）。
	tenantTreeRoot = "t-super-01"
	// tenantTreeSibling 是挂在根下的直接子租户，供树隔离断言使用。
	tenantTreeSibling = "t-test-01"
	// tenantTreeOther 是另一棵兄弟子树（对 sibling 的树外目标）。
	tenantTreeOther = "t-test-02"
)

// tenantIDPattern 是服务端字符串发号的形态：`t-` + 14 位小写十六进制
// （总长 16，恰好填满 varchar(16)）。
var tenantIDPattern = regexp.MustCompile(`^t-[0-9a-f]{14}$`)

// tenantAuth 返回指定租户的操作者身份；租户表无 @scope，操作者仅落入
// 审计列，租户树边界由 RLS 按 auth.TenantID 承担。
func tenantAuth(tenant string) biz.Identity {
	return biz.Identity{TenantID: tenant, UserID: 1}
}

// mustCreateTenant 创建租户，失败即终止用例。
func mustCreateTenant(t *testing.T, d *Data, auth biz.Identity, in *biz.Tenant) *biz.Tenant {
	t.Helper()
	out, err := NewTenantRepo(d).CreateSuper(context.Background(), auth, in)
	if err != nil {
		t.Fatalf("create tenant %s: %v", in.Code, err)
	}
	return out
}

// mustListTenants 按动态查询列出租户，失败即终止用例。
func mustListTenants(t *testing.T, d *Data, auth biz.Identity, q *dynq.Query) *biz.TenantList {
	t.Helper()
	out, err := NewTenantRepo(d).List(context.Background(), auth, q)
	if err != nil {
		t.Fatalf("list tenants: %v", err)
	}
	return out
}

// sameOptionalTime 比较两个可空时间（nil 与不同时刻区分）。
func sameOptionalTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

// tenantCodes 提取列表中的租户 code 集合（断言租户树可见范围）。
func tenantCodes(list *biz.TenantList) map[string]struct{} {
	codes := make(map[string]struct{}, len(list.Items))
	for _, it := range list.Items {
		codes[it.Code] = struct{}{}
	}
	return codes
}

// backdateTenantUpdatedAt 以部署超级用户把 sys_tenant 行的 updated_at
// 回拨为 CURRENT_TIMESTAMP - 10 秒并返回新值：审计列用例以已知旧时间做
// 比较，不依赖真实睡眠。sys_tenant 虽对运行角色开放直写，回拨仍走超级
// 用户面以保持与其他审计列用例同一写法。
func backdateTenantUpdatedAt(t *testing.T, id string) time.Time {
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
	if err := conn.QueryRow(context.Background(),
		`UPDATE public.sys_tenant SET updated_at = CURRENT_TIMESTAMP - interval '10 seconds' WHERE id = $1 RETURNING updated_at`,
		id).Scan(&out); err != nil {
		t.Fatalf("backdate tenant %s updated_at: %v", id, err)
	}
	return out
}

func TestTenantRepo_list_sees_parent_tree(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	// 平台租户视角：树根与其名下全部直接子租户（3 条种子），不含树外。
	superList := mustListTenants(t, d, tenantAuth(tenantTreeRoot), &dynq.Query{})
	if len(superList.Items) != 3 || superList.Total == nil || *superList.Total != 3 {
		t.Fatalf("root list = %d items (total %+v), want 3", len(superList.Items), superList.Total)
	}
	for _, code := range []string{"default", "test", "test2"} {
		if _, ok := tenantCodes(superList)[code]; !ok {
			t.Fatalf("root list missing seeded tenant %q", code)
		}
	}

	// 子租户视角：只看到自己（tenant_id = 自己 id 或 id = 自己）；兄弟
	// 租户挂在根下（tenant_id = t-super-01），对子租户不可见。
	childAuth := tenantAuth(tenantTreeSibling)
	childList := mustListTenants(t, d, childAuth, &dynq.Query{})
	if len(childList.Items) != 1 || childList.Items[0].ID != tenantTreeSibling {
		t.Fatalf("child list = %+v, want only itself", childList.Items)
	}

	// 子租户新建自己的子租户后可见集合为 {自己, 子租户}，仍看不到兄弟。
	mustCreateTenant(t, d, childAuth, &biz.Tenant{
		Status: biz.TenantStatusEnabled, Code: "demo-child", Name: "演示子租户",
		ParentID: &childAuth.TenantID, DefaultLocale: "zh-CN",
	})
	childList = mustListTenants(t, d, childAuth, &dynq.Query{})
	if len(childList.Items) != 2 {
		t.Fatalf("child list after create = %d items, want 2", len(childList.Items))
	}
	if _, ok := tenantCodes(childList)["test2"]; ok {
		t.Fatal("sibling tenant must stay invisible to child")
	}

	// 孙子租户挂在子租户名下，对根不可见（RLS 只放行直接子租户与自身）。
	superList = mustListTenants(t, d, tenantAuth(tenantTreeRoot), &dynq.Query{})
	if _, ok := tenantCodes(superList)["demo-child"]; ok {
		t.Fatal("grandchild tenant must stay invisible to root")
	}
}

func TestTenantRepo_get_outside_tree_returns_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	ctx := context.Background()

	if _, err := NewTenantRepo(d).Get(ctx, tenantAuth(tenantTreeSibling), tenantTreeOther); !errors.Is(err, biz.ErrTenantNotFound) {
		t.Fatalf("sibling get err = %v, want ErrTenantNotFound", err)
	}
	out, err := NewTenantRepo(d).Get(ctx, tenantAuth(tenantTreeRoot), tenantTreeOther)
	if err != nil {
		t.Fatalf("root get child: %v", err)
	}
	if out.Code != "test2" || out.TenantID != tenantTreeRoot {
		t.Fatalf("row = (%s, %s), want (test2, %s)", out.Code, out.TenantID, tenantTreeRoot)
	}
}

func TestTenantRepo_writes_outside_tree_return_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	ctx := context.Background()
	repo := NewTenantRepo(d)
	childAuth := tenantAuth(tenantTreeSibling)

	if _, err := repo.UpdateSuper(ctx, childAuth, &biz.Tenant{
		ID: tenantTreeOther, Status: biz.TenantStatusEnabled, Code: "hacked", Name: "hacked", DefaultLocale: "zh-CN",
	}); !errors.Is(err, biz.ErrTenantNotFound) {
		t.Fatalf("update sibling err = %v, want ErrTenantNotFound", err)
	}
	if err := repo.DeleteSuper(ctx, childAuth, tenantTreeOther); !errors.Is(err, biz.ErrTenantNotFound) {
		t.Fatalf("delete sibling err = %v, want ErrTenantNotFound", err)
	}
	if _, err := repo.DisableSuper(ctx, childAuth, tenantTreeOther); !errors.Is(err, biz.ErrTenantNotFound) {
		t.Fatalf("disable sibling err = %v, want ErrTenantNotFound", err)
	}
	if _, err := repo.RestoreSuper(ctx, childAuth, tenantTreeOther); !errors.Is(err, biz.ErrTenantNotFound) {
		t.Fatalf("restore sibling err = %v, want ErrTenantNotFound", err)
	}

	// 目标行保持原值（跨树写没有产生副作用）。
	out, err := repo.Get(ctx, tenantAuth(tenantTreeRoot), tenantTreeOther)
	if err != nil {
		t.Fatalf("root re-read sibling: %v", err)
	}
	if out.Code != "test2" || out.Name != "测试租户 2" || out.Status != biz.TenantStatusEnabled {
		t.Fatalf("row changed: %+v", out)
	}
}

func TestTenantUsecaseCreate_defaults_parent_to_caller_tenant(t *testing.T) {
	t.Parallel()
	// 用例 + 仓储全链：parent_id 省略时缺省为调用方租户，tenant_id 由
	// SQL 取会话 app.tenant_id 落值。
	d := setupTestDB(t)
	ctx := context.Background()
	uc := biz.NewTenantUsecase(NewTenantRepo(d))
	auth := tenantAuth(tenantTreeRoot)

	created, err := uc.CreateTenantSuper(ctx, auth, &biz.Tenant{
		Status: biz.TenantStatusUnspecified, Code: "usecase-child", Name: "用例子租户",
	})
	if err != nil {
		t.Fatalf("create tenant via usecase: %v", err)
	}
	if !tenantIDPattern.MatchString(created.ID) || len(created.ID) != 16 {
		t.Fatalf("id = %q, want t- + 14 hex chars (16 total)", created.ID)
	}
	if created.ParentID == nil || *created.ParentID != tenantTreeRoot {
		t.Fatalf("parent = %v, want caller tenant %s", created.ParentID, tenantTreeRoot)
	}
	if created.TenantID != tenantTreeRoot {
		t.Fatalf("tenant_id = %q, want %s", created.TenantID, tenantTreeRoot)
	}
	if created.Status != biz.TenantStatusEnabled || created.DefaultLocale != biz.DefaultTenantLocale {
		t.Fatalf("status/locale = (%d, %q), want (1, %q)", created.Status, created.DefaultLocale, biz.DefaultTenantLocale)
	}

	// 树外父级引用（任何非调用方租户）按结构父级规则拒绝：用例直接
	// 返回 INVALID_ARGUMENT，不做存在性探测。
	outsideParent := tenantTreeOther
	_, err = uc.CreateTenantSuper(ctx, tenantAuth(tenantTreeSibling), &biz.Tenant{
		Status: biz.TenantStatusUnspecified, Code: "outside-parent", Name: "树外父级", ParentID: &outsideParent,
	})
	if !errors.Is(err, biz.ErrTenantInvalidArgument) {
		t.Fatalf("create with outside parent err = %v, want ErrTenantInvalidArgument", err)
	}
}

func TestTenantRepo_create_rejects_foreign_parent(t *testing.T) {
	t.Parallel()
	// 结构父级必须等于调用方租户（结构父级与行归属一致）：
	// 指向其它（即便可见的）租户或任何非调用方值都按入参错误拒绝，
	// 不写入分裂的树。
	d := setupTestDB(t)
	ctx := context.Background()
	repo := NewTenantRepo(d)
	childAuth := tenantAuth(tenantTreeSibling)

	for _, parent := range []string{tenantTreeOther, "t-0000000000"} {
		t.Run(parent, func(t *testing.T) {
			code := "bad-parent-" + parent
			_, err := repo.CreateSuper(ctx, childAuth, &biz.Tenant{
				Status: biz.TenantStatusEnabled, Code: code, Name: "非调用方父级",
				ParentID: &parent, DefaultLocale: "zh-CN",
			})
			if !errors.Is(err, biz.ErrTenantInvalidArgument) {
				t.Fatalf("foreign parent %q err = %v, want ErrTenantInvalidArgument", parent, err)
			}
			list := mustListTenants(t, d, childAuth, &dynq.Query{
				Where: []dynq.Condition{{Field: "code", Op: "eq", Value: code}},
			})
			if len(list.Items) != 0 {
				t.Fatalf("tenant %q must not be created", code)
			}
		})
	}

	// 可见的其它租户同样不能作为父级：root 以直接子租户 t-test-01 为
	// parent 创建被拒（与 biz 层规则一致）。
	rootAuth := tenantAuth(tenantTreeRoot)
	visibleParent := tenantTreeSibling
	_, err := repo.CreateSuper(ctx, rootAuth, &biz.Tenant{
		Status: biz.TenantStatusEnabled, Code: "root-foreign-parent", Name: "可见非调用方父级",
		ParentID: &visibleParent, DefaultLocale: "zh-CN",
	})
	if !errors.Is(err, biz.ErrTenantInvalidArgument) {
		t.Fatalf("visible foreign parent err = %v, want ErrTenantInvalidArgument", err)
	}
	rootList := mustListTenants(t, d, rootAuth, &dynq.Query{
		Where: []dynq.Condition{{Field: "code", Op: "eq", Value: "root-foreign-parent"}},
	})
	if len(rootList.Items) != 0 {
		t.Fatal("tenant with visible foreign parent must not be created")
	}

	// 省略与显式等于调用方租户都成功。
	omitted := mustCreateTenant(t, d, childAuth, &biz.Tenant{
		Status: biz.TenantStatusEnabled, Code: "omitted-parent", Name: "省略父级", DefaultLocale: "zh-CN",
	})
	if omitted.ParentID == nil || *omitted.ParentID != tenantTreeSibling {
		t.Fatalf("parent = %v, want caller tenant %s", omitted.ParentID, tenantTreeSibling)
	}
	explicitParent := tenantTreeSibling
	explicit := mustCreateTenant(t, d, childAuth, &biz.Tenant{
		Status: biz.TenantStatusEnabled, Code: "explicit-parent", Name: "显式父级",
		ParentID: &explicitParent, DefaultLocale: "zh-CN",
	})
	if explicit.ParentID == nil || *explicit.ParentID != tenantTreeSibling {
		t.Fatalf("parent = %v, want caller tenant %s", explicit.ParentID, tenantTreeSibling)
	}
}

func TestTenantRepo_create_requires_live_caller(t *testing.T) {
	t.Parallel()
	// 调用方（同时也是结构父级）行必须存在且未软删：插入前同事务锁行，
	// 未命中返回 ErrTenantCallerMissing（INVALID_ARGUMENT）且不落库。
	// 正常调用（活租户）仍成功。
	d := setupTestDB(t)
	ctx := context.Background()
	repo := NewTenantRepo(d)
	superAuth := tenantAuth(tenantTreeRoot)

	// 正常调用：活租户 t-test-01 创建自己的子租户成功。
	live := tenantAuth(tenantTreeSibling)
	normal := mustCreateTenant(t, d, live, &biz.Tenant{
		Status: biz.TenantStatusEnabled, Code: "caller-normal", Name: "活租户子租户", DefaultLocale: "zh-CN",
	})
	if normal.ParentID == nil || *normal.ParentID != tenantTreeSibling {
		t.Fatalf("normal parent = %v, want %s", normal.ParentID, tenantTreeSibling)
	}

	// 以父身份软删另一个调用方租户（t-test-02 无子租户）。
	doomed := tenantAuth(tenantTreeOther)
	if err := repo.DeleteSuper(ctx, superAuth, doomed.TenantID); err != nil {
		t.Fatalf("soft delete caller tenant: %v", err)
	}
	_, err := repo.CreateSuper(ctx, doomed, &biz.Tenant{
		Status: biz.TenantStatusEnabled, Code: "caller-after-delete", Name: "已删调用方子租户", DefaultLocale: "zh-CN",
	})
	if !errors.Is(err, biz.ErrTenantCallerMissing) {
		t.Fatalf("create under deleted caller err = %v, want ErrTenantCallerMissing", err)
	}
	if !errors.Is(err, biz.ErrTenantInvalidArgument) {
		t.Fatalf("create under deleted caller err = %v, want errors.Is ErrTenantInvalidArgument", err)
	}

	// 被拒绝的创建没有落库（父视角按 code 过滤为空）。
	list := mustListTenants(t, d, superAuth, &dynq.Query{
		Where: []dynq.Condition{{Field: "code", Op: "eq", Value: "caller-after-delete"}},
	})
	if len(list.Items) != 0 {
		t.Fatal("tenant under deleted caller must not be created")
	}
}

func TestTenantRepo_create_unique_conflicts_and_reuse_after_soft_delete(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	ctx := context.Background()
	repo := NewTenantRepo(d)
	auth := tenantAuth(tenantTreeRoot)
	parent := tenantTreeRoot

	created := mustCreateTenant(t, d, auth, &biz.Tenant{
		Status: biz.TenantStatusEnabled, Code: "uniq", Name: "唯一租户", ParentID: &parent, DefaultLocale: "zh-CN",
	})

	_, err := repo.CreateSuper(ctx, auth, &biz.Tenant{
		Status: biz.TenantStatusEnabled, Code: "uniq", Name: "另一个", ParentID: &parent, DefaultLocale: "zh-CN",
	})
	if !errors.Is(err, biz.ErrTenantAlreadyExists) {
		t.Fatalf("duplicate code err = %v, want ErrTenantAlreadyExists", err)
	}
	_, err = repo.CreateSuper(ctx, auth, &biz.Tenant{
		Status: biz.TenantStatusEnabled, Code: "uniq-2", Name: "唯一租户", ParentID: &parent, DefaultLocale: "zh-CN",
	})
	if !errors.Is(err, biz.ErrTenantAlreadyExists) {
		t.Fatalf("duplicate name err = %v, want ErrTenantAlreadyExists", err)
	}

	// 唯一约束限定未删除行间：软删后 code/name 可复用。
	if err := repo.DeleteSuper(ctx, auth, created.ID); err != nil {
		t.Fatalf("soft delete created tenant: %v", err)
	}
	recreated := mustCreateTenant(t, d, auth, &biz.Tenant{
		Status: biz.TenantStatusEnabled, Code: "uniq", Name: "唯一租户", ParentID: &parent, DefaultLocale: "zh-CN",
	})
	if recreated.ID == created.ID {
		t.Fatalf("recreated id = %q, want a fresh id", recreated.ID)
	}
}

func TestTenantRepo_delete_rejects_children_and_hides_row(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	ctx := context.Background()
	repo := NewTenantRepo(d)
	auth := tenantAuth(tenantTreeRoot)
	parent := tenantTreeRoot

	top := mustCreateTenant(t, d, auth, &biz.Tenant{
		Status: biz.TenantStatusEnabled, Code: "branch", Name: "分支租户", ParentID: &parent, DefaultLocale: "zh-CN",
	})
	// 子租户由 top 自己创建（结构父级与行归属都是 top）——祖父对
	// leaf 行不可见，删除前置完整性判定依赖受控计数函数。
	topAuth := biz.Identity{TenantID: top.ID, UserID: 1}
	leaf := mustCreateTenant(t, d, topAuth, &biz.Tenant{
		Status: biz.TenantStatusEnabled, Code: "leaf", Name: "叶租户", ParentID: &top.ID, DefaultLocale: "zh-CN",
	})

	// 存在未删除直接子租户：拒绝软删且不写入。
	if err := repo.DeleteSuper(ctx, auth, top.ID); !errors.Is(err, biz.ErrTenantHasChildren) {
		t.Fatalf("delete parent err = %v, want ErrTenantHasChildren", err)
	}
	if _, err := repo.Get(ctx, auth, top.ID); err != nil {
		t.Fatalf("parent must survive rejected delete: %v", err)
	}

	if err := repo.DeleteSuper(ctx, topAuth, leaf.ID); err != nil {
		t.Fatalf("delete leaf: %v", err)
	}
	if err := repo.DeleteSuper(ctx, auth, top.ID); err != nil {
		t.Fatalf("delete parent after leaf: %v", err)
	}
	if _, err := repo.Get(ctx, auth, top.ID); !errors.Is(err, biz.ErrTenantNotFound) {
		t.Fatalf("get after soft delete err = %v, want ErrTenantNotFound", err)
	}
	if err := repo.DeleteSuper(ctx, auth, top.ID); !errors.Is(err, biz.ErrTenantNotFound) {
		t.Fatalf("delete soft-deleted err = %v, want ErrTenantNotFound", err)
	}
	if _, err := repo.UpdateSuper(ctx, auth, &biz.Tenant{
		ID: top.ID, Status: biz.TenantStatusEnabled, Code: "top-2", Name: "已删租户", DefaultLocale: "zh-CN",
	}); !errors.Is(err, biz.ErrTenantNotFound) {
		t.Fatalf("update soft-deleted err = %v, want ErrTenantNotFound", err)
	}
	if _, err := repo.DisableSuper(ctx, auth, top.ID); !errors.Is(err, biz.ErrTenantNotFound) {
		t.Fatalf("disable soft-deleted err = %v, want ErrTenantNotFound", err)
	}
	if _, err := repo.RestoreSuper(ctx, auth, top.ID); !errors.Is(err, biz.ErrTenantNotFound) {
		t.Fatalf("restore soft-deleted err = %v, want ErrTenantNotFound", err)
	}
}

func TestTenantRepo_delete_rejects_invisible_grandchildren(t *testing.T) {
	t.Parallel()
	// 祖父看不到孙行：删除前置完整性判定经受控计数函数取得全局事实，
	// 祖父删除子租户仍被拒绝，不产生孤儿行。
	d := setupTestDB(t)
	ctx := context.Background()
	repo := NewTenantRepo(d)
	grandparent := tenantAuth(tenantTreeRoot)
	parent := tenantAuth(tenantTreeSibling)
	parentID := parent.TenantID

	child := mustCreateTenant(t, d, parent, &biz.Tenant{
		Status: biz.TenantStatusEnabled, Code: "demo-grandchild", Name: "演示孙租户",
		ParentID: &parentID, DefaultLocale: "zh-CN",
	})
	if child.TenantID != tenantTreeSibling {
		t.Fatalf("child tenant_id = %q, want %s", child.TenantID, tenantTreeSibling)
	}
	// 祖父视角确认孙行确实不可见（RLS 直接 count 会漏掉它）。
	if _, err := repo.Get(ctx, grandparent, child.ID); !errors.Is(err, biz.ErrTenantNotFound) {
		t.Fatalf("grandchild get as grandparent err = %v, want ErrTenantNotFound", err)
	}

	if err := repo.DeleteSuper(ctx, grandparent, tenantTreeSibling); !errors.Is(err, biz.ErrTenantHasChildren) {
		t.Fatalf("delete parent with invisible grandchild err = %v, want ErrTenantHasChildren", err)
	}
	// 目标未被软删：祖父与子租户两侧都仍可读到。
	if _, err := repo.Get(ctx, grandparent, tenantTreeSibling); err != nil {
		t.Fatalf("parent must survive rejected delete: %v", err)
	}
	if _, err := repo.Get(ctx, parent, child.ID); err != nil {
		t.Fatalf("grandchild must survive rejected delete: %v", err)
	}

	// 清理后（子租户删除）祖父才能删除中间层租户。
	if err := repo.DeleteSuper(ctx, parent, child.ID); err != nil {
		t.Fatalf("delete grandchild: %v", err)
	}
	if err := repo.DeleteSuper(ctx, grandparent, tenantTreeSibling); err != nil {
		t.Fatalf("delete parent after grandchild: %v", err)
	}
}

func TestTenantGuard_active_child_count_denies_without_tenant(t *testing.T) {
	t.Parallel()
	// 受控计数函数仍校验认证事务纪律：裸连接不设 app.tenant_id 即拒绝
	//（fail-closed），不因 DEFINER 放开为无身份可调。
	d := setupTestDB(t)

	var n int64
	err := d.db.QueryRow(context.Background(),
		`SELECT public.fn_tenant_active_child_count('t-test-01')`).Scan(&n)
	if err == nil {
		t.Fatalf("function without app.tenant_id returned %d, want error", n)
	}
	if !strings.Contains(err.Error(), "app.tenant_id") {
		t.Fatalf("err = %v, want app.tenant_id requirement", err)
	}
}

func TestTenantGuard_active_child_count_denies_empty_tenant(t *testing.T) {
	t.Parallel()
	// 空串与未设置同口径拒绝（函数内 fail-closed 检查覆盖空串）。
	d := setupTestDB(t)
	ctx := context.Background()

	tx, err := d.db.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', '', true)`); err != nil {
		t.Fatalf("set empty tenant: %v", err)
	}
	var n int64
	err = tx.QueryRow(ctx, `SELECT public.fn_tenant_active_child_count('t-test-01')`).Scan(&n)
	if err == nil {
		t.Fatalf("function with empty app.tenant_id returned %d, want error", n)
	}
	if !strings.Contains(err.Error(), "app.tenant_id") {
		t.Fatalf("err = %v, want app.tenant_id requirement", err)
	}
}

func TestTenantGuard_active_child_count_requires_visible_target(t *testing.T) {
	t.Parallel()
	// 目标必须对调用方可见（id 或 tenant_id 命中调用方租户）：树外目标
	// 拒绝，消除跨租户结构侧信道；可见目标返回正确计数（祖父对孙行
	// 不可见但计数仍包含它们）。
	d := setupTestDB(t)
	ctx := context.Background()
	repo := NewTenantRepo(d)

	// t-test-01 创建自己的子租户；t-super-01 看不到该孙行，但计数函数
	// 对可见目标 t-test-01 返回 1。
	parent := tenantAuth(tenantTreeSibling)
	parentID := parent.TenantID
	child := mustCreateTenant(t, d, parent, &biz.Tenant{
		Status: biz.TenantStatusEnabled, Code: "guard-child", Name: "计数子租户",
		ParentID: &parentID, DefaultLocale: "zh-CN",
	})
	if _, err := repo.Get(ctx, tenantAuth(tenantTreeRoot), child.ID); !errors.Is(err, biz.ErrTenantNotFound) {
		t.Fatalf("grandchild must stay invisible to grandparent: %v", err)
	}

	var n int64
	err := d.IdentityTx(ctx, pgAuth(tenantAuth(tenantTreeRoot)), func(s *IdentityTx) error {
		return s.Tx().QueryRow(ctx,
			`SELECT public.fn_tenant_active_child_count($1)`, tenantTreeSibling).Scan(&n)
	})
	if err != nil {
		t.Fatalf("count visible target: %v", err)
	}
	if n != 1 {
		t.Fatalf("visible target count = %d, want 1 (includes invisible grandchild)", n)
	}

	// 目标为调用方自身（id = app.tenant_id）同样可见并返回正确计数。
	n = 0
	err = d.IdentityTx(ctx, pgAuth(parent), func(s *IdentityTx) error {
		return s.Tx().QueryRow(ctx,
			`SELECT public.fn_tenant_active_child_count($1)`, parent.TenantID).Scan(&n)
	})
	if err != nil {
		t.Fatalf("count self target: %v", err)
	}
	if n != 1 {
		t.Fatalf("self target count = %d, want 1", n)
	}

	// 树外目标（兄弟租户 / 不存在）一律拒绝。
	for _, target := range []string{tenantTreeOther, "t-0000000000"} {
		err := d.IdentityTx(ctx, pgAuth(parent), func(s *IdentityTx) error {
			return s.Tx().QueryRow(ctx,
				`SELECT public.fn_tenant_active_child_count($1)`, target).Scan(&n)
		})
		if err == nil {
			t.Fatalf("foreign target %q returned %d, want error", target, n)
		}
		if !strings.Contains(err.Error(), "not visible") {
			t.Fatalf("foreign target %q err = %v, want not visible", target, err)
		}
	}

	// 软删目标同样不可见（可见性校验含 deleted_at IS NULL）。
	rootAuth := tenantAuth(tenantTreeRoot)
	rootParent := tenantTreeRoot
	soft := mustCreateTenant(t, d, rootAuth, &biz.Tenant{
		Status: biz.TenantStatusEnabled, Code: "guard-soft", Name: "软删目标",
		ParentID: &rootParent, DefaultLocale: "zh-CN",
	})
	if err := repo.DeleteSuper(ctx, rootAuth, soft.ID); err != nil {
		t.Fatalf("soft delete guard target: %v", err)
	}
	err = d.IdentityTx(ctx, pgAuth(rootAuth), func(s *IdentityTx) error {
		return s.Tx().QueryRow(ctx,
			`SELECT public.fn_tenant_active_child_count($1)`, soft.ID).Scan(&n)
	})
	if err == nil {
		t.Fatalf("soft-deleted target returned %d, want error", n)
	}
	if !strings.Contains(err.Error(), "not visible") {
		t.Fatalf("soft-deleted target err = %v, want not visible", err)
	}
}

func TestTenantRepo_disable_restore_target_state_and_audit(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	ctx := context.Background()
	repo := NewTenantRepo(d)
	auth := tenantAuth(tenantTreeRoot)
	parent := tenantTreeRoot

	created := mustCreateTenant(t, d, auth, &biz.Tenant{
		Status: biz.TenantStatusEnabled, Code: "status", Name: "状态租户", ParentID: &parent, DefaultLocale: "zh-CN",
	})
	baseline := backdateTenantUpdatedAt(t, created.ID)

	disabled, err := repo.DisableSuper(ctx, biz.Identity{TenantID: tenantTreeRoot, UserID: 2}, created.ID)
	if err != nil {
		t.Fatalf("disable tenant: %v", err)
	}
	if disabled.Status != biz.TenantStatusDisabled || disabled.UpdatedBy != 2 {
		t.Fatalf("disabled = (status %d, by %d), want (0, 2)", disabled.Status, disabled.UpdatedBy)
	}
	if disabled.UpdatedAt == nil || !disabled.UpdatedAt.After(baseline) {
		t.Fatalf("updated_at after disable = %v, want after %v", disabled.UpdatedAt, baseline)
	}

	// 幂等目标状态写：已停用再停用不出错，仍为 0。
	again, err := repo.DisableSuper(ctx, auth, created.ID)
	if err != nil {
		t.Fatalf("disable twice: %v", err)
	}
	if again.Status != biz.TenantStatusDisabled {
		t.Fatalf("status after second disable = %d, want 0", again.Status)
	}

	baseline = backdateTenantUpdatedAt(t, created.ID)
	restored, err := repo.RestoreSuper(ctx, biz.Identity{TenantID: tenantTreeRoot, UserID: 3}, created.ID)
	if err != nil {
		t.Fatalf("restore tenant: %v", err)
	}
	if restored.Status != biz.TenantStatusEnabled || restored.UpdatedBy != 3 {
		t.Fatalf("restored = (status %d, by %d), want (1, 3)", restored.Status, restored.UpdatedBy)
	}
	if restored.UpdatedAt == nil || !restored.UpdatedAt.After(baseline) {
		t.Fatalf("updated_at after restore = %v, want after %v", restored.UpdatedAt, baseline)
	}
}

func TestTenantRepo_update_covers_mutable_fields_only(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	ctx := context.Background()
	repo := NewTenantRepo(d)
	auth := tenantAuth(tenantTreeRoot)
	parent := tenantTreeRoot

	created := mustCreateTenant(t, d, auth, &biz.Tenant{
		Status: biz.TenantStatusEnabled, Code: "mutable", Name: "可变租户", ParentID: &parent, DefaultLocale: "zh-CN",
	})

	expired := time.Unix(1800000000, 0).UTC()
	maxUsers := int32(100)
	contactName := "李四"
	contactPhone := "13900000000"
	forgedParent := "t-hacked"
	updated, err := repo.UpdateSuper(ctx, biz.Identity{TenantID: tenantTreeRoot, UserID: 2}, &biz.Tenant{
		ID:            created.ID,
		Status:        biz.TenantStatusDisabled,
		Code:          "mutable-2",
		Name:          "可变租户-新",
		ContactName:   &contactName,
		ContactPhone:  &contactPhone,
		ExpiredAt:     &expired,
		MaxUserCount:  &maxUsers,
		DefaultLocale: "en-US",
		// code 是身份列、parent_id/tenant_id 是结构/归属列：
		// 不在更新列内，传入也无效。
		ParentID: &forgedParent,
		TenantID: "t-hacked",
	})
	if err != nil {
		t.Fatalf("update tenant: %v", err)
	}
	if updated.Status != biz.TenantStatusDisabled || updated.Code != "mutable" || updated.Name != "可变租户-新" {
		t.Fatalf("row = (%d, %s, %s), want (0, mutable, 可变租户-新) with code unchanged",
			updated.Status, updated.Code, updated.Name)
	}
	if updated.ContactName == nil || *updated.ContactName != contactName ||
		updated.ContactPhone == nil || *updated.ContactPhone != contactPhone {
		t.Fatalf("contact = (%v, %v), want (%q, %q)", updated.ContactName, updated.ContactPhone, contactName, contactPhone)
	}
	if updated.ExpiredAt == nil || !updated.ExpiredAt.Equal(expired) {
		t.Fatalf("expired_at = %v, want %v", updated.ExpiredAt, expired)
	}
	if updated.MaxUserCount == nil || *updated.MaxUserCount != maxUsers {
		t.Fatalf("max_user_count = %v, want %d", updated.MaxUserCount, maxUsers)
	}
	if updated.DefaultLocale != "en-US" {
		t.Fatalf("default locale = %q, want en-US", updated.DefaultLocale)
	}
	if updated.ParentID == nil || *updated.ParentID != tenantTreeRoot || updated.TenantID != tenantTreeRoot {
		t.Fatalf("parent/tenant = (%v, %q), want unchanged (%q, %q)",
			updated.ParentID, updated.TenantID, tenantTreeRoot, tenantTreeRoot)
	}
	if updated.UpdatedBy != 2 {
		t.Fatalf("updated_by = %d, want 2", updated.UpdatedBy)
	}

	// 未命中目标（不存在/已软删/树外）：NotFound。
	_, err = repo.UpdateSuper(ctx, auth, &biz.Tenant{
		ID: "t-0000000000", Status: biz.TenantStatusEnabled, Code: "missing", Name: "不存在", DefaultLocale: "zh-CN",
	})
	if !errors.Is(err, biz.ErrTenantNotFound) {
		t.Fatalf("update missing err = %v, want ErrTenantNotFound", err)
	}
}

func TestTenantRepo_update_unique_conflicts(t *testing.T) {
	t.Parallel()
	// code 是身份列：更新输入中的 code 被忽略——即使改成已占用值也
	// 不产生冲突、不落库（保持原值）；name 是名称列，改成已占用值返回
	// ErrTenantAlreadyExists，目标行保持原值。
	d := setupTestDB(t)
	ctx := context.Background()
	repo := NewTenantRepo(d)
	auth := tenantAuth(tenantTreeRoot)
	parent := tenantTreeRoot

	first := mustCreateTenant(t, d, auth, &biz.Tenant{
		Status: biz.TenantStatusEnabled, Code: "upd-a", Name: "更新 A", ParentID: &parent, DefaultLocale: "zh-CN",
	})
	mustCreateTenant(t, d, auth, &biz.Tenant{
		Status: biz.TenantStatusEnabled, Code: "upd-b", Name: "更新 B", ParentID: &parent, DefaultLocale: "zh-CN",
	})

	// 输入试图把 code 改成已占用的 upd-b：忽略（成功且 code 仍为 upd-a）。
	updated, err := repo.UpdateSuper(ctx, auth, &biz.Tenant{
		ID: first.ID, Status: biz.TenantStatusEnabled, Code: "upd-b", Name: "更新 A", DefaultLocale: "zh-CN",
	})
	if err != nil {
		t.Fatalf("update with ignored code: %v", err)
	}
	if updated.Code != "upd-a" {
		t.Fatalf("code after update = %q, want unchanged upd-a", updated.Code)
	}

	// name 改成已占用值：唯一冲突。
	_, err = repo.UpdateSuper(ctx, auth, &biz.Tenant{
		ID: first.ID, Status: biz.TenantStatusEnabled, Code: "upd-a", Name: "更新 B", DefaultLocale: "zh-CN",
	})
	if !errors.Is(err, biz.ErrTenantAlreadyExists) {
		t.Fatalf("update duplicate name err = %v, want ErrTenantAlreadyExists", err)
	}

	got, err := repo.Get(ctx, auth, first.ID)
	if err != nil {
		t.Fatalf("re-read first tenant: %v", err)
	}
	if got.Code != "upd-a" || got.Name != "更新 A" {
		t.Fatalf("row = (%q, %q), want unchanged (upd-a, 更新 A)", got.Code, got.Name)
	}
}

func TestTenantRepo_update_self_status_guard(t *testing.T) {
	t.Parallel()
	// 自身租户的 status 变更被拒绝（须走 Disable/Restore），同值更新
	//（只改 name）放行：当前启用时改 0 被拒、同值 1 放行；把自身置 0 后
	// 改 1 被拒、同值 0 放行。行状态始终按同值更新保持当前值。
	d := setupTestDB(t)
	ctx := context.Background()
	repo := NewTenantRepo(d)
	auth := biz.Identity{TenantID: tenantTreeRoot, UserID: 2}

	before, err := repo.Get(ctx, auth, tenantTreeRoot)
	if err != nil {
		t.Fatalf("read own tenant before: %v", err)
	}
	// 当前种子状态为启用：改成 0 被拒。
	_, err = repo.UpdateSuper(ctx, auth, &biz.Tenant{
		ID: tenantTreeRoot, Status: biz.TenantStatusDisabled, Code: "default", Name: "默认租户", DefaultLocale: "zh-CN",
	})
	if !errors.Is(err, biz.ErrTenantSelfStatusChange) {
		t.Fatalf("own status 0 err = %v, want ErrTenantSelfStatusChange", err)
	}
	if !errors.Is(err, biz.ErrTenantInvalidArgument) {
		t.Fatalf("own status 0 err = %v, want errors.Is ErrTenantInvalidArgument", err)
	}
	// 拒绝后立即回读：整行不变（name/code/status/updated_by/updated_at）。
	afterReject, err := repo.Get(ctx, auth, tenantTreeRoot)
	if err != nil {
		t.Fatalf("read own tenant after rejected update: %v", err)
	}
	if afterReject.Name != before.Name || afterReject.Code != before.Code ||
		afterReject.Status != before.Status || afterReject.UpdatedBy != before.UpdatedBy {
		t.Fatalf("row changed after rejected update: before %+v, after %+v", before, afterReject)
	}
	if !sameOptionalTime(afterReject.UpdatedAt, before.UpdatedAt) {
		t.Fatalf("updated_at changed after rejected update: before %v, after %v", before.UpdatedAt, afterReject.UpdatedAt)
	}
	// 同值 1 更新只改 name：放行。
	updated, err := repo.UpdateSuper(ctx, auth, &biz.Tenant{
		ID: tenantTreeRoot, Status: biz.TenantStatusEnabled, Code: "default", Name: "默认租户-新", DefaultLocale: "zh-CN",
	})
	if err != nil {
		t.Fatalf("update own tenant same status 1: %v", err)
	}
	if updated.Status != biz.TenantStatusEnabled || updated.Name != "默认租户-新" {
		t.Fatalf("row = (%d, %q), want (1, 默认租户-新)", updated.Status, updated.Name)
	}

	// 仓储层把自身置 0（biz 守卫之外的仓储原语），再验证改 1 被拒。
	if _, err := repo.DisableSuper(ctx, auth, tenantTreeRoot); err != nil {
		t.Fatalf("disable own tenant via repo primitive: %v", err)
	}
	_, err = repo.UpdateSuper(ctx, auth, &biz.Tenant{
		ID: tenantTreeRoot, Status: biz.TenantStatusEnabled, Code: "default", Name: "默认租户-新", DefaultLocale: "zh-CN",
	})
	if !errors.Is(err, biz.ErrTenantSelfStatusChange) {
		t.Fatalf("own status 1 err = %v, want ErrTenantSelfStatusChange", err)
	}
	// 同值 0 更新只改 name：放行。
	updated, err = repo.UpdateSuper(ctx, auth, &biz.Tenant{
		ID: tenantTreeRoot, Status: biz.TenantStatusDisabled, Code: "default", Name: "默认租户-再改", DefaultLocale: "zh-CN",
	})
	if err != nil {
		t.Fatalf("update own tenant same status 0: %v", err)
	}
	if updated.Status != biz.TenantStatusDisabled || updated.Name != "默认租户-再改" {
		t.Fatalf("row = (%d, %q), want (0, 默认租户-再改)", updated.Status, updated.Name)
	}
	got, err := repo.Get(ctx, auth, tenantTreeRoot)
	if err != nil {
		t.Fatalf("re-read own tenant: %v", err)
	}
	if got.Status != biz.TenantStatusDisabled {
		t.Fatalf("own tenant status = %d, want disabled (unchanged by same-value update)", got.Status)
	}
}

func TestTenantRepo_list_cursor_page_keeps_total_unshifted(t *testing.T) {
	t.Parallel()
	// 游标模式：过滤 list- 前缀的 3 条新增租户，首屏 2 条（id 降序）并
	// 给出 NextCursor；次屏 1 条。两屏 total 均为 3，不随游标收窄。
	d := setupTestDB(t)
	ctx := context.Background()
	repo := NewTenantRepo(d)
	auth := tenantAuth(tenantTreeRoot)
	parent := tenantTreeRoot

	for _, code := range []string{"list-a", "list-b", "list-c"} {
		mustCreateTenant(t, d, auth, &biz.Tenant{
			Status: biz.TenantStatusEnabled, Code: code, Name: code, ParentID: &parent, DefaultLocale: "zh-CN",
		})
	}
	pageSize := 2
	includeTotal := true
	first := &dynq.Query{
		Where:      []dynq.Condition{{Field: "code", Op: "prefix", Value: "list-"}},
		Sort:       []dynq.SortItem{{Field: "id", Order: "desc"}},
		Pagination: &dynq.Pagination{PageSize: &pageSize, IncludeTotal: &includeTotal},
	}
	out, err := repo.List(ctx, auth, first)
	if err != nil {
		t.Fatalf("list first cursor page: %v", err)
	}
	if len(out.Items) != 2 {
		t.Fatalf("first page items = %d, want 2", len(out.Items))
	}
	if out.NextCursor == "" {
		t.Fatal("first page next cursor is empty, want a cursor")
	}
	if out.Total == nil || *out.Total != 3 {
		t.Fatalf("first page total = %+v, want 3", out.Total)
	}

	after := out.NextCursor
	second := &dynq.Query{
		Where:      []dynq.Condition{{Field: "code", Op: "prefix", Value: "list-"}},
		Sort:       []dynq.SortItem{{Field: "id", Order: "desc"}},
		Pagination: &dynq.Pagination{After: &after, PageSize: &pageSize, IncludeTotal: &includeTotal},
	}
	secondOut, err := repo.List(ctx, auth, second)
	if err != nil {
		t.Fatalf("list second cursor page: %v", err)
	}
	if len(secondOut.Items) != 1 {
		t.Fatalf("second page items = %d, want 1", len(secondOut.Items))
	}
	if secondOut.Total == nil || *secondOut.Total != 3 {
		t.Fatalf("second page total = %+v, want 3 (not narrowed by cursor)", secondOut.Total)
	}
}

func TestTenantRepo_list_filters_sorts_paginates_and_excludes_soft_deleted(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	ctx := context.Background()
	repo := NewTenantRepo(d)
	auth := tenantAuth(tenantTreeRoot)
	parent := tenantTreeRoot

	first := mustCreateTenant(t, d, auth, &biz.Tenant{
		Status: biz.TenantStatusEnabled, Code: "list-a", Name: "A 租户", ParentID: &parent, DefaultLocale: "zh-CN",
	})
	mustCreateTenant(t, d, auth, &biz.Tenant{
		Status: biz.TenantStatusEnabled, Code: "list-b", Name: "B 租户", ParentID: &parent, DefaultLocale: "zh-CN",
	})
	mustCreateTenant(t, d, auth, &biz.Tenant{
		Status: biz.TenantStatusEnabled, Code: "list-c", Name: "C 租户", ParentID: &parent, DefaultLocale: "zh-CN",
	})

	// 过滤 + total。
	filtered := mustListTenants(t, d, auth, &dynq.Query{
		Where: []dynq.Condition{{Field: "code", Op: "eq", Value: "list-b"}},
	})
	if len(filtered.Items) != 1 || filtered.Items[0].Code != "list-b" {
		t.Fatalf("filtered = %+v, want list-b", filtered.Items)
	}
	if filtered.Total == nil || *filtered.Total != 1 {
		t.Fatalf("filtered total = %+v, want 1", filtered.Total)
	}

	// 排序（name ASC）+ 分页（页大小 2）：total 与翻页位置无关。
	page := 1
	pageSize := 2
	pageOne := mustListTenants(t, d, auth, &dynq.Query{
		Where:      []dynq.Condition{{Field: "code", Op: "prefix", Value: "list-"}},
		Sort:       []dynq.SortItem{{Field: "name", Order: "asc"}},
		Pagination: &dynq.Pagination{Page: &page, PageSize: &pageSize},
	})
	if len(pageOne.Items) != 2 || pageOne.Items[0].Code != "list-a" || pageOne.Items[1].Code != "list-b" {
		t.Fatalf("page one = %+v, want [list-a list-b]", pageOne.Items)
	}
	if pageOne.Total == nil || *pageOne.Total != 3 {
		t.Fatalf("page one total = %+v, want 3", pageOne.Total)
	}
	if !pageOne.HasMore {
		t.Fatal("page one must report has_more")
	}

	// 软删行被表级 @filter 排除（List 与 Count 同口径）。
	if err := repo.DeleteSuper(ctx, auth, first.ID); err != nil {
		t.Fatalf("soft delete first: %v", err)
	}
	afterDelete := mustListTenants(t, d, auth, &dynq.Query{
		Where: []dynq.Condition{{Field: "code", Op: "prefix", Value: "list-"}},
	})
	if len(afterDelete.Items) != 2 || afterDelete.Total == nil || *afterDelete.Total != 2 {
		t.Fatalf("items/total after delete = (%d, %+v), want (2, 2)", len(afterDelete.Items), afterDelete.Total)
	}

	// 协议错误映射为 INVALID_ARGUMENT。
	_, err := repo.List(ctx, auth, &dynq.Query{
		Where: []dynq.Condition{{Field: "noSuchField", Op: "eq", Value: "x"}},
	})
	if !errors.Is(err, biz.ErrTenantInvalidArgument) {
		t.Fatalf("unknown field err = %v, want ErrTenantInvalidArgument", err)
	}
}

func TestTenantRepo_own_tenant_guards_reject_self_operations(t *testing.T) {
	t.Parallel()
	// 禁止操作自身租户是纯业务规则（用例层）：删除/停用/恢复目标等于
	// 调用方租户时返回 INVALID_ARGUMENT，仓储不被触达。
	d := setupTestDB(t)
	uc := biz.NewTenantUsecase(NewTenantRepo(d))
	auth := tenantAuth(tenantTreeRoot)

	if err := uc.DeleteTenantSuper(context.Background(), auth, tenantTreeRoot); !errors.Is(err, biz.ErrTenantInvalidArgument) {
		t.Fatalf("delete self err = %v, want ErrTenantInvalidArgument", err)
	}
	if _, err := uc.DisableTenantSuper(context.Background(), auth, tenantTreeRoot); !errors.Is(err, biz.ErrTenantInvalidArgument) {
		t.Fatalf("disable self err = %v, want ErrTenantInvalidArgument", err)
	}
	if _, err := uc.RestoreTenantSuper(context.Background(), auth, tenantTreeRoot); !errors.Is(err, biz.ErrTenantInvalidArgument) {
		t.Fatalf("restore self err = %v, want ErrTenantInvalidArgument", err)
	}

	// 种子树根未被删除/停用。
	out, err := NewTenantRepo(d).Get(context.Background(), auth, tenantTreeRoot)
	if err != nil {
		t.Fatalf("re-read root: %v", err)
	}
	if out.Status != biz.TenantStatusEnabled {
		t.Fatalf("root status = %d, want enabled", out.Status)
	}
}

func TestTenantRepo_rls_denies_without_tenant(t *testing.T) {
	t.Parallel()
	// 绕过门面的裸连接上不设 app.tenant_id：RLS 默认全拒。
	d := setupTestDB(t)

	var n int
	if err := d.db.QueryRow(context.Background(), "SELECT count(*) FROM public.sys_tenant").Scan(&n); err != nil {
		t.Fatalf("query: %v", err)
	}
	if n != 0 {
		t.Fatalf("bare connection without tenant saw %d tenant rows, want 0", n)
	}
}

func TestTenantRepo_writes_denied_without_tenant(t *testing.T) {
	t.Parallel()
	// 写侧 fail-closed：裸连接不设 app.tenant_id 时 INSERT 违反 WITH CHECK
	// 报错（42501），UPDATE/DELETE 看不到任何行（RETURNING 无行）；目标
	// 种子行保持原值。
	d := setupTestDB(t)
	ctx := context.Background()
	repo := NewTenantRepo(d)
	auth := tenantAuth(tenantTreeRoot)

	before, err := repo.Get(ctx, auth, tenantTreeSibling)
	if err != nil {
		t.Fatalf("read seeded tenant before: %v", err)
	}

	id, err := newTenantID()
	if err != nil {
		t.Fatalf("generate tenant id: %v", err)
	}
	_, err = d.db.Exec(ctx,
		`INSERT INTO public.sys_tenant (id, tenant_id, status, code, "name", default_locale, created_by, updated_by)
		 VALUES ($1, 't-super-01', 1, 'smuggled', 'smuggled', 'zh-CN', 1, 1)`, id)
	assertRowLevelSecurityDenied(t, err)

	var gotID string
	if err := d.db.QueryRow(ctx,
		`UPDATE public.sys_tenant SET "name" = 'hacked' WHERE id = $1 RETURNING id`, tenantTreeSibling).Scan(&gotID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("update err = %v, want pgx.ErrNoRows", err)
	}
	if err := d.db.QueryRow(ctx,
		`DELETE FROM public.sys_tenant WHERE id = $1 RETURNING id`, tenantTreeSibling).Scan(&gotID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("delete err = %v, want pgx.ErrNoRows", err)
	}

	after, err := repo.Get(ctx, auth, tenantTreeSibling)
	if err != nil {
		t.Fatalf("read seeded tenant after: %v", err)
	}
	if after.Name != before.Name || after.Status != before.Status || after.UpdatedBy != before.UpdatedBy {
		t.Fatalf("seeded row changed: before %+v, after %+v", before, after)
	}
}
