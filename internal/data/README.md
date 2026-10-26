# data

存储基础设施集合：一能力一子目录，目录内自包含（装配、实现、声明资产、生成产物）。能力目录实现 `biz` 声明的仓储接口，DO ↔ PO 转换、驱动错误映射在仓储内完成；接入规范见 [internal/README.md](../README.md) 的「能力目录接入规范」。

## 现有能力

- [pg_sqlc](pg_sqlc/)：关系存储（PostgreSQL，pgx/v5）。装配与仓储实现在 `pg_sqlc/`，声明资产在 [pg_sqlc/sqlc](pg_sqlc/sqlc/)（schema 按业务域拆分于 `sqlc/schema/`，另有 role/rls/operate/expose/queries/seed 与 sqlc.yaml），生成产物在 `pg_sqlc/gen/`（禁止手改）；SQL 对象索引、权限模型与生成命令见 [sqlc/README.md](pg_sqlc/sqlc/README.md)。

## 诊断面

能力目录可另挂「诊断面」：实现 [biz/maintenance.go](../biz/maintenance.go) 声明的 `FacilityDiagnostics`，输出本设施的**原始**压力指标（不做比率与判定，阈值归监控配置层），经 data 的 `NewFacilityDiagnostics` 聚合、ops 的指标出口统一输出到 `/metrics`（server 直挂）；维护契约 `FacilityMaintenance` 按同一接缝消费。postgres 同时实现诊断面（[pg_sqlc/diagnostics.go](pg_sqlc/diagnostics.go)）与维护面（[pg_sqlc/session_repo.go](pg_sqlc/session_repo.go)）；缓存等新设施按同一模式接入（实现 + 追加进聚合点，不新增出口）。

## 测试

现有覆盖：驱动错误映射、debug 参数脱敏、会话门面与 RLS/@scope 纪律、诊断面采集、介质维护清理与索引膨胀判定；集成测试由 `GRAPE_TEST_ADMIN_DSN` / `GRAPE_TEST_DSN` 门控，未设置时自动跳过。
