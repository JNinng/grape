# task

后台异步任务入口，与 [service](../service/) 平级构成第二类业务入口：无 DTO，
直接以 DO 或设施契约调用 biz 声明的能力。任务只做编排与生命周期管理，
判定逻辑与业务规则留在 biz 或设施实现；当前只有周期维护任务一种。

## 关键约束

- **只依赖 biz**：不经 `api/...`，没有 DTO 与查询协议转换；跨界对象就是
  biz 的契约类型。
- **只调度不判定**：维护任务消费 `[]biz.FacilityMaintenance`，取
  `Facility()` 与 `Maintain(ctx)`，读取 `biz.MaintenanceReport` 并记录；
  阈值、对象选择、跳过理由属于设施实现。
- **以 `transport.Server` 接入**：实现 `Start` / `Stop` 即任务生命周期，
  作为普通 server 注册进 `kratos.Server(...)`，由 cmd 装配。
- **失败隔离**：单设施失败只记 Warn 并继续，不阻断同轮其他设施。
- **新增任务沿用同一形态**：依赖只从 biz 用例或设施契约取，加入
  `task.ProviderSet`；需随应用启停者实现 `transport.Server`，改依赖后
  `make generate`。

## 关键文件导览

| 文件 | 职责 |
| ---- | ---- |
| [maintenance.go](maintenance.go) | `MaintenanceTask`：生产间隔 1h；`Start` 先立即执行一轮再进 ticker 循环并立即返回，`Stop` 取消循环、等待退出且幂等（尊重传入 ctx）；`runOnce` 单设施失败记 Warn 后继续。 |
| [provider.go](provider.go) | `ProviderSet` 聚合 `NewMaintenanceTask`，由 cmd 的 Wire 统一装配。 |

## 测试

- 周期与启停：`Start` 立即执行一轮、短间隔下持续执行，`Stop` 后不再有
  新一轮。
- 幂等停止：重复 `Stop` 与未 `Start` 直接 `Stop` 均立即返回。
- 失败隔离：前一设施失败不阻断后续设施的同轮维护。

## 相关文档

- 分层与依赖方向见
  [internal/README.md](../README.md#分层与数据流向)。
- 设施维护契约见 [biz/maintenance.go](../biz/maintenance.go)。
