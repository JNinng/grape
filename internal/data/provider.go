// Package data 是存储基础设施集合：关系存储、缓存、发号、分布式锁等，
// 一能力一子目录，目录内自包含（装配、实现、声明资产、生成产物）。
package data

import (
	"github.com/google/wire"

	"grape/internal/biz"
	openidembedder "grape/internal/data/openaiembedder"
	pgsqlc "grape/internal/data/pg_sqlc"
)

// ProviderSet 是 data 层的 Wire 依赖注入集合，也是存储后端选择点：
// 当前按编译期静态方式选定 pg_sqlc 关系存储后端，引入第二个后端时
// 再定切换方案（多 injector / build tag / 运行时按 conf 分支）。
var ProviderSet = wire.NewSet(pgsqlc.ProviderSet, openidembedder.ProviderSet, NewFacilityDiagnostics, NewFacilityMaintenance)

// NewFacilityDiagnostics 聚合各能力目录的诊断面实现（biz.FacilityDiagnostics）。
// data 是设施聚合点：新设施（如缓存）落地诊断面时在此追加参数与切片元素，
// 消费方（server 出口、task）只依赖设施无关的契约切片。
func NewFacilityDiagnostics(postgres biz.FacilityDiagnostics) []biz.FacilityDiagnostics {
	return []biz.FacilityDiagnostics{postgres}
}

// NewFacilityMaintenance 聚合各能力目录的维护面实现（biz.FacilityMaintenance）。
// 与 NewFacilityDiagnostics 同形态：新设施（如缓存）落地维护面时在此
// 追加参数与切片元素，消费方（task）只依赖设施无关的契约切片。
func NewFacilityMaintenance(postgres biz.FacilityMaintenance) []biz.FacilityMaintenance {
	return []biz.FacilityMaintenance{postgres}
}
