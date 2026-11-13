package deploy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
)

// 集成测试模板库：全量清单（TestDeployFiles）只在模板库装载一次，各测试
// setup 经 CloneTestDatabase 以 CREATE DATABASE ... TEMPLATE 克隆出全新
// 测试库——隔离语义与逐库全量装载完全一致，成本从重放全部 DDL 降为文件级
// 拷贝。marker 取 COMMENT ON DATABASE（集群目录、不随克隆进入测试库），
// 以清单文件内容（含 test_seed.sql）与清单顺序的 sha256 加 version() 作
// 失效判定。
const (
	// TestTemplateDatabase 是模板库 clustered 级固定名。
	TestTemplateDatabase = "grape_test_tpl"

	// TestDatabasePrefix 是按测试名派生测试库的前缀：模板库失效重建时
	// 按此前缀清理由旧模板克隆的遗留测试库（已随旧清单失效）。
	TestDatabasePrefix = "grape_t_"
)

// manifestHashCommentPrefix 是模板库注释中失效指纹的前缀。
const manifestHashCommentPrefix = "manifest-sha256: "

// testRunRoles 是需要 CONNECT 授权的运行角色（与 role.sql 一致）。
const testRunRoles = "grape_u, test_u"

// EnsureTestTemplate 保证模板库存在且与当前清单一致：指纹一致则幂等返回；
// 否则持部署锁重建——删旧模板与其遗留测试库、CREATE DATABASE、按序装载
// 全量清单、置 is_template 并写入指纹。锁内先复查指纹，并发调用只重建
// 一次。
//
// 指纹跨调用不一致即重建是判定唯一事实源：清单 SQL、清单顺序或 PG 版本
// 任一变化都会触发重建，无须人工清理。
func EnsureTestTemplate(ctx context.Context, adminDSN, grapeUPassword, testUPassword string) error {
	admin, err := connectAdmin(ctx, adminDSN, "postgres")
	if err != nil {
		return err
	}
	defer func() { _ = admin.Close(ctx) }()

	var pgVersion string
	if err := admin.QueryRow(ctx, "SELECT version()").Scan(&pgVersion); err != nil {
		return fmt.Errorf("deploy: read server version: %w", err)
	}
	want, err := manifestFingerprint(pgVersion)
	if err != nil {
		return err
	}
	if templateUpToDate(ctx, admin, want) {
		return nil
	}

	// role.sql 的角色目录操作是集群级的，重建必须与其他部署互斥。
	release, err := AcquireDeployLock(ctx)
	if err != nil {
		return err
	}
	defer release()

	// 锁内复查：并发调用可能已完成重建。
	if templateUpToDate(ctx, admin, want) {
		return nil
	}
	if err := dropStaleTestDatabases(ctx, admin); err != nil {
		return err
	}
	// 模板标记须先摘除：datistemplate=true 的库拒绝 DROP（42809）。模板
	// 不存在时本句报错属预期，真正的失败由下方 DROP IF EXISTS 暴露。
	_, _ = admin.Exec(ctx, "ALTER DATABASE "+TestTemplateDatabase+" WITH is_template FALSE")
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+TestTemplateDatabase+" WITH (FORCE)"); err != nil {
		return fmt.Errorf("deploy: drop template database: %w", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+TestTemplateDatabase); err != nil {
		return fmt.Errorf("deploy: create template database: %w", err)
	}

	// 装载全量清单：文件内含多语句与函数体，走 simple protocol。
	tplConn, err := connectAdmin(ctx, adminDSN, TestTemplateDatabase)
	if err != nil {
		return err
	}
	defer func() { _ = tplConn.Close(ctx) }()
	if err := runTestManifest(ctx, tplConn, grapeUPassword, testUPassword); err != nil {
		return err
	}
	if _, err := admin.Exec(ctx, "ALTER DATABASE "+TestTemplateDatabase+" WITH is_template TRUE"); err != nil {
		return fmt.Errorf("deploy: mark template: %w", err)
	}
	if _, err := admin.Exec(ctx, "COMMENT ON DATABASE "+TestTemplateDatabase+" IS '"+manifestHashCommentPrefix+want+"'"); err != nil {
		return fmt.Errorf("deploy: record manifest fingerprint: %w", err)
	}
	return nil
}

// CloneTestDatabase 从模板库克隆全新测试库并对运行角色授权 CONNECT，返回
// 库名。库名由 testName 确定性派生（DatabaseNameFor）：跨测试互异、可追
// 溯，DROP IF EXISTS 同时兜住理论碰撞与上轮运行的遗留同名库；调用方结束
// 后经 DropTestDatabase 删除。
func CloneTestDatabase(ctx context.Context, adminDSN, grapeUPassword, testUPassword, testName string) (string, error) {
	name := DatabaseNameFor(testName)
	if err := EnsureTestTemplate(ctx, adminDSN, grapeUPassword, testUPassword); err != nil {
		return "", err
	}
	admin, err := connectAdmin(ctx, adminDSN, "postgres")
	if err != nil {
		return "", err
	}
	defer func() { _ = admin.Close(ctx) }()

	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
		return "", fmt.Errorf("deploy: drop test database: %w", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name+" TEMPLATE "+TestTemplateDatabase); err != nil {
		return "", fmt.Errorf("deploy: clone test database: %w", err)
	}
	// CONNECT 授权依赖库名，由克隆方执行（role.sql 同此约定）。
	if _, err := admin.Exec(ctx, "GRANT CONNECT ON DATABASE "+name+" TO "+testRunRoles); err != nil {
		return "", fmt.Errorf("deploy: grant connect: %w", err)
	}
	return name, nil
}

// DropTestDatabase 删除指定测试库：WITH (FORCE) 允许仍有连接时强删，
// 清理函数无须先等连接池归零。
func DropTestDatabase(ctx context.Context, adminDSN, dbName string) error {
	admin, err := connectAdmin(ctx, adminDSN, "postgres")
	if err != nil {
		return err
	}
	defer func() { _ = admin.Close(ctx) }()
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+dbName+" WITH (FORCE)"); err != nil {
		return fmt.Errorf("deploy: drop test database %s: %w", dbName, err)
	}
	return nil
}

// RewriteDSNDatabase 把连接串的目标库改写为 dbName，其余参数原样保留：
// 支持 postgres:// URL 与关键字/值两种形态（集成测试 DSN 指向固定库名，
// 指向派生库名的测试池须经此改写）。
func RewriteDSNDatabase(dsn, dbName string) (string, error) {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return "", fmt.Errorf("deploy: empty dsn")
	}
	if u, err := url.Parse(dsn); err == nil &&
		(u.Scheme == "postgres" || u.Scheme == "postgresql" || u.Scheme == "pgsql") {
		u.Path = "/" + dbName
		return u.String(), nil
	}
	// 关键字/值形态：替换既有 dbname，缺失则追加。
	kvDBName := regexp.MustCompile(`(?i)(^|\s)dbname=([^\s]+)`)
	if kvDBName.MatchString(dsn) {
		return kvDBName.ReplaceAllString(dsn, "${1}dbname="+dbName), nil
	}
	return dsn + " dbname=" + dbName, nil
}

// connectAdmin 以管理员 DSN 连接指定库：模板维护与克隆均在 postgres 库
// 发起，模板装载连接目标模板库。
func connectAdmin(ctx context.Context, adminDSN, database string) (*pgx.Conn, error) {
	cfg, err := pgx.ParseConfig(adminDSN)
	if err != nil {
		return nil, fmt.Errorf("deploy: parse admin dsn: %w", err)
	}
	cfg.Database = database
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("deploy: connect admin: %w", err)
	}
	return conn, nil
}

// templateUpToDate 报告模板库存在、已置 is_template 且指纹与 want 一致。
func templateUpToDate(ctx context.Context, admin *pgx.Conn, want string) bool {
	var isTemplate bool
	var comment *string
	err := admin.QueryRow(ctx,
		// 数据库注释在共享目录 pg_shdescription，须经 shobj_description 读取
		`SELECT datistemplate, shobj_description(oid, 'pg_database')
		   FROM pg_database WHERE datname = $1`, TestTemplateDatabase).
		Scan(&isTemplate, &comment)
	if err != nil {
		return false
	}
	if !isTemplate || comment == nil {
		return false
	}
	hash, ok := strings.CutPrefix(*comment, manifestHashCommentPrefix)
	return ok && hash == want
}

// dropStaleTestDatabases 删除按前缀识别的遗留测试库：模板失效重建意味着
// 清单已变化，旧模板克隆出的测试库全部失效。前提是同一集群不并发运行
// 不同代码版本的测试套件（先克隆旧模板、后撞上重建会被误删）——开发与
// CI 均满足；命中中的运行遗留库（进程被杀未及清理）由此一并回收。
func dropStaleTestDatabases(ctx context.Context, admin *pgx.Conn) error {
	rows, err := admin.Query(ctx,
		`SELECT datname FROM pg_database WHERE datname LIKE $1`,
		TestDatabasePrefix+`\_%`)
	if err != nil {
		return fmt.Errorf("deploy: list stale test databases: %w", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return fmt.Errorf("deploy: scan stale test database: %w", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("deploy: list stale test databases: %w", err)
	}
	for _, name := range names {
		if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
			return fmt.Errorf("deploy: drop stale test database %s: %w", name, err)
		}
	}
	return nil
}

// manifestFingerprint 计算测试部署清单的失效指纹：逐文件（文件名 + 内容）
// 按清单顺序进 sha256，尾部混入 PG 版本——扩展与版本相关的 DDL 结果随
// version 变化。
func manifestFingerprint(pgVersion string) (string, error) {
	dir, err := DeployDir()
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, f := range TestDeployFiles() {
		content, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			return "", fmt.Errorf("deploy: read %s: %w", f, err)
		}
		h.Write([]byte(f))
		h.Write([]byte{0x1f})
		h.Write(content)
		h.Write([]byte{0x1e})
	}
	h.Write([]byte{0x1f})
	h.Write([]byte(pgVersion))
	return hex.EncodeToString(h.Sum(nil)), nil
}

// runTestManifest 在目标库按序执行测试部署清单（TestDeployFiles，含测试
// 种子）：口令占位符装载前替换。与 dbdeploy 共享清单与校验，执行环境为
// 管理员连接（RLS 已启用，超级用户绕过）。
func runTestManifest(ctx context.Context, conn *pgx.Conn, grapeUPassword, testUPassword string) error {
	dir, err := DeployDir()
	if err != nil {
		return err
	}
	if err := ValidateDeployFiles(dir); err != nil {
		return err
	}
	for _, f := range TestDeployFiles() {
		sql, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			return fmt.Errorf("deploy: read %s: %w", f, err)
		}
		deployed := ResolveDeployPasswords(string(sql), grapeUPassword, testUPassword)
		if _, err := conn.Exec(ctx, deployed); err != nil {
			return fmt.Errorf("deploy: exec %s: %w", f, err)
		}
	}
	return nil
}

// DatabaseNameFor 由测试名确定性派生测试库名：grape_t_ 前缀 + 测试名
// sha256 的 12 位十六进制。库标识符字符集天然安全；setup 与后续直连该库
// 做管理面维护的用例取同一函数即可复现库名，无需注册表。并行测试名互异
// 保证库名互异。
func DatabaseNameFor(testName string) string {
	h := sha256.Sum256([]byte(testName))
	return TestDatabasePrefix + hex.EncodeToString(h[:])[:12]
}
