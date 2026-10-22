// Package version 集中存放经 go build -ldflags -X 注入的构建信息，
// 是版本号、commit、构建时间的单点声明，供日志、诊断与探针统一读取。
//
// 注入契约：以下变量均为包级 string，注入示例（在仓库根执行）：
//
//	go build -ldflags "-X grape/pkg/version.Version=v1.0.0 -X grape/pkg/version.Commit=$(git rev-parse --short HEAD) -X grape/pkg/version.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
//
// 未注入时保留可读占位默认值，据此可区分开发构建与正式构建。
package version

import "runtime"

var (
	// Version 语义化版本号，未注入时为 "dev"。
	Version = "dev"

	// Commit 构建对应源码的 git 短哈希，未注入时为 "unknown"。
	Commit = "unknown"

	// BuildTime UTC 构建时间（RFC3339 格式），未注入时为 "unknown"。
	BuildTime = "unknown"
)

// Info 将各构建字段拼成单行 key=value 摘要，便于日志输出与检索；
// 值中不含空白与换行，可安全嵌入任意结构化日志格式。
// 附带 runtime.Version() 的运行时 Go 版本：产物运行所在 Go 工具链
// 可能与构建时不同，排查运行时问题（如标准库行为差异）时两者都需要。
func Info() string {
	return "version=" + Version + " commit=" + Commit + " build_time=" + BuildTime + " go=" + runtime.Version()
}
