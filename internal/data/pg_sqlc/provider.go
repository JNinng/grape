package pg_sqlc

import "github.com/google/wire"

// ProviderSet 是 pg_sqlc 能力（关系存储）的 Wire 依赖注入集合。
var ProviderSet = wire.NewSet(NewData)
