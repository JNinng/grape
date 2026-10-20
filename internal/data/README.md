# Data

存储基础设施集合：一能力一子目录，目录内自包含（装配、实现、声明资产、生成产物）。能力目录实现 `biz` 声明的仓储接口，DO ↔ PO 转换、驱动错误映射在仓储内完成；接入规范见 [internal/README.md](../README.md) 的「能力目录接入规范」。

## 现有能力

- [pg_sqlc](pg_sqlc/)：关系存储（PostgreSQL，pgx/v5）。装配与仓储实现在 `pg_sqlc/`，声明资产（schema.sql、queries.sql、sqlc.yaml）在 [pg_sqlc/sqlc](pg_sqlc/sqlc/)，生成产物在 `pg_sqlc/gen/`（禁止手改）；sqlc 生成命令与产物说明见项目根 [README.md](../../README.md)。
