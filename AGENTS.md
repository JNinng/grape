# Grape

Go 后端（Kratos v3 + Wire + sqlc/pgx + buf），多租户 RBAC 数据权限体系。文档、注释与提交信息用中文；代码内硬编码字符串（错误信息、日志文本、测试名与断言文案等）用英文。

## 常用命令

- `make api` / `make config` — 从 proto 生成 `api/` 契约代码与根目录 `openapi.yaml`、生成 `internal/conf/conf.pb.go`
- `make sqlc` — 生成存储代码；改了 sqlc 输入（`schema/`、`rls/operate/expose`、`queries/`）后必须执行并连同产物提交
- `make generate`（含 wire + tidy）— 改依赖注入后必须重跑以刷新 `cmd/server/wire_gen.go`
- `make all` — 上述全部
- `go test ./...` — 集成测试由 `GRAPE_TEST_ADMIN_DSN` / `GRAPE_TEST_DSN` 门控，未设置时自动跳过
- 运行（仓库根）：`go run ./cmd/server -conf configs`，DSN 走 `DATABASE_SOURCE` 环境变量

## 生成物禁止手改

`api/**/*.pb.go`、`internal/conf/conf.pb.go`、`internal/data/pg_sqlc/gen/`、`cmd/server/wire_gen.go`、`openapi.yaml` 一律经 make 目标再生成。`internal/data/pg_sqlc/sqlc/`（schema 九个域文件 + rls/operate/expose/queries/seed）是单一事实源，动态查询能力由列注释 DSL（@dynamic/@scope 等）声明。

## 分层

service/task → biz → data 单向依赖；跨界对象 DTO/Query/DO/PO 各归其主，同一 DO 贯穿全程禁止中途换形。各层允许的 import 方向与新增资源速查见 [internal/README.md](internal/README.md)。

## 安全不变量（动相关代码前逐条对照）

- 应用以非超级用户 `grape_u` 连接（超级用户绕过 RLS）；业务事务每事务 `set_config('app.tenant_id', ..., true)`，漏设默认全拒（身份成立前的令牌事务与介质维护是文档化例外，见 [sqlc/README.md](internal/data/pg_sqlc/sqlc/README.md)）
- `@scope` 表的 List/Count 缺 Scope 即报错（fail-closed）；Scope 只从服务端认证上下文注入，不受前端影响
- 有权限的领域方法默认按权限处理（Scope/RLS 强制生效），无需额外标注；需要无权限或超高权限行为的接口，签名须带 `super` 字样显式区分
- 令牌与口令哈希只在 biz 单点生成/校验，仓储出入参只见哈希；登录点查对软删除/跨租户与口令错误同因返回（防枚举）
- 改 SQL 函数需三处同步：SQL 注释、`COMMENT ON`、[sqlc/README.md](internal/data/pg_sqlc/sqlc/README.md) 索引表；改命名查询需同步查询注释（sqlc 带入 Go doc）与索引表
- 数据库部署按 13 文件固定顺序执行（seed 须以超级用户连接），顺序与升级注意事项见 [deploy_db.md](deploy_db.md)

## 改前必读

- [internal/README.md](internal/README.md) — 分层规则、能力目录接入规范、新增资源速查、测试约定
- [internal/data/pg_sqlc/sqlc/README.md](internal/data/pg_sqlc/sqlc/README.md) — SQL 对象索引、三层权限模型
- [deploy_db.md](deploy_db.md) — 数据库部署执行顺序与升级注意事项
- [TODO.md](TODO.md) — 在建纵切、运行前提与既定约束（含决策理由）
