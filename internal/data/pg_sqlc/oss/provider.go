package oss

import (
	"github.com/google/wire"
)

// ProviderSet 是内嵌对象存储设施的 Wire 依赖注入集合。NewHandler 在
// oss.enabled=false 或 provider 非 local 时返回 nil Handler，挂载点按
// nil 跳过（server.ReservedAPIPrefixes 恒保留 /s3 前缀的 API 语义）。
var ProviderSet = wire.NewSet(NewHandler)
