package main

import (
	"flag"
	"io"
	"log/slog"
	"os"

	"grape/internal/conf"
	"grape/internal/encoding/jsoncodec"
	"grape/internal/task"

	"github.com/go-kratos/kratos/contrib/otel/v3/tracing"
	"github.com/go-kratos/kratos/v3"
	"github.com/go-kratos/kratos/v3/config"
	"github.com/go-kratos/kratos/v3/config/env"
	"github.com/go-kratos/kratos/v3/config/file"
	"github.com/go-kratos/kratos/v3/log"
	"github.com/go-kratos/kratos/v3/transport/grpc"
	"github.com/go-kratos/kratos/v3/transport/http"

	_ "go.uber.org/automaxprocs"
	"gopkg.in/natefinch/lumberjack.v2"
)

// go build -ldflags "-X main.Version=x.y.z"
var (
	// Name 是编译产物的服务名。
	Name string
	// Version 是编译产物的版本号。
	Version string
	// flagconf 是配置文件路径的命令行参数。
	flagconf string

	id, _ = os.Hostname()
)

func init() {
	flag.StringVar(&flagconf, "conf", "../../configs", "config path, eg: -conf config.yaml")
}

// newApp 组装 Kratos 应用。task 是后台维护任务，以 transport.Server
// 的身份随应用启停。
func newApp(logger *slog.Logger, gs *grpc.Server, hs *http.Server, mt *task.MaintenanceTask) *kratos.App {
	return kratos.New(
		kratos.ID(id),
		kratos.Name(Name),
		kratos.Version(Version),
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
	flag.Parse()
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
		slog.String("service.version", Version),
	)
	log.SetDefault(logger)

	app, cleanup, err := wireApp(bc.Server, bc.Data, bc.Auth, bc.Agent, bc.Vector, bc.Telemetry, bc.Oss, bc.DocParse, logger)
	if err != nil {
		panic(err)
	}
	defer cleanup()

	if err := app.Run(); err != nil {
		panic(err)
	}
}
