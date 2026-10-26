# pkg

与业务无关的通用包，不依赖 `internal/`，供各层直接引用：

- [utils](utils/)：基础工具。`ResolveLocalIP` 按 `INSTANCE_IP` → `POD_IP` → 本机网卡顺序解析非回环 IPv4；`InstanceId` 生成进程内稳定的「IP-PID」实例标识（IP 解析失败退化为纯 PID）；`NewTraceID` / `NewSpanID` 生成合法 otel 标识；`SafeString` / `SafeBody` 按 4 KiB 上限在 rune 边界截断日志体；`MaskString` / `MaskString4` 按 rune 保留首尾、掩码中段。
- [version](version/)：经 `go build -ldflags -X` 注入的版本、提交与构建时间单点声明，未注入时保留开发占位值；`Info()` 输出含运行时 Go 版本的单行摘要。
- [redact](redact/)：按类别登记的确定性脱敏器注册表（TODO E1 单一代码点）。标准四类：`password` / `token`（固定掩码）、`tool_param`（敏感键名清单按键匹配，JSON 深序遍历）、`retrieval`（rune 边界截断 + 可配置保留长度）；值形态支持固定掩码、keyed 哈希（同盐同输出可关联）与整段截断。并发安全（写少读多），未登记类别原样返回并报 false，绝不静默假脱敏。审计表（E2）与复现视图（A3）消费同一注册表。

## 测试

现有覆盖：工具函数契约（IP 解析、ID 生成、日志截断/掩码、版本摘要）；脱敏器边界与确定性（空值/超长/多键/嵌套 JSON/非字符串值）、注册表并发安全（-race）、标准注册表四类覆盖。
