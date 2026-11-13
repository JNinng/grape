// Package configinit 实现 server 二进制的 configinit 子命令：向指定目录
// 输出服务启动配置 config.yaml。
//
// 模板单一事实源为本包 template/config.yaml（与 internal/conf/conf.proto
// 对应），经 go:embed 按字节原样复制；本命令不解析、不替换
// ${ENV:default} 密钥占位，占位由 Kratos 配置加载面展开。conf 结构变更时
// 须同批更新模板。
//
// 用法：server configinit [-dir <输出目录>] [-force]
package configinit

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	// embed 以匿名导入满足 go:embed 对 []byte 变量的要求。
	_ "embed"
)

// outputName 是输出的配置文件名，落在 -conf 指向的目录即可被 file source 加载。
const outputName = "config.yaml"

//go:embed template/config.yaml
var templateConfig []byte

// Run 解析 configinit 子命令参数并执行模板输出，返回进程退出码；输出只写
// 传入的 writer。
func Run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("server configinit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", ".", "output directory for config.yaml (default: current directory)")
	force := fs.Bool("force", false, "overwrite the target file if it already exists")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "server configinit: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	if err := writeTemplate(*dir, *force, stdout); err != nil {
		fmt.Fprintln(stderr, "server configinit:", err)
		return 1
	}
	return 0
}

// writeTemplate 向 dir 输出配置模板：目录缺失时逐级创建；目标文件已存在
// 且未指定 force 时拒绝。模板按字节原样写出，密钥占位不做任何展开。
func writeTemplate(dir string, force bool, stdout io.Writer) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	target := filepath.Join(dir, outputName)
	writeFlags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if !force {
		// O_EXCL 保证「已存在即拒绝」在检查与创建之间无竞态。
		writeFlags |= os.O_EXCL
	}
	f, err := os.OpenFile(target, writeFlags, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("target file already exists (use --force to overwrite): %s", target)
		}
		return fmt.Errorf("open target file: %w", err)
	}
	if _, err := f.Write(templateConfig); err != nil {
		f.Close()
		return fmt.Errorf("write target file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close target file: %w", err)
	}

	target, err = filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("resolve absolute path: %w", err)
	}
	fmt.Fprintf(stdout, "config written: %s\n", target)
	fmt.Fprintf(stdout, "next steps:\n")
	fmt.Fprintf(stdout, "  1. edit the file and fill in sensitive items (database DSNs etc., or keep ${ENV:default} placeholders for the runtime environment);\n")
	fmt.Fprintf(stdout, "  2. make sure the target database is deployed per deploy_db.md at the repo root;\n")
	fmt.Fprintf(stdout, "  3. start the service with that directory: go run ./cmd/server -conf %s\n", filepath.Dir(target))
	return nil
}
