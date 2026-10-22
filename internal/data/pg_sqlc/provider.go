package pg_sqlc

import "github.com/google/wire"

// ProviderSet 是 pg_sqlc 能力（关系存储）的 Wire 依赖注入集合。
// NewSystemData 当前无消费方，wire 不实例化；留在集合里供将来系统库
// 消费方按用途直接注入 SystemData。
var ProviderSet = wire.NewSet(NewData, NewSystemData, NewUserRepo, NewSessionRepo, NewDiagnostics, NewSessionMaintenance)
