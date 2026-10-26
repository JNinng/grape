//go:build wireinject
// +build wireinject

// 构建标签保证本桩文件不参与最终编译。

package main

import (
	"log/slog"

	"grape/internal/agent"
	"grape/internal/biz"
	"grape/internal/conf"
	"grape/internal/data"
	"grape/internal/middleware"
	"grape/internal/ops"
	"grape/internal/server"
	"grape/internal/service"
	"grape/internal/task"
	"grape/internal/telemetry"

	"github.com/go-kratos/kratos/v3"
	"github.com/google/wire"
)

// wireApp 装配 Kratos 应用。
func wireApp(*conf.Server, *conf.Data, *conf.Auth, *conf.Agent, *conf.Vector, *conf.Telemetry, *slog.Logger) (*kratos.App, func(), error) {
	panic(wire.Build(server.ProviderSet, middleware.ProviderSet, ops.ProviderSet, data.ProviderSet, biz.ProviderSet, service.ProviderSet, agent.ProviderSet, task.ProviderSet, telemetry.ProviderSet, defaultTenantID, newApp))
}
