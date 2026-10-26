package pg_sqlc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"grape/internal/biz"
	"grape/internal/data/pg_sqlc/gen"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

// TestTokenLookupValidate 校验令牌维度事务的凭据组合约束（纯单测，
// 不触数据库）：查找凭据只能二选一，刷新轮换必须成对声明新旧哈希。
func TestTokenLookupValidate(t *testing.T) {
	cases := []struct {
		name   string
		lookup TokenLookup
		ok     bool
	}{
		{"access only", TokenLookup{AccessTokenHash: "a"}, true},
		{"refresh pair", TokenLookup{RefreshTokenHash: "r", NewRefreshTokenHash: "n"}, true},
		{"empty", TokenLookup{}, false},
		{"both lookups", TokenLookup{AccessTokenHash: "a", RefreshTokenHash: "r", NewRefreshTokenHash: "n"}, false},
		{"refresh without new", TokenLookup{RefreshTokenHash: "r"}, false},
		{"new without refresh", TokenLookup{NewRefreshTokenHash: "n"}, false},
		{"access with new refresh", TokenLookup{AccessTokenHash: "a", NewRefreshTokenHash: "n"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.lookup.validate()
			if tc.ok != (err == nil) {
				t.Fatalf("validate(%+v) = %v, want ok=%v", tc.lookup, err, tc.ok)
			}
		})
	}
}

// 存储边界集成测试：以真实 PostgreSQL 验证会话门面、RLS/@scope 运行
// 纪律与用户/会话仓储（断言基于 sqlc/seed.sql 的种子事实）。设置以下环境
// 变量后运行，未设置时跳过：
//
//	GRAPE_TEST_ADMIN_DSN=postgres://postgresql:root@127.0.0.1:5432/postgres \
//	GRAPE_TEST_DSN=postgres://grape_u:grape_u_pwd@127.0.0.1:5432/grape_pgsqlc_test \
//	go test ./internal/data/pg_sqlc -run TestSession -count=1 -v
//
// 测试自重建 grape_pgsqlc_test 库（DROP+CREATE+按 DeployFiles 部署清单
// 装载 sqlc/ 全部文件，见 deploy.go），不触碰其他数据库。

const testDatabase = "grape_pgsqlc_test"

// 测试环境角色口令：与上方 GRAPE_TEST_DSN 示例一致。role.sql 的口令
// 占位符装载前按此替换（干净集群上角色不存在时按真实口令创建）。
const (
	testGrapeUPassword = "grape_u_pwd"
	testTestUPassword  = "test_u_pwd"
)

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

	// 装载部署文件：清单即 deploy.go 的 DeployFiles（与 dbdeploy 共用）；
	// 文件内含多语句与函数体，走 simple protocol
	deployCfg, err := pgx.ParseConfig(adminDSN)
	if err != nil {
		t.Fatalf("parse admin dsn: %v", err)
	}
	deployDir, err := DeployDir()
	if err != nil {
		t.Fatalf("locate deploy dir: %v", err)
	}
	if err := ValidateDeployFiles(deployDir); err != nil {
		t.Fatalf("validate deploy files: %v", err)
	}
	deployCfg.Database = testDatabase
	deployCfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	deployConn, err := pgx.ConnectConfig(ctx, deployCfg)
	if err != nil {
		t.Fatalf("connect deploy: %v", err)
	}
	defer deployConn.Close(ctx)
	for _, f := range DeployFiles {
		sql, err := os.ReadFile(filepath.Join(deployDir, f))
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		deployed := ResolveDeployPasswords(string(sql), testGrapeUPassword, testTestUPassword)
		if _, err := deployConn.Exec(ctx, deployed); err != nil {
			t.Fatalf("deploy %s: %v", f, err)
		}
	}
	// grape_u/test_u 由 role.sql 创建（见部署清单），此时才可授权 CONNECT
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

// countBasicInIdentityTx 统计当前会话可见的 basic 行数。
func countBasicInIdentityTx(ctx context.Context, t *testing.T, d *Data, auth biz.Identity) int64 {
	t.Helper()
	var total int64
	err := d.IdentityTx(ctx, Identity{TenantID: auth.TenantID, UserID: auth.UserID}, func(s *IdentityTx) error {
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
	super := biz.Identity{TenantID: "t-super-01", UserID: 1}

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
		err := d.IdentityTx(ctx, Identity{TenantID: super.TenantID, UserID: super.UserID}, func(s *IdentityTx) error {
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
		err := d.IdentityTx(ctx, Identity{TenantID: "t-nope", UserID: 1}, func(s *IdentityTx) error {
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
		if got := countBasicInIdentityTx(ctx, t, d, super); got != 10 {
			t.Fatalf("data_scope=7 total = %d, want 10", got)
		}
	})

	t.Run("scope 1 sees only own basic rows", func(t *testing.T) {
		if got := countBasicInIdentityTx(ctx, t, d, biz.Identity{TenantID: super.TenantID, UserID: 3}); got == 0 || got >= 10 {
			t.Fatalf("data_scope=1 total = %d, want (0, 10)", got)
		}
	})

	t.Run("identity mismatch with tenant sees nothing", func(t *testing.T) {
		// 用户 3 不存在于 t-demo-01：RLS 查不到其行，守卫函数返回空集
		if got := countBasicInIdentityTx(ctx, t, d, biz.Identity{TenantID: "t-demo-01", UserID: 3}); got != 0 {
			t.Fatalf("mismatched identity total = %d, want 0", got)
		}
	})

	t.Run("list fails closed without scope", func(t *testing.T) {
		err := d.IdentityTx(ctx, Identity{TenantID: super.TenantID, UserID: super.UserID}, func(s *IdentityTx) error {
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
		if err := d.IdentityTx(ctx, Identity{TenantID: "", UserID: 1}, func(*IdentityTx) error { return nil }); err == nil {
			t.Fatal("empty tenant accepted")
		}
		if err := d.IdentityTx(ctx, Identity{TenantID: "t-super-01", UserID: 0}, func(*IdentityTx) error { return nil }); err == nil {
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
	super := biz.Identity{TenantID: "t-super-01", UserID: 1}
	selfOnly := biz.Identity{TenantID: "t-super-01", UserID: 3}
	deptAdmin := biz.Identity{TenantID: "t-super-01", UserID: 7}

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

	t.Run("permitted queries honor optional permission code", func(t *testing.T) {
		allowed := "sys:user:query" // 角色 102 持有
		denied := "sys:user:delete" // 角色 102 不持有

		err := d.IdentityTx(ctx, Identity{TenantID: deptAdmin.TenantID, UserID: deptAdmin.UserID}, func(s *IdentityTx) error {
			if _, err := s.Q().GetSysUserPermitted(ctx, gen.GetSysUserPermittedParams{
				ID: deptAdmin.UserID, Operator: deptAdmin.UserID, PermissionCode: &allowed,
			}); err != nil {
				t.Fatalf("get with allowed code: %v", err)
			}
			if _, err := s.Q().GetSysUserPermitted(ctx, gen.GetSysUserPermittedParams{
				ID: deptAdmin.UserID, Operator: deptAdmin.UserID, PermissionCode: &denied,
			}); !errors.Is(err, pgx.ErrNoRows) {
				t.Fatalf("get with denied code: want ErrNoRows, got %v", err)
			}
			if _, err := s.Q().LockSysUserPermitted(ctx, gen.LockSysUserPermittedParams{
				ID: deptAdmin.UserID, Operator: deptAdmin.UserID, PermissionCode: &allowed,
			}); err != nil {
				t.Fatalf("lock with allowed code: %v", err)
			}
			if _, err := s.Q().LockSysUserPermitted(ctx, gen.LockSysUserPermittedParams{
				ID: deptAdmin.UserID, Operator: deptAdmin.UserID, PermissionCode: &denied,
			}); !errors.Is(err, pgx.ErrNoRows) {
				t.Fatalf("lock with denied code: want ErrNoRows, got %v", err)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("identity tx: %v", err)
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

	// 软删过滤由表级 @filter 在解析期注入（服务端固定不变量），先软删
	// 再 List：行与 total 都不可见。
	t.Run("soft deleted row invisible in list", func(t *testing.T) {
		u := newUser(t, "grape_it_list_del")
		if err := repo.SoftDelete(ctx, super, u.ID); err != nil {
			t.Fatalf("soft delete: %v", err)
		}
		q := &dynq.Query{Where: []dynq.Condition{{Field: "name", Op: "eq", Value: "grape_it_list_del"}}}
		list, err := repo.List(ctx, super, q)
		if err != nil {
			t.Fatalf("list after soft delete: %v", err)
		}
		if len(list.Items) != 0 || list.Total == nil || *list.Total != 0 {
			t.Fatalf("soft deleted row visible in list: items = %d, total = %v, want 0/0", len(list.Items), list.Total)
		}
	})

	// 动态协议不暴露软删查询：deletedAt 条件在解析期按未知字段拒绝。
	t.Run("deletedAt where condition rejected by protocol", func(t *testing.T) {
		q := &dynq.Query{Where: []dynq.Condition{{Field: "deletedAt", Op: "is_null"}}}
		if _, err := repo.List(ctx, super, q); !errors.Is(err, biz.ErrUserInvalidArgument) {
			t.Fatalf("deletedAt condition: want ErrUserInvalidArgument, got %v", err)
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

// TestSessionRepo 以真实 PostgreSQL 验证会话仓储：令牌只以哈希出入、
// 校验只看有效行、撤销幂等且受租户边界约束、登出按令牌哈希撤销并使
// 刷新令牌一并失效、刷新轮换后旧令牌立即失效，以及令牌维度事务只放
// 行哈希命中的那一行。
func TestSessionRepo(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()
	repo := NewSessionRepo(d)
	owner := biz.Identity{TenantID: "t-super-01", UserID: 1}
	otherTenant := biz.Identity{TenantID: "t-demo-01", UserID: 5}

	issue := func(t *testing.T, owner biz.Identity, access, refresh string, accessTTL, refreshTTL time.Duration) *biz.Session {
		t.Helper()
		s, err := repo.Issue(ctx, owner, &biz.IssueSessionInput{
			AccessTokenHash:  testTokenHash(access),
			RefreshTokenHash: testTokenHash(refresh),
			AccessTTL:        accessTTL,
			RefreshTTL:       refreshTTL,
			DeviceInfo:       ptrString("integration-test"),
			IPAddress:        ptrString("127.0.0.1"),
		})
		if err != nil {
			t.Fatalf("issue session: %v", err)
		}
		return s
	}

	t.Run("issue and validate", func(t *testing.T) {
		s := issue(t, owner, "access-issue", "refresh-issue", time.Hour, 24*time.Hour)
		if s.ID <= 0 || s.TenantID != owner.TenantID || s.UserID != owner.UserID {
			t.Fatalf("issued session = %+v, want owner %+v", s, owner)
		}
		if s.Status != biz.SessionStatusEnabled || s.IsRevoked {
			t.Fatalf("issued status = %d revoked=%v, want enabled", s.Status, s.IsRevoked)
		}
		if s.CreatedBy != owner.UserID || s.UpdatedBy != owner.UserID {
			t.Fatalf("audit cols = %d/%d, want operator %d", s.CreatedBy, s.UpdatedBy, owner.UserID)
		}
		if s.LastActivityAt == nil {
			t.Fatal("last_activity_at must be set on issue")
		}

		got, err := repo.Validate(ctx, testTokenHash("access-issue"))
		if err != nil {
			t.Fatalf("validate: %v", err)
		}
		if got.ID != s.ID || got.Identity() != owner {
			t.Fatalf("validated = %+v (id %d), want %+v (id %d)", got.Identity(), got.ID, owner, s.ID)
		}
		if _, err := repo.Validate(ctx, testTokenHash("access-wrong")); !errors.Is(err, biz.ErrSessionInvalid) {
			t.Fatalf("unknown token: want ErrSessionInvalid, got %v", err)
		}
	})

	t.Run("expired session is invalid", func(t *testing.T) {
		issue(t, owner, "access-expired", "refresh-expired", -time.Minute, time.Hour)
		if _, err := repo.Validate(ctx, testTokenHash("access-expired")); !errors.Is(err, biz.ErrSessionInvalid) {
			t.Fatalf("want ErrSessionInvalid, got %v", err)
		}
	})

	t.Run("disabled session is invalid", func(t *testing.T) {
		s := issue(t, owner, "access-disabled", "refresh-disabled", time.Hour, 24*time.Hour)
		err := d.IdentityTx(ctx, Identity{TenantID: owner.TenantID, UserID: owner.UserID}, func(sess *IdentityTx) error {
			_, err := sess.Tx().Exec(ctx, "UPDATE public.sys_session SET status = 0 WHERE id = $1", s.ID)
			return err
		})
		if err != nil {
			t.Fatalf("disable session: %v", err)
		}
		if _, err := repo.Validate(ctx, testTokenHash("access-disabled")); !errors.Is(err, biz.ErrSessionInvalid) {
			t.Fatalf("want ErrSessionInvalid, got %v", err)
		}
	})

	t.Run("revoke is idempotent and tenant scoped", func(t *testing.T) {
		s := issue(t, owner, "access-revoke", "refresh-revoke", time.Hour, 24*time.Hour)
		if err := repo.Revoke(ctx, owner, s.ID); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		if _, err := repo.Validate(ctx, testTokenHash("access-revoke")); !errors.Is(err, biz.ErrSessionInvalid) {
			t.Fatalf("revoked session: want ErrSessionInvalid, got %v", err)
		}
		if err := repo.Revoke(ctx, owner, s.ID); err != nil {
			t.Fatalf("repeat revoke must be idempotent: %v", err)
		}

		// 跨租户按 id 撤销：RLS 不可见 → 0 行，不报错也不生效
		cross := issue(t, owner, "access-cross", "refresh-cross", time.Hour, 24*time.Hour)
		if err := repo.Revoke(ctx, otherTenant, cross.ID); err != nil {
			t.Fatalf("cross-tenant revoke: %v", err)
		}
		if _, err := repo.Validate(ctx, testTokenHash("access-cross")); err != nil {
			t.Fatalf("cross-tenant revoke must not take effect: %v", err)
		}
	})

	// 登出按访问令牌哈希撤销：访问令牌与刷新令牌一并失效（会话级撤销）。
	t.Run("logout by access token kills session and refresh", func(t *testing.T) {
		issue(t, owner, "access-logout", "refresh-logout", time.Hour, 24*time.Hour)
		if err := repo.RevokeByAccessTokenHash(ctx, testTokenHash("access-logout")); err != nil {
			t.Fatalf("logout: %v", err)
		}
		if _, err := repo.Validate(ctx, testTokenHash("access-logout")); !errors.Is(err, biz.ErrSessionInvalid) {
			t.Fatalf("logged-out access token: want ErrSessionInvalid, got %v", err)
		}
		if _, err := repo.Refresh(ctx, &biz.RefreshSessionInput{
			CurrentRefreshTokenHash: testTokenHash("refresh-logout"),
			NewAccessTokenHash:      testTokenHash("access-logout-2"),
			NewRefreshTokenHash:     testTokenHash("refresh-logout-2"),
			AccessTTL:               time.Hour,
			RefreshTTL:              24 * time.Hour,
		}); !errors.Is(err, biz.ErrSessionInvalid) {
			t.Fatalf("refresh after logout: want ErrSessionInvalid, got %v", err)
		}
	})

	// 重复登出幂等：重放不报错，审计归属记会话用户（令牌维度事务无
	// 调用者身份，updated_by = user_id）。
	t.Run("logout is idempotent and attributes audit to owner", func(t *testing.T) {
		issue(t, owner, "access-logout-replay", "refresh-logout-replay", time.Hour, 24*time.Hour)
		hash := testTokenHash("access-logout-replay")
		readRow := func(t *testing.T) (revoked bool, updatedBy int64) {
			t.Helper()
			err := d.IdentityTx(ctx, Identity{TenantID: owner.TenantID, UserID: owner.UserID}, func(s *IdentityTx) error {
				return s.Tx().QueryRow(ctx,
					"SELECT is_revoked, updated_by FROM public.sys_session WHERE token_hash = $1", hash,
				).Scan(&revoked, &updatedBy)
			})
			if err != nil {
				t.Fatalf("read session row: %v", err)
			}
			return revoked, updatedBy
		}

		if err := repo.RevokeByAccessTokenHash(ctx, hash); err != nil {
			t.Fatalf("logout: %v", err)
		}
		if err := repo.RevokeByAccessTokenHash(ctx, hash); err != nil {
			t.Fatalf("replayed logout must be idempotent: %v", err)
		}
		revoked, updatedBy := readRow(t)
		if !revoked {
			t.Fatal("session must stay revoked after replayed logout")
		}
		if updatedBy != owner.UserID {
			t.Fatalf("updated_by = %d, want session owner %d", updatedBy, owner.UserID)
		}
	})

	// 未知令牌无可撤销行：幂等成功，且不影响其他会话。
	t.Run("logout with unknown token is a no-op", func(t *testing.T) {
		issue(t, owner, "access-keep", "refresh-keep", time.Hour, 24*time.Hour)
		if err := repo.RevokeByAccessTokenHash(ctx, testTokenHash("access-unknown")); err != nil {
			t.Fatalf("unknown token logout: %v", err)
		}
		if _, err := repo.Validate(ctx, testTokenHash("access-keep")); err != nil {
			t.Fatalf("unrelated session must stay valid: %v", err)
		}
	})

	t.Run("refresh rotates both tokens", func(t *testing.T) {
		s := issue(t, owner, "access-old", "refresh-old", time.Hour, 24*time.Hour)
		rotated, err := repo.Refresh(ctx, &biz.RefreshSessionInput{
			CurrentRefreshTokenHash: testTokenHash("refresh-old"),
			NewAccessTokenHash:      testTokenHash("access-new"),
			NewRefreshTokenHash:     testTokenHash("refresh-new"),
			AccessTTL:               2 * time.Hour,
			RefreshTTL:              48 * time.Hour,
		})
		if err != nil {
			t.Fatalf("refresh: %v", err)
		}
		if rotated.ID != s.ID || rotated.UserID != owner.UserID {
			t.Fatalf("rotated session = %+v, want id %d user %d", rotated, s.ID, owner.UserID)
		}
		// 轮换的审计列记会话用户，活跃时间刷新到轮换时刻
		if rotated.UpdatedBy != owner.UserID {
			t.Fatalf("rotated updated_by = %d, want %d", rotated.UpdatedBy, owner.UserID)
		}
		if rotated.LastActivityAt == nil || rotated.LastActivityAt.Before(*s.LastActivityAt) {
			t.Fatalf("rotated last_activity_at = %v, want >= issue time %v", rotated.LastActivityAt, s.LastActivityAt)
		}
		if _, err := repo.Validate(ctx, testTokenHash("access-old")); !errors.Is(err, biz.ErrSessionInvalid) {
			t.Fatalf("old access token: want ErrSessionInvalid, got %v", err)
		}
		if _, err := repo.Validate(ctx, testTokenHash("access-new")); err != nil {
			t.Fatalf("new access token: %v", err)
		}

		// 旧刷新令牌已轮换掉，不能再次使用
		if _, err := repo.Refresh(ctx, &biz.RefreshSessionInput{
			CurrentRefreshTokenHash: testTokenHash("refresh-old"),
			NewAccessTokenHash:      testTokenHash("access-again"),
			NewRefreshTokenHash:     testTokenHash("refresh-again"),
			AccessTTL:               time.Hour,
			RefreshTTL:              24 * time.Hour,
		}); !errors.Is(err, biz.ErrSessionInvalid) {
			t.Fatalf("reused refresh token: want ErrSessionInvalid, got %v", err)
		}

		// 新刷新令牌继续可用
		if _, err := repo.Refresh(ctx, &biz.RefreshSessionInput{
			CurrentRefreshTokenHash: testTokenHash("refresh-new"),
			NewAccessTokenHash:      testTokenHash("access-next"),
			NewRefreshTokenHash:     testTokenHash("refresh-next"),
			AccessTTL:               time.Hour,
			RefreshTTL:              24 * time.Hour,
		}); err != nil {
			t.Fatalf("rotate with new refresh token: %v", err)
		}
	})

	t.Run("revoked session cannot refresh", func(t *testing.T) {
		s := issue(t, owner, "access-revoked-r", "refresh-revoked-r", time.Hour, 24*time.Hour)
		if err := repo.Revoke(ctx, owner, s.ID); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		if _, err := repo.Refresh(ctx, &biz.RefreshSessionInput{
			CurrentRefreshTokenHash: testTokenHash("refresh-revoked-r"),
			NewAccessTokenHash:      testTokenHash("access-revoked-r2"),
			NewRefreshTokenHash:     testTokenHash("refresh-revoked-r2"),
			AccessTTL:               time.Hour,
			RefreshTTL:              24 * time.Hour,
		}); !errors.Is(err, biz.ErrSessionInvalid) {
			t.Fatalf("want ErrSessionInvalid, got %v", err)
		}
	})

	t.Run("expired refresh token cannot rotate but access stays valid", func(t *testing.T) {
		issue(t, owner, "access-expired-rf", "refresh-expired-rf", time.Hour, -time.Minute)
		if _, err := repo.Validate(ctx, testTokenHash("access-expired-rf")); err != nil {
			t.Fatalf("access token should stay valid: %v", err)
		}
		if _, err := repo.Refresh(ctx, &biz.RefreshSessionInput{
			CurrentRefreshTokenHash: testTokenHash("refresh-expired-rf"),
			NewAccessTokenHash:      testTokenHash("access-expired-rf2"),
			NewRefreshTokenHash:     testTokenHash("refresh-expired-rf2"),
			AccessTTL:               time.Hour,
			RefreshTTL:              24 * time.Hour,
		}); !errors.Is(err, biz.ErrSessionInvalid) {
			t.Fatalf("want ErrSessionInvalid, got %v", err)
		}
	})

	t.Run("disabled session cannot refresh", func(t *testing.T) {
		s := issue(t, owner, "access-dis-rf", "refresh-dis-rf", time.Hour, 24*time.Hour)
		err := d.IdentityTx(ctx, Identity{TenantID: owner.TenantID, UserID: owner.UserID}, func(sess *IdentityTx) error {
			_, err := sess.Tx().Exec(ctx, "UPDATE public.sys_session SET status = 0 WHERE id = $1", s.ID)
			return err
		})
		if err != nil {
			t.Fatalf("disable session: %v", err)
		}
		if _, err := repo.Refresh(ctx, &biz.RefreshSessionInput{
			CurrentRefreshTokenHash: testTokenHash("refresh-dis-rf"),
			NewAccessTokenHash:      testTokenHash("access-dis-rf2"),
			NewRefreshTokenHash:     testTokenHash("refresh-dis-rf2"),
			AccessTTL:               time.Hour,
			RefreshTTL:              24 * time.Hour,
		}); !errors.Is(err, biz.ErrSessionInvalid) {
			t.Fatalf("want ErrSessionInvalid, got %v", err)
		}
	})

	t.Run("concurrent refresh: exactly one wins", func(t *testing.T) {
		issue(t, owner, "access-race", "refresh-race", time.Hour, 24*time.Hour)
		const workers = 4
		var wg sync.WaitGroup
		start := make(chan struct{})
		results := make([]error, workers)
		for i := range workers {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				_, err := repo.Refresh(ctx, &biz.RefreshSessionInput{
					CurrentRefreshTokenHash: testTokenHash("refresh-race"),
					NewAccessTokenHash:      testTokenHash("access-race-" + strconv.Itoa(i)),
					NewRefreshTokenHash:     testTokenHash("refresh-race-" + strconv.Itoa(i)),
					AccessTTL:               time.Hour,
					RefreshTTL:              24 * time.Hour,
				})
				results[i] = err
			}(i)
		}
		close(start)
		wg.Wait()
		wins := 0
		for i, err := range results {
			switch {
			case err == nil:
				wins++
			case !errors.Is(err, biz.ErrSessionInvalid):
				t.Fatalf("worker %d: unexpected error %v", i, err)
			}
		}
		if wins != 1 {
			t.Fatalf("wins = %d, want exactly 1", wins)
		}
	})

	t.Run("usecase clamps long metadata end to end", func(t *testing.T) {
		uc := biz.NewSessionUsecase(repo)
		device := strings.Repeat("設", 600)
		ip := strings.Repeat("2001:db8::", 10)
		issued, err := uc.Issue(ctx, owner, biz.SessionOptions{}, &device, &ip)
		if err != nil {
			t.Fatalf("issue with oversized metadata: %v", err)
		}
		got, err := uc.Validate(ctx, issued.AccessToken)
		if err != nil {
			t.Fatalf("validate: %v", err)
		}
		if got.DeviceInfo == nil || len([]rune(*got.DeviceInfo)) != biz.MaxSessionDeviceInfoChars {
			t.Fatalf("stored device len = %d, want %d", len([]rune(*got.DeviceInfo)), biz.MaxSessionDeviceInfoChars)
		}
		if got.IPAddress == nil || len([]rune(*got.IPAddress)) != biz.MaxSessionIPAddressChars {
			t.Fatalf("stored ip len = %d, want %d", len([]rune(*got.IPAddress)), biz.MaxSessionIPAddressChars)
		}
		if *got.DeviceInfo != string([]rune(device)[:biz.MaxSessionDeviceInfoChars]) {
			t.Fatal("stored device must be the rune prefix")
		}
	})

	t.Run("token transaction sees only the matched row", func(t *testing.T) {
		issue(t, owner, "access-rls-a", "refresh-rls-a", time.Hour, 24*time.Hour)
		issue(t, owner, "access-rls-b", "refresh-rls-b", time.Hour, 24*time.Hour)
		issue(t, otherTenant, "access-rls-c", "refresh-rls-c", time.Hour, 24*time.Hour)

		var seen int
		err := d.TokenTx(ctx, TokenLookup{AccessTokenHash: testTokenHash("access-rls-a")}, func(s *TokenTx) error {
			return s.Tx().QueryRow(ctx, "SELECT count(*) FROM public.sys_session").Scan(&seen)
		})
		if err != nil {
			t.Fatalf("token transaction: %v", err)
		}
		if seen != 1 {
			t.Fatalf("token transaction saw %d session rows, want 1", seen)
		}

		// 裸连接没有令牌凭据：RLS 默认全拒
		if err := d.db.QueryRow(ctx, "SELECT count(*) FROM public.sys_session").Scan(&seen); err != nil {
			t.Fatalf("bare query: %v", err)
		}
		if seen != 0 {
			t.Fatalf("bare connection saw %d session rows, want 0", seen)
		}

		// 令牌是全局凭据：跨租户也能按哈希校验，身份事实取自行内
		got, err := repo.Validate(ctx, testTokenHash("access-rls-c"))
		if err != nil {
			t.Fatalf("cross-tenant validate: %v", err)
		}
		if got.TenantID != otherTenant.TenantID || got.UserID != otherTenant.UserID {
			t.Fatalf("cross-tenant identity = %+v, want %+v", got.Identity(), otherTenant)
		}
	})

	// 登出全链路（用例路径）：明文令牌哈希后撤销，访问/刷新令牌一并
	// 失效，重放登出幂等成功。
	t.Run("usecase logout works end to end", func(t *testing.T) {
		uc := biz.NewSessionUsecase(repo)
		issued, err := uc.Issue(ctx, owner, biz.SessionOptions{}, ptrString("logout-e2e"), ptrString("127.0.0.1"))
		if err != nil {
			t.Fatalf("issue via usecase: %v", err)
		}
		if err := uc.Logout(ctx, issued.AccessToken); err != nil {
			t.Fatalf("logout via usecase: %v", err)
		}
		if _, err := uc.Validate(ctx, issued.AccessToken); !errors.Is(err, biz.ErrSessionInvalid) {
			t.Fatalf("logged-out access token: want ErrSessionInvalid, got %v", err)
		}
		if _, err := uc.Refresh(ctx, issued.RefreshToken, biz.SessionOptions{}); !errors.Is(err, biz.ErrSessionInvalid) {
			t.Fatalf("refresh after logout: want ErrSessionInvalid, got %v", err)
		}
		if err := uc.Logout(ctx, issued.AccessToken); err != nil {
			t.Fatalf("replayed logout must be idempotent: %v", err)
		}
	})

	t.Run("usecase tokens work end to end", func(t *testing.T) {
		uc := biz.NewSessionUsecase(repo)
		issued, err := uc.Issue(ctx, owner, biz.SessionOptions{}, ptrString("e2e"), ptrString("127.0.0.1"))
		if err != nil {
			t.Fatalf("issue via usecase: %v", err)
		}
		got, err := uc.Validate(ctx, issued.AccessToken)
		if err != nil {
			t.Fatalf("validate issued token: %v", err)
		}
		if got.ID != issued.Session.ID || got.Identity() != owner {
			t.Fatalf("validated = %+v (id %d), want owner %+v (id %d)", got.Identity(), got.ID, owner, issued.Session.ID)
		}

		rotated, err := uc.Refresh(ctx, issued.RefreshToken, biz.SessionOptions{})
		if err != nil {
			t.Fatalf("refresh via usecase: %v", err)
		}
		if _, err := uc.Validate(ctx, issued.AccessToken); !errors.Is(err, biz.ErrSessionInvalid) {
			t.Fatalf("old access token after refresh: want ErrSessionInvalid, got %v", err)
		}
		if _, err := uc.Validate(ctx, rotated.AccessToken); err != nil {
			t.Fatalf("new access token: %v", err)
		}
	})
}

// testTokenHash 模拟 biz 用例的令牌哈希（sha256 → 十六进制）。
func testTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func ptrString(v string) *string { return &v }

// TestAuthLogin 以种子事实验证登录全链路：种子口令（123456，scrypt
// 格式）校验通过后透明重哈希为 argon2id，二次登录走 argon2id 路径，
// 签发的令牌可校验、可轮换；凭据错误与禁用账号按域错误拒绝。
func TestAuthLogin(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()
	users := NewUserRepo(d)
	sessionUC := biz.NewSessionUsecase(NewSessionRepo(d))
	authUC := biz.NewAuthUsecase(users, sessionUC, "")
	device, ip := ptrString("it-device"), ptrString("127.0.0.1")

	// 种子事实：t-super-01/admin，口令 123456（scrypt），持全租户角色
	login, err := authUC.Login(ctx, &biz.LoginInput{TenantID: "t-super-01", UserName: "admin", Password: "123456", DeviceInfo: device, IPAddress: ip})
	if err != nil {
		t.Fatalf("seed admin login: %v", err)
	}
	if login.User.ID != 1 || login.User.Name != "admin" || login.User.Nickname != "系统管理员" {
		t.Fatalf("login summary = %+v, want admin/系统管理员", login.User)
	}
	if got, err := sessionUC.Validate(ctx, login.Issued.AccessToken); err != nil {
		t.Fatalf("validate issued token: %v", err)
	} else if got.Identity() != (biz.Identity{TenantID: "t-super-01", UserID: 1}) {
		t.Fatalf("validated identity = %+v, want admin", got.Identity())
	}

	// 透明重哈希：登录后库内哈希应已是 argon2id，且原口令仍可登录
	cred, err := users.GetCredentialForLogin(ctx, "t-super-01", "admin")
	if err != nil {
		t.Fatalf("credential after login: %v", err)
	}
	if !strings.HasPrefix(cred.PasswordHash, "$argon2id$") {
		t.Fatalf("hash after login = %.20q..., want argon2id", cred.PasswordHash)
	}
	if again, err := authUC.Login(ctx, &biz.LoginInput{TenantID: "t-super-01", UserName: "admin", Password: "123456"}); err != nil {
		t.Fatalf("second login (argon2id path): %v", err)
	} else if again.Issued.Session.ID == login.Issued.Session.ID {
		t.Fatal("second login must issue a distinct session")
	}

	// 刷新令牌轮换
	rotated, err := sessionUC.Refresh(ctx, login.Issued.RefreshToken, biz.SessionOptions{})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if _, err := sessionUC.Validate(ctx, login.Issued.AccessToken); !errors.Is(err, biz.ErrSessionInvalid) {
		t.Fatalf("old access token after refresh: want ErrSessionInvalid, got %v", err)
	}
	if _, err := sessionUC.Validate(ctx, rotated.AccessToken); err != nil {
		t.Fatalf("rotated access token: %v", err)
	}

	// 凭据错误：口令不匹配与用户不存在同因，防枚举
	if _, err := authUC.Login(ctx, &biz.LoginInput{TenantID: "t-super-01", UserName: "admin", Password: "wrong"}); !errors.Is(err, biz.ErrInvalidCredentials) {
		t.Fatalf("wrong password: want ErrInvalidCredentials, got %v", err)
	}
	if _, err := authUC.Login(ctx, &biz.LoginInput{TenantID: "t-super-01", UserName: "nobody", Password: "123456"}); !errors.Is(err, biz.ErrInvalidCredentials) {
		t.Fatalf("unknown user: want ErrInvalidCredentials, got %v", err)
	}
	// 跨租户的用户名等同不存在
	if _, err := authUC.Login(ctx, &biz.LoginInput{TenantID: "t-demo-01", UserName: "admin", Password: "123456"}); !errors.Is(err, biz.ErrInvalidCredentials) {
		t.Fatalf("cross-tenant name: want ErrInvalidCredentials, got %v", err)
	}
	// 软删除用户等同不存在
	deleted, err := users.Create(ctx, biz.Identity{TenantID: "t-super-01", UserID: 1}, &biz.User{
		Name: "grape_it_login_del", Nickname: "del", Password: "x", Locale: "zh-CN", Status: biz.UserStatusEnabled,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := users.SoftDelete(ctx, biz.Identity{TenantID: "t-super-01", UserID: 1}, deleted.ID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	if _, err := authUC.Login(ctx, &biz.LoginInput{TenantID: "t-super-01", UserName: "grape_it_login_del", Password: "x"}); !errors.Is(err, biz.ErrInvalidCredentials) {
		t.Fatalf("deleted user: want ErrInvalidCredentials, got %v", err)
	}

	// 禁用账号：口令通过后拒绝（仓储直写需预哈希，用例路径才负责哈希）
	disHash, err := biz.HashPassword("pw-dis")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	created, err := users.Create(ctx, biz.Identity{TenantID: "t-super-01", UserID: 1}, &biz.User{
		Name: "grape_it_login_dis", Nickname: "dis", Password: disHash, Locale: "zh-CN", Status: biz.UserStatusEnabled,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := d.IdentityTx(ctx, Identity{TenantID: "t-super-01", UserID: 1}, func(s *IdentityTx) error {
		_, err := s.Tx().Exec(ctx, "UPDATE public.sys_user SET status = 0 WHERE id = $1", created.ID)
		return err
	}); err != nil {
		t.Fatalf("disable user: %v", err)
	}
	if _, err := authUC.Login(ctx, &biz.LoginInput{TenantID: "t-super-01", UserName: "grape_it_login_dis", Password: "pw-dis"}); !errors.Is(err, biz.ErrAuthUserDisabled) {
		t.Fatalf("disabled user: want ErrAuthUserDisabled, got %v", err)
	}
}
