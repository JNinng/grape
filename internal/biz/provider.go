package biz

import "github.com/google/wire"

// ProviderSet 是 biz 层的 Wire 依赖注入集合。
// 当前暂无资源用例；新增资源时按 internal/README.md「新增资源速查」
// 在此注册用例构造函数。
var ProviderSet = wire.NewSet()
