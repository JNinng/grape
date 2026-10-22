# 数据库部署

grape 关系存储（PostgreSQL）的部署：对目标库按序执行 `internal/data/pg_sqlc/sqlc/` 下的 13 个 SQL 文件，完成后补一次连接授权。SQL 对象的函数与命名查询索引见 [sqlc/README.md](internal/data/pg_sqlc/sqlc/README.md)。

## 执行顺序

按序执行以下 13 个文件，顺序不能变。同目录不参与部署的 SQL：`queries/` 五个文件是 sqlc 的 Go 生成输入，不对数据库执行。

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
| 10 | [rls.sql](internal/data/pg_sqlc/sqlc/rls.sql) | RLS 策略、权限过滤函数（fn_accessible_* 三件套）、updated_at 触发器、grape_u 运行角色与 test_u 演示授权 | 依赖步骤 1–9 的表已存在 |
| 11 | [operate.sql](internal/data/pg_sqlc/sqlc/operate.sql) | 写前判断函数（fn_can_* 四件） | 依赖步骤 10 的 fn_accessible_* |
| 12 | [expose.sql](internal/data/pg_sqlc/sqlc/expose.sql) | 权限查询判定函数、范围版本表与 bump 触发器 | 必须先于步骤 13：种子的角色映射写入要 bump 版本表 |
| 13 | [seed.sql](internal/data/pg_sqlc/sqlc/seed.sql) | 种子数据（含权限演示数据，覆盖多个租户） | RLS 已启用，必须以**超级用户/表 owner** 连接执行（超级用户绕过 RLS） |

```bash
# psql 按序执行示例
cd internal/data/pg_sqlc/sqlc
set -e
for f in schema/10_basic.sql schema/20_tenant.sql schema/30_user.sql schema/40_rbac.sql \
         schema/50_dept.sql schema/60_dict.sql schema/70_audit.sql schema/80_session.sql \
         schema/90_config.sql rls.sql operate.sql expose.sql seed.sql; do
  psql -v ON_ERROR_STOP=1 -d <库名> -f "$f"
done
```

## 幂等性与全量重建

除 [seed.sql](internal/data/pg_sqlc/sqlc/seed.sql)（语句均带 `ON CONFLICT DO NOTHING`，不覆盖已有行）外，各文件不保证可重复执行：步骤 1–9 的 `CREATE TABLE`/`CREATE INDEX`、[rls.sql](internal/data/pg_sqlc/sqlc/rls.sql) 的 `CREATE POLICY`、rls/expose 的 `CREATE TRIGGER` 均为裸创建，重复执行直接报错（函数为 `CREATE OR REPLACE`、角色创建带 IF NOT EXISTS 防重，可重复，但不足以让文件整体可重复）。全量重建先 DROP DATABASE，再从步骤 1 开始。

## 部署后授权

部署完成后以超级用户执行一次：

```sql
GRANT CONNECT ON DATABASE <库名> TO grape_u, test_u;
```

角色由 [rls.sql](internal/data/pg_sqlc/sqlc/rls.sql) 创建：`grape_u` 供应用运行连接使用，`test_u` 供演示/验收使用。CONNECT 授权依赖库名，无法写进 rls.sql；一条 GRANT 同时覆盖两个角色。此后应用 DSN 以 `grape_u` 连接，勿用超级用户（会绕过 RLS）。

## 升级

[seed.sql](internal/data/pg_sqlc/sqlc/seed.sql) 的语句均带 `ON CONFLICT DO NOTHING`，对已部署库重复执行不报错、也不覆盖已有行：涉及种子行修正的变更，对历史版本部署过的库需手工执行 UPDATE 或重建库。

## 种子口令

种子用户口令统一为 123456，scrypt 格式 `{scrypt}$e0801$…`（N=2^14, r=8, p=1）。登录校验识别该格式，校验通过后由应用透明重哈希为 argon2id，无需批量重置。
