// Package seed 是生产默认种子的声明包：数据按表拆分到独立 SQL 文件，
// Files 给出相对 sqlc/ 目录的执行顺序。
//
// 依赖：schema/ → rls.sql → expose.sql → operate.sql 之后执行；RLS 已启用，
// 需以超级用户/表 owner 执行。所有语句带 ON CONFLICT DO NOTHING，可重复执行。
//
// 仅含平台树根租户 t-super-01 的自举数据；集成测试演示数据由 test_seed.sql
// 承载，不在生产部署清单内。
package seed

// Files 是生产种子文件清单（相对 sqlc/ 目录），按表依赖顺序执行：
// 租户 → 角色 → 用户 → 用户角色映射 → 菜单 → 角色菜单映射 → 配置族 → 翻译。
// 新增表级种子时在对应位置插入文件，并同步 deploy_db.md 与 sqlc/README.md
// 的部署顺序描述。
var Files = []string{
	"seed/010_sys_tenant.sql",
	"seed/020_sys_role.sql",
	"seed/030_sys_user.sql",
	"seed/040_sys_user_role_mapping.sql",
	"seed/050_sys_menu.sql",
	"seed/060_sys_role_menu.sql",
	"seed/070_sys_config.sql",
	"seed/080_sys_tenant_config.sql",
	"seed/090_sys_feature_flag.sql",
	"seed/100_sys_translation.sql",
}
