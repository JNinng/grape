package biz

import "context"

// MetricType 声明指标类别，只保留出口映射所需的最小集合。
type MetricType int

const (
	// MetricGauge 是可升可降的瞬时值（连接数、字节数、会话数等）。
	MetricGauge MetricType = iota
	// MetricCounter 是只增不减的累计值（自进程起的失败次数等）。
	MetricCounter
)

// MetricSample 是设施压力的单个原始样本：出口只做格式映射，不理解语义。
// 数值保持介质给出的原始形态，不做比率、不做聚合指数——阈值与告警
// 归监控配置层，不在诊断面落地。
type MetricSample struct {
	// Name 是指标全名（约定设施前缀，如 grape_pg_pool_connections）。
	Name string
	// Help 是随指标输出的单句说明。
	Help string
	// Type 区分瞬时值与累计值。
	Type MetricType
	// Labels 是维度标签（如 state/table/index），可为 nil。
	Labels map[string]string
	// Value 是原始采样值。
	Value float64
}

// FacilityDiagnostics 是单个设施的只读诊断面：暴露该设施的原始压力
// 指标。设施无关的统一词汇表——server 出口与后续 task 均只认本接口，
// 不感知具体介质；新设施（缓存等）落地时按同一契约接入。
type FacilityDiagnostics interface {
	// Facility 返回设施名（如 "postgres"），用于指标归属与错误归因。
	Facility() string
	// Pressure 采集一轮原始压力样本。实现必须尊重 ctx 的超时与取消。
	Pressure(ctx context.Context) ([]MetricSample, error)
}

// MaintenanceAction 是一次维护动作的结果记录。
type MaintenanceAction struct {
	// Kind 是动作类型（如 "vacuum"），词汇由介质实现定义。
	Kind string
	// Target 是动作对象（如表名）。
	Target string
	// Rows 是受影响行数，动作不涉及行数时为 0。
	Rows int64
	// Performed 表示动作已实际执行；false 表示判定后跳过。
	Performed bool
	// Note 是补充说明（跳过原因、执行细节）。
	Note string
}

// MaintenanceReport 是一轮维护运行的动作汇总。
type MaintenanceReport struct {
	Actions []MaintenanceAction
}

// FacilityMaintenance 是单个设施的维护面：判定逻辑（阈值、对象选择）
// 完全下沉到介质实现，维护任务只认本契约、不读原始指标。本任务只定
// 契约，具体维护由后续任务落地。
type FacilityMaintenance interface {
	// Facility 返回设施名（如 "postgres"）。
	Facility() string
	// Maintain 执行一轮判定与维护，返回逐动作报告。
	Maintain(ctx context.Context) (MaintenanceReport, error)
}
