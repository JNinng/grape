package pg_sqlc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// DeployFiles 是数据库部署清单：按序执行的 SQL 文件，相对 sqlc/ 目录。
// 这是 dbdeploy 命令与集成测试（setupTestDB）共用的单一事实源，
// 顺序与仓库根 deploy_db.md 一致，两处禁止另行维护副本：
//
//	schema/10..90 → role.sql → rls.sql → operate.sql → expose.sql →
//	seed.sql → agent_session.sql → agent_vector.sql →
//	schema/95_agent_audit.sql → 96_agent_grant.sql →
//	schema/97_agent_retrieval_log.sql
//
// role.sql 建角色与授权（口令占位符见下方占位符常量），必须先于
// rls.sql：后续 operate/expose 的函数授权语句依赖角色先行存在；
// seed.sql 依赖 expose.sql 的范围版本表（角色映射写入要触发 bump）；
// agent_session.sql 建 Agent 会话存储（trpc-agent-go 框架表，独立
// schema agent），只依赖 role.sql 的 grape_u 存在（GRANT 授权对象），
// 与 public 业务域零耦合；agent_vector.sql（向量设施，同超级用户面）
// 与其互不依赖，两文件均幂等建 schema、尾部追加互不扰动；
// schema/95_agent_audit.sql 建 Agent 业务审计两表（public + RLS），
// 96_agent_grant.sql 建工具授权表（public + RLS）：两者均只依赖
// role.sql 的角色存在，尾部独立追加互不扰动；public 表的 DML 授权由
// role.sql 默认权限覆盖（同角色于 public 建表自动生效）。
// schema/97_agent_retrieval_log.sql 建检索留痕表（public + RLS）：
// 同 95/96 口径的尾部追加域，只依赖 role.sql 的角色存在，与前列零耦合。
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
	// Agent 会话存储：CREATE SCHEMA agent + 框架
	// session/postgres 六张 agent_* 表与索引 + grape_u 授权；超级用户
	// 执行面，不入 sqlc catalog，升级以框架建表模板 diff 该文件。
	"agent_session.sql",
	// 向量设施：pgvector 扩展 + agent.agent_document
	// 与 HNSW 索引 + 收窄到该表的授权；超级用户执行面，不入 sqlc catalog。
	"agent_vector.sql",
	// Agent 业务审计：public.agent_tool_call_log 与
	// public.agent_confirmation_log 两表、索引与租户 RLS；sqlc catalog
	// 输入（命名查询见 queries/agent_audit.sql），尾部独立追加不改
	// 既有顺序，grape_u 的 DML 授权由 role.sql 默认权限覆盖。
	"schema/95_agent_audit.sql",
	// Agent 工具授权：public.agent_tool_grant 表 + 租户
	// RLS 策略 + 运行角色授权；入 sqlc catalog（命名查询见
	// queries/agent_grant.sql），策略随域文件走（口径同 expose.sql 的
	// sys_perm_version），只依赖 role.sql 的角色存在。
	"96_agent_grant.sql",
	// Agent 检索留痕：public.agent_retrieval_log 表 +
	// 租户 RLS 策略；入 sqlc catalog（命名查询见
	// queries/agent_retrieval_log.sql），策略随域文件走（95 同口径），
	// 只依赖 role.sql 的角色存在，grape_u 的 DML 授权由 role.sql 默认
	// 权限覆盖（文件内不含授权语句）。
	"schema/97_agent_retrieval_log.sql",
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

// AcquireDeployLock 串行化跨包、跨进程的清单部署：role.sql 的角色与参数
// 授权是集群级目录操作，多个测试包（pg_sqlc / dbdeploy / agent 会话面）
// 或多个 dbdeploy 进程并发执行时会在共享目录上触发 tuple concurrently
// updated（SQLSTATE XX000）。锁文件落在系统临时目录；持锁超过 staleLimit
// 视为持有者已崩溃，允许接管，避免测试崩溃残留死锁。
func AcquireDeployLock(ctx context.Context) (func(), error) {
	const staleLimit = 10 * time.Minute
	path := filepath.Join(os.TempDir(), "grape-sqlc-deploy.lock")
	deadline := time.Now().Add(time.Minute)
	for {
		if fi, err := os.Stat(path); err == nil && time.Since(fi.ModTime()) > staleLimit {
			_ = os.Remove(path)
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			return func() { _ = f.Close(); _ = os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("deploy: create lock: %w", err)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("deploy: acquire lock timeout waiting for %s", path)
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("deploy: acquire lock: %w", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
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
