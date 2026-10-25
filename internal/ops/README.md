# ops

运维出口：手写原生 `net/http` Handler，暴露设施诊断指标（当前为 `/metrics`），由 [server](../server/) 经 `srv.Handle` 直挂于业务中间件链外。端点免认证、只读；本包只依赖 `biz` 的设施只读契约（`FacilityDiagnostics` / `MetricSample`），不触达业务用例。

## 指标出口流向

```
data 各能力目录（如 pg_sqlc）
  │  实现 biz.FacilityDiagnostics（Facility / Pressure）
  ▼
biz 设施只读契约：[]MetricSample（Name / Help / Type / Labels / Value）
  │  Wire 注入 []biz.FacilityDiagnostics
  ▼
ops.NewMetricsHandler（独立 registry）
  │  facilityCollector：每次抓取逐设施并发 Pressure，单设施 5s 超时
  │  MetricSample → Prometheus const metric；采集失败按设施计数
  ▼
promhttp 文本格式 → /metrics
  │
  ▼
server：srv.Handle("/metrics", metrics)   链外、免认证、只读
```

- 采集按抓取触发，没有请求级业务数据；ops 只做格式映射，不理解样本语义，不做比率或二次计算；
- 单设施采集失败只影响自身样本输出与失败计数，其余设施照常输出；阈值与告警规则归监控配置层。

## 关键文件导览

| 文件 | 职责 |
| ---- | ---- |
| [metrics.go](metrics.go) | `NewMetricsHandler` 以独立 registry 承载设施诊断指标并经 `promhttp` 输出；`facilityCollector.Collect` 每次抓取并发调用各设施 `Pressure`（单设施 `diagnosticsTimeout` 5s），把 `biz.MetricSample` 经 `constMetric` 映射为 Prometheus const metric（标签按字典序固定，`MetricCounter` → Counter、其余 → Gauge），采集失败按设施累计为 `grape_diagnostics_scrape_errors_total`。 |
| [provider.go](provider.go) | Wire `ProviderSet`：导出 `NewMetricsHandler`。 |

## 接入要点

- 新指标族走 biz 设施契约：在 [biz/maintenance.go](../biz/maintenance.go) 扩充或复用 `FacilityDiagnostics` / `MetricSample`，由 data 能力目录实现 `Facility()` / `Pressure()`，再注册进 data 的 `NewFacilityDiagnostics` 聚合；ops 不写逐指标采集代码。
- 原始采集留在 data：阈值、比率、聚合与维护判定不下沉到出口，样本保持介质给出的原始形态。
- 端点保持只读、免认证并挂在业务中间件链外：经 `srv.Handle` 注册，不得改走 `srv.Route`；新增同类运维 / 探针端点按 [internal/README.md](../README.md) 的「手写端点归属」归入本包。

## 测试

现有覆盖：指标出口的样本映射与采集失败计数。
