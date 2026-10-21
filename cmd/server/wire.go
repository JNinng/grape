//go:build wireinject
// +build wireinject

// 构建标签保证本桩文件不参与最终编译。

package main

import (
	"log/slog"

	"grape/internal/biz"
	"grape/internal/conf"
	"grape/internal/data"
	"grape/internal/server"
	"grape/internal/service"

	"github.com/go-kratos/kratos/v3"
	"github.com/google/wire"
)

// wireApp 装配 Kratos 应用。
func wireApp(*conf.Server, *conf.Data, *conf.Auth, *slog.Logger) (*kratos.App, func(), error) {
	panic(wire.Build(server.ProviderSet, data.ProviderSet, biz.ProviderSet, service.ProviderSet, newApp))
}
