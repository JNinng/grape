# internal

Kratos 服务的内部实现层。`internal/` 下的包对外部模块不可见，业务逻辑只
收敛在这里，通过 `cmd/<app>` 组装，并以 `api/` 中声明的契约对外暴露。

## 目录职责

| 目录 | 简述职责 | 文档 |
| --- | --- | --- |
| conf | 配置结构定义（`conf.proto` 生成） | [conf/](conf/) |
| server | HTTP / gRPC 构建与挂载 | [server/README.md](server/README.md) |
| middleware | 入口中间件（认证、链路、权限、审计） | [middleware/README.md](middleware/README.md) |
| ops | 运维出口（`/metrics`） | [ops/README.md](ops/README.md) |
| telemetry | 观测装配（OTel SDK 与框架桥接） | [telemetry/README.md](telemetry/README.md) |
| service | 传输适配 | [service/README.md](service/README.md) |
| agent | Agent 框架适配层 | [agent/README.md](agent/README.md) |
| task | 异步任务入口 | [task/README.md](task/README.md) |
| biz | 领域层 | [biz/README.md](biz/README.md) |
| data | 存储基础设施集合 | [data/README.md](data/README.md) |
| encoding | 编码适配（`jsoncodec`） | [encoding/jsoncodec/README.md](encoding/jsoncodec/README.md) |

`conf` 无独立 README，其余子目录的 README 承接其数据流向、关键文件与
接入要点。

## 分层与数据流向

service 与 task 是两类业务入口，各自自上而下单向流动，越往下越贴近存储。
`middleware` 与 `ops` 挂在入口侧但不承载业务数据流：`middleware` 在身份
成立前放行/拒绝并注入链路事实，`ops` 输出只读运维指标。`telemetry` 是
观测装配层：OTel TracerProvider 生命周期与框架日志/Tracer 桥接只在这里
落子。`agent` 是框架适配层，平行于 service 挂在入口侧：构造模型、runner、
AG-UI handler 与许可网关，经 service 的手写端点入 Kratos 链，不承载
DTO ↔ DO 数据流。

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

task 入口与之同构，只是没有 DTO 与查询协议转换：task 只调 biz 用例，
`dynq.Query` 直接构造。

## 跨层对象

| 对象 | 定义位置 | 形态 | 举例 |
| --- | --- | --- | --- |
| DTO | `api/` 生成 | proto 请求 / 响应 | [`userv1.User`](../api/user/v1/user.pb.go) |
| Query | `api/query/v1` 声明 | 动态 List 查询协议（镜像 `dynq.Query`） | [`queryv1.Query`](../api/query/v1/query.pb.go) |
| DO | `biz` 声明 | 纯领域模型，无 proto、无存储标签 | [`biz.User`](biz/user.go) |
| PO | `data` 生成 | 存储形态，仅 data 层可见 | [`gen.Basic`](data/pg_sqlc/gen/models.go) |

- 同一个 DO 类型贯穿 service（或 task）→ biz → data，禁止中途换形或引入
  第四种对象形态。
- 跨箭头的错误引用即为分层 bug，应修正设计而不是添加 import。

## 依赖方向

各层允许的 import 白名单：

- `service`：`api/...` 与 `biz`；手写端点可额外导入 `transport/http` 与
  `agent`（Agent 路由挂载物），`Logout` 因免认证幂等语义自取 Authorization
  头而导入 `transport`，`query` 服务为构造查询协议导入 `dynq`。
- `task`：只导入 `biz`，不经 `api/...`，无 DTO。
- `middleware`：`api/...`（免认证操作名）、`biz`、`conf`、`pkg/utils`、
  `pkg/redact` 与传输/错误/追踪基础库（`transport`、kratos errors、otel
  trace、grpc peer、protobuf 反射）。
- `ops`：`biz` 设施只读契约与运行观测契约。
- `telemetry`：`conf`、otel SDK 与 trpc-agent-go 观测门面（`log` /
  `telemetry/trace`，仅桥接赋值，不装配框架对象）。
- `server`：`conf`、`service`、生成代码注册函数、`middleware` / `telemetry`
  （otel 中间件开关的时序锚点）与 `web`（静态托管的 `fs.FS` 导出）；ops 的
  指标 Handler 以 `net/http.Handler` 参数注入，不直接 import。
- `agent`：根包是唯一装配点，只导入 `conf`、`biz`（含随仓储接口跨界的
  `dynq`）、自身子包与框架包；子包依赖单向无环（`httpapi → audit →
  identity`，`tools → identity`），不反向 import 根包，也不被 agent 树以外
  的包消费。
- `biz`：仅允许为错误 reason 枚举导入 `api/...`；第三方依赖限装配与基础库
  （wire、`dynq`、kratos errors、`golang.org/x/crypto`、otel trace）；
  **禁止导入 `agent` 与任何 Agent 框架包**。
- `data`：只为仓储接口与设施契约导入 `biz`。
- `pkg/`：与 internal 各层解耦。`pkg/redact` 被 `data`（debug tracer、
  检索留痕装饰器）、`biz`（审计注册表）、`middleware`（操作审计快照）与
  `agent`（LLM 日志）消费——data 层 debug tracer 自建同默认值实例；
  `pkg/utils` 仅被 `middleware` 消费。

## 手写端点归属

非 proto 生成的手写 handler 按是否需要身份分两类，实现都不放 `server`，
`server` 只保留挂载：

| 类别 | 典型端点 | 归属 | 注册方式 | 中间件链 |
| --- | --- | --- | --- | --- |
| 运维 / 探针 | `/metrics`；同类新增 `/healthz`、`/readyz`、`/debug/pprof` | `ops` | `srv.Handle`（原生 `net/http.Handler`） | 链外、免认证、只读 |
| 业务手写 | 文件上传下载、SSE、WebSocket | `service/<资源>_http.go` | `srv.Route` + handler 内 `ctx.Middleware` | 入链、认证生效 |
| 前端静态托管 | SPA 页面壳与 assets（`/` 兜底） | `server/web.go`（挂载）＋根目录 `web/`（产物与 embed） | `srv.HandlePrefix("/")` 直挂、最后注册 | 链外、免认证；保留 API 前缀内 404 不回页面壳 |

静态托管免认证的口径：页面壳与 assets 本身不含身份事实（登录页即页面壳），
鉴权发生在 API 调用上，故与 `/metrics` 同类走链外直挂，不进认证中间件的
免认证名单（该名单只作用于链内操作名）。

Kratos 的服务中间件只在 handler 调用 `ctx.Middleware` 时套上：`srv.Handle`
的原生 Handler 拿不到 `http.Context`；`srv.Route` 中漏调则静默绕过认证。
业务手写端点必须调用它，并用「无令牌 401」测试守住。

确需免认证的业务回调（验签替代令牌）仍归 `service`，但需把路径显式加入
`middleware` 的免认证集合（`tr.Operation()` 对手写路由取值即路径模板），
不要靠漏调入链实现。

## 依赖注入

各层与能力目录各自导出 `ProviderSet`（`service` / `task` / `biz` / `data` /
`server` / `middleware` / `ops` / `agent`，能力目录每能力一个），由
[cmd](../cmd/) 中的 Wire 统一装配。`data` 是存储后端选择点：聚合各能力目录
的 ProviderSet 与设施诊断面/维护面；`server` 是唯一挂载点（生成代码路由 +
运维原始 Handler + Agent 手写路由 + SPA 静态兜底）。

诊断面是仓储之外的另一条依赖反转接缝（[biz/maintenance.go](biz/maintenance.go)）：
biz 声明设施无关的 `FacilityDiagnostics` / `FacilityMaintenance`，data 各
能力实现，ops 经 `/metrics` 出口、server 直挂，task 只消费契约；PostgreSQL
采集范围、指标清单与可见性边界见
[diagnostics.md](data/pg_sqlc/diagnostics.md)。运行观测同型
（[biz/metrics.go](biz/metrics.go)）：biz 声明 `AgentRunObserver`，agent
推送事实，ops 的 `RuntimeMetrics` 实现并注册进同一出口。

修改依赖关系后需重新运行 `make generate`（wire + tidy）刷新
`wire_gen.go`。

## 能力目录接入规范

data 按存储能力分目录：一能力一子目录，目录内自包含（装配、实现、声明
资产、生成产物）。新增能力目录遵循：

- 各能力构造返回 cleanup、注册进 data 的 ProviderSet；
- 仅作基础设施支撑的能力（如缓存加速既有读路径）不触达 biz；
- 形成独立领域能力的（如发号、锁）由 biz 声明接口、对应能力目录实现，
  依赖反转接缝与关系存储一致。

### 客户端热更换

热更换是能力目录的可选机制：目录内出现 `reloader.go` 即声明该能力可热换，
wire 仅注入稳定句柄。

## 新增资源速查

1. 在 `api/<domain>/<version>/` 定义 CRUD 的 proto 并 `make api`；动态 List
   直接复用 `query.v1.Query`。
2. 在 `biz` 声明 DO + `Repo` 接口（List 收 `*dynq.Query`），并基于接口
   实现用例。
3. 在 `data/pg_sqlc/sqlc/schema/` 相应域文件声明表（业务表照
   `10_basic.sql` 的范式：tenant_id + 审计列 + 归属三元组 + `@scope`），用
   列注释 DSL 声明动态查询能力；在 `queries/<资源>.sql` 写固定 CRUD 并补入
   `sqlc.yaml`，在 `sqlc/` 下执行 `sqlc generate -f sqlc.yaml`。租户表还需在
   `rls.sql` 补策略并 ENABLE/FORCE。业务表的 `tenant_id` 注释统一为
   `[@internal]`（不参与动态 schema/投影/过滤/排序）；唯一例外是
   `sys_tenant.tenant_id`（租户树结构列，保留 `[@dynamic: exact]`）。
4. 在 `pg_sqlc` 实现仓储（构造函数返回 `biz.<Resource>Repo`），注册进
   `pg_sqlc` / `biz` / `service` 的 `ProviderSet`，并在 `server` 注册 HTTP /
   gRPC 服务。
5. `make generate` 刷新装配；需要同时重生成契约/存储代码时用 `make all`。

权限约束落点（租户边界 / 行级数据范围 / 功能权限码）、读写形态、错误语义
与逐项实现清单见根目录 [permission.md](../permission.md)。

## 测试

测试文件与被测代码同目录（`*_test.go`），按层隔离。覆盖主题见各目录
README 的「测试」一节：[biz](biz/README.md)、[service](service/README.md)、
[task](task/README.md)、[middleware](middleware/README.md)、
[ops](ops/README.md)、[telemetry](telemetry/README.md)、
[server](server/README.md)、[data](data/README.md)、[pkg](../pkg/README.md)
与 [encoding/jsoncodec](encoding/jsoncodec/README.md)。

新增测试沿用分层职责：`service` 用假仓储隔离传输适配，`biz` mock 仓储接口
只验证用例逻辑，`data` 以真实 PostgreSQL 集成测试验证存储边界（自建临时库、
按部署清单装载 SQL、以 `grape_u` 运行角色断言语义）。
