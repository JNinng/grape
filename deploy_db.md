# 数据库部署

grape 关系存储（PostgreSQL）的部署：对目标库按序执行 `internal/data/pg_sqlc/sqlc/` 下的 15 个 SQL 文件，最后补一次连接授权。执行顺序的单一事实源是 `internal/data/pg_sqlc/deploy.go` 的 `DeployFiles` 清单（dbdeploy 命令与集成测试共用），本文表格是其镜像；SQL 对象的函数与命名查询索引见 [sqlc/README.md](internal/data/pg_sqlc/sqlc/README.md)。

## 一键部署（推荐）

```bash
# 参数走 flag 或环境变量（推荐环境变量，避免口令进 shell 历史）
export GRAPE_DEPLOY_ADMIN_DSN=postgres://<user>:<pass>@127.0.0.1:5432/postgres
export GRAPE_DEPLOY_GRAPE_U_PASSWORD=<grape_u 口令>
export GRAPE_DEPLOY_TEST_U_PASSWORD=<test_u 口令>
make dbdeploy ARGS="--database <库名>"
```

流程：连接 `postgres` 库幂等创建目标库（已存在时沿用）→ 连接目标库 → **单事务内**按清单顺序执行 15 个文件（任一文件失败整体回滚）→ 事务内以当前库名补 `GRANT CONNECT`。role.sql 的角色口令以 `{{GRAPE_U_PASSWORD}}` / `{{TEST_U_PASSWORD}}` 占位，dbdeploy 执行前在内存中一次性替换，仓库与日志不落明文。

幂等边界：角色（判存）与种子（`ON CONFLICT DO NOTHING`）可重复执行；结构 DDL 为裸创建，对已部署库重复执行会报错退出——全量重建先手动 `DROP DATABASE` 再执行，本命令不宣称全量幂等（若需全量幂等，另立迁移台账任务）。

## 执行顺序

按序执行以下 15 个文件，顺序不能变。同目录不参与部署的 SQL：`queries/` 五个文件是 sqlc 的 Go 生成输入，不对数据库执行。

| 步骤 | 文件 | 干什么 | 顺序约束 |
|---|---|---|---|
| 1 | [schema/10_basic.sql](internal/data/pg_sqlc/sqlc/schema/10_basic.sql) | basic 业务表范式示例（归属三元组 + @scope 守卫） | 步骤 1–9 相互无外键依赖，按序号 10→90 依次执行 |
| 2 | [schema/20_tenant.sql](internal/data/pg_sqlc/sqlc/schema/20_tenant.sql) | sys_tenant 租户表（RLS 允许命中自身 id） | 同上 |
| 3 | [schema/30_user.sql](internal/data/pg_sqlc/sqlc/schema/30_user.sql) | sys_user（@scope 守卫）、用户-角色/用户组映射、用户组 | 同上 |
| 4 | [schema/40_rbac.sql](internal/data/pg_sqlc/sqlc/schema/40_rbac.sql) | sys_role、范围明细、分发部门映射、sys_menu、角色-菜单映射 | 同上 |
| 5 | [schema/50_dept.sql](internal/data/pg_sqlc/sqlc/schema/50_dept.sql) | sys_dept 组织树、闭包表、闭包维护触发器 | 同上 |
| 6 | [schema/60_dict.sql](internal/data/pg_sqlc/sqlc/schema/60_dict.sql) | 字典类型 + 字典数据 | 同上 |
| 7 | [schema/70_audit.sql](internal/data/pg_sqlc/sqlc/schema/70_audit.sql) | 登录/操作日志（append-only） | 同上 |
| 8 | [schema/80_session.sql](internal/data/pg_sqlc/sqlc/schema/80_session.sql) | 会话/令牌（仅存哈希） | 同上 |
| 9 | [schema/90_config.sql](internal/data/pg_sqlc/sqlc/schema/90_config.sql) | 全局配置、租户配置、功能开关、翻译 | 同上 |
| 10 | [role.sql](internal/data/pg_sqlc/sqlc/role.sql) | grape_u 运行角色与 test_u 演示用户（口令占位符）、schema 使用权与全表/全序列 DML 授权、默认权限、GRANT SET ON PARAMETER | 必须先于步骤 12–13：operate/expose 的函数授权依赖角色存在 |
| 11 | [rls.sql](internal/data/pg_sqlc/sqlc/rls.sql) | RLS 策略、权限过滤函数（fn_accessible_* 三件套）、updated_at 触发器 | 依赖步骤 1–9 的表已存在 |
| 12 | [operate.sql](internal/data/pg_sqlc/sqlc/operate.sql) | 写前判断函数（fn_can_* 四件） | 依赖步骤 11 的 fn_accessible_* |
| 13 | [expose.sql](internal/data/pg_sqlc/sqlc/expose.sql) | 权限查询判定函数、范围版本表与 bump 触发器 | 必须先于步骤 14：种子的角色映射写入要 bump 版本表 |
| 14 | [seed.sql](internal/data/pg_sqlc/sqlc/seed.sql) | 种子数据（含权限演示数据，覆盖多个租户） | RLS 已启用，必须以**超级用户/表 owner** 连接执行（超级用户绕过 RLS） |
| 15 | [agent_session.sql](internal/data/pg_sqlc/sqlc/agent_session.sql) | Agent 会话存储：`CREATE SCHEMA agent` + 框架 session/postgres 六张 `agent_*` 表与索引 + grape_u 授权（trpc-agent-go 框架自管表，不入 sqlc catalog） | 只依赖步骤 10 的 grape_u 存在（授权对象）；与 public 业务域零耦合，放尾部不动既有顺序 |

手工按序执行时用 psql：

```bash
# psql 按序执行示例
cd internal/data/pg_sqlc/sqlc
set -e
for f in schema/10_basic.sql schema/20_tenant.sql schema/30_user.sql schema/40_rbac.sql \
         schema/50_dept.sql schema/60_dict.sql schema/70_audit.sql schema/80_session.sql \
         schema/90_config.sql role.sql rls.sql operate.sql expose.sql seed.sql \
         agent_session.sql; do
  psql -v ON_ERROR_STOP=1 -d <库名> -f "$f"
done
# role.sql 的口令占位符需在执行前自行替换，随后补 CONNECT 授权（见下节）
```

## 幂等性与全量重建

role.sql（角色判存 + 授权语句）与 [seed.sql](internal/data/pg_sqlc/sqlc/seed.sql)（语句均带 `ON CONFLICT DO NOTHING`，不覆盖已有行）可重复执行；其余各文件不保证可重复执行：步骤 1–9 的 `CREATE TABLE`/`CREATE INDEX`、[rls.sql](internal/data/pg_sqlc/sqlc/rls.sql) 的 `CREATE POLICY`、rls/expose 的 `CREATE TRIGGER`、[agent_session.sql](internal/data/pg_sqlc/sqlc/agent_session.sql) 的 `CREATE SCHEMA` 均为裸创建，重复执行直接报错（表/索引沿用框架基线的 `IF NOT EXISTS` 文本，但 schema 已存在时文件整体仍不可重复；函数为 `CREATE OR REPLACE`，可重复，但不足以让文件整体可重复）。全量重建先 DROP DATABASE，再从步骤 1 开始。

## 部署后授权

dbdeploy 在部署事务内以当前库名自动补齐；手工部署时以超级用户执行一次：

```sql
GRANT CONNECT ON DATABASE <库名> TO grape_u, test_u;
```

角色由 [role.sql](internal/data/pg_sqlc/sqlc/role.sql) 创建：`grape_u` 供应用运行连接使用，`test_u` 供演示/验收使用。CONNECT 授权依赖库名，无法写进 role.sql；一条 GRANT 同时覆盖两个角色。此后应用 DSN 以 `grape_u` 连接，勿用超级用户（会绕过 RLS）。

## 升级

[seed.sql](internal/data/pg_sqlc/sqlc/seed.sql) 的语句均带 `ON CONFLICT DO NOTHING`，对已部署库重复执行不报错、也不覆盖已有行：涉及种子行修正的变更，对历史版本部署过的库需手工执行 UPDATE 或重建库。

## 种子口令

种子用户口令统一为 123456，scrypt 格式 `{scrypt}$e0801$…`（N=2^14, r=8, p=1）。登录校验识别该格式，校验通过后由应用透明重哈希为 argon2id，无需批量重置。
