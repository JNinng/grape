package service

import "github.com/google/wire"

// ProviderSet 是 service 层的 Wire 依赖注入集合。
// 当前暂无资源服务；新增资源时按 internal/README.md「新增资源速查」
// 在此注册服务构造函数。
var ProviderSet = wire.NewSet()
