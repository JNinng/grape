package main

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"grape/internal/cmd/configinit"
	"grape/internal/cmd/secretctl"
	cmdversion "grape/internal/cmd/version"
	"grape/internal/conf"
	"grape/internal/encoding/jsoncodec"
	"grape/internal/task"
	"grape/pkg/version"

	"github.com/go-kratos/kratos/contrib/otel/v3/tracing"
	"github.com/go-kratos/kratos/v3"
	"github.com/go-kratos/kratos/v3/config"
	"github.com/go-kratos/kratos/v3/config/env"
	"github.com/go-kratos/kratos/v3/config/file"
	"github.com/go-kratos/kratos/v3/log"
	"github.com/go-kratos/kratos/v3/transport/grpc"
	"github.com/go-kratos/kratos/v3/transport/http"

	"go.uber.org/automaxprocs/maxprocs"
	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	// Name 是编译产物的服务名，经 go build -ldflags -X 注入；版本号、
	// commit、构建时间的注入契约见 pkg/version，version 子命令与 Kratos
	// 元信息统一取自该包。
	Name string
	// flagconf 是配置文件路径的命令行参数。
	flagconf string

	id, _ = os.Hostname()
)

// usage 输出命令总用法；写 stdout 表示主动求助，写 stderr 表示用法错误。
func usage(w io.Writer) {
	fmt.Fprintf(w, `usage:
  server [-conf <dir>]            run the service (default)
  server serve [-conf <dir>]      run the service (explicit)
  server version                  print build information
  server configinit [-dir <dir>] [-force]
                                  write initial config.yaml
  server secretctl algorithms     list supported key algorithm kinds
  server secretctl keygen <kind>  generate key material for the secret registry
`)
}

// run 分发子命令（实现收在 internal/cmd/ 各子包）：version / configinit /
// secretctl / serve。无参数或首参为 flag（如 -conf）时按 serve 处理，保持
// 历史用法 `go run ./cmd/server -conf <目录>` 不变。
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "serve":
			return runServe(args[1:])
		case "version":
			return cmdversion.Run(args[1:], stdout, stderr)
		case "configinit":
			return configinit.Run(args[1:], stdout, stderr)
		case "secretctl":
			return secretctl.Run(args[1:], stdout, stderr)
		case "help", "-h", "--help":
			usage(stdout)
			return 0
		default:
			if !strings.HasPrefix(args[0], "-") {
				fmt.Fprintf(stderr, "server: unknown command %q\n", args[0])
				usage(stderr)
				return 2
			}
		}
	}
	return runServe(args)
}

// runServe 启动 Kratos 服务。配置加载与装配失败直接 panic（配置加载期无
// 日志依赖，fail-fast），进程以 panic 退出码结束。
func runServe(args []string) int {
	// 容器 CPU 配额下修正 GOMAXPROCS（按 cgroup 配额）。只在 serve 路径
	// 执行：version/configinit/secretctl 是短命命令，无需调度器校正，
	// 也不应在输出中混入 maxprocs 日志。Set 默认静默，失败不阻断启动。
	_, _ = maxprocs.Set()

	fs := flag.NewFlagSet("server", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&flagconf, "conf", "../../configs", "config path, eg: -conf config.yaml")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	// HTTP JSON 必须按 protobuf JSON 映射编解码，默认 json codec 会
	// 产出与 api 契约不一致的字段形态。
	jsoncodec.Install()
	// 配置先于日志装配：日志落盘路径来自 log.file，未配置仅输出标准
	// 输出。配置加载期无日志依赖（失败直接 panic）。
	c := config.New(
		config.WithSource(
			file.NewSource(flagconf),
			env.NewSource("KRATOS"),
		),
	)
	defer c.Close()

	if err := c.Load(); err != nil {
		panic(err)
	}

	var bc conf.Bootstrap
	if err := c.Scan(&bc); err != nil {
		panic(err)
	}

	// 密钥登记表在装配期统一校验并固化为只读注册表：任一组/版本/材料
	// 不合法即拒绝启动（fail-fast）。注册表启动期固化，改配置需重启。
	secretRegistry, err := conf.NewSecretRegistry(bc.Secret)
	if err != nil {
		panic(err)
	}

	// 日志 sink：标准输出恒在；log.file 配置了路径时额外落盘，滚动与
	// 保留策略由 lumberjack 承担（0 值项沿用其默认）。
	sinks := []io.Writer{os.Stdout}
	if c := bc.GetLog(); c.GetFile() != "" {
		w := &lumberjack.Logger{
			Filename:   c.GetFile(),
			MaxSize:    int(c.GetMaxSizeMb()),
			MaxBackups: int(c.GetMaxBackups()),
			MaxAge:     int(c.GetMaxAgeDays()),
			Compress:   c.GetCompress(),
			LocalTime:  true,
		}
		defer w.Close()
		sinks = append(sinks, w)
	}
	logger := log.NewLogger(
		slog.NewJSONHandler(io.MultiWriter(sinks...), &slog.HandlerOptions{
			AddSource: true,
			Level:     slog.LevelInfo,
		}),
		log.WithExtractor(tracing.TraceAttrs),
	).With(
		slog.String("service.id", id),
		slog.String("service.name", Name),
		slog.String("service.version", version.Version),
	)
	log.SetDefault(logger)

	app, cleanup, err := wireApp(bc.Server, bc.Data, bc.Auth, bc.Agent, bc.Vector, bc.Telemetry, bc.Oss, bc.DocParse, secretRegistry, logger)
	if err != nil {
		panic(err)
	}
	defer cleanup()

	if err := app.Run(); err != nil {
		panic(err)
	}
	return 0
}

// newApp 组装 Kratos 应用。task 是后台维护任务，以 transport.Server
// 的身份随应用启停。
func newApp(logger *slog.Logger, gs *grpc.Server, hs *http.Server, mt *task.MaintenanceTask) *kratos.App {
	return kratos.New(
		kratos.ID(id),
		kratos.Name(Name),
		kratos.Version(version.Version),
		kratos.Metadata(map[string]string{}),
		kratos.Logger(logger),
		kratos.Server(
			gs,
			hs,
			mt,
		),
	)
}

// defaultTenantID 提供登录默认租户（auth.default_tenant_id）。
// 配置到领域的映射收在装配点：biz 与 service 均不导入 conf。
func defaultTenantID(c *conf.Auth) string {
	return c.GetDefaultTenantId()
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
