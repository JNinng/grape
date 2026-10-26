# data

存储基础设施集合：一能力一子目录，目录内自包含（装配、实现、声明资产、生成产物）。能力目录实现 `biz` 声明的仓储接口，DO ↔ PO 转换、驱动错误映射在仓储内完成；接入规范见 [internal/README.md](../README.md) 的「能力目录接入规范」。

## 现有能力

- [pg_sqlc](pg_sqlc/)：关系存储（PostgreSQL，pgx/v5）。装配与仓储实现在 `pg_sqlc/`，声明资产在 [pg_sqlc/sqlc](pg_sqlc/sqlc/)（schema 按业务域拆分于 `sqlc/schema/`，另有 role/rls/operate/expose/queries/seed 与 sqlc.yaml），生成产物在 `pg_sqlc/gen/`（禁止手改）；SQL 对象索引、权限模型与生成命令见 [sqlc/README.md](pg_sqlc/sqlc/README.md)。 pg_sqlc 内另有向量存储设施（biz.VectorStore 契约的 pgvector 介质，[vector_repo.go](pg_sqlc/vector_repo.go)）：DDL 是超级用户面 [pg_sqlc/sqlc/agent_vector.sql](pg_sqlc/sqlc/agent_vector.sql)（不进 sqlc catalog，存取 SQL 手写参数化），租户隔离由应用层强制（每条 SQL 带租户过滤、缺认证身份拒绝），装配期探针校验 conf.vector.dimension 与部署列一致。向量仓储经检索留痕装饰器（D3，[retrieval_log.go](pg_sqlc/retrieval_log.go)）包装后对外供给：Search 记 query/top_k/命中（来源 tool）、Add/Update/DeleteByFilter 记摄取侧（来源 pipeline），留痕失败只记日志不中断检索，检索内容与过滤摘要落库前经 pkg/redact 脱敏（wire 组合：`NewVectorStoreSupply` → `NewLoggedVectorStore` → 框架适配器与摄取管线）。Agent 业务审计仓储在 [pg_sqlc/audit_repo.go](pg_sqlc/audit_repo.go)（TODO E2，biz.AuditRepo 契约的 PostgreSQL 实现）：两表 append-only + 租户 RLS（DDL 见 [pg_sqlc/sqlc/schema/95_agent_audit.sql](pg_sqlc/sqlc/schema/95_agent_audit.sql)，命名查询见 queries/agent_audit.sql），写入/查询经 IdentityTx，读侧 WHERE 带同源租户谓词兜底。检索留痕仓储在 [pg_sqlc/retrieval_repo.go](pg_sqlc/retrieval_repo.go)（D3，biz.RetrievalLogRepo 契约的 PostgreSQL 实现）：单表 append-only + 租户 RLS（DDL 见 [pg_sqlc/sqlc/schema/97_agent_retrieval_log.sql](pg_sqlc/sqlc/schema/97_agent_retrieval_log.sql)，命名查询见 queries/agent_retrieval_log.sql），写入/查询经 IdentityTx，读侧 WHERE 带同源租户谓词兜底。
- [openaiembedder](openaiembedder/)：文本向量化设施（biz.Embedder 契约的 OpenAI 兼容端点实现，[openaiembedder.go](openaiembedder/openaiembedder.go)）：POST `{base_url}/embeddings`，base_url/api_key/model/timeout 取 conf.vector.embedding（决策 5 同口径，密钥经 ${ENV:...} 占位注入）；`vector.enabled=false` 返回禁用占位（调用按设施缺失拒绝）；维度在构造期取 conf.vector.dimension 并在每次响应复核，端点漂移调用点显式报错（维度链路三道校验：embedder 响应复核 / 摄取管线装配期与 conf 对账 / D1 探针 conf vs DDL）。


## 诊断面

能力目录可另挂「诊断面」：实现 [biz/maintenance.go](../biz/maintenance.go) 声明的 `FacilityDiagnostics`，输出本设施的**原始**压力指标（不做比率与判定，阈值归监控配置层），经 data 的 `NewFacilityDiagnostics` 聚合、ops 的指标出口统一输出到 `/metrics`（server 直挂）；维护契约 `FacilityMaintenance` 按同一接缝消费。postgres 同时实现诊断面（[pg_sqlc/diagnostics.go](pg_sqlc/diagnostics.go)）与维护面（[pg_sqlc/session_repo.go](pg_sqlc/session_repo.go)）；缓存等新设施按同一模式接入（实现 + 追加进聚合点，不新增出口）。

## 测试

现有覆盖：驱动错误映射、debug 参数脱敏（经 [pkg/redact](../../pkg/redact/) 注册表：会话令牌哈希注入脱敏 + 凭据哈希语句按值形态脱敏）、会话门面与 RLS/@scope 纪律、诊断面采集、介质维护清理与索引膨胀判定、向量存储租户隔离与检索语义、Agent 审计租户隔离（RLS 启用与关闭两口径）与写入-查询往返、检索留痕装饰器（转发语义、留痕失败不中断、脱敏与结构兜底）与留痕表租户隔离（RLS 启用与关闭两口径）、检索→留痕行→document 反查链路与管线摄取留痕；集成测试由 `GRAPE_TEST_ADMIN_DSN` / `GRAPE_TEST_DSN`（会话等关系面）、`GRAPE_VECTOR_TEST_ADMIN_DSN` / `GRAPE_VECTOR_TEST_DSN`（向量面，专属库 grape_vector_d1_test）、`GRAPE_E2_TEST_ADMIN_DSN` / `GRAPE_E2_TEST_DSN`（审计面，专属库 grape_audit_e2_test）与 `GRAPE_D3_TEST_ADMIN_DSN` / `GRAPE_D3_TEST_DSN`（检索留痕面，专属库 grape_retrieval_d3_test）门控，未设置时自动跳过。
