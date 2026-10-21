package pg_sqlc

import (
	"math/rand/v2"
	"time"
)

// idEpoch 是发号用的毫秒纪元（2026-01-01 UTC），保证高位时间戳左移
// 后在 63 位内可承载约 69 年。
var idEpoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// newID 生成 int64 主键：毫秒时间戳（自 idEpoch）左移 22 位拼 22 位
// 随机低位。体系表主键无数据库默认值，由应用层发号；本实现是过渡
// 方案（同毫秒碰撞概率约 1/4M，单实例可忽略），独立发号能力目录落地
// 后按 internal/README「能力目录接入规范」替换。
func newID() int64 {
	return time.Since(idEpoch).Milliseconds()<<22 | rand.Int64N(1<<22)
}
