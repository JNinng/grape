# internal

Kratos 服务的内部实现层。`internal/` 下的包对外部模块不可见，业务逻辑只收敛在这里，通过 `cmd/<app>` 组装，并以 `api/` 中声明的契约对外暴露。

## 目录职责

| 目录                | 职责与规则                                                                                                                              |
| ------------------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| [conf](conf/)       | 配置结构定义。只改 `conf.proto`，生成文件禁止手改，改后由 `make config` 重新生成。                                                       |
| [server](server/)   | HTTP / gRPC 服务器构建。只做配置读取、中间件装配与服务注册；禁止出现 DTO 翻译或业务逻辑。                                               |
| [service](service/) | 传输层适配器，每个资源一个文件。在边界完成 DTO ↔ DO 转换；把 query.v1 动态查询协议转为 dynq.Query；委托给 biz 用例；禁止实现业务规则（入参校验在 biz 落地）。 |
| task                | 后台异步任务入口（定时、事件、手动触发），与 service 平级构成第二类入口。分层与 service 同构（task → biz → data），无 DTO，直接以 DO 调用 biz 用例；禁止实现业务规则（入参校验在 biz 落地）。 |
| [biz](biz/)         | 领域层，只认 DO。定义 DO、用例、仓储接口（依赖反转的接缝）、类型化错误与查询选项；领域规则（如必填校验）只在这里落地。                   |
| [data](data/)       | 存储基础设施集合：一能力一子目录，目录内自包含（定位与接入细则见「能力目录接入规范」）。能力目录实现 `biz` 声明的仓储接口，DO ↔ PO 转换、驱动错误映射在仓储内完成；现有能力与生成资产见 [data/README.md](data/README.md)。  |

## 分层与数据流向

service 与 task 是两类平级入口，各自自上而下单向流动，越往下越贴近存储。

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
- 各层禁止的依赖方向：`service` 只导入 `api/...` 与 `biz`；`task` 只导入 `biz`（不经 `api/...`，无 DTO）；`biz` 仅允许为错误 reason 枚举导入 `api/...`（`dynq.Query` 作为查询协议随仓储接口跨界）；`data` 只为实现仓储接口导入 `biz`；
- 跨箭头的错误引用即为分层 bug，应修正设计而不是添加 import。

## 依赖注入

各层与能力目录的 `ProviderSet`（`service` / `task` / `biz` / `data` / `server` 各一个，能力目录每能力一个）由 [cmd](../cmd/) 中的 Wire 统一装配：

- `data`：存储后端选择点，聚合各能力目录的 ProviderSet（当前静态选定关系存储 `pg_sqlc`）；
- 能力目录 `pg_sqlc`：存储客户端构造函数（返回 cleanup）+ 仓储构造函数（返回 `biz.<Resource>Repo` 接口）；
- `biz`：用例构造函数（依赖仓储接口）；
- `service`：服务构造函数（依赖用例）；
- `task`：任务构造函数（依赖用例）；
- `server`：`NewGRPCServer` / `NewHTTPServer`。

修改依赖关系后需重新运行 `make all`（或 `wire`）刷新 `wire_gen.go`。

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
5. `make all`（或 `wire`）重新生成 Wire 与 go.mod。

## 测试

测试文件与被测代码同目录（`*_test.go`），按层隔离。目前 `data` 的 `pg_sqlc` 已有错误映射单测与存储边界集成测试（事务门面 IdentityTx 族 + RLS/@scope 纪律，`GRAPE_TEST_ADMIN_DSN` / `GRAPE_TEST_DSN` 环境变量门控、未设置时跳过），`encoding/jsoncodec` 已有编解码契约单测；`service` / `biz` 用例级测试随资源落地补充：

- `service`：以内存版假仓储替换 `biz` 仓储接口，隔离测试 DTO 转换与请求语义；
- `biz`：mock 仓储接口，只验证用例逻辑与领域规则；
- `data`：`pg_sqlc/errors_test.go` 单测驱动错误到领域错误的映射；`pg_sqlc/session_test.go` 集成测试自建临时库并按部署顺序装载 schema，以 grape_u 运行角色验证租户注入、默认全拒、@scope 过滤、fail-closed，以及用户/会话仓储的读写、令牌维度 RLS 边界与登录全链路（种子 scrypt 校验 → 透明重哈希 → argon2id 再登录）。
