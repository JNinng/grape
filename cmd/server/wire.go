//go:build wireinject
// +build wireinject

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
	"grape/pkg/secret"

	"github.com/go-kratos/kratos/v3"
	"github.com/google/wire"
)

// wireApp 装配 Kratos 应用。
func wireApp(*conf.Server, *conf.Data, *conf.Auth, *conf.Agent, *conf.Vector, *conf.Telemetry, *conf.OSS, *conf.DocParse, *secret.Registry, *slog.Logger) (*kratos.App, func(), error) {
	panic(wire.Build(server.ProviderSet, middleware.ProviderSet, ops.ProviderSet, data.ProviderSet, biz.ProviderSet, service.ProviderSet, agent.ProviderSet, task.ProviderSet, telemetry.ProviderSet, defaultTenantID, newApp))
}
