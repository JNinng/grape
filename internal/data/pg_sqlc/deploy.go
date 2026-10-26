package pg_sqlc

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// DeployFiles 是数据库部署清单：按序执行的 SQL 文件，相对 sqlc/ 目录。
// 这是 dbdeploy 命令与集成测试（setupTestDB）共用的单一事实源，
// 顺序与仓库根 deploy_db.md 一致，两处禁止另行维护副本：
//
//	schema/10..90 → role.sql → rls.sql → operate.sql → expose.sql → seed.sql
//
// role.sql 建角色与授权（口令占位符见下方占位符常量），必须先于
// rls.sql：后续 operate/expose 的函数授权语句依赖角色先行存在；
// seed.sql 依赖 expose.sql 的范围版本表（角色映射写入要触发 bump）。
var DeployFiles = []string{
	"schema/10_basic.sql",
	"schema/20_tenant.sql",
	"schema/30_user.sql",
	"schema/40_rbac.sql",
	"schema/50_dept.sql",
	"schema/60_dict.sql",
	"schema/70_audit.sql",
	"schema/80_session.sql",
	"schema/90_config.sql",
	"role.sql",
	"rls.sql",
	"operate.sql",
	"expose.sql",
	"seed.sql",
}

// role.sql 中的口令占位符：部署方（dbdeploy）在执行前对文件内容做
// 一次性替换，仓库内的 SQL 与日志不落明文。占位符声明只存在于
// role.sql，此处是 Go 侧的对应引用。
const (
	GrapeUPasswordPlaceholder = "{{GRAPE_U_PASSWORD}}"
	TestUPasswordPlaceholder  = "{{TEST_U_PASSWORD}}"
)

// residualPlaceholder 在替换后的 SQL 中检测残留占位符，防止把字面
// 占位符发给数据库（CREATE USER 会把 {{...}} 当成口令）。
var residualPlaceholder = regexp.MustCompile(`\{\{[A-Z_]+\}\}`)

// ResolveDeployPasswords 用真实口令替换 SQL 文本中的口令占位符：
// 口令内的单引号按 SQL 字面量规则转义为两个单引号。只在内存中替换，
// 不写回任何文件。
func ResolveDeployPasswords(sql, grapeUPassword, testUPassword string) string {
	sql = replacePlaceholder(sql, GrapeUPasswordPlaceholder, grapeUPassword)
	sql = replacePlaceholder(sql, TestUPasswordPlaceholder, testUPassword)
	return sql
}

func replacePlaceholder(sql, placeholder, password string) string {
	// 单引号转义防注入：口令出现在 CREATE USER ... PASSWORD '<字面量>' 中
	escaped := strings.ReplaceAll(password, "'", "''")
	return strings.ReplaceAll(sql, placeholder, escaped)
}

// HasResidualPlaceholder 报告 SQL 文本是否残留未替换的占位符。
func HasResidualPlaceholder(sql string) bool {
	return residualPlaceholder.MatchString(sql)
}

// DeployDir 返回 sqlc/ 目录在当前文件系统中的路径，以本文件源码位置
// 定位，与进程工作目录无关：包内测试（go test）与从仓库根运行的
// dbdeploy 命令共用同一解析。部署依赖源码树中的 SQL 文件存在。
func DeployDir() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("deploy: cannot locate deploy.go source file")
	}
	dir := filepath.Join(filepath.Dir(file), "sqlc")
	info, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("deploy: sqlc dir not found: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("deploy: %s is not a directory", dir)
	}
	return dir, nil
}

// deployForbiddenPattern 匹配破坏单事务部署前提的语句：CREATE DATABASE
// 不能在事务块内执行；CONCURRENTLY 建索引/触发器不能在事务块内执行。
// 清单文件出现任一即拒绝部署（校验同时作为单测断言与运行时防线）。
var deployForbiddenPattern = regexp.MustCompile(`(?mi)\bCREATE\s+DATABASE\b|\bCONCURRENTLY\b`)

// ValidateDeployFiles 校验清单完整性：每个文件存在于 dir 下，且不含
// 破坏单事务执行前提的语句（CREATE DATABASE / CONCURRENTLY）。
// dbdeploy 执行前调用一次，返回首个失败的文件即退出。
func ValidateDeployFiles(dir string) error {
	for _, name := range DeployFiles {
		content, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return fmt.Errorf("deploy: read %s: %w", name, err)
		}
		if loc := deployForbiddenPattern.FindIndex(content); loc != nil {
			line := 1 + strings.Count(string(content[:loc[0]]), "\n")
			return fmt.Errorf("deploy: %s:%d contains a transaction-breaking statement (CREATE DATABASE / CONCURRENTLY)", name, line)
		}
	}
	return nil
}
