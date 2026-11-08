# Grape

单二进制的多租户后台：Go 后端（Kratos v3 + Wire + sqlc/pgx + buf）承载
RBAC 数据权限体系与 Agent 运行时适配（trpc-agent-go / ack），前端
（Vite + React）构建产物经 `go:embed` 内嵌。文档、注释与提交信息用中文；
代码内硬编码字符串（错误信息、日志文本、测试名与断言文案）用英文。

## 快速开始

- `make api` / `make config` — 从 proto 生成 `api/` 契约、`openapi.yaml` 与
  `internal/conf/conf.pb.go`
- `make sqlc` — 生成存储代码（改了 `schema/`、`rls/operate/expose`、
  `96_agent_grant.sql`、`98_*` 函数声明文件或 `queries/` 后必须执行并提交）
- `make generate` — wire + tidy，刷新 `cmd/server/wire_gen.go`
- `make all` — 上述全部
- `make web` — 构建前端到 `web/dist`（交付前必须执行）
- `go test ./...` — 集成测试由 `GRAPE_TEST_ADMIN_DSN` / `GRAPE_TEST_DSN`
  门控，未设置时自动跳过
- 运行：`go run ./cmd/server -conf configs`；DSN 走
  `KRATOS_DATABASE_SOURCE`（env source 仅识别 `KRATOS_` 前缀）

## 文档地图

| 主题 | 权威文档 |
| --- | --- |
| 分层规则、依赖方向、手写端点归属、新增资源速查 | [internal/README.md](internal/README.md) |
| 权限体系：租户边界 / 行级数据范围 / 功能权限码、读写形态、错误语义 | [permission.md](permission.md) |
| SQL 对象索引与 schema 注释 DSL | [internal/data/pg_sqlc/sqlc/README.md](internal/data/pg_sqlc/sqlc/README.md) |
| 数据库部署执行顺序与升级 | [deploy_db.md](deploy_db.md) |
| 生产运维：SSE/超时、粘性、优雅停机、保留期、升级检查单 | [operations.md](operations.md) |
| Agent 审计证据面、关联键与复现流程 | [replay.md](replay.md) |
| 各层实现说明 | `internal/<层>/README.md`（见 [目录职责](internal/README.md#目录职责)） |
| 前端构建、令牌策略与管理页 | [web/README.md](web/README.md) |
| 设施诊断指标 | [internal/data/pg_sqlc/diagnostics.md](internal/data/pg_sqlc/diagnostics.md) |
| 编码与 AI 助手约束 | [AGENTS.md](AGENTS.md) |

## 生成物

`api/**/*.pb.go`、`internal/conf/conf.pb.go`、
`internal/data/pg_sqlc/gen/`、`cmd/server/wire_gen.go`、`openapi.yaml` 一律经
make 目标再生成，禁止手改。`internal/data/pg_sqlc/sqlc/` 是存储侧单一事实
源：schema 十一个文件 + `rls/operate/expose` + `96_agent_grant.sql` + `98_*`
函数声明文件 + `queries/`，动态查询能力由列注释 DSL（`@dynamic` / `@scope`
等）声明。

```bash
# 在 internal/data/pg_sqlc/sqlc 目录执行（或项目根 make sqlc）
sqlc generate -f sqlc.yaml
```
