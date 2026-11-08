# pg_sqlc 诊断面（Diagnostics）

PostgreSQL 设施的只读压力指标采集，实现
[biz/maintenance.go](../../biz/maintenance.go) 声明的 `FacilityDiagnostics`
（[diagnostics.go](diagnostics.go)）。本文是该设施的专题展开，入口与
设施无关的聚合、出口说明见
[data/README.md](../README.md#诊断面与维护面)；诊断面只输出**原始值**，
不做比率、聚合指数或判定——阈值与告警归监控配置层。程序内维护决策不读
这些指标，走同文件的 `FacilityMaintenance` 契约（会话清理见
[session_repo.go](session_repo.go)）。

## 链路

```text
diagnostics.go（采集） → data.NewFacilityDiagnostics（聚合，../provider.go）
                       → ops.NewMetricsHandler（出口，../../ops/metrics.go）
                       → server 直挂 /metrics（HTTP 端口，业务中间件链外）
```

采集是**抓取驱动**的：出口收到 scrape 时才调用 `Pressure`，无后台循环、
无缓存。一轮采集从池取一条连接跑完几条只读查询（系统视图 + 池自带
统计），不开事务、不写任何数据；单设施采集超时 5 秒，超时按失败计数。
挂载位置、免认证与中间件链外语义见 [ops/README.md](../../ops/README.md)
与 [server/README.md](../../server/README.md)。

## 指标清单

前缀 `grape_pg_`（设施间不撞名），全部为 gauge：

| 指标 | 标签 | 含义 | 来源 |
|---|---|---|---|
| grape_pg_pool_connections | state=total/idle/acquired | 池内连接数（总数/空闲/借出） | `pgxpool.Stat()`，无 SQL |
| grape_pg_pool_connections_max | — | 池允许的最大连接数 | 同上，单列便于告警直接比对上限 |
| grape_pg_activity_sessions | state | 应用角色可见的后端会话数，按 `pg_stat_activity.state` 分组（NULL 归 unknown） | `pg_stat_activity` |
| grape_pg_transaction_longest_seconds | — | 可见后端中最长事务已运行秒数 | 同上，`clock_timestamp() - xact_start` 取最大 |
| grape_pg_queries_over_1s | — | 运行超过 1 秒的活跃查询数 | 同上 |
| grape_pg_table_live_tuples | table | 用户表活元组估算值 | `pg_stat_user_tables`（schemaname=public） |
| grape_pg_table_dead_tuples | table | 用户表死元组估算值 | 同上 |
| grape_pg_table_size_bytes | table | 表主 fork 磁盘体积 | `pg_relation_size(relid)` |
| grape_pg_table_index_size_bytes | table | 表全部索引总体积 | `pg_indexes_size(relid)` |
| grape_pg_index_size_bytes | table, index | 逐索引磁盘体积 | `pg_stat_user_indexes` + `pg_relation_size(indexrelid)` |

表/索引族覆盖 public 下**全部**用户表（含无 RLS 的全局配置表），未访问过
的表计数为零，体积始终有值。

**可见性边界**：应用角色未授予 `pg_monitor`。`pg_stat_activity` 对其他
后端的行仍可见，但查询文本、等待事件等敏感列显示为占位——因此会话计数、
最长事务与慢查询计数覆盖全实例，查询文本只见自身；这是应用级诊断的
预期形态，不视为配置缺陷。全库视角与敏感列归监控侧以超级用户/独立
采集器查询。

## 使用

出口挂在与业务同一个 HTTP 端口（`server.http.addr`，仓库示例配置为
`0.0.0.0:8000`）的 `/metrics` 路径上。该路由直挂、不经业务中间件链，
**免认证**——只应允许监控网络可达，勿暴露公网。

```bash
curl http://127.0.0.1:8000/metrics
```

输出为 Prometheus 文本格式（节选）：

```text
# HELP grape_pg_pool_connections Current connections in the pool by state (total, idle, acquired).
# TYPE grape_pg_pool_connections gauge
grape_pg_pool_connections{state="acquired"} 2
...
```

Prometheus 接入：

```yaml
scrape_configs:
  - job_name: grape
    metrics_path: /metrics
    static_configs:
      - targets: ["<grape-host>:8000"]
```

常用查询（阈值仅作示例，实际以监控配置层为准）：

```promql
# 池占用率
grape_pg_pool_connections{state="acquired"} / grape_pg_pool_connections_max
# 死元组最多的表（膨胀观察）
topk(5, grape_pg_table_dead_tuples)
# 长事务（超过 5 分钟）
grape_pg_transaction_longest_seconds > 300
# 出口自身健康：近 5 分钟该设施采集是否失败过
increase(grape_diagnostics_scrape_errors_total{facility="postgres"}[5m]) > 0
```

## 失败行为

- 数据库不可达或采集超时：该设施样本整体缺席，出口仍返回 200，
  `grape_diagnostics_scrape_errors_total{facility}` 计数递增（进程起累计，
  含单条样本映射失败）。
- 单设施失败不影响其他设施的样本输出。

## 扩展

- **新增设施**（缓存等）：在能力目录实现 `biz.FacilityDiagnostics`，把
  构造函数追加进 [../provider.go](../provider.go) 的
  `NewFacilityDiagnostics` 聚合即可，出口与 `/metrics` 不变。
- **新增指标**：在 `Pressure` 里追加 `biz.MetricSample`，命名沿用
  `grape_<设施>_` 前缀；Help 一句英文，随样本输出为 `# HELP` 行。

## 测试

- 出口映射：样本到 Prometheus 文本的映射、单设施故障隔离与失败计数
  累计。
- 路由挂载：`/metrics` 直挂且绕过业务中间件链。
- 集成采集：`GRAPE_TEST_ADMIN_DSN` / `GRAPE_TEST_DSN` 门控（未设置自动
  跳过），自建临时库后断言池、表、索引样本齐全。
