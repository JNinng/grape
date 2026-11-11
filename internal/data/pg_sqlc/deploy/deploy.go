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

	"grape/internal/data/pg_sqlc/sqlc/seed"
)

// DeployFiles 是生产数据库部署清单：按序执行的 SQL 文件，相对 sqlc/ 目录，
// 是部署顺序的单一事实源；dbdeploy 执行本清单，集成测试经 TestDeployFiles
// 在其后追加测试种子。仓库根 deploy_db.md 的「执行顺序」表是本清单的镜像，
// 两处禁止另行维护副本。
var DeployFiles = deployFiles()

// deployFiles 按序拼装清单：schema → 权限函数与角色 → 生产种子包 →
// agent 域尾部追加。种子文件顺序只在 seed.Files 维护，避免两处副本。
func deployFiles() []string {
	files := []string{
		"schema/10_basic.sql",
		"schema/20_tenant.sql",
		"schema/30_user.sql",
		"schema/40_rbac.sql",
		"schema/50_dept.sql",
		"schema/60_dict.sql",
		"schema/70_audit.sql",
		"schema/80_session.sql",
		"schema/90_config.sql",
		"schema/91_knowledge.sql",
		"schema/92_model.sql",
		"role.sql",
		"rls.sql",
		"expose.sql",
		"operate.sql",
	}
	files = append(files, seed.Files...)
	return append(files,
		// 内嵌 OSS 数据面：oss_object 单表（bucket + object_key 主键），
		// 服务内嵌 gofakes3 的数据介质。设施面无 RLS（bucket 即命名空间，
		// SigV4 签名自证身份），入 sqlc catalog（queries/oss_object.sql）；
		// grape_u 的 DML 授权在文件内显式声明，依赖 role.sql 的角色
		// 先行存在。
		"schema/93_oss.sql",
		// Agent 会话存储：CREATE SCHEMA agent + 框架
		// session/postgres 六张 agent_* 表与索引 + grape_u 授权；超级用户
		// 执行面，不入 sqlc catalog，升级以框架建表模板 diff 该文件。
		"agent_session.sql",
		// 向量设施：pgvector 扩展 + agent.agent_document
		// 与 HNSW 索引 + 收窄到该表的授权；超级用户执行面，不入 sqlc catalog。
		"agent_vector.sql",
		// Agent 业务审计：两表、索引与租户 RLS；入 sqlc catalog
		// （queries/agent_audit.sql），grape_u 的 DML 授权由 role.sql
		// 默认权限覆盖。
		"schema/95_agent_audit.sql",
		// Agent 工具授权：表 + 租户 RLS + 运行角色授权；入 sqlc catalog
		// （queries/agent_grant.sql），策略随域文件走，只依赖 role.sql
		// 的角色存在。
		"96_agent_grant.sql",
		// Agent 检索留痕：表 + 租户 RLS；入 sqlc catalog
		// （queries/agent_retrieval_log.sql），策略随域文件走，
		// grape_u 的 DML 授权由 role.sql 默认权限覆盖。
		"schema/97_agent_retrieval_log.sql",
		// 菜单受控写面：sys_menu 的写函数（建/改/移/软删），租户复位、
		// 父级校验、防环与软删口径收在 SECURITY DEFINER 函数内；
		// 入 sqlc catalog，只依赖 schema/40_rbac.sql。
		"98_menu_write.sql",
		// 全局配置受控写面：sys_config 的写函数（建/改/软删），
		// 认证事务纪律与软删口径收在 SECURITY DEFINER 函数内；
		// 入 sqlc catalog，只依赖 schema/90_config.sql。
		"98_config_write.sql",
		// 模型目录平台行受控写面：sys_model 平台行（tenant_id=''）的建/改/
		// 软删，函数内固定空串租户并校验认证事务纪律；入 sqlc catalog，
		// 只依赖 schema/92_model.sql。
		"98_model_write.sql",
		// 租户删除前置计数：SECURITY DEFINER 函数只返回未删除直接子租户
		// 数（不返回行数据，不构成跨租户读面）；入 sqlc catalog，
		// 只依赖 schema/20_tenant.sql。
		"98_tenant_guard.sql",
		// Agent 模型调用留痕：管线侧模型/类模型调用面（文本生成/向量化/
		// 外部解析）的证据表 + 索引 + 租户 RLS；入 sqlc catalog
		// （queries/agent_model_call_log.sql），策略随域文件走，
		// grape_u 的 DML 授权由 role.sql 默认权限覆盖。
		"schema/99_model_call_log.sql",
	)
}

// TestSeedFiles 是测试专用种子清单：在 DeployFiles 全清单之后追加执行，
// 仅由集成测试装载（经 TestDeployFiles），dbdeploy 生产部署不包含。文件
// 依赖 seed 包的树根租户 t-super-01 存在；内容属测试租户，与生产默认
// 数据分离。
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
// 授权是集群级目录操作，并发执行会触发 tuple concurrently updated。锁文件
// 落在系统临时目录；持锁超过 staleLimit 视为持有者已崩溃，允许接管。
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
