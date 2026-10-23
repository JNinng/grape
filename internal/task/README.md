# task

后台异步任务入口（定时、事件、手动触发），与 [service](../service/) 平级构成第二类业务入口：无 DTO，直接以 DO 或设施契约调用 `biz` 声明的能力；任务只做编排，不实现业务规则。

## 分层角色

- 与 service 平行同构：上游是触发源（定时器、事件、手动调用），下游是 [biz](../biz/)。
- 只导入 `biz`，不经 `api/...`，因此没有 DTO，也没有查询协议转换。
- 当前的维护任务以 Kratos `transport.Server` 的身份接入：实现 `Start` / `Stop` 后作为普通 server 注册进 `kratos.Server(...)`，随应用启停；装配在 cmd 层完成。

## 数据流向

```
   触发（定时 / 事件 / 手动）
     │
     ▼
┌─────────┐
│  task   │  只调 biz 用例；无 DTO ↔ DO 转换
└─────────┘
     │  DO / 设施契约
     ▼
┌─────────┐
│   biz   │  用例与入参校验；声明仓储接口与设施契约
└─────────┘
     │  DO + dynq.Query
     ▼   repo IF：biz 声明，data 实现（依赖反转）
```

`MaintenanceTask` 直接消费 `[]biz.FacilityMaintenance`，按 [biz/maintenance.go](../biz/maintenance.go) 的契约取 `Facility()` 与 `Maintain(ctx)`，结果以 `biz.MaintenanceReport` 返回；维护判定（阈值、对象选择、跳过理由）属于设施实现，任务只读取报告并记录。

## 关键文件导览

| 文件 | 职责 |
| ---- | ---- |
| [maintenance.go](maintenance.go) | `MaintenanceTask`：通用维护任务。`NewMaintenanceTask` 接收 `[]biz.FacilityMaintenance` 与 logger，生产间隔固定为 `maintenanceInterval`（1h）；`Start` 先立即执行一轮再进入 ticker 循环并立即返回，`Stop` 取消循环、等待退出且幂等（尊重传入 ctx）；`runOnce` 逐设施调用 `Maintain`，成功时逐动作记 Info，单设施失败记 Warn 后继续；`newMaintenanceTaskWithInterval` 是测试用短间隔构造器。 |
| [provider.go](provider.go) | `ProviderSet` 聚合 `NewMaintenanceTask`，由 cmd 的 Wire 统一装配。 |

## 接入要点

新增任务：

1. 写构造函数，依赖只从 `biz` 用例或设施契约取（当前 `NewMaintenanceTask` 依赖 `[]biz.FacilityMaintenance` 与 `*slog.Logger`），并加入 `task.ProviderSet`；
2. 若任务需随应用启停，实现 `transport.Server` 的 `Start` / `Stop`，由 Wire 装配后作为 `kratos.Server(...)` 成员传入 [newApp](../../cmd/server/main.go)（当前见 [wire_gen.go](../../cmd/server/wire_gen.go)）；`Start` 立即返回、循环在后台运行，`Stop` 幂等且尊重传入 ctx；
3. 周期任务逐轮执行：单设施失败只记 Warn 并继续，不阻断同轮其他设施；
4. 改依赖后 `make generate` 刷新 wire 装配。

## 测试

现有覆盖：维护循环的启停、失败隔离与动作日志。
