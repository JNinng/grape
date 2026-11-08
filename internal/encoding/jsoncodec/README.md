# jsoncodec

protobuf JSON codec：Kratos 默认 json codec 直接调用 `encoding/json`，产出
snake_case 字段、数字型 int64 与 `{seconds,nanos}` 时间戳，与 proto 及
OpenAPI 契约不一致。本包以 protojson 覆盖同名 `json` content-subtype，使
HTTP 与 gRPC 共享同一套字段映射。[Install](jsoncodec.go) 须在开始接收请求前
调用。

## 编解码契约

- **输出**：小驼峰字段、RFC3339 时间戳、字符串型 int64、符号化枚举名；
  未填充字段（空 repeated、零值标量、未设置的 optional）省略键，客户端按
  缺省值消费。
- **输入**：同时接受小驼峰与 proto 原名（snake_case），未知字段忽略；空输入
  直接返回 nil。
- **回落**：非 proto 消息走 `encoding/json`；`**T` 目标（生成 HTTP handler
  绑定嵌套 body 的形态）解引用为可写入的 `*T`，未分配时先分配。

## 关键文件导览

| 文件 | 职责 |
| ---- | ---- |
| [jsoncodec.go](jsoncodec.go) | `Install` 与 codec 实现：protojson 序列化选项、`**T` 解引用、非 proto 回落。 |

## 测试

- 序列化符合 proto JSON 映射（小驼峰、RFC3339、字符串 int64、符号化枚举）。
- 反序列化同时接受小驼峰与 snake_case。
- 嵌套消息指针目标可写入。
- `Install` 覆盖 Kratos 默认 `json` codec。
