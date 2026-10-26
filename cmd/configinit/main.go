// configinit 是配置初始化命令：向指定目录输出服务启动配置 config.yaml。
//
// 模板单一事实源为 template/config.yaml（与 internal/conf/conf.proto 对应），
// 经 go:embed 按字节原样复制；本命令不解析、不替换 ${ENV:default} 密钥占位，
// 占位由 cmd/server 的 Kratos 配置加载面展开。conf 结构变更时须同批更新模板。
//
// 用法：go run ./cmd/configinit [-dir <输出目录>] [-force]
package main

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

var (
	// dirFlag 是输出目录，默认当前工作目录。
	dirFlag string
	// forceFlag 允许覆盖已存在的目标文件。
	forceFlag bool
)

func init() {
	flag.StringVar(&dirFlag, "dir", ".", "output directory for config.yaml (default: current directory)")
	flag.BoolVar(&forceFlag, "force", false, "overwrite the target file if it already exists")
}

func main() {
	flag.Parse()
	if err := run(dirFlag, forceFlag, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "configinit:", err)
		os.Exit(1)
	}
}

// run 向 dir 输出配置模板：目录缺失时逐级创建；目标文件已存在且未指定
// force 时拒绝。模板按字节原样写出，密钥占位不做任何展开。
func run(dir string, force bool, stdout io.Writer) error {
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
	fmt.Fprintf(stdout, "配置已写入：%s\n", target)
	fmt.Fprintf(stdout, "下一步：\n")
	fmt.Fprintf(stdout, "  1. 编辑该文件，填入敏感项（数据源 DSN 等，或保持 ${ENV:default} 占位由启动环境注入）；\n")
	fmt.Fprintf(stdout, "  2. 确认目标数据库已按根目录 deploy_db.md 完成部署；\n")
	fmt.Fprintf(stdout, "  3. 以该目录启动服务：go run ./cmd/server -conf %s\n", filepath.Dir(target))
	return nil
}
