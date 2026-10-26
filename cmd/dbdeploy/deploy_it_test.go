package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"grape/internal/data/pg_sqlc"

	"github.com/jackc/pgx/v5"
)

// 存储边界集成测试：以真实 PostgreSQL 验证 dbdeploy 全量部署。使用
// pg_sqlc 套件专用库之外的独立库名，互不干扰。设置以下环境变量后
// 运行，未设置时跳过：
//
//	GRAPE_TEST_ADMIN_DSN=postgres://postgresql:root@127.0.0.1:5432/postgres \
//	go test ./cmd/dbdeploy -count=1 -v

const itDatabase = "grape_dbdeploy_test"

const (
	itGrapeUPassword = "grape_it_secret_pwd"
	itTestUPassword  = "test_it_secret_pwd"
)

func TestDeploy_fresh_database_deployment_succeeds(t *testing.T) {
	adminDSN := requireAdminDSN(t)
	resetTestDatabase(t, adminDSN)
	opts := itOptions(adminDSN)

	var logs []string
	if err := Deploy(context.Background(), opts, recordingLogf(&logs)); err != nil {
		t.Fatalf("deploy fresh database: %v", err)
	}

	// grape_u 可连接目标库
	conn := connectGrapeU(t, adminDSN)
	defer conn.Close(context.Background())

	// 裸 grape_u 连接不设 app.tenant_id：RLS 默认全拒
	var dictRows int
	if err := conn.QueryRow(context.Background(),
		"SELECT count(*) FROM public.sys_dict_data").Scan(&dictRows); err != nil {
		t.Fatalf("query dict data as grape_u: %v", err)
	}
	if dictRows != 0 {
		t.Fatalf("bare grape_u connection saw %d dict rows, want 0 (RLS must deny)", dictRows)
	}
	assertNoPassword(t, logs, nil, opts.GrapeUPassword, opts.TestUPassword)
}

func TestDeploy_grape_u_password_with_quotes_deploys(t *testing.T) {
	adminDSN := requireAdminDSN(t)
	resetTestDatabase(t, adminDSN)
	opts := itOptions(adminDSN)
	opts.GrapeUPassword = "grape_it'quoted_pwd"

	var logs []string
	if err := Deploy(context.Background(), opts, recordingLogf(&logs)); err != nil {
		t.Fatalf("deploy with quoted password: %v", err)
	}
	assertNoPassword(t, logs, nil, opts.GrapeUPassword, opts.TestUPassword)
}

func TestDeploy_repeated_role_and_seed_apply_on_deployed_database(t *testing.T) {
	adminDSN := requireAdminDSN(t)
	resetTestDatabase(t, adminDSN)
	if err := Deploy(context.Background(), itOptions(adminDSN), discardLogf); err != nil {
		t.Fatalf("initial deploy: %v", err)
	}

	ctx := context.Background()
	conn := connectAdmin(t, adminDSN, itDatabase)
	defer conn.Close(ctx)

	// 重放前基线：种子租户 5 行
	var before int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM public.sys_tenant").Scan(&before); err != nil {
		t.Fatalf("count tenants before replay: %v", err)
	}

	// 角色段重放成功（DO 块判存，不改写已有口令）
	if err := applyFile(ctx, t, conn, "role.sql"); err != nil {
		t.Fatalf("replay role.sql: %v", err)
	}
	var roles int
	if err := conn.QueryRow(ctx,
		"SELECT count(*) FROM pg_roles WHERE rolname IN ('grape_u', 'test_u')").Scan(&roles); err != nil {
		t.Fatalf("count roles: %v", err)
	}
	if roles != 2 {
		t.Fatalf("roles after replay = %d, want 2", roles)
	}

	// 种子重放成功且不覆盖已有行（ON CONFLICT DO NOTHING）
	if err := applyFile(ctx, t, conn, "seed.sql"); err != nil {
		t.Fatalf("replay seed.sql: %v", err)
	}
	var after int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM public.sys_tenant").Scan(&after); err != nil {
		t.Fatalf("count tenants after replay: %v", err)
	}
	if after != before {
		t.Fatalf("tenant rows after seed replay = %d, want unchanged %d", after, before)
	}
}

func TestDeploy_full_manifest_rerun_fails_on_deployed_database(t *testing.T) {
	adminDSN := requireAdminDSN(t)
	resetTestDatabase(t, adminDSN)
	if err := Deploy(context.Background(), itOptions(adminDSN), discardLogf); err != nil {
		t.Fatalf("initial deploy: %v", err)
	}

	// 结构 DDL 为裸创建：全清单重复执行报错（幂等边界声明），
	// 全量重建须先 DROP DATABASE
	var logs []string
	err := Deploy(context.Background(), itOptions(adminDSN), recordingLogf(&logs))
	if err == nil {
		t.Fatal("full manifest rerun on deployed database must fail")
	}
	assertNoPassword(t, logs, err, itGrapeUPassword, itTestUPassword)
}

func itOptions(adminDSN string) *Options {
	return &Options{
		AdminDSN:       adminDSN,
		Database:       itDatabase,
		GrapeUPassword: itGrapeUPassword,
		TestUPassword:  itTestUPassword,
	}
}

func requireAdminDSN(t *testing.T) string {
	t.Helper()
	adminDSN := os.Getenv("GRAPE_TEST_ADMIN_DSN")
	if adminDSN == "" {
		t.Skip("GRAPE_TEST_ADMIN_DSN 未设置，跳过 dbdeploy 集成测试")
	}
	return adminDSN
}

// resetTestDatabase 重建集成测试专属库，不触碰其他数据库。
func resetTestDatabase(t *testing.T, adminDSN string) {
	t.Helper()
	ctx := context.Background()
	admin := connectAdmin(t, adminDSN, "postgres")
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+itDatabase+" WITH (FORCE)"); err != nil {
		t.Fatalf("drop test database: %v", err)
	}
}

func connectAdmin(t *testing.T, adminDSN, database string) *pgx.Conn {
	t.Helper()
	cfg, err := pgx.ParseConfig(adminDSN)
	if err != nil {
		t.Fatalf("parse admin dsn: %v", err)
	}
	cfg.Database = database
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	conn, err := pgx.ConnectConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect %s: %v", database, err)
	}
	return conn
}

// connectGrapeU 以部署后的运行角色连接目标库（模拟应用 DSN）。
func connectGrapeU(t *testing.T, adminDSN string) *pgx.Conn {
	t.Helper()
	u, err := url.Parse(adminDSN)
	if err != nil {
		t.Fatalf("parse admin dsn: %v", err)
	}
	u.User = url.UserPassword("grape_u", itGrapeUPassword)
	u.Path = "/" + itDatabase
	cfg, err := pgx.ParseConfig(u.String())
	if err != nil {
		t.Fatalf("parse grape_u dsn: %v", err)
	}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	conn, err := pgx.ConnectConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect as grape_u: %v", err)
	}
	return conn
}

// applyFile 对已部署库重放清单中的单个文件（替换口令占位符）。
func applyFile(ctx context.Context, t *testing.T, conn *pgx.Conn, name string) error {
	t.Helper()
	dir, err := pg_sqlc.DeployDir()
	if err != nil {
		t.Fatalf("locate deploy dir: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	sql := pg_sqlc.ResolveDeployPasswords(string(content), itGrapeUPassword, itTestUPassword)
	if _, err := conn.Exec(ctx, sql); err != nil {
		return fmt.Errorf("exec %s: %w", name, err)
	}
	return nil
}

func recordingLogf(buf *[]string) func(format string, args ...any) {
	return func(format string, args ...any) {
		*buf = append(*buf, fmt.Sprintf(format, args...))
	}
}

func discardLogf(format string, args ...any) {}

// assertNoPassword 断言口令不出现在任何日志行或错误文本中。
func assertNoPassword(t *testing.T, logs []string, err error, passwords ...string) {
	t.Helper()
	for _, password := range passwords {
		for _, line := range logs {
			if strings.Contains(line, password) {
				t.Fatalf("log line leaks password: %q", line)
			}
		}
		if err != nil && strings.Contains(err.Error(), password) {
			t.Fatalf("error text leaks password: %q", err.Error())
		}
	}
}
