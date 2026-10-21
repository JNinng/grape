package pg_sqlc

import (
	"context"
	"errors"
	"os"
	"testing"

	"grape/internal/biz"
	"grape/internal/data/pg_sqlc/gen"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

// 存储边界集成测试：以真实 PostgreSQL 验证会话门面、RLS/@scope 运行
// 纪律与用户仓储（断言基于 sqlc/seed.sql 的种子事实）。设置以下环境
// 变量后运行，未设置时跳过：
//
//	GRAPE_TEST_ADMIN_DSN=postgres://postgresql:root@127.0.0.1:5432/postgres \
//	GRAPE_TEST_DSN=postgres://grape_u:grape_u_pwd@127.0.0.1:5432/grape_pgsqlc_test \
//	go test ./internal/data/pg_sqlc -run TestSession -count=1 -v
//
// 测试自重建 grape_pgsqlc_test 库（DROP+CREATE+按部署顺序装载 sqlc/
// 全部文件），不触碰其他数据库。

const testDatabase = "grape_pgsqlc_test"

// deployFiles 是部署执行顺序（sqlc/README「数据库部署执行顺序」）。
var deployFiles = []string{
	"sqlc/schema/10_basic.sql",
	"sqlc/schema/20_tenant.sql",
	"sqlc/schema/30_user.sql",
	"sqlc/schema/40_rbac.sql",
	"sqlc/schema/50_dept.sql",
	"sqlc/schema/60_dict.sql",
	"sqlc/schema/70_audit.sql",
	"sqlc/schema/80_session.sql",
	"sqlc/schema/90_config.sql",
	"sqlc/rls.sql",
	"sqlc/operate.sql",
	"sqlc/expose.sql",
	"sqlc/seed.sql",
}

// setupTestDB 重建测试库、按序装载部署文件并返回门面所属的连接池。
func setupTestDB(t *testing.T) *Data {
	t.Helper()
	adminDSN := os.Getenv("GRAPE_TEST_ADMIN_DSN")
	testDSN := os.Getenv("GRAPE_TEST_DSN")
	if adminDSN == "" || testDSN == "" {
		t.Skip("GRAPE_TEST_ADMIN_DSN / GRAPE_TEST_DSN 未设置，跳过存储边界集成测试")
	}
	ctx := context.Background()

	// 重建测试库；CONNECT 授权依赖库名，由部署方执行（rls.sql 同此约定）
	cfg, err := pgx.ParseConfig(adminDSN)
	if err != nil {
		t.Fatalf("parse admin dsn: %v", err)
	}
	cfg.Database = "postgres"
	admin, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+testDatabase+" WITH (FORCE)"); err != nil {
		t.Fatalf("drop test database: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+testDatabase); err != nil {
		t.Fatalf("create test database: %v", err)
	}
	if err := admin.Close(ctx); err != nil {
		t.Fatalf("close admin: %v", err)
	}

	// 装载部署文件：文件内含多语句与函数体，走 simple protocol
	deployCfg, err := pgx.ParseConfig(adminDSN)
	if err != nil {
		t.Fatalf("parse admin dsn: %v", err)
	}
	deployCfg.Database = testDatabase
	deployCfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	deployConn, err := pgx.ConnectConfig(ctx, deployCfg)
	if err != nil {
		t.Fatalf("connect deploy: %v", err)
	}
	defer deployConn.Close(ctx)
	for _, f := range deployFiles {
		sql, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if _, err := deployConn.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("deploy %s: %v", f, err)
		}
	}
	// grape_u/test_u 由 rls.sql 创建（见部署清单），此时才可授权 CONNECT
	if _, err := deployConn.Exec(ctx, "GRANT CONNECT ON DATABASE "+testDatabase+" TO grape_u, test_u"); err != nil {
		t.Fatalf("grant connect: %v", err)
	}

	pool, err := pgxpool.New(ctx, testDSN)
	if err != nil {
		t.Fatalf("connect grape_u: %v", err)
	}
	t.Cleanup(pool.Close)
	return &Data{db: pool}
}

// countBasicInSession 统计当前会话可见的 basic 行数。
func countBasicInSession(ctx context.Context, t *testing.T, d *Data, auth biz.AuthContext) int64 {
	t.Helper()
	var total int64
	err := d.Session(ctx, AuthContext{TenantID: auth.TenantID, UserID: auth.UserID}, func(s *Session) error {
		p, err := gen.ParseBasicQuery(&dynq.Query{})
		if err != nil {
			return err
		}
		p.Scope = s.BasicScope()
		total, err = gen.CountBasic(ctx, s.Tx(), p)
		return err
	})
	if err != nil {
		t.Fatalf("count basic: %v", err)
	}
	return total
}

func TestSessionDiscipline(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()
	super := biz.AuthContext{TenantID: "t-super-01", UserID: 1}

	// 绕过门面的裸连接上不设 app.tenant_id：RLS 默认全拒。
	// 这是连接纪律的数据库层兜底，门面只是让它不可能被绕过。
	t.Run("rls denies without tenant", func(t *testing.T) {
		var n int
		if err := d.db.QueryRow(ctx, "SELECT count(*) FROM public.sys_dict_data").Scan(&n); err != nil {
			t.Fatalf("query: %v", err)
		}
		if n != 0 {
			t.Fatalf("bare connection without tenant saw %d dict rows, want 0", n)
		}
	})

	t.Run("session sees tenant data", func(t *testing.T) {
		err := d.Session(ctx, AuthContext{TenantID: super.TenantID, UserID: super.UserID}, func(s *Session) error {
			var n int
			if err := s.Tx().QueryRow(ctx, "SELECT count(*) FROM public.sys_dict_data").Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				t.Fatal("dict data invisible inside session tenant")
			}
			return nil
		})
		if err != nil {
			t.Fatalf("session: %v", err)
		}
	})

	t.Run("unknown tenant sees nothing", func(t *testing.T) {
		err := d.Session(ctx, AuthContext{TenantID: "t-nope", UserID: 1}, func(s *Session) error {
			var n int
			if err := s.Tx().QueryRow(ctx, "SELECT count(*) FROM public.sys_dict_data").Scan(&n); err != nil {
				return err
			}
			if n != 0 {
				t.Fatalf("unknown tenant saw %d dict rows, want 0", n)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("session: %v", err)
		}
	})

	// 种子事实：t-super-01 内 basic 共 10 行；用户 1 持全租户角色
	// （data_scope=7），用户 3 持仅本人角色（data_scope=1）。
	t.Run("scope 7 sees all basic rows of tenant", func(t *testing.T) {
		if got := countBasicInSession(ctx, t, d, super); got != 10 {
			t.Fatalf("data_scope=7 total = %d, want 10", got)
		}
	})

	t.Run("scope 1 sees only own basic rows", func(t *testing.T) {
		if got := countBasicInSession(ctx, t, d, biz.AuthContext{TenantID: super.TenantID, UserID: 3}); got == 0 || got >= 10 {
			t.Fatalf("data_scope=1 total = %d, want (0, 10)", got)
		}
	})

	t.Run("identity mismatch with tenant sees nothing", func(t *testing.T) {
		// 用户 3 不存在于 t-demo-01：RLS 查不到其行，守卫函数返回空集
		if got := countBasicInSession(ctx, t, d, biz.AuthContext{TenantID: "t-demo-01", UserID: 3}); got != 0 {
			t.Fatalf("mismatched identity total = %d, want 0", got)
		}
	})

	t.Run("list fails closed without scope", func(t *testing.T) {
		err := d.Session(ctx, AuthContext{TenantID: super.TenantID, UserID: super.UserID}, func(s *Session) error {
			p, err := gen.ParseBasicQuery(&dynq.Query{})
			if err != nil {
				return err
			}
			_, err = gen.ListBasic(ctx, s.Tx(), p)
			var de *dynq.Error
			if !errors.As(err, &de) || de.Kind != dynq.KindScope {
				t.Fatalf("want dynq scope error, got %v", err)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("session: %v", err)
		}
	})

	t.Run("invalid auth rejected before touching database", func(t *testing.T) {
		if err := d.Session(ctx, AuthContext{TenantID: "", UserID: 1}, func(*Session) error { return nil }); err == nil {
			t.Fatal("empty tenant accepted")
		}
		if err := d.Session(ctx, AuthContext{TenantID: "t-super-01", UserID: 0}, func(*Session) error { return nil }); err == nil {
			t.Fatal("zero user id accepted")
		}
	})
}

// TestUserRepo 以种子事实验证用户仓储：读写路径都在门面与判定函数的
// 纪律内。种子事实：用户 1 全租户（scope 7）、用户 3 仅本人（scope 1）、
// 用户 6 sunqi 前端组、用户 7 zhouba 技术部部门管理员（scope 3）。
func TestUserRepo(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()
	repo := NewUserRepo(d)
	super := biz.AuthContext{TenantID: "t-super-01", UserID: 1}
	selfOnly := biz.AuthContext{TenantID: "t-super-01", UserID: 3}
	deptAdmin := biz.AuthContext{TenantID: "t-super-01", UserID: 7}

	newUser := func(t *testing.T, name string) *biz.User {
		t.Helper()
		u, err := repo.Create(ctx, super, &biz.User{
			Name: name, Nickname: name + "-nick", Password: "hashed-" + name,
			Locale: "zh-CN", Status: biz.UserStatusEnabled,
		})
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		return u
	}

	t.Run("create defaults and audit", func(t *testing.T) {
		u := newUser(t, "grape_it_user")
		if u.ID == 0 || u.Status != biz.UserStatusEnabled || u.Locale != "zh-CN" {
			t.Fatalf("unexpected defaults: %+v", u)
		}
		if u.CreatedBy != super.UserID || u.UpdatedBy != super.UserID {
			t.Fatalf("audit cols = %d/%d, want operator %d", u.CreatedBy, u.UpdatedBy, super.UserID)
		}
		if u.Password != "" {
			t.Fatal("hash must not cross read path")
		}
	})

	t.Run("create duplicate name conflicts", func(t *testing.T) {
		if _, err := repo.Create(ctx, super, &biz.User{Name: "grape_it_user", Nickname: "x", Password: "h"}); !errors.Is(err, biz.ErrUserAlreadyExists) {
			t.Fatalf("want ErrUserAlreadyExists, got %v", err)
		}
	})

	t.Run("get invisible equals not found", func(t *testing.T) {
		u := newUser(t, "grape_it_hidden")
		if _, err := repo.GetByID(ctx, super, u.ID); err != nil {
			t.Fatalf("super get: %v", err)
		}
		// 无归属的新行对仅本人角色不可见：等同不存在
		if _, err := repo.GetByID(ctx, selfOnly, u.ID); !errors.Is(err, biz.ErrUserNotFound) {
			t.Fatalf("want ErrUserNotFound, got %v", err)
		}
	})

	t.Run("list respects scope and filters", func(t *testing.T) {
		q := &dynq.Query{Where: []dynq.Condition{{Field: "name", Op: "eq", Value: "grape_it_user"}}}
		superList, err := repo.List(ctx, super, q)
		if err != nil {
			t.Fatalf("super list: %v", err)
		}
		if len(superList.Items) != 1 {
			t.Fatalf("super list items = %d, want 1", len(superList.Items))
		}
		selfList, err := repo.List(ctx, selfOnly, q)
		if err != nil {
			t.Fatalf("self list: %v", err)
		}
		if len(selfList.Items) != 0 {
			t.Fatalf("self list items = %d, want 0", len(selfList.Items))
		}
	})

	t.Run("update and soft delete", func(t *testing.T) {
		u := newUser(t, "grape_it_upd")
		u.Status = biz.UserStatusDisabled
		u.Nickname = "updated"
		u, err := repo.Update(ctx, super, u)
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if u.Status != biz.UserStatusDisabled || u.Nickname != "updated" {
			t.Fatalf("update lost: %+v", u)
		}
		if err := repo.SoftDelete(ctx, super, u.ID); err != nil {
			t.Fatalf("soft delete: %v", err)
		}
		if _, err := repo.GetByID(ctx, super, u.ID); !errors.Is(err, biz.ErrUserNotFound) {
			t.Fatalf("want ErrUserNotFound after delete, got %v", err)
		}
		// 软删后租户内用户名可复用（部分唯一索引只约束未删除行）
		reused := newUser(t, "grape_it_upd")
		if reused.ID == 0 {
			t.Fatal("reuse after soft delete failed")
		}
	})

	t.Run("assign roles allow deny and idempotent skip", func(t *testing.T) {
		// 角色 103 未配分发部门映射：超管（全租户）可分配给 sunqi；
		// 重复分配跳过。角色 102 配了映射（仅部门 2/5），sunqi 在部门
		// 7——映射分发规则下即使超管也被拒。
		added, err := repo.AssignRoles(ctx, super, 6, []int64{103, 103})
		if err != nil {
			t.Fatalf("super assign: %v", err)
		}
		if added != 1 {
			t.Fatalf("added = %d, want 1 (dedup)", added)
		}
		if added, err := repo.AssignRoles(ctx, super, 6, []int64{103}); err != nil || added != 0 {
			t.Fatalf("reassign = (%d, %v), want (0, nil)", added, err)
		}
		if _, err := repo.AssignRoles(ctx, super, 6, []int64{102}); !errors.Is(err, biz.ErrUserAccessDenied) {
			t.Fatalf("mapped-role assign want ErrUserAccessDenied, got %v", err)
		}
		if _, err := repo.AssignRoles(ctx, selfOnly, 6, []int64{103}); !errors.Is(err, biz.ErrUserAccessDenied) {
			t.Fatalf("want ErrUserAccessDenied, got %v", err)
		}
	})

	t.Run("transfer owner two sided check", func(t *testing.T) {
		u, err := repo.TransferOwner(ctx, super, 6, biz.OwnerTriple{DeptID: ptrInt64(7)})
		if err != nil {
			t.Fatalf("super transfer: %v", err)
		}
		if u.OwnerDeptID == nil || *u.OwnerDeptID != 7 {
			t.Fatalf("owner dept = %v, want 7", u.OwnerDeptID)
		}
		// zhouba（scope 3，本部门及子部门）能看见 sunqi（前端组），但新
		// 归属部门 6（财务部）不在其可见范围内：判定拒绝
		if _, err := repo.TransferOwner(ctx, deptAdmin, 6, biz.OwnerTriple{DeptID: ptrInt64(6)}); !errors.Is(err, biz.ErrUserAccessDenied) {
			t.Fatalf("want ErrUserAccessDenied, got %v", err)
		}
		// 仅本人角色看不见用户 6：等同不存在
		if _, err := repo.TransferOwner(ctx, selfOnly, 6, biz.OwnerTriple{DeptID: ptrInt64(7)}); !errors.Is(err, biz.ErrUserNotFound) {
			t.Fatalf("want ErrUserNotFound, got %v", err)
		}
	})
}

func ptrInt64(v int64) *int64 { return &v }
