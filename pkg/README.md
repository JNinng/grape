# pkg

与业务无关的通用包，不依赖 `internal/`，供各层直接引用：

- [utils](utils/)：基础工具。`ResolveLocalIP` 按 `INSTANCE_IP` → `POD_IP` → 本机网卡顺序解析非回环 IPv4；`InstanceId` 生成进程内稳定的「IP-PID」实例标识（IP 解析失败退化为纯 PID）；`NewTraceID` / `NewSpanID` 生成合法 otel 标识；`SafeString` / `SafeBody` 按 4 KiB 上限在 rune 边界截断日志体；`MaskString` / `MaskString4` 按 rune 保留首尾、掩码中段。
- [version](version/)：经 `go build -ldflags -X` 注入的版本、提交与构建时间单点声明，未注入时保留开发占位值；`Info()` 输出含运行时 Go 版本的单行摘要。

## 测试

现有覆盖：工具函数契约（IP 解析、ID 生成、日志截断/掩码、版本摘要）。
