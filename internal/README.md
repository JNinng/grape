# internal

Kratos 服务的内部实现层。`internal/` 下的包对外部模块不可见，业务逻辑只收敛在这里，通过 `cmd/<app>` 组装，并以 `api/` 中声明的契约对外暴露。

## 目录职责

| 目录                | 职责与规则                                                                                                                              |
| ------------------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| [conf](conf/)       | 配置结构定义。只改 `conf.proto`，生成文件禁止手改，改后由 `make config` 重新生成。                                                       |
| [server](server/)   | HTTP / gRPC 服务器构建。只做配置读取、中间件装配与服务注册；禁止出现 DTO 翻译或业务逻辑。                                               |
| [service](service/) | 传输层适配器，每个资源一个文件。在边界完成 DTO ↔ DO 转换；把 query.v1 动态查询协议转为 dynq.Query；委托给 biz 用例；禁止实现业务规则（入参校验在 biz 落地）。 |
| [biz](biz/)         | 领域层，只认 DO。定义 DO、用例、仓储接口（依赖反转的接缝）、类型化错误与查询选项；领域规则（如必填校验）只在这里落地。                   |
| [data](data/)       | 存储层。实现 `biz` 声明的仓储接口；DO ↔ PO 转换、驱动错误映射都在仓储内完成。schema 与查询由 `data/schema.sql` + `data/queries.sql` 经 sqlc 生成（固定 CRUD 走 sqlc-gen-go，动态 List 走 sqlc_gen_dynamic 插件），生成文件禁止手改。  |

## 分层与数据流向

自上而下单向流动，越往下越贴近存储：

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

跨界对象有四种，各归其主：

| 对象 | 定义位置            | 形态                                        | 对象举例（以 user 为例）     |
| ---- | ------------------- | ------------------------------------------- | ---------------------------- |
| DTO  | `api/` 生成         | proto 请求 / 响应                           | [`userv1.CreateUserRequest`](../api/user/v1/user.pb.go) |
| Query | `api/query/v1` 声明 | 动态 List 查询协议（proto 镜像 dynq.Query） | [`queryv1.Query`](../api/query/v1/query.pb.go) |
| DO   | `biz` 声明          | 纯领域模型，无 proto、无存储标签            | [`biz.User`](biz/user.go) |
| PO   | `data` 生成         | 存储形态，仅 data 层可见                    | [`db_sqlc.User`](data/db_sqlc/models.go) |

规则：

- 同一个 DO 类型贯穿 service → biz → data，禁止中途换形或引入第四种对象形态；
- 各层禁止的依赖方向：`service` 只导入 `api/...` 与 `biz`；`biz` 仅允许为错误 reason 枚举导入 `api/...`（`dynq.Query` 作为查询协议随仓储接口跨界）；`data` 只为实现仓储接口导入 `biz`；
- 跨箭头的错误引用即为分层 bug，应修正设计而不是添加 import。

## 依赖注入

各层的 `ProviderSet`（`service` / `biz` / `data` / `server` 中各有一个）由 [cmd](../cmd/) 中的 Wire 统一装配：

- `data`：存储客户端构造函数（返回 cleanup）+ 仓储构造函数（返回 `biz.<Resource>Repo` 接口）；
- `biz`：用例构造函数（依赖仓储接口）；
- `service`：服务构造函数（依赖用例）；
- `server`：`NewGRPCServer` / `NewHTTPServer`。

修改依赖关系后需重新运行 `make all`（或 `wire`）刷新 `wire_gen.go`。

## 新增资源速查

1. 在 `api/<domain>/<version>/` 定义 CRUD 的 proto 并 `make api`；动态 List 直接复用 `query.v1.Query`；
2. 在 `biz` 声明 DO + `Repo` 接口（List 收 `*dynq.Query`），并基于接口实现用例；
3. 在 `data/schema.sql` 声明表结构并用列注释 DSL 声明动态查询能力，在 `data/queries.sql` 写固定 CRUD 查询，在 `internal/data` 下执行 `sqlc generate -f sqlc.yaml` 重新生成；
4. 在 `data` 实现仓储（构造函数返回 `biz.<Resource>Repo`），分别注册到 `data` / `biz` / `service` 的 `ProviderSet`，并在 `server` 中注册 HTTP / gRPC 服务；
5. `make all`（或 `wire`）重新生成 Wire 与 go.mod。

## 测试

测试文件与被测代码同目录（`*_test.go`），按层隔离。目前仅 `service` 层已有测试，`biz` / `data` 待补：

- `service`：以内存版假仓储替换 `biz` 仓储接口，隔离测试 DTO 转换与请求语义；
- `biz`：mock 仓储接口，只验证用例逻辑与领域规则；
- `data`：在存储边界用真实 schema 验证仓储实现与错误映射。
