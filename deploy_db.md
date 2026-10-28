# 数据库部署

grape 关系存储（PostgreSQL）的部署：对目标库按序执行 `internal/data/pg_sqlc/sqlc/` 下的 19 个 SQL 文件，最后补一次连接授权。执行顺序的单一事实源是 `internal/data/pg_sqlc/deploy.go` 的 `DeployFiles` 清单（dbdeploy 命令与集成测试共用），本文表格是其镜像；SQL 对象的函数与命名查询索引见 [sqlc/README.md](internal/data/pg_sqlc/sqlc/README.md)。生产部署的其余口径（多实例粘性、优雅停机、密钥管理、框架升级检查单）见 [operations.md](operations.md)。

## 一键部署（推荐）

```bash
# 参数走 flag 或环境变量（推荐环境变量，避免口令进 shell 历史）
export GRAPE_DEPLOY_ADMIN_DSN=postgres://<user>:<pass>@127.0.0.1:5432/postgres
export GRAPE_DEPLOY_GRAPE_U_PASSWORD=<grape_u 口令>
export GRAPE_DEPLOY_TEST_U_PASSWORD=<test_u 口令>
make dbdeploy ARGS="--database <库名>"
```

流程：连接 `postgres` 库幂等创建目标库（已存在时沿用）→ 连接目标库 → **单事务内**按清单顺序执行 19 个文件（任一文件失败整体回滚）→ 事务内以当前库名补 `GRANT CONNECT`。role.sql 的角色口令以 `{{GRAPE_U_PASSWORD}}` / `{{TEST_U_PASSWORD}}` 占位，dbdeploy 执行前在内存中一次性替换，仓库与日志不落明文。

幂等边界：角色（判存）与种子（`ON CONFLICT DO NOTHING`）可重复执行；结构 DDL 为裸创建，对已部署库重复执行会报错退出——全量重建先手动 `DROP DATABASE` 再执行，本命令不宣称全量幂等（全量幂等需另建迁移台账，不在本命令范围）。

## 执行顺序

按序执行以下 19 个文件，顺序不能变。同目录不参与部署的 SQL：`queries/` 八个文件是 sqlc 的 Go 生成输入，不对数据库执行。

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
| 15 | [agent_session.sql](internal/data/pg_sqlc/sqlc/agent_session.sql) | Agent 会话存储：`CREATE SCHEMA agent` + 框架 session/postgres 六张 `agent_*` 表与索引 + grape_u 授权（trpc-agent-go 框架自管表，不入 sqlc catalog） | 只依赖步骤 10 的 grape_u 存在（授权对象）；与 public 业务域零耦合，放尾部不动既有顺序；schema 幂等建立 |
| 16 | [agent_vector.sql](internal/data/pg_sqlc/sqlc/agent_vector.sql) | 向量检索设施（pgvector 扩展、agent schema、agent_document 表、HNSW 索引与运行角色授权；非 sqlc catalog，存取 SQL 见 internal/data/pg_sqlc/vector_repo.go） | 必须后于步骤 10（授权语句引用 grape_u/test_u）；以**超级用户/表 owner** 连接执行；不启用 RLS，租户隔离由应用层适配器强制；授权收窄到 agent_document，不覆盖 agent schema 全表 |
| 17 | [schema/95_agent_audit.sql](internal/data/pg_sqlc/sqlc/schema/95_agent_audit.sql) | Agent 业务审计：`agent_tool_call_log` 与 `agent_confirmation_log` 两表、索引与租户 RLS（sqlc catalog 输入，命名查询见 queries/agent_audit.sql） | 步骤 1–9 的同型追加（append-only 审计表 + 租户 RLS），放清单尾部独立追加不改既有顺序；依赖步骤 10 的角色存在——grape_u 的 DML 授权由 role.sql 默认权限覆盖（部署角色于 public 建表自动 GRANT），文件内不含授权语句；RLS 策略落本域文件而非 rls.sql（新域独立成文件，见文件头注释） |
| 18 | [96_agent_grant.sql](internal/data/pg_sqlc/sqlc/96_agent_grant.sql) | Agent 工具授权：public.agent_tool_grant 表、未撤销唯一约束（部分唯一表达式索引）、租户 RLS 策略与运行角色授权；入 sqlc catalog，命名查询见 queries/agent_grant.sql | 必须后于步骤 10（授权语句引用 grape_u/test_u）；以**超级用户/表 owner** 连接执行；策略随域文件走（口径同 expose.sql 的 sys_perm_version，不进 rls.sql）；吊销置 revoked_at 不删行，保留与清理口径见根目录 [operations.md](operations.md)「保留期与归档统一口径」 |
| 19 | [schema/97_agent_retrieval_log.sql](internal/data/pg_sqlc/sqlc/schema/97_agent_retrieval_log.sql) | Agent 检索留痕：public.agent_retrieval_log 表（append-only）、租户+时间/会话/来源三类索引与租户 RLS；入 sqlc catalog，命名查询见 queries/agent_retrieval_log.sql | 步骤 17 的同型追加（95 同口径），放清单尾部独立追加不改既有顺序；只依赖步骤 10 的角色存在——grape_u 的 DML 授权由 role.sql 默认权限覆盖，文件内不含授权语句；RLS 策略落本域文件而非 rls.sql（新域独立成文件，见文件头注释）；保留与归档口径见根目录 [operations.md](operations.md)「保留期与归档统一口径」 |

手工按序执行时用 psql：

```bash
# psql 按序执行示例
cd internal/data/pg_sqlc/sqlc
set -e
for f in schema/10_basic.sql schema/20_tenant.sql schema/30_user.sql schema/40_rbac.sql \
         schema/50_dept.sql schema/60_dict.sql schema/70_audit.sql schema/80_session.sql \
         schema/90_config.sql role.sql rls.sql operate.sql expose.sql seed.sql \
         agent_session.sql agent_vector.sql schema/95_agent_audit.sql \
         96_agent_grant.sql schema/97_agent_retrieval_log.sql; do
  psql -v ON_ERROR_STOP=1 -d <库名> -f "$f"
done
# role.sql 的口令占位符需在执行前自行替换，随后补 CONNECT 授权（见下节）
```

## 幂等性与全量重建

role.sql（角色判存 + 授权语句）与 [seed.sql](internal/data/pg_sqlc/sqlc/seed.sql)（语句均带 `ON CONFLICT DO NOTHING`，不覆盖已有行）可重复执行；其余各文件不保证可重复执行：步骤 1–9 的 `CREATE TABLE`/`CREATE INDEX`、[rls.sql](internal/data/pg_sqlc/sqlc/rls.sql) 的 `CREATE POLICY`、rls/expose 的 `CREATE TRIGGER` 均为裸创建，重复执行直接报错；agent 域两个文件（[agent_session.sql](internal/data/pg_sqlc/sqlc/agent_session.sql) / agent_vector.sql）的 `CREATE SCHEMA IF NOT EXISTS` 与扩展安装幂等沿用、两文件先后顺序无关，但文件内的建表/建索引仍按结构 DDL 口径不保证可重复（函数为 `CREATE OR REPLACE`，可重复，但不足以让文件整体可重复）。全量重建先 DROP DATABASE，再从步骤 1 开始。

## 部署后授权

dbdeploy 在部署事务内以当前库名自动补齐；手工部署时以超级用户执行一次：

```sql
GRANT CONNECT ON DATABASE <库名> TO grape_u, test_u;
```

角色由 [role.sql](internal/data/pg_sqlc/sqlc/role.sql) 创建：`grape_u` 供应用运行连接使用，`test_u` 供演示/验收使用。CONNECT 授权依赖库名，无法写进 role.sql；一条 GRANT 同时覆盖两个角色。此后应用 DSN 以 `grape_u` 连接，勿用超级用户（会绕过 RLS）。

## 升级

[seed.sql](internal/data/pg_sqlc/sqlc/seed.sql) 的语句均带 `ON CONFLICT DO NOTHING`，对已部署库重复执行不报错、也不覆盖已有行：涉及种子行修正的变更，对历史版本部署过的库需手工执行 UPDATE 或重建库。

[agent_vector.sql](internal/data/pg_sqlc/sqlc/agent_vector.sql) 对历史版本部署过的库属追加对象：以超级用户连接目标库单独执行即可（扩展与 schema 判存沿用，建表/建索引为裸创建，重复执行在表已存在时报错）。调整 embedding 维度时须同时改该文件的列类型 `vector(维度)` 与 `conf.vector.dimension` 并重建表与索引（改维度的存量迁移不在本文范围）。

[schema/95_agent_audit.sql](internal/data/pg_sqlc/sqlc/schema/95_agent_audit.sql) 对历史版本部署过的库同属追加对象：以部署角色（与 role.sql 同一执行者，默认权限才覆盖新表）连接目标库单独执行即可；建表/建索引/建策略为裸创建，重复执行在对象已存在时报错。文件为裸创建 + RLS 的组合，幂等边界与步骤 1–9 的表一致；`grape_u` 的 DML 授权由 role.sql 默认权限自动覆盖，无需补授权。

[96_agent_grant.sql](internal/data/pg_sqlc/sqlc/96_agent_grant.sql) 同为历史库追加对象：以超级用户连接目标库单独执行（建表/建索引/策略为裸创建，重复执行在对象已存在时报错）；授权管理权限码的种子（sys_menu 250–253 与角色 101 的映射）随 [seed.sql](internal/data/pg_sqlc/sqlc/seed.sql) 追加段执行，`ON CONFLICT DO NOTHING` 可重复。已部署库升级时，权限码在 seed 执行前不生效——入口层判定返回 403，属预期收紧。

[schema/97_agent_retrieval_log.sql](internal/data/pg_sqlc/sqlc/schema/97_agent_retrieval_log.sql) 对历史版本部署过的库同属追加对象（95 同口径）：以部署角色（与 role.sql 同一执行者，默认权限才覆盖新表）连接目标库单独执行即可；建表/建索引/建策略为裸创建，重复执行在对象已存在时报错；`grape_u` 的 DML 授权由 role.sql 默认权限自动覆盖，无需补授权。

## 种子口令

种子用户口令统一为 123456，scrypt 格式 `{scrypt}$e0801$…`（N=2^14, r=8, p=1）。登录校验识别该格式，校验通过后由应用透明重哈希为 argon2id，无需批量重置。
