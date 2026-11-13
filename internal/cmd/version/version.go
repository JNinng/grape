// Package version 实现 server 二进制的 version 子命令：输出构建信息摘要
// （版本号、commit、构建时间、运行时 Go 版本）。字段经 go build -ldflags
// -X 注入 pkg/version（契约见该包）；未注入的开发构建输出可读占位默认值。
//
// 用法：server version
package version

import (
	"flag"
	"fmt"
	"io"

	"grape/pkg/version"
)

// Run 输出 pkg/version 的单行 key=value 构建信息摘要，返回进程退出码。
func Run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("server version", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "server version: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	fmt.Fprintln(stdout, version.Info())
	return 0
}
