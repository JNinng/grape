// Package task 是后台异步任务入口（定时、事件、手动触发），与 service
// 平级构成第二类入口：task → biz → data 单向依赖，无 DTO，直接以契约
// 调用 biz 声明的能力面；任务自身不实现业务规则。
package task

import (
	"context"
	"log/slog"
	"time"

	"grape/internal/biz"
)

// maintenanceInterval 是维护任务的默认运行间隔。
const maintenanceInterval = 1 * time.Hour

// MaintenanceTask 是通用维护任务：周期性逐设施执行一轮判定与维护。
// 只认 []biz.FacilityMaintenance 契约切片，不感知会话/介质细节——
// 判定逻辑（阈值、对象选择、跳过理由）全部下沉在介质实现里。实现
// Kratos 的 transport.Server 接口，以普通 server 的身份注册进
// kratos.Server(...)：Start/Stop 即任务生命周期，随应用启停。
type MaintenanceTask struct {
	maintainers []biz.FacilityMaintenance
	logger      *slog.Logger
	// interval 是两轮维护的间隔；生产固定 maintenanceInterval，
	// 测试经内部构造器注入短间隔。
	interval time.Duration

	// cancel 与 done 由 Start 建立：cancel 触发循环退出（CancelFunc
	// 本身幂等），done 在循环退出后关闭，Stop 幂等等待它。
	cancel context.CancelFunc
	done   chan struct{}
}

// NewMaintenanceTask 构造通用维护任务。
func NewMaintenanceTask(maintainers []biz.FacilityMaintenance, logger *slog.Logger) *MaintenanceTask {
	return &MaintenanceTask{
		maintainers: maintainers,
		logger:      logger,
		interval:    maintenanceInterval,
	}
}

// newMaintenanceTaskWithInterval 是测试用内部构造器：注入短间隔，
// 免去为验证周期行为等待一个完整 maintenanceInterval。
func newMaintenanceTaskWithInterval(maintainers []biz.FacilityMaintenance, logger *slog.Logger, interval time.Duration) *MaintenanceTask {
	t := NewMaintenanceTask(maintainers, logger)
	t.interval = interval
	return t
}

// Start 启动维护循环：先立即执行一轮再进 ticker 循环（应用启动即获得
// 一次维护，不必等第一个间隔），随后立即返回，循环在后台运行。
func (t *MaintenanceTask) Start(ctx context.Context) error {
	ctx, t.cancel = context.WithCancel(ctx)
	t.done = make(chan struct{})
	go t.loop(ctx)
	return nil
}

// Stop 停止维护循环并等待退出。幂等：重复调用或未 Start 时立即返回
// （cancel 幂等、done 关闭后可重复读）。等待尊重传入的 ctx：超时/取消
// 时返回其错误，循环随后自行退出。
func (t *MaintenanceTask) Stop(ctx context.Context) error {
	if t.cancel != nil {
		t.cancel()
	}
	if t.done == nil {
		return nil
	}
	select {
	case <-t.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// loop 是维护循环主体：立即一轮，随后按 interval 周期执行，ctx 取消
// 即退出。
func (t *MaintenanceTask) loop(ctx context.Context) {
	defer close(t.done)
	t.runOnce(ctx)
	ticker := time.NewTicker(t.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			t.runOnce(ctx)
		}
	}
}

// runOnce 执行一轮维护：逐设施调用 Maintain，成功时逐动作输出 Info
// （设施、动作、对象、行数、是否执行与跳过理由），单设施失败输出 Warn
// 后继续下一设施——设施之间互不牵连，本轮其余设施照常维护。
func (t *MaintenanceTask) runOnce(ctx context.Context) {
	for _, m := range t.maintainers {
		facility := m.Facility()
		report, err := m.Maintain(ctx)
		if err != nil {
			t.logger.WarnContext(ctx, "facility maintenance failed",
				slog.String("facility", facility), slog.Any("error", err))
			continue
		}
		for _, a := range report.Actions {
			t.logger.InfoContext(ctx, "facility maintenance action",
				slog.String("facility", facility),
				slog.String("action", a.Kind),
				slog.String("target", a.Target),
				slog.Int64("rows", a.Rows),
				slog.Bool("performed", a.Performed),
				slog.String("note", a.Note),
			)
		}
	}
}
