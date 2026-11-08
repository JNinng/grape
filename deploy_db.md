# 数据库部署

grape 关系存储（PostgreSQL）的部署：按序执行
`internal/data/pg_sqlc/sqlc/` 下的 36 个 SQL 文件，最后补一次连接授权。
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

按序执行 25 个步骤、36 个文件，顺序不能变（步骤 16 的种子包按
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
| 17 | agent_session.sql | 框架 session/postgres 六张 `agent_*` 表 | 只依赖 12 的 grape_u；尾部追加不改序 |
| 18 | agent_vector.sql | pgvector 扩展与 `agent_document` 多空间向量表 | 后于 12；超级用户执行；无 RLS，应用层隔离 |
| 19 | schema/95_agent_audit.sql | Agent 审计两表、索引与租户 RLS | 尾部追加；依赖 12；授权由 role.sql 默认权限覆盖 |
| 20 | 96_agent_grant.sql | 工具授权表、未撤销唯一约束与租户 RLS | 后于 12；超级用户执行；策略随域文件 |
| 21 | schema/97_agent_retrieval_log.sql | 检索留痕表、索引与租户 RLS | 同 19 追加口径 |
| 22 | 98_menu_write.sql | 四个受控写函数 | 尾部追加；依赖 4；EXECUTE 默认 PUBLIC |
| 23 | 98_config_write.sql | 三个受控写函数 | 尾部；依赖 9；EXECUTE 默认 PUBLIC |
| 24 | 98_model_write.sql | 平台模型建/改/软删三个受控写函数 | 尾部；依赖 11；EXECUTE 默认 PUBLIC |
| 25 | 98_tenant_guard.sql | 租户删除前置计数函数 | 尾部；依赖 2；`CREATE OR REPLACE` 可重复 |

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

## 升级

- **种子**：`ON CONFLICT DO NOTHING` 对已部署库可重跑但不覆盖已有行；种子
  行修正需手工 `UPDATE` 或重建库。生产种子只含根租户自举数据，历史库中的
  旧演示租户/用户/部门/basic 矩阵不会被新种子删除；模型字典（`model_kind`
  / `model_provider`，`120_sys_dict.sql`）与平台模型默认行（`110_sys_model.sql`）
  在历史库重跑后补入；字典标签翻译在 `sys_translation` 46–56、配置翻译
  57–63，重放不冲突。
- **schema/91_knowledge.sql / 92_model.sql**：历史库追加对象（`kb_library` /
  `kb_document` / `sys_model` 与索引），以超级用户/表 owner 执行；裸创建，
  重复执行报错；RLS 策略随 `rls.sql` 重放（`CREATE POLICY` 亦为裸创建，
  历史库需手工补对应策略或重建）。
- **98_model_write.sql**：历史库追加对象，以部署超级用户执行；只含
  `CREATE OR REPLACE FUNCTION`，可重复。平台模型写端点（`sys:model:platform:*`）
  与模型管理页菜单（279–286）依赖 `050_sys_menu.sql` / `060_sys_role_menu.sql`
  重放补入，补齐前平台写返回 403，属预期收紧。
- **agent_vector.sql**：历史库追加对象，以超级用户单独执行（扩展/schema 判存，
  建表建索引裸创建）。调整嵌入空间须 conf 与 DDL 成对变更（`conf.vector.embedders`
  加条目 + 对应维度索引，装配期探针逐维度对账）；单维旧表升级到多空间形状
  是结构变更，走全量重建；换默认空间的迁移靠应用侧重摄取（source_id 幂等
  覆盖并清除旧切片）。
- **95_agent_audit.sql / 97_agent_retrieval_log.sql**：历史库追加对象，以部署
  角色（与 role.sql 同一执行者）执行；裸创建，重复执行报错；`grape_u` 的
  DML 授权由 role.sql 默认权限覆盖。
- **96_agent_grant.sql**：历史库追加对象，以超级用户执行。授权管理权限码的
  种子（菜单 252/253 与角色 101 映射）随 `050_sys_menu.sql` /
  `060_sys_role_menu.sql` 执行；已部署库在种子执行前判定返回 403，属预期
  收紧。
- **98_menu_write.sql / 98_config_write.sql / 98_tenant_guard.sql**：历史库
  追加对象，以部署超级用户执行；只含 `CREATE OR REPLACE FUNCTION`，可重复。
  函数属主 = 执行者，换执行者重跑会连带改属主，部署口径以 dbdeploy 为准。
  未部署 `fn_tenant_active_child_count` 的历史库在删除租户时调用失败，属
  预期收紧。
- **菜单种子**：历史库按 `seed.Files` 重跑补入缺失权限点（246–249/254–273），
  不覆盖既有行；route_path/component 修正需手工 `UPDATE` 或重建库；旧演示
  范围明细（`sys_role_id_scope` 1001）不会被删除。种子补齐前新增权限码返回
  403（超级管理员同样失去这些码），补齐后恢复。
- **sys_user_role_mapping 撤销改软删**：结构变更。新表以应用层发号 `id` 为
  主键，加 `deleted_by` / `deleted_at`，原 `(user_id, role_id)` 主键由部分
  唯一索引 `(tenant_id, user_id, role_id) WHERE deleted_at IS NULL` 替代；
  历史库需手工迁移或重建，未迁移库在撤销/重复授予路径会因缺列或主键冲突
  报错。判定函数经清单 `CREATE OR REPLACE` 重放即可生效。

## 种子口令

种子用户口令统一为 123456，scrypt 格式 `{scrypt}$e0801$…`
（N=2^14, r=8, p=1）。登录校验识别该格式，通过后由应用透明重哈希为
argon2id，无需批量重置。
