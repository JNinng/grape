// Package deploy 是 pg_sqlc 声明资产（../sqlc/）的部署面：执行清单、口令
// 占位符替换、跨进程部署锁与清单校验。dbdeploy 命令与各包 PG 集成测试
// 共用本包作为部署的单一事实源；不依赖运行期存储门面（*pg_sqlc.Data）。
package deploy

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

// DeployFiles 是生产数据库部署清单：按序执行的 SQL 文件，相对 sqlc/ 目录。
// dbdeploy 命令执行本清单；集成测试经 TestDeployFiles() 在本清单之后追加
// test_seed.sql。顺序与仓库根 deploy_db.md 的「执行顺序」表互为镜像，
// 两处禁止另行维护副本；各步骤的依赖关系、执行者身份与幂等边界以该表为准
// （要点：role.sql 先于 rls/operate/expose——函数授权依赖角色先行；
// rls.sql → expose.sql → operate.sql：LANGUAGE sql 函数体创建期解析，
// operate 的 fn_can_* 引用 rls 的 fn_accessible_* 与 expose 的
// fn_has_permission，引用必须后于定义；
// seed.sql 在 RLS 启用后以超级用户/表 owner 执行；agent 域尾部追加，
// 只依赖 role.sql 的角色存在）。
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
	"expose.sql",
	"operate.sql",
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
	// 菜单受控写面（D2）：sys_menu 的 SECURITY
	// DEFINER 固定口径写函数（fn_menu_create/update/move/delete——建/
	// 改/移/软删，租户边界复位、父级校验、防环与软删口径收在函数内）；
	// 入 sqlc catalog（函数签名，命名查询见 queries/menu.sql），尾部
	// 独立追加不改既有顺序，只依赖 schema/40_rbac.sql 的 sys_menu 存在。
	// grape_u 对 sys_menu 的直写 REVOKE 在 role.sql，本文件不动授权：
	// EXECUTE 沿用 PostgreSQL 默认授权 PUBLIC（fn_cleanup_expired_sessions
	// 同款）。
	"98_menu_write.sql",
	// 全局配置受控写面（D2）：sys_config 的 SECURITY DEFINER
	// 固定口径写函数（fn_config_create/update/delete——建/改/软删，
	// 认证事务纪律校验与软删口径收在函数内）；入 sqlc catalog（函数
	// 签名，命名查询见 queries/sys_config.sql），尾部独立追加不改既有
	// 顺序，只依赖 schema/90_config.sql 的 sys_config 存在。grape_u 对
	// sys_config 的直写 REVOKE 在 role.sql，本文件不动授权：EXECUTE
	// 沿用 PostgreSQL 默认授权 PUBLIC（fn_menu_* 同款）。
	"98_config_write.sql",
	// 租户删除前置完整性判定（受控读取面）：sys_tenant 的
	// SECURITY DEFINER 计数函数 fn_tenant_active_child_count——租户树
	// RLS 下祖父看不到孙行，删除前置校验需要全局事实；函数只返回计数、
	// 不返回行数据（不构成跨租户数据读面），函数内校验 app.tenant_id
	// 已设置（未设置即拒绝）。入 sqlc catalog（函数签名，命名查询见
	// queries/sys_tenant.sql），尾部独立追加不改既有顺序，只依赖
	// schema/20_tenant.sql 的 sys_tenant 存在；EXECUTE 沿用 PostgreSQL
	// 默认授权 PUBLIC（fn_menu_* / fn_cleanup_expired_sessions 同款）。
	"98_tenant_guard.sql",
	// 管理面功能权限码种子：sys_menu 操作权限点
	//（246-249 用户/用户组/角色管理补充操作码、254-256 会话治理权限点、
	// 257 通用翻译页面、258-265 配置族全局/租户操作码、266-269 功能开关
	// 操作码、270-273 翻译操作码）与超级管理员（101）的角色-菜单
	// 映射；不入 sqlc catalog，尾部独立
	// 追加不改既有顺序，只依赖 seed.sql 的租户/角色/既有菜单树存在。
	// 全量 ON CONFLICT DO NOTHING（seed.sql 同款幂等口径），超级用户/
	// 表 owner 执行。
	"99_perm_seed.sql",
}

// TestSeedFiles 是测试专用种子清单：在 DeployFiles 全清单之后追加执行，
// 仅由集成测试装载（setupTestDB 与 testsupport 套件部署，经 TestDeployFiles），
// dbdeploy 生产部署不包含。文件依赖 seed.sql 的树根租户 t-super-01 存在；
// 除平台租户用户 12（superplain）外，内容属测试租户（t-test-*），与生产
// 默认数据分离。
var TestSeedFiles = []string{
	"test_seed.sql",
}

// TestDeployFiles 返回集成测试用部署清单：生产清单 + 测试种子。
// 返回新切片，不修改 DeployFiles。
func TestDeployFiles() []string {
	files := make([]string, 0, len(DeployFiles)+len(TestSeedFiles))
	files = append(files, DeployFiles...)
	files = append(files, TestSeedFiles...)
	return files
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

// DeployDir 返回 pg_sqlc/sqlc/ 目录在当前文件系统中的路径，以本文件源码
// 位置（pg_sqlc/deploy/）的上一级定位，与进程工作目录无关：包内测试（go test）与从仓库根运行的
// dbdeploy 命令共用同一解析。部署依赖源码树中的 SQL 文件存在。
func DeployDir() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("deploy: cannot locate deploy.go source file")
	}
	dir := filepath.Join(filepath.Dir(file), "..", "sqlc")
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
