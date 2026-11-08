# ops

运维出口：手写原生 `net/http` Handler，经 [../server/](../server/) 的
`srv.Handle` 直挂于业务中间件链外（免认证、只读）。本包只依赖 `biz` 的
设施只读契约（`FacilityDiagnostics` / `MetricSample`）与运行观测契约
（`AgentRunObserver`），不触达业务用例；端点注册与免认证口径见
[../README.md](../README.md#手写端点归属)。

## 指标三原则

- **只做格式映射**：样本保持原始值，出口不理解语义、不做比率、聚合或
  二次计算；阈值、告警与维护判定留在事实方与监控配置层。
- **抓取驱动**：出口收到 scrape 才调用 `Pressure`，无后台循环、无缓存；
  单设施一次采集 5s 超时，超时按采集失败计数。
- **失败隔离**：单设施或单样本失败只影响自身输出与该设施的
  `grape_diagnostics_scrape_errors_total`，其余设施照常输出。

设施样本的来源、指标清单与可见性边界见
[../data/pg_sqlc/diagnostics.md](../data/pg_sqlc/diagnostics.md)。

## agent 运行面

`RuntimeMetrics` 实现 `biz.AgentRunObserver`，是推送制：事实由 agent 适配层
在运行起止、限流拒绝与工具终态处经契约推送。标签基数有界——`tool` 取装配
层清单、`status` 取 biz 两值枚举，无用户/租户维度。nil 接收者 no-op，测试
装配省略 observer 时无需判空。

指标语义：`tool_calls_total` 含 panic 终态（`status=error`）与恢复路径的代理
执行；`tool_call_duration_seconds` 成功与失败终态都观测，失败通常更快，对比
时注意幸存者偏差。

## 关键文件导览

| 文件 | 职责 |
| ---- | ---- |
| [metrics.go](metrics.go) | `NewMetricsHandler`：独立 registry + `facilityCollector` 逐设施并发采集，样本经 `constMetric` 映射，失败按设施累计。 |
| [runtime_metrics.go](runtime_metrics.go) | `RuntimeMetrics`：四族 agent 运行面指标，实现 `biz.AgentRunObserver`。 |
| [provider.go](provider.go) | Wire `ProviderSet`：导出 `NewMetricsHandler` / `NewRuntimeMetrics`，并把后者绑定为 `biz.AgentRunObserver`。 |

## 测试

- 设施样本按出口契约原样映射（标签字典序、Counter/Gauge 分类）。
- 单设施采集失败只累计该设施计数，其余样本照常输出。
- agent 推送语义：活跃数配对增减、拒绝计数、工具计数/耗时分列。
- nil 接收者 no-op；指标集注册进出口 registry 后抓取可见。

## 接入要点

- 设施指标走 biz 契约：在 [../biz/maintenance.go](../biz/maintenance.go)
  扩充或复用契约，由 data 能力目录实现 `Facility()` / `Pressure()` 并注册进
  聚合，ops 不写逐指标采集代码。
- agent 指标走 biz 观测契约：事实方经 `biz.AgentRunObserver` 推送，新指标族
  扩充契约与 `RuntimeMetrics`，agent 层不直接持有 Prometheus 对象。
- 端点保持只读、免认证、链外：经 `srv.Handle` 注册，不得改走 `srv.Route`。
