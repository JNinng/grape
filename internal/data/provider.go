// Package data 是存储基础设施集合：关系存储、缓存、发号、分布式锁等，
// 一能力一子目录，目录内自包含（装配、实现、声明资产、生成产物）。
package data

import (
	"github.com/google/wire"

	pgsqlc "grape/internal/data/pg_sqlc"
)

// ProviderSet 是 data 层的 Wire 依赖注入集合，也是存储后端选择点：
// 当前按编译期静态方式选定 pg_sqlc 关系存储后端，引入第二个后端时
// 再定切换方案（多 injector / build tag / 运行时按 conf 分支）。
var ProviderSet = wire.NewSet(pgsqlc.ProviderSet)
