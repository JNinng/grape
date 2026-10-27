# ops

运维出口：手写原生 `net/http` Handler，暴露设施诊断指标与 agent 运行面指标
（当前为 `/metrics`），由 [server](../server/) 经 `srv.Handle` 直挂于业务中间
件链外。端点免认证、只读；本包只依赖 `biz` 的设施只读契约
（`FacilityDiagnostics` / `MetricSample`）与运行观测契约（`AgentRunObserver`），
不触达业务用例。

## 指标出口流向

```
data 各能力目录（如 pg_sqlc）
  │  实现 biz.FacilityDiagnostics（Facility / Pressure）
  ▼
biz 设施只读契约：[]MetricSample（Name / Help / Type / Labels / Value）
  │  Wire 注入 []biz.FacilityDiagnostics
  ▼
ops.NewMetricsHandler（独立 registry）      agent 适配层推事实（运行起止 /
  │ facilityCollector：逐设施并发采集        限流拒绝 / 工具终态，E3）
  │  MetricSample → Prometheus const metric  │
  │                                          ▼
  │                          RuntimeMetrics（实现 biz.AgentRunObserver，
  │                           wire.Bind 以该契约提供给 agent 层）
  ▼
promhttp 文本格式 → /metrics
  │
  ▼
server：srv.Handle("/metrics", metrics)   链外、免认证、只读
```

- 设施采集按抓取触发，没有请求级业务数据；出口只做格式映射，不理解样本
  语义，不做比率或二次计算；
- 单设施采集失败只影响自身样本输出与失败计数，其余设施照常输出；阈值与
  告警规则归监控配置层；
- agent 运行面指标是推送制（事实由 agent 适配层在运行起止、限流拒绝与
  工具终态处经 biz 契约推送），标签基数有界（工具清单个位数 × 两值状态），
  无用户/租户维度。

## 关键文件导览

| 文件 | 职责 |
| ---- | ---- |
| [metrics.go](metrics.go) | `NewMetricsHandler` 以独立 registry 承载设施诊断指标与 agent 运行面指标并经 `promhttp` 输出；`facilityCollector.Collect` 每次抓取并发调用各设施 `Pressure`（单设施 `diagnosticsTimeout` 5s），把 `biz.MetricSample` 经 `constMetric` 映射为 Prometheus const metric（标签按字典序固定，`MetricCounter` → Counter、其余 → Gauge），采集失败按设施累计为 `grape_diagnostics_scrape_errors_total`。 |
| [runtime_metrics.go](runtime_metrics.go) | `RuntimeMetrics` 是 agent 运行面指标集（E3）：实现 `biz.AgentRunObserver`，四族指标——`grape_agent_sse_active_connections`（gauge）、`grape_agent_run_limit_rejections_total`（counter）、`grape_agent_tool_calls_total`（counter，tool/status 分列）、`grape_agent_tool_call_duration_seconds`（histogram，tool 分列）；nil 接收者 no-op；检索耗时/LLM 计数/token/压缩等扩展点在头注备案接线落点。 |
| [provider.go](provider.go) | Wire `ProviderSet`：导出 `NewMetricsHandler` / `NewRuntimeMetrics`，并把 `*RuntimeMetrics` 绑定为 `biz.AgentRunObserver` 供 agent 层注入。 |

## 接入要点

- 设施指标走 biz 设施契约：在 [biz/maintenance.go](../biz/maintenance.go)
  扩充或复用 `FacilityDiagnostics` / `MetricSample`，由 data 能力目录实现
  `Facility()` / `Pressure()`，再注册进 data 的 `NewFacilityDiagnostics` 聚合；
  ops 不写逐指标采集代码。
- agent 运行面指标走 biz 观测契约：事实产生方（agent 适配层）经
  `biz.AgentRunObserver` 推送，新指标族扩充该契约与 `RuntimeMetrics`，
  不在 agent 层直接持有 Prometheus 对象（分层纪律：agent 只导入 conf/biz
  与框架包）。
- 原始采集留在事实方：阈值、比率、聚合与维护判定不下沉到出口，样本保持
  原始形态。
- 端点保持只读、免认证并挂在业务中间件链外：经 `srv.Handle` 注册，不得
  改走 `srv.Route`；新增同类运维 / 探针端点按 [internal/README.md](../README.md)
  的「手写端点归属」归入本包。

## 测试

现有覆盖：指标出口的样本映射与采集失败计数；RuntimeMetrics 的推送语义
（活跃数配对增减、拒绝计数、工具计数/耗时分列）、nil 接收者 no-op 与
注册进出口后的抓取可见性。
