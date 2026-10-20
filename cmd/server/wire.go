//go:build wireinject
// +build wireinject

// 构建标签保证本桩文件不参与最终编译。

package main

import (
	"log/slog"

	"grape/internal/conf"
	"grape/internal/server"

	"github.com/go-kratos/kratos/v3"
	"github.com/google/wire"
)

// wireApp 装配 Kratos 应用。data / biz / service 的 ProviderSet 暂为
// 空集或无消费者（仓储随首个资源落地），届时加入 wire.Build
// （见 internal/README.md「新增资源速查」）。
func wireApp(*conf.Server, *conf.Data, *slog.Logger) (*kratos.App, func(), error) {
	panic(wire.Build(server.ProviderSet, newApp))
}
