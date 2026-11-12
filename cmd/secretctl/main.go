// secretctl 是密钥运维命令：列出支持的完整算法标识，并按算法生成可直接
// 填入登记表的密钥材料。
//
// 用法：go run ./cmd/secretctl <algorithms|keygen <算法标识>>
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"grape/pkg/secret"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// usage 输出命令用法；写 stdout 表示主动求助，写 stderr 表示用法错误。
func usage(w io.Writer) {
	fmt.Fprintf(w, "usage: secretctl algorithms | secretctl keygen <kind>\n")
	fmt.Fprintf(w, "supported kinds: %s\n", joinKinds(secret.SupportedKinds()))
}

// joinKinds 把算法标识清单拼为逗号分隔文本，供 usage 展示。
func joinKinds(kinds []secret.Kind) string {
	if len(kinds) == 0 {
		return "(none)"
	}
	parts := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		parts = append(parts, string(kind))
	}
	return strings.Join(parts, ", ")
}

// run 是命令入口的可测试形态：返回进程退出码，输出只写传入的 writer。
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "algorithms":
		return runAlgorithms(args[1:], stdout, stderr)
	case "keygen":
		return runKeygen(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "secretctl: unknown command %q\n", args[0])
		usage(stderr)
		return 2
	}
}

// runAlgorithms 逐行输出受支持的完整算法标识，顺序确定（pkg/secret 的
// 支持集合排序）。
func runAlgorithms(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("secretctl algorithms", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "secretctl algorithms: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	for _, kind := range secret.SupportedKinds() {
		fmt.Fprintln(stdout, kind)
	}
	return 0
}

// runKeygen 生成指定算法标识的密钥材料。输出首行固定 kind，其后按算法
// 字段输出「字段名: 值」：对称算法一行 key，非对称算法固定先 public_key
// 后 private_key。未知或不支持的算法标识只写 stderr 并以非零码退出，
// stdout 不输出任何密钥。
func runKeygen(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("secretctl keygen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintf(stderr, "secretctl keygen: exactly one kind argument is required\n")
		usage(stderr)
		return 2
	}
	material, err := secret.Generate(secret.Kind(fs.Arg(0)))
	if err != nil {
		fmt.Fprintf(stderr, "secretctl keygen: %v\n", err)
		return 1
	}
	for _, line := range materialLines(material) {
		fmt.Fprintln(stdout, line)
	}
	return 0
}

// materialLines 把密钥材料展开为 keygen 的输出行序列：首行固定
// `kind: <算法标识>`，其后按算法字段输出 `字段名: 值`——对称算法一行
// `key:`，非对称算法固定先 `public_key:` 后 `private_key:`。字段名与登记表
// 一致，输出可直接填入登记表。
func materialLines(m secret.Material) []string {
	lines := []string{"kind: " + string(m.Kind)}
	if m.Key != "" {
		return append(lines, "key: "+m.Key)
	}
	if m.PublicKey != "" {
		lines = append(lines, "public_key: "+m.PublicKey)
	}
	if m.PrivateKey != "" {
		lines = append(lines, "private_key: "+m.PrivateKey)
	}
	return lines
}
