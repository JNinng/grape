# seed — 生产默认种子包

平台树根租户 `t-super-01` 的自举数据，按表拆分为独立 SQL 文件。`seed.go`
的 `Files` 声明相对 `../`（`sqlc/`）目录的执行顺序，`deploy.DeployFiles`
直接引用该清单，dbdeploy 与集成测试共用同一事实源。

## 文件导览

| 文件 | 表 | 内容与依赖 |
| --- | --- | --- |
| [010_sys_tenant.sql](010_sys_tenant.sql) | sys_tenant | 树根租户；首个执行 |
| [020_sys_role.sql](020_sys_role.sql) | sys_role | 超级管理员角色 101（全数据范围） |
| [030_sys_user.sql](030_sys_user.sql) | sys_user | admin 账号 |
| [040_sys_user_role_mapping.sql](040_sys_user_role_mapping.sql) | sys_user_role_mapping | admin → 角色 101 |
| [050_sys_menu.sql](050_sys_menu.sql) | sys_menu | 后台管理菜单树与操作权限点（入口层码表与前端显隐的生产事实源） |
| [060_sys_role_menu.sql](060_sys_role_menu.sql) | sys_role_menu | 角色 101 的全量菜单授权 |
| [070_sys_config.sql](070_sys_config.sql) | sys_config | 系统全局配置（无租户边界） |
| [080_sys_tenant_config.sql](080_sys_tenant_config.sql) | sys_tenant_config | 树根租户配置 |
| [090_sys_feature_flag.sql](090_sys_feature_flag.sql) | sys_feature_flag | 树根租户功能开关 |
| [100_sys_translation.sql](100_sys_translation.sql) | sys_translation | 菜单/配置名称的 en-US 翻译 |

## 执行约束

- 依赖 `schema/` 全部文件 → `rls.sql` → `expose.sql` → `operate.sql` 之后
  执行；`expose.sql` 必须先于本包，角色映射写入要触发范围版本 bump。
- RLS 已启用，必须以**超级用户/表 owner** 连接执行（超级用户绕过 RLS）。
- 所有语句带 `ON CONFLICT DO NOTHING`：可重复执行，且不覆盖已有行。
- 字典不播种：数据状态/数据范围/角色类型等系统级枚举由列注释 `@options`
  声明，字典表仅承载租户运营的分类数据；测试用演示字典在 `../test_seed.sql`。
- 只含生产自举数据。集成测试使用的 `t-test-*` 租户与演示数据在
  `../test_seed.sql`，生产部署（dbdeploy）不执行。
