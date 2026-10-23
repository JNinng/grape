# internal

Kratos 服务的内部实现层。`internal/` 下的包对外部模块不可见，业务逻辑只收敛在这里，通过 `cmd/<app>` 组装，并以 `api/` 中声明的契约对外暴露。

## 目录职责

| 目录 | 简述职责 | 文档 |
| ---- | -------- | ---- |
| conf | 配置结构定义 | [conf/](conf/) |
| server | HTTP / gRPC 构建与挂载 | [server/README.md](server/README.md) |
| middleware | 入口中间件 | [middleware/README.md](middleware/README.md) |
| ops | 运维出口 | [ops/README.md](ops/README.md) |
| service | 传输适配 | [service/README.md](service/README.md) |
| task | 异步任务入口 | [task/README.md](task/README.md) |
| biz | 领域层 | [biz/README.md](biz/README.md) |
| data | 存储基础设施集合 | [data/README.md](data/README.md) |

上表各子目录的 README 承接其数据流向、关键文件与接入要点；`conf` 无 README，链接指向目录。

## 分层与数据流向

service 与 task 是两类业务入口，各自自上而下单向流动，越往下越贴近存储。`middleware` 与 `ops` 挂在入口侧但不承载业务数据流：`middleware` 在身份成立前放行/拒绝并注入链路事实，`ops` 输出运维只读指标。

service 入口（HTTP / gRPC）：

```
   client
     │  proto 请求 / 响应
     ▼
┌─────────┐
│ service │  DTO ↔ DO；把 query.v1 动态查询协议转为 dynq.Query
└─────────┘
     │  DO + dynq.Query
     ▼
┌─────────┐
│   biz   │  用例与入参校验；声明仓储接口
└─────────┘
     │  DO + dynq.Query
     ▼   repo IF：biz 声明，data 实现（依赖反转）
┌─────────┐
│  data   │  DO ↔ PO；dynq.Query 解析为类型化查询参数；驱动错误映射
└─────────┘
     │  PO
     ▼
   storage
```

task 入口（定时 / 事件 / 手动触发），与 service 同构但无 DTO / 查询协议转换：

```
   触发（定时 / 事件 / 手动）
     │
     ▼
┌─────────┐
│  task   │  只调 biz 用例；无 DTO ↔ DO 转换，dynq.Query 直接构造
└─────────┘
     │  DO + dynq.Query
     ▼
┌─────────┐
│   biz   │  用例与入参校验；声明仓储接口
└─────────┘
     │  DO + dynq.Query
     ▼   repo IF：biz 声明，data 实现（依赖反转）
┌─────────┐
│  data   │  DO ↔ PO；dynq.Query 解析为类型化查询参数；驱动错误映射
└─────────┘
     │  PO
     ▼
   storage
```

跨界对象有四种，各归其主：

| 对象 | 定义位置            | 形态                                        | 对象举例（以 user 为例）     |
| ---- | ------------------- | ------------------------------------------- | ---------------------------- |
| DTO  | `api/` 生成         | proto 请求 / 响应                           | [`userv1.User`](../api/user/v1/user.pb.go) |
| Query | `api/query/v1` 声明 | 动态 List 查询协议（proto 镜像 dynq.Query） | [`queryv1.Query`](../api/query/v1/query.pb.go) |
| DO   | `biz` 声明          | 纯领域模型，无 proto、无存储标签            | [`biz.User`](biz/user.go) |
| PO   | `data` 生成         | 存储形态，仅 data 层可见                    | [`gen.Basic`](data/pg_sqlc/gen/models.go) |

规则：

- 同一个 DO 类型贯穿 service（或 task）→ biz → data，禁止中途换形或引入第四种对象形态；
- 各层禁止的依赖方向：`service` 只导入 `api/...` 与 `biz`（手写端点可额外导入 `transport/http`）；`task` 只导入 `biz`（不经 `api/...`，无 DTO）；`middleware` 只导入 `api/...`（免认证操作名）、`biz`、`conf` 与 `pkg/utils`；`ops` 只导入 `biz` 设施只读契约；`server` 只导入 `conf`、`service`、生成代码的注册函数（`api/...`）与 `middleware` / `ops`；`biz` 仅允许为错误 reason 枚举导入 `api/...`（`dynq.Query` 作为查询协议随仓储接口跨界）；`data` 只为仓储接口与设施契约导入 `biz`；
- 跨箭头的错误引用即为分层 bug，应修正设计而不是添加 import。

## 手写端点归属

非 proto 生成的手写 handler 按是否需要身份分两类，实现都不放 `server`，`server` 只保留挂载：

| 类别 | 典型端点 | 归属 | 注册方式 | 中间件链 |
| ---- | -------- | ---- | -------- | -------- |
| 运维 / 探针 | `/metrics`（现有）；`/healthz`、`/readyz`、`/debug/pprof` 同类新增 | `ops` | `srv.Handle`（原生 `net/http.Handler`） | 链外、免认证、只读 |
| 业务手写 | 文件上传下载、SSE、WebSocket | `service/<资源>_http.go` | `srv.Route` + handler 内 `ctx.Middleware` | 入链、认证生效 |

Kratos 的服务中间件只在 handler 调用 `ctx.Middleware` 时套上：`srv.Handle` 的原生 Handler 拿不到 `http.Context`；`srv.Route` 中漏调 `ctx.Middleware` 则静默绕过认证。业务手写端点必须调用它，并用「无令牌 401」的测试守住。

确需免认证的业务回调（验签替代令牌）仍归 `service`，但需把路径显式加入 `middleware` 的免认证集合（`tr.Operation()` 对手写路由取值即路径模板），不要靠漏调入链实现。

## 依赖注入

各层与能力目录的 `ProviderSet`（`service` / `task` / `biz` / `data` / `server` / `middleware` / `ops` 各一个，能力目录每能力一个）由 [cmd](../cmd/) 中的 Wire 统一装配：

- `data`：存储后端选择点，聚合各能力目录的 ProviderSet（当前静态选定关系存储 `pg_sqlc`）与设施诊断面/维护面（`NewFacilityDiagnostics` / `NewFacilityMaintenance`，新设施在此追加）；
- 能力目录 `pg_sqlc`：存储客户端构造函数（返回 cleanup）+ 仓储构造函数（返回 `biz.<Resource>Repo` 接口）+ 诊断面构造函数（返回 `biz.FacilityDiagnostics`）+ 维护面构造函数（返回 `biz.FacilityMaintenance`）；
- `biz`：用例构造函数（依赖仓储接口）；
- `service`：服务构造函数（依赖用例）；
- `task`：任务构造函数（依赖用例）；
- `middleware`：`NewAuthMiddleware` / `NewTraceMiddleware`；
- `ops`：`NewMetricsHandler`（设施压力指标的 Prometheus 出口）；
- `server`：`NewGRPCServer` / `NewHTTPServer`，是唯一挂载点（生成代码路由 + 运维原始 Handler）。

诊断面是仓储之外的另一条依赖反转接缝（[biz/maintenance.go](biz/maintenance.go)）：biz 声明 `FacilityDiagnostics` / `FacilityMaintenance` 设施无关契约，data 各能力目录实现原始采集与维护判定，ops 的 `NewMetricsHandler` 把样本经 `/metrics` 统一出口、由 server 直挂（免认证、不在业务中间件链内），task 只认契约消费维护面、不读原始指标。

修改依赖关系后需重新运行 `make generate`（wire + tidy）刷新 `wire_gen.go`。

## 能力目录接入规范

data 按存储能力分目录：一能力一子目录，目录内自包含（装配、实现、声明资产、生成产物）。新增能力目录遵循：

- 各能力构造返回 cleanup、注册进 data 的 ProviderSet；
- 仅作基础设施支撑的能力（如缓存加速既有读路径）不触达 biz；
- 形成独立领域能力的（如发号、锁）由 biz 声明接口、对应能力目录实现，依赖反转接缝与关系存储一致。

### 客户端热更换

热更换是能力目录的可选机制，以 `reloader.go` 承载（文件存在即声明该能力可热换），wire 仅注入稳定句柄；细则待引入 reloader 时按实际需求补充。

## 新增资源速查

1. 在 `api/<domain>/<version>/` 定义 CRUD 的 proto 并 `make api`；动态 List 直接复用 `query.v1.Query`；
2. 在 `biz` 声明 DO + `Repo` 接口（List 收 `*dynq.Query`），并基于接口实现用例；
3. 在 `data/pg_sqlc/sqlc/schema/` 相应域文件声明表结构并用列注释 DSL 声明动态查询能力（业务表照 `10_basic.sql` 的 basic 范式：tenant_id + 审计列 + 归属三元组 + @scope 守卫），在 `data/pg_sqlc/sqlc/queries/<资源>.sql` 写固定 CRUD 查询并补入 sqlc.yaml 的 queries 清单，在 `internal/data/pg_sqlc/sqlc` 下执行 `sqlc generate -f sqlc.yaml` 重新生成；租户表还需在 `rls.sql` 补一条 RLS 策略并 ENABLE/FORCE；
4. 在 `pg_sqlc` 实现仓储（构造函数返回 `biz.<Resource>Repo`），分别注册到 `pg_sqlc` / `biz` / `service` 的 `ProviderSet`（能力 ProviderSet 由 `data` 聚合），并在 `server` 中注册 HTTP / gRPC 服务；
5. `make generate`（wire + tidy）刷新装配与模块依赖；需要同时重生成契约/存储代码时用 `make all`。

## 测试

测试文件与被测代码同目录（`*_test.go`），按层隔离。现有覆盖见各目录 README 的「测试」一节：[biz](biz/README.md)、[service](service/README.md)、[task](task/README.md)、[middleware](middleware/README.md)、[ops](ops/README.md)、[server](server/README.md)、[data](data/README.md)，以及 [pkg](../pkg/README.md) 与 [encoding/jsoncodec](encoding/jsoncodec/README.md)。

新增测试沿用分层职责：`service` 用假仓储隔离传输适配，`biz` mock 仓储接口只验证用例逻辑，`data` 以真实 PostgreSQL 集成测试验证存储边界（自建临时库、按 `deploy_db.md` 顺序装载 SQL、以 `grape_u` 运行角色断言语义）。
