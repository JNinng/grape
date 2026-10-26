# Grape

Go 后端（Kratos v3 + Wire + sqlc/pgx + buf），多租户 RBAC 数据权限体系。文档、注释与提交信息用中文；代码内硬编码字符串（错误信息、日志文本、测试名与断言文案等）用英文。

## 常用命令

- `make api` / `make config` — 从 proto 生成 `api/` 契约代码与根目录 `openapi.yaml`、生成 `internal/conf/conf.pb.go`
- `make sqlc` — 生成存储代码；改了 sqlc 输入（`schema/`、`rls/operate/expose`、`queries/`）后必须执行并连同产物提交
- `make generate`（含 wire + tidy）— 改依赖注入后必须重跑以刷新 `cmd/server/wire_gen.go`
- `make all` — 上述全部
- `go test ./...` — 集成测试由 `GRAPE_TEST_ADMIN_DSN` / `GRAPE_TEST_DSN` 门控，未设置时自动跳过
- 运行（仓库根）：`go run ./cmd/server -conf configs`，DSN 走 `KRATOS_DATABASE_SOURCE` 环境变量（env source 仅识别 `KRATOS_` 前缀，未设置时回落配置占位内的默认 DSN）

## 生成物禁止手改

`api/**/*.pb.go`、`internal/conf/conf.pb.go`、`internal/data/pg_sqlc/gen/`、`cmd/server/wire_gen.go`、`openapi.yaml` 一律经 make 目标再生成。`internal/data/pg_sqlc/sqlc/`（schema 九个域文件 + rls/operate/expose/queries/seed）是单一事实源，动态查询能力由列注释 DSL（@dynamic/@scope 等）声明。

## 分层

service/task → biz → data 单向依赖；跨界对象 DTO/Query/DO/PO 各归其主，同一 DO 贯穿全程禁止中途换形。各层允许的 import 方向与新增资源速查见 [internal/README.md](internal/README.md)。

## 关键约束

- 应用以非超级用户 `grape_u` 连接（超级用户绕过 RLS）；业务事务每事务 `set_config('app.tenant_id', ..., true)`，漏设默认全拒（身份成立前的令牌事务与介质维护是文档化例外，见 [sqlc/README.md](internal/data/pg_sqlc/sqlc/README.md)）
- 有权限的领域方法默认按权限处理（Scope/RLS 强制生效），无需额外标注；只有身份已成立但接口有意绕过租户/数据范围约束时才在方法名带 `super`。身份成立前的凭据/令牌查询、设施维护面、已授权事务内的内部回读属文档化例外，不使用 `super`（见 [permission.md](permission.md)）

## 改前必读（按变更选读）

- [internal/README.md](internal/README.md) — 分层规则、能力目录接入规范、新增资源速查、测试约定
- [permission.md](permission.md) — 权限约束落点（租户边界/行级数据范围/功能权限码）、读写形态与错误语义
- [internal/data/pg_sqlc/sqlc/README.md](internal/data/pg_sqlc/sqlc/README.md) — SQL 对象索引、三层权限模型、变更流程与部署顺序
- [deploy_db.md](deploy_db.md) — 数据库部署执行顺序与升级注意事项

## 变更收尾同步文档

- 增删文件同步所在子目录 README 的「关键文件导览」表；改 SQL 对象或命名查询同步 [sqlc/README.md](internal/data/pg_sqlc/sqlc/README.md) 索引表；上层文档仅当变更触及其内容时更新，不逐级联动；与代码同批提交。
