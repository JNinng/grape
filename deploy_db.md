# 数据库部署

grape 关系存储（PostgreSQL）的部署：按序执行
`internal/data/pg_sqlc/sqlc/` 下的 38 个 SQL 文件，最后补一次连接授权。
执行顺序的单一事实源是
[deploy.go](internal/data/pg_sqlc/deploy/deploy.go) 的 `DeployFiles` 清单
（dbdeploy 执行生产清单；集成测试经 `TestDeployFiles()` 追加测试种子），
本文表格是其镜像。SQL 对象索引见
[sqlc/README.md](internal/data/pg_sqlc/sqlc/README.md)；生产运行口径
（多实例粘性、优雅停机、密钥管理、框架升级）见 [operations.md](operations.md)。

## 一键部署（推荐）

```bash
export GRAPE_DEPLOY_ADMIN_DSN=postgres://<user>:<pass>@127.0.0.1:5432/postgres
export GRAPE_DEPLOY_GRAPE_U_PASSWORD=<grape_u 口令>
export GRAPE_DEPLOY_TEST_U_PASSWORD=<test_u 口令>
make dbdeploy ARGS="--database <库名>"
```

连接 `postgres` 库幂等创建目标库（已存在沿用）→ 连接目标库 → **单事务内**
按清单顺序执行全部文件（任一失败整体回滚）→ 事务内补 `GRANT CONNECT`。
`role.sql` 的角色口令以 `{{GRAPE_U_PASSWORD}}` / `{{TEST_U_PASSWORD}}` 占位，
dbdeploy 执行前在内存中一次性替换，仓库与日志不落明文。

幂等边界：角色（判存）与种子（`ON CONFLICT DO NOTHING`）可重复；结构 DDL 为
裸创建，对已部署库重复执行会报错退出——全量重建先 `DROP DATABASE` 再执行，
本命令不宣称全量幂等。

## 执行顺序

按序执行 27 个步骤、38 个文件，顺序不能变（步骤 16 的种子包按
`seed.Files` 逐文件执行）。`queries/` 是 sqlc 的 Go 生成输入，不执行。

| 步骤 | 文件 | 作用 | 关键约束 |
| --- | --- | --- | --- |
| 1 | schema/10_basic.sql | basic 表范式（归属三元组 + `@scope`） | 1–11 无外键依赖，按 10→92 执行 |
| 2 | schema/20_tenant.sql | sys_tenant 租户表（RLS 允许命中自身 id） | 同上 |
| 3 | schema/30_user.sql | sys_user、用户-角色/用户组映射、用户组 | 同上 |
| 4 | schema/40_rbac.sql | 角色、范围明细、分发部门、菜单、角色-菜单映射 | 同上 |
| 5 | schema/50_dept.sql | 组织树、闭包表与闭包触发器 | 同上 |
| 6 | schema/60_dict.sql | 字典类型与数据 | 同上 |
| 7 | schema/70_audit.sql | 登录/操作日志（append-only） | 同上 |
| 8 | schema/80_session.sql | 会话/令牌（仅存哈希） | 同上 |
| 9 | schema/90_config.sql | 全局/租户配置、功能开关、翻译 | 同上 |
| 10 | schema/91_knowledge.sql | kb_library 知识库与 kb_document 条目 | 同上；租户 RLS 在 rls.sql 建策略 |
| 11 | schema/92_model.sql | sys_model 模型目录（平台/租户双态） | 同上；平台行只能经受控函数写 |
| 12 | role.sql | grape_u / test_u、schema 使用权、DML 授权、GRANT SET | 必须先于 13–15（函数授权依赖角色） |
| 13 | rls.sql | RLS 策略（含 kb_* 与 sys_model 双策略）、`fn_accessible_*` 三件套、updated_at 触发器 | 依赖 1–11 的表 |
| 14 | expose.sql | 只读判定、范围版本表与 bump 触发器 | 依赖 13；先于 15（函数体引用）与 16（种子 bump） |
| 15 | operate.sql | `fn_can_*` 与角色判定族 | 依赖 13/14 的函数；`fn_visible_role_ids` 依赖同文件 `fn_can_see_role` |
| 16 | [seed/](internal/data/pg_sqlc/sqlc/seed/) | 根租户自举 12 个文件，顺序见 [seed.go](internal/data/pg_sqlc/sqlc/seed/seed.go) | RLS 已启用，须以超级用户/表 owner 执行 |
| 17 | schema/93_oss.sql | oss_object 对象表（内嵌 gofakes3 数据介质，设施面无 RLS） | 尾部追加；依赖 12 的 grape_u（DML 授权文件内显式声明） |
| 18 | agent_session.sql | 框架 session/postgres 六张 `agent_*` 表 | 只依赖 12 的 grape_u；尾部追加不改序 |
| 19 | agent_vector.sql | pgvector 扩展与 `agent_document` 多空间向量表 | 后于 12；超级用户执行；无 RLS，应用层隔离 |
| 20 | schema/95_agent_audit.sql | Agent 审计两表、索引与租户 RLS | 尾部追加；依赖 12；授权由 role.sql 默认权限覆盖 |
| 21 | 96_agent_grant.sql | 工具授权表、未撤销唯一约束与租户 RLS | 后于 12；超级用户执行；策略随域文件 |
| 22 | schema/97_agent_retrieval_log.sql | 检索留痕表、索引与租户 RLS | 同 20 追加口径 |
| 23 | 98_menu_write.sql | 四个受控写函数 | 尾部追加；依赖 4；EXECUTE 默认 PUBLIC |
| 24 | 98_config_write.sql | 三个受控写函数 | 尾部；依赖 9；EXECUTE 默认 PUBLIC |
| 25 | 98_model_write.sql | 平台模型建/改/软删三个受控写函数 | 尾部；依赖 11；EXECUTE 默认 PUBLIC |
| 26 | 98_tenant_guard.sql | 租户删除前置计数函数 | 尾部；依赖 2；`CREATE OR REPLACE` 可重复 |
| 27 | schema/99_model_call_log.sql | 模型调用留痕表、索引与租户 RLS | 同 20 追加口径 |

手工执行按上表顺序；`role.sql` 的口令占位符需先替换，最后补
`GRANT CONNECT`（见下节）。

集成测试种子 [test_seed.sql](internal/data/pg_sqlc/sqlc/test_seed.sql)
（`t-test-*` 租户及演示数据）不在生产清单内：仅由 `TestDeployFiles()` 在
生产清单后追加，依赖种子包 `010_sys_tenant.sql` 的树根租户。开发/演示库
如需数据，在生产清单后以超级用户单独执行；该文件复用固定主键，仅适用于
全新建库，历史残留库会被文件头守卫拒绝，重复执行会重复追加日志。

## 幂等性与全量重建

`role.sql`（角色判存 + 授权）与种子包（`ON CONFLICT DO NOTHING`，不覆盖
已有行）可重复执行；其余文件不保证可重复：步骤 1–11 的 `CREATE TABLE` /
`CREATE INDEX`、`rls.sql` 的 `CREATE POLICY`、rls/expose 的
`CREATE TRIGGER` 均为裸创建，重复执行报错。agent 域两个文件的
`CREATE SCHEMA IF NOT EXISTS` 与扩展安装幂等，但文件内建表/建索引仍为裸
创建；函数为 `CREATE OR REPLACE` 不足以让文件整体可重复。全量重建先
`DROP DATABASE`，再从步骤 1 开始。

## 部署后授权

dbdeploy 在部署事务内自动补齐；手工部署时以超级用户执行一次：

```sql
GRANT CONNECT ON DATABASE <库名> TO grape_u, test_u;
```

`grape_u` 供应用运行连接，`test_u` 供演示/验收。CONNECT 依赖库名，无法写进
`role.sql`。此后应用 DSN 以 `grape_u` 连接，勿用超级用户（会绕过 RLS）。

## 种子口令

种子用户口令统一为 123456，scrypt 格式 `{scrypt}$e0801$…`
（N=2^14, r=8, p=1）。登录校验识别该格式，通过后由应用透明重哈希为
argon2id，无需批量重置。
