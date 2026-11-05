package testsupport

import (
	"context"
	"net/url"
	"os"
	"testing"

	"grape/internal/data/pg_sqlc/deploy"

	"github.com/jackc/pgx/v5"
)

// testUPassword 与 role.sql 占位符替换值：test_u 的既定测试口令。
const testUPassword = "test_u_pwd"

// SuiteDatabase 描述一个 PG 集成测试套件的专属库定位：管理员 DSN 走
// 环境变量门控（未设置即跳过本套件），库名专属本套件（其余套件的测试库
// 绝不触碰），grape_u 口令与 role.sql 占位符替换值一致。
type SuiteDatabase struct {
	EnvVar         string
	Database       string
	GrapeUPassword string
	SkipMessage    string
}

// AdminDSN 返回套件的管理员 DSN（未设置环境变量即跳过套件）：供在部署
// 之外还需管理员连接的用例（如关闭 RLS 模拟策略缺失）先行门控。
func AdminDSN(t *testing.T, s SuiteDatabase) string {
	t.Helper()
	adminDSN := os.Getenv(s.EnvVar)
	if adminDSN == "" {
		t.Skip(s.SkipMessage)
	}
	return adminDSN
}

// SetupSuiteDatabase 重建套件专属测试库并按 DeployFiles 全清单部署（与
// dbdeploy 共用单一事实源；清单尾部含 agent_session.sql 等设施面），返回
// grape_u 连接专属库的 DSN。不裁剪最小子集：role.sql 的授权/回收语句引用
// public 业务表，任何「最小子集」都随 role.sql 的授权面漂移而脆断；全清单
// 是唯一稳态，且回归面与 dbdeploy 一致。role.sql 的角色目录操作是集群级
// 的，跨测试包并发部署必须互斥（AcquireDeployLock）。
func SetupSuiteDatabase(t *testing.T, s SuiteDatabase) (grapeUDSN string) {
	t.Helper()
	adminDSN := os.Getenv(s.EnvVar)
	if adminDSN == "" {
		t.Skip(s.SkipMessage)
	}
	ctx := context.Background()

	cfg, err := pgx.ParseConfig(adminDSN)
	if err != nil {
		t.Fatalf("parse admin dsn: %v", err)
	}
	cfg.Database = "postgres"
	admin, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+s.Database+" WITH (FORCE)"); err != nil {
		t.Fatalf("drop test database: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+s.Database); err != nil {
		t.Fatalf("create test database: %v", err)
	}

	deployDir, err := deploy.DeployDir()
	if err != nil {
		t.Fatalf("locate deploy dir: %v", err)
	}
	if err := deploy.ValidateDeployFiles(deployDir); err != nil {
		t.Fatalf("validate deploy files: %v", err)
	}
	deployCfg := *cfg
	deployCfg.Database = s.Database
	deployCfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	deployConn, err := pgx.ConnectConfig(ctx, &deployCfg)
	if err != nil {
		t.Fatalf("connect deploy: %v", err)
	}
	defer deployConn.Close(ctx)
	release, err := deploy.AcquireDeployLock(ctx)
	if err != nil {
		t.Fatalf("acquire deploy lock: %v", err)
	}
	defer release()
	for _, name := range deploy.TestDeployFiles() {
		sql, err := os.ReadFile(deployDir + "/" + name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		deployed := deploy.ResolveDeployPasswords(string(sql), s.GrapeUPassword, testUPassword)
		if _, err := deployConn.Exec(ctx, deployed); err != nil {
			t.Fatalf("deploy %s: %v", name, err)
		}
	}
	if _, err := deployConn.Exec(ctx,
		"GRANT CONNECT ON DATABASE "+s.Database+" TO grape_u, test_u"); err != nil {
		t.Fatalf("grant connect: %v", err)
	}

	dsn, err := GrapeUDSN(adminDSN, s.Database, s.GrapeUPassword)
	if err != nil {
		t.Fatalf("build grape_u dsn: %v", err)
	}
	return dsn
}

// GrapeUDSN 把管理员 DSN 改写为指定角色连接指定测试库的 DSN。
func GrapeUDSN(adminDSN, database, password string) (string, error) {
	u, err := url.Parse(adminDSN)
	if err != nil {
		return "", err
	}
	u.User = url.UserPassword("grape_u", password)
	u.Path = "/" + database
	return u.String(), nil
}
