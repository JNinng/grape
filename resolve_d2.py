# -*- coding: utf-8 -*-
# D2 合并冲突解决（一次性脚本）
def resolve(path, repls):
    s = open(path, encoding='utf-8').read()
    for old, new in repls:
        assert old in s, (path, old[:80])
        s = s.replace(old, new)
    assert '<<<<<<<' not in s, path
    open(path, 'w', encoding='utf-8', newline='').write(s)
    print(path, 'OK')

# biz/provider.go：HEAD 的 redact 注册表 + D2 的 NewDocumentUsecase
resolve('internal/biz/provider.go', [('''<<<<<<< HEAD
	"grape/pkg/redact"
)

// ProviderSet 是 biz 层的 Wire 依赖注入集合。NewRedactRegistry 提供审计
// 表与复现视图（A3）共用的脱敏注册表（E1 单一代码点）：经 wire 装配为
// 同一实例，消费方按类别取用、不自行构造。
var ProviderSet = wire.NewSet(NewUserUsecase, NewSessionUsecase, NewAuthUsecase, NewAuditUsecase, NewRedactRegistry)

// NewRedactRegistry 提供标准脱敏注册表（四类骨架默认值，装配方按需
// Register 覆盖）。独立成函数是为了让 wire 装配层有明确的提供点注释：
// 日志 debug tracer（data 层）当前自带同默认值实例，收拢到同一实例的
// 重构待其消费方变更窗口进行。
func NewRedactRegistry() *redact.Registry {
	return redact.NewStandardRegistry()
}
=======
// ProviderSet 是 biz 层的 Wire 依赖注入集合。
var ProviderSet = wire.NewSet(NewUserUsecase, NewSessionUsecase, NewAuthUsecase, NewDocumentUsecase)
>>>>>>> feat/retrieval-d2''',
'''	"grape/pkg/redact"
)

// ProviderSet 是 biz 层的 Wire 依赖注入集合。NewRedactRegistry 提供审计
// 表与复现视图（A3）共用的脱敏注册表（E1 单一代码点）：经 wire 装配为
// 同一实例，消费方按类别取用、不自行构造。
var ProviderSet = wire.NewSet(NewUserUsecase, NewSessionUsecase, NewAuthUsecase, NewAuditUsecase, NewDocumentUsecase, NewRedactRegistry)

// NewRedactRegistry 提供标准脱敏注册表（四类骨架默认值，装配方按需
// Register 覆盖）。独立成函数是为了让 wire 装配层有明确的提供点注释：
// 日志 debug tracer（data 层）当前自带同默认值实例，收拢到同一实例的
// 重构待其消费方变更窗口进行。
func NewRedactRegistry() *redact.Registry {
	return redact.NewStandardRegistry()
}''')])

# internal/agent/README.md：HEAD 全保留（audit_hook 行 + provider 行含用户用例），provider 行加 embedder
resolve('internal/agent/README.md', [('''<<<<<<< HEAD
| [audit_hook.go](audit_hook.go) | 审计写入挂点 `AuditHook`（TODO E2 接口预留，不接线）：工具执行与 ack 决定两个写入点的形状，签名与 biz.AuditUsecase 同名方法一致（编译期断言固定）；Stack 注入点与调用接线归 C1 并入后的小修，写入失败只记日志、不中断运行。deny 决定按 ack 语义忽略 remember，接线时归一 `Remember=""` 再入参。 |
| [provider.go](provider.go) | Wire `ProviderSet`：`NewStack`（依赖会话存储/向量存储/用户用例；cleanup 释放 runner）、`NewSessionService`（框架 session/postgres：schema agent + agent_ 表前缀 + SkipDBInit，TTL 按 conf 注入，连接复用 master DSN——框架强制自建池）、`NewMemorySessionService`（单测/本地体验用进程内实现）。 |
=======
| [provider.go](provider.go) | Wire `ProviderSet`：`NewStack`（依赖会话存储/向量存储/用户用例/embedder；cleanup 释放 runner）、`NewSessionService`（框架 session/postgres：schema agent + agent_ 表前缀 + SkipDBInit，TTL 按 conf 注入，连接复用 master DSN——框架强制自建池）、`NewMemorySessionService`（单测/本地体验用进程内实现）。 |
>>>>>>> feat/retrieval-d2''',
'''| [audit_hook.go](audit_hook.go) | 审计写入挂点 `AuditHook`（TODO E2 接口预留，不接线）：工具执行与 ack 决定两个写入点的形状，签名与 biz.AuditUsecase 同名方法一致（编译期断言固定）；Stack 注入点与调用接线归 C1 并入后的小修，写入失败只记日志、不中断运行。deny 决定按 ack 语义忽略 remember，接线时归一 `Remember=""` 再入参。 |
| [provider.go](provider.go) | Wire `ProviderSet`：`NewStack`（依赖会话存储/向量存储/用户用例/embedder；cleanup 释放 runner）、`NewSessionService`（框架 session/postgres：schema agent + agent_ 表前缀 + SkipDBInit，TTL 按 conf 注入，连接复用 master DSN——框架强制自建池）、`NewMemorySessionService`（单测/本地体验用进程内实现）。 |''')])

# internal/biz/README.md：HEAD 的 audit 行 + E2 provider 行加 NewDocumentUsecase/NewRedactRegistry + D2 的 embedder/document 两行
resolve('internal/biz/README.md', [('''<<<<<<< HEAD
| [audit.go](audit.go)               | Agent 业务审计（TODO E2）：工具调用/授权确认的记录入参与完整行 DO、`AuditRepo` 契约（Append/List/Get/组合批量）与 `AuditUsecase`（入参校验、身份与 trace_id 从认证 ctx 提取、参数/结果摘要按 `pkg/redact` 的 tool_param/retrieval 类别脱敏——类别未登记放行原值但保留 4096 rune 截断兜底）；`ErrAuditInvalidArgument` / `ErrAuditNotFound`。 |
| [provider.go](provider.go)        | Wire `ProviderSet`：注册 `NewUserUsecase`、`NewSessionUsecase`、`NewAuthUsecase`、`NewAuditUsecase`、`NewRedactRegistry`（审计表与复现视图共用的脱敏注册表单点）。                                                                                                                                       |
=======
| [embedder.go](embedder.go)        | 文本向量化设施契约 `Embedder`（Embed/Dimensions，框架无关最小面）：维度纪律——声明维度须与 conf.vector.dimension 对账（摄取管线装配期校验），实现须在每次响应复核；`ErrEmbedderDisabled` 类型化错误。                      |
| [document.go](document.go)        | 摄取管线 `DocumentUsecase`（D2）：简版固定长度 + 重叠切分（rune 窗口，可替换点）→ 逐块 embed → 经 VectorStore 契约写入；幂等取「同来源覆盖」（切片 ID 确定性派生 `#index`，摄取前按 source_id 锚点清旧行）；切片元数据含来源标识与原文 rune 区间锚点；装配期校验 embedder 维度与 conf 一致（不一致拒绝构造）。`ErrDocumentSourceTooLong` 类型化错误。 |
| [provider.go](provider.go)        | Wire `ProviderSet`：注册 `NewUserUsecase`、`NewSessionUsecase`、`NewAuthUsecase`、`NewDocumentUsecase`。                                                                                                                 |
>>>>>>> feat/retrieval-d2''',
'''| [audit.go](audit.go)               | Agent 业务审计（TODO E2）：工具调用/授权确认的记录入参与完整行 DO、`AuditRepo` 契约（Append/List/Get/组合批量）与 `AuditUsecase`（入参校验、身份与 trace_id 从认证 ctx 提取、参数/结果摘要按 `pkg/redact` 的 tool_param/retrieval 类别脱敏——类别未登记放行原值但保留 4096 rune 截断兜底）；`ErrAuditInvalidArgument` / `ErrAuditNotFound`。 |
| [embedder.go](embedder.go)        | 文本向量化设施契约 `Embedder`（Embed/Dimensions，框架无关最小面）：维度纪律——声明维度须与 conf.vector.dimension 对账（摄取管线装配期校验），实现须在每次响应复核；`ErrEmbedderDisabled` 类型化错误。                      |
| [document.go](document.go)        | 摄取管线 `DocumentUsecase`（D2）：简版固定长度 + 重叠切分（rune 窗口，可替换点）→ 逐块 embed → 经 VectorStore 契约写入；幂等取「同来源覆盖」（切片 ID 确定性派生 `#index`，摄取前按 source_id 锚点清旧行）；切片元数据含来源标识与原文 rune 区间锚点；装配期校验 embedder 维度与 conf 一致（不一致拒绝构造）。`ErrDocumentSourceTooLong` 类型化错误。 |
| [provider.go](provider.go)        | Wire `ProviderSet`：注册 `NewUserUsecase`、`NewSessionUsecase`、`NewAuthUsecase`、`NewAuditUsecase`、`NewDocumentUsecase`、`NewRedactRegistry`（审计表与复现视图共用的脱敏注册表单点）。                                                                                                                                       |''')])

# internal/biz/README.md 第二处：现有覆盖合取
resolve('internal/biz/README.md', [('''<<<<<<< HEAD
现有覆盖：口令多算法校验与重哈希、会话令牌边界、登录编排与防枚举、审计用例的脱敏消费（类别命中/未登记放行但截断）、写入校验与批量转发。
=======
现有覆盖：口令多算法校验与重哈希、会话令牌边界、登录编排与防枚举；摄取管线（[document_test.go](document_test.go)，替身 embedder + 契约替身）：切分边界（短文/恰界/重叠窗/零重叠/多字节/空白）、维度失配构造期拒绝、同来源覆盖幂等与删除先行、来源标识与锚点元数据、身份缺失 fail-closed、embed 失败不触库、中途 Add 失败上报。
>>>>>>> feat/retrieval-d2''',
'''现有覆盖：口令多算法校验与重哈希、会话令牌边界、登录编排与防枚举、审计用例的脱敏消费（类别命中/未登记放行但截断）、写入校验与批量转发；摄取管线（[document_test.go](document_test.go)，替身 embedder + 契约替身）：切分边界（短文/恰界/重叠窗/零重叠/多字节/空白）、维度失配构造期拒绝、同来源覆盖幂等与删除先行、来源标识与锚点元数据、身份缺失 fail-closed、embed 失败不触库、中途 Add 失败上报。''')])

# internal/data/README.md：HEAD 的 audit_repo 句 + D2 的 openaiembedder 行
resolve('internal/data/README.md', [('''<<<<<<< HEAD
- [pg_sqlc](pg_sqlc/)：关系存储（PostgreSQL，pgx/v5）。装配与仓储实现在 `pg_sqlc/`，声明资产在 [pg_sqlc/sqlc](pg_sqlc/sqlc/)（schema 按业务域拆分于 `sqlc/schema/`，另有 role/rls/operate/expose/queries/seed 与 sqlc.yaml），生成产物在 `pg_sqlc/gen/`（禁止手改）；SQL 对象索引、权限模型与生成命令见 [sqlc/README.md](pg_sqlc/sqlc/README.md)。 pg_sqlc 内另有向量存储设施（biz.VectorStore 契约的 pgvector 介质，[vector_repo.go](pg_sqlc/vector_repo.go)）：DDL 是超级用户面 [pg_sqlc/sqlc/agent_vector.sql](pg_sqlc/sqlc/agent_vector.sql)（不进 sqlc catalog，存取 SQL 手写参数化），租户隔离由应用层强制（每条 SQL 带租户过滤、缺认证身份拒绝），装配期探针校验 conf.vector.dimension 与部署列一致。Agent 业务审计仓储在 [pg_sqlc/audit_repo.go](pg_sqlc/audit_repo.go)（TODO E2，biz.AuditRepo 契约的 PostgreSQL 实现）：两表 append-only + 租户 RLS（DDL 见 [pg_sqlc/sqlc/schema/95_agent_audit.sql](pg_sqlc/sqlc/schema/95_agent_audit.sql)，命名查询见 queries/agent_audit.sql），写入/查询经 IdentityTx，读侧 WHERE 带同源租户谓词兜底。
=======
- [pg_sqlc](pg_sqlc/)：关系存储（PostgreSQL，pgx/v5）。装配与仓储实现在 `pg_sqlc/`，声明资产在 [pg_sqlc/sqlc](pg_sqlc/sqlc/)（schema 按业务域拆分于 `sqlc/schema/`，另有 role/rls/operate/expose/queries/seed 与 sqlc.yaml），生成产物在 `pg_sqlc/gen/`（禁止手改）；SQL 对象索引、权限模型与生成命令见 [sqlc/README.md](pg_sqlc/sqlc/README.md)。 pg_sqlc 内另有向量存储设施（biz.VectorStore 契约的 pgvector 介质，[vector_repo.go](pg_sqlc/vector_repo.go)）：DDL 是超级用户面 [pg_sqlc/sqlc/agent_vector.sql](pg_sqlc/sqlc/agent_vector.sql)（不进 sqlc catalog，存取 SQL 手写参数化），租户隔离由应用层强制（每条 SQL 带租户过滤、缺认证身份拒绝），装配期探针校验 conf.vector.dimension 与部署列一致。
- [openaiembedder](openaiembedder/)：文本向量化设施（biz.Embedder 契约的 OpenAI 兼容端点实现，[openaiembedder.go](openaiembedder/openaiembedder.go)）：POST `{base_url}/embeddings`，base_url/api_key/model/timeout 取 conf.vector.embedding（决策 5 同口径，密钥经 ${ENV:...} 占位注入）；`vector.enabled=false` 返回禁用占位（调用按设施缺失拒绝）；维度在构造期取 conf.vector.dimension 并在每次响应复核，端点漂移调用点显式报错（维度链路三道校验：embedder 响应复核 / 摄取管线装配期与 conf 对账 / D1 探针 conf vs DDL）。
>>>>>>> feat/retrieval-d2''',
'''- [pg_sqlc](pg_sqlc/)：关系存储（PostgreSQL，pgx/v5）。装配与仓储实现在 `pg_sqlc/`，声明资产在 [pg_sqlc/sqlc](pg_sqlc/sqlc/)（schema 按业务域拆分于 `sqlc/schema/`，另有 role/rls/operate/expose/queries/seed 与 sqlc.yaml），生成产物在 `pg_sqlc/gen/`（禁止手改）；SQL 对象索引、权限模型与生成命令见 [sqlc/README.md](pg_sqlc/sqlc/README.md)。 pg_sqlc 内另有向量存储设施（biz.VectorStore 契约的 pgvector 介质，[vector_repo.go](pg_sqlc/vector_repo.go)）：DDL 是超级用户面 [pg_sqlc/sqlc/agent_vector.sql](pg_sqlc/sqlc/agent_vector.sql)（不进 sqlc catalog，存取 SQL 手写参数化），租户隔离由应用层强制（每条 SQL 带租户过滤、缺认证身份拒绝），装配期探针校验 conf.vector.dimension 与部署列一致。Agent 业务审计仓储在 [pg_sqlc/audit_repo.go](pg_sqlc/audit_repo.go)（TODO E2，biz.AuditRepo 契约的 PostgreSQL 实现）：两表 append-only + 租户 RLS（DDL 见 [pg_sqlc/sqlc/schema/95_agent_audit.sql](pg_sqlc/sqlc/schema/95_agent_audit.sql)，命名查询见 queries/agent_audit.sql），写入/查询经 IdentityTx，读侧 WHERE 带同源租户谓词兜底。
- [openaiembedder](openaiembedder/)：文本向量化设施（biz.Embedder 契约的 OpenAI 兼容端点实现，[openaiembedder.go](openaiembedder/openaiembedder.go)）：POST `{base_url}/embeddings`，base_url/api_key/model/timeout 取 conf.vector.embedding（决策 5 同口径，密钥经 ${ENV:...} 占位注入）；`vector.enabled=false` 返回禁用占位（调用按设施缺失拒绝）；维度在构造期取 conf.vector.dimension 并在每次响应复核，端点漂移调用点显式报错（维度链路三道校验：embedder 响应复核 / 摄取管线装配期与 conf 对账 / D1 探针 conf vs DDL）。''')])
print('ALL RESOLVED')
