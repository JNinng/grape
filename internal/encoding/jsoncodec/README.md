# jsoncodec

protobuf JSON codec：以 protojson 覆盖 Kratos 默认的 `json` content-subtype，使 HTTP 与 gRPC 共享同一套字段映射。`Install` 须在开始接收请求前调用。

编解码契约见 [jsoncodec.go](jsoncodec.go)：输出小驼峰字段、RFC3339 时间戳、字符串型 int64 与符号化枚举名；未填充字段（空 repeated、零值标量、未设置的 optional）省略键，客户端按缺省值消费；输入同时接受小驼峰与 proto 原名（snake_case），未知字段忽略。非 proto 消息回落到 `encoding/json`；`**T` 目标按生成 HTTP handler 的嵌套 body 绑定解引用；空输入直接返回 nil。

## 测试

现有覆盖：protobuf JSON 编解码契约。
