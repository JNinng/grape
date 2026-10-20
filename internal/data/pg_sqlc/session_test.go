package pg_sqlc

import (
	"context"
	"errors"
	"os"
	"testing"

	"grape/internal/data/pg_sqlc/gen"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

// 存储边界集成测试：以真实 PostgreSQL 验证会话门面与 RLS/@scope 运行
// 纪律（种子数据的事实断言，见 sqlc/seed.sql 的演示矩阵）。设置以下
// 环境变量后运行，未设置时跳过：
//
//	GRAPE_TEST_ADMIN_DSN=postgres://postgresql:root@127.0.0.1:5432/postgres \
//	GRAPE_TEST_DSN=postgres://grape_u:grape_u_pwd@127.0.0.1:5432/grape_pgsqlc_test \
//	go test ./internal/data/pg_sqlc -run TestSessionDiscipline -count=1 -v
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

func TestSessionDiscipline(t *testing.T) {
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
	defer pool.Close()
	d := &Data{db: pool}

	// 绕过门面的裸连接上不设 app.tenant_id：RLS 默认全拒。
	// 这是连接纪律的数据库层兜底，门面只是让它不可能被绕过。
	t.Run("rls denies without tenant", func(t *testing.T) {
		var n int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM public.sys_dict_data").Scan(&n); err != nil {
			t.Fatalf("query: %v", err)
		}
		if n != 0 {
			t.Fatalf("bare connection without tenant saw %d dict rows, want 0", n)
		}
	})

	t.Run("session sees tenant data", func(t *testing.T) {
		err := d.Session(ctx, AuthContext{TenantID: "t-super-01", UserID: 1}, func(s *Session) error {
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
		err := d.Session(ctx, AuthContext{TenantID: "t-super-01", UserID: 1}, func(s *Session) error {
			p, err := gen.ParseBasicQuery(&dynq.Query{})
			if err != nil {
				return err
			}
			p.Scope = s.BasicScope()
			total, err := gen.CountBasic(ctx, s.Tx(), p)
			if err != nil {
				return err
			}
			if total != 10 {
				t.Fatalf("data_scope=7 total = %d, want 10", total)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("session: %v", err)
		}
	})

	t.Run("scope 1 sees only own basic rows", func(t *testing.T) {
		err := d.Session(ctx, AuthContext{TenantID: "t-super-01", UserID: 3}, func(s *Session) error {
			p, err := gen.ParseBasicQuery(&dynq.Query{})
			if err != nil {
				return err
			}
			p.Scope = s.BasicScope()
			total, err := gen.CountBasic(ctx, s.Tx(), p)
			if err != nil {
				return err
			}
			if total == 0 || total >= 10 {
				t.Fatalf("data_scope=1 total = %d, want (0, 10)", total)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("session: %v", err)
		}
	})

	t.Run("identity mismatch with tenant sees nothing", func(t *testing.T) {
		// 用户 3 不存在于 t-demo-01：RLS 查不到其行，守卫函数返回空集
		err := d.Session(ctx, AuthContext{TenantID: "t-demo-01", UserID: 3}, func(s *Session) error {
			p, err := gen.ParseBasicQuery(&dynq.Query{})
			if err != nil {
				return err
			}
			p.Scope = s.BasicScope()
			total, err := gen.CountBasic(ctx, s.Tx(), p)
			if err != nil {
				return err
			}
			if total != 0 {
				t.Fatalf("mismatched identity total = %d, want 0", total)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("session: %v", err)
		}
	})

	t.Run("list fails closed without scope", func(t *testing.T) {
		err := d.Session(ctx, AuthContext{TenantID: "t-super-01", UserID: 1}, func(s *Session) error {
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
