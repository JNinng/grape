# -*- coding: utf-8 -*-
# D3 合并冲突解决（一次性脚本）
def resolve(path, repls):
    s = open(path, encoding='utf-8').read()
    for old, new in repls:
        assert old in s, (path, old[:80])
        s = s.replace(old, new)
    assert '<<<<<<<' not in s, path
    open(path, 'w', encoding='utf-8', newline='').write(s)
    print(path, 'OK')

# internal/agent/README.md 四处
resolve('internal/agent/README.md', [
 # hunk1: HEAD 的 gateway+vectorstore(钳制) + THEIRS 的 retrieval_context 行
 ('''<<<<<<< HEAD
| [gateway.go](gateway.go) | ack 许可网关：app 名与 AG-UI runner 一致，身份经 WithUserFunc 取同一编码，日志经 WithLogger 落 grape logger（ack 日志 API 无 ctx，不带 trace_id——全框架日志桥接见 [../telemetry/README.md](../telemetry/README.md)）；WithTiers 静态分级（读 allow/写 confirm）、WithStaticTools 服务恢复代执行；许可源经参数注入（C2 接授权存储，nil 供测试装配）、策略源恒弃权空实现（C3 接入点）。 |
| [vectorstore.go](vectorstore.go) | biz 向量契约 → 框架 `knowledge/vectorstore.VectorStore` 适配器（决策 6）：Add/Get/Update/Delete/Search 向量模式/DeleteByFilter/Count/GetMetadata 直映，Search 钳制 top_k 上限（20，D2 检索的唯一强制点）；keyword/hybrid 与 searchfilter DSL 无消费方、DeleteAll 归 super 管理口径、UpdateByFilter 无消费方，均显式 unsupported 并注明理由。 |
=======
| [vectorstore.go](vectorstore.go) | biz 向量契约 → 框架 `knowledge/vectorstore.VectorStore` 适配器（决策 6）：Add/Get/Update/Delete/Search 向量模式/DeleteByFilter/Count/GetMetadata 直映，Search 钳制 top_k 上限（20，D2 检索的唯一强制点）；keyword/hybrid 与 searchfilter DSL 无消费方、DeleteAll 归 super 管理口径、UpdateByFilter 无消费方，均显式 unsupported 并注明理由。 |
| [retrieval_context.go](retrieval_context.go) | D3 留痕的 ctx 事实接线：检索执行现场把会话 ID（框架 Invocation 携带）与查询文本放进 biz 中性载体（biz/data 不导入框架包，事实只能由能读到它们的本层传递），留痕装饰器从载体读取；事实缺失不伪造（留痕落 NULL）。 |
>>>>>>> feat/retrieval-log-d3''',
  '''| [gateway.go](gateway.go) | ack 许可网关：app 名与 AG-UI runner 一致，身份经 WithUserFunc 取同一编码，日志经 WithLogger 落 grape logger（ack 日志 API 无 ctx，不带 trace_id——全框架日志桥接见 [../telemetry/README.md](../telemetry/README.md)）；WithTiers 静态分级（读 allow/写 confirm）、WithStaticTools 服务恢复代执行；许可源经参数注入（C2 接授权存储，nil 供测试装配）、策略源恒弃权空实现（C3 接入点）。 |
| [vectorstore.go](vectorstore.go) | biz 向量契约 → 框架 `knowledge/vectorstore.VectorStore` 适配器（决策 6）：Add/Get/Update/Delete/Search 向量模式/DeleteByFilter/Count/GetMetadata 直映，Search 钳制 top_k 上限（20，D2 检索的唯一强制点）；keyword/hybrid 与 searchfilter DSL 无消费方、DeleteAll 归 super 管理口径、UpdateByFilter 无消费方，均显式 unsupported 并注明理由。 |
| [retrieval_context.go](retrieval_context.go) | D3 留痕的 ctx 事实接线：检索执行现场把会话 ID（框架 Invocation 携带）与查询文本放进 biz 中性载体（biz/data 不导入框架包，事实只能由能读到它们的本层传递），留痕装饰器从载体读取；事实缺失不伪造（留痕落 NULL）。 |'''),
 # hunk2: 取 HEAD 的 tool_audit/ack_audit/audit_hook(已接线) 行 + THEIRS 的 provider 行（含 NewVectorStore 装配说明），gateway 行已在 hunk1 保留故 THEIRS 侧的重复 gateway 行丢弃
 ('''<<<<<<< HEAD
| [tool_audit.go](tool_audit.go) | 工具执行终态审计 wrapper（E2 接口、C1 接线）：自建 CallableTool 包装器包住装配层全量工具（echo 含），Call 终态记工具调用行——调用 ID 取框架 ctx 注入（恢复路径代执行由决定审计面注入确认 ID）、会话 ID 取 Invocation、耗时自带计时、重试数记 0（框架不暴露，E3 评估）、参数/结果摘要传原文由 biz 侧脱敏；命中恢复路径捕获器只归集不直写；挂点错误与 panic 只记日志不中断。选型说明：框架 AfterTool 回调看不到恢复路径代执行且无耗时，wrapper 是两条执行路径的单一覆盖面。 |
| [ack_audit.go](ack_audit.go) | ack 决定路由审计面（E2 接口、C1 接线）：ackhttp.Mount 收具体网关无法装饰，在归属守卫内层拦截 `POST {prefix}/confirmation`——请求体得决定事实（与 ackhttp/归属守卫同一解码契约）、结算前 `gateway.Pending` 预读待确认负载（工具名/工具调用 ID，结算即删指针）、ctx 注入确认 ID 与捕获器供代执行归集、200 响应体得生效路径；live 只写确认行（工具行由执行终态自然落库）、recovery 与代执行工具行组合批量（无代执行单边批量 `RecordToolCallWithConfirmation(ctx, nil, conf)`）、deny 归一 `Remember=""`、decided_by 取网关回填的同一 userKey；无结算（400/404/409）不审计。 |
| [audit_hook.go](audit_hook.go) | 审计写入挂点 `AuditHook`（E2 接口、C1 已接线）：工具调用、授权确认与组合批量三个方法的形状，签名与 biz.AuditUsecase 同名方法一致（编译期断言固定）；Stack 经 `WithAuditHook` 可选注入（wire 生产装配传 biz.AuditUsecase），缺省 nil 即不审计（测试装配不受影响），写入失败只记日志、不中断运行。deny 决定按 ack 语义忽略 remember，接线时归一 `Remember=""` 再入参。 |
| [provider.go](provider.go) | Wire `ProviderSet`：`NewStack`（依赖会话存储/向量存储/用户用例/授权用例/审计用例/embedder；cleanup 释放 runner）、`NewSessionService`（框架 session/postgres：schema agent + agent_ 表前缀 + SkipDBInit，TTL 按 conf 注入，连接复用 master DSN——框架强制自建池）、`NewMemorySessionService`（单测/本地体验用进程内实现）。 |
=======
| [gateway.go](gateway.go) | ack 许可网关：app 名与 AG-UI runner 一致，身份经 WithUserFunc 取同一编码，日志经 WithLogger 落 grape logger（ack 日志 API 无 ctx，不带 trace_id——全框架日志桥接见 [../telemetry/README.md](../telemetry/README.md)）；WithTiers 静态分级（读 allow/写 confirm）、WithStaticTools 服务恢复代执行；许可源经参数注入（C2 接授权存储，nil 供测试装配）、策略源恒弃权空实现（C3 接入点）。 |
| [audit_hook.go](audit_hook.go) | 审计写入挂点 `AuditHook`（TODO E2 接口预留，不接线）：工具执行与 ack 决定两个写入点的形状，签名与 biz.AuditUsecase 同名方法一致（编译期断言固定）；Stack 注入点与调用接线归 C1 并入后的小修，写入失败只记日志、不中断运行。deny 决定按 ack 语义忽略 remember，接线时归一 `Remember=""` 再入参。 |
| [provider.go](provider.go) | Wire `ProviderSet`：`NewStack`（依赖会话存储/向量存储/用户用例/授权用例/embedder；cleanup 释放 runner）、`NewSessionService`（框架 session/postgres：schema agent + agent_ 表前缀 + SkipDBInit，TTL 按 conf 注入，连接复用 master DSN——框架强制自建池）、`NewMemorySessionService`（单测/本地体验用进程内实现）、`NewVectorStore`（biz 向量契约的框架适配器；wire 侧向量供给经 data 层留痕装饰器两跳组合，见 [../data/pg_sqlc/provider.go](../data/pg_sqlc/provider.go)）。 |
>>>>>>> feat/retrieval-log-d3''',
  '''| [tool_audit.go](tool_audit.go) | 工具执行终态审计 wrapper（E2 接口、C1 接线）：自建 CallableTool 包装器包住装配层全量工具（echo 含），Call 终态记工具调用行——调用 ID 取框架 ctx 注入（恢复路径代执行由决定审计面注入确认 ID）、会话 ID 取 Invocation、耗时自带计时、重试数记 0（框架不暴露，E3 评估）、参数/结果摘要传原文由 biz 侧脱敏；命中恢复路径捕获器只归集不直写；挂点错误与 panic 只记日志不中断。选型说明：框架 AfterTool 回调看不到恢复路径代执行且无耗时，wrapper 是两条执行路径的单一覆盖面。 |
| [ack_audit.go](ack_audit.go) | ack 决定路由审计面（E2 接口、C1 接线）：ackhttp.Mount 收具体网关无法装饰，在归属守卫内层拦截 `POST {prefix}/confirmation`——请求体得决定事实（与 ackhttp/归属守卫同一解码契约）、结算前 `gateway.Pending` 预读待确认负载（工具名/工具调用 ID，结算即删指针）、ctx 注入确认 ID 与捕获器供代执行归集、200 响应体得生效路径；live 只写确认行（工具行由执行终态自然落库）、recovery 与代执行工具行组合批量（无代执行单边批量 `RecordToolCallWithConfirmation(ctx, nil, conf)`）、deny 归一 `Remember=""`、decided_by 取网关回填的同一 userKey；无结算（400/404/409）不审计。 |
| [audit_hook.go](audit_hook.go) | 审计写入挂点 `AuditHook`（E2 接口、C1 已接线）：工具调用、授权确认与组合批量三个方法的形状，签名与 biz.AuditUsecase 同名方法一致（编译期断言固定）；Stack 经 `WithAuditHook` 可选注入（wire 生产装配传 biz.AuditUsecase），缺省 nil 即不审计（测试装配不受影响），写入失败只记日志、不中断运行。deny 决定按 ack 语义忽略 remember，接线时归一 `Remember=""` 再入参。 |
| [provider.go](provider.go) | Wire `ProviderSet`：`NewStack`（依赖会话存储/向量存储/用户用例/授权用例/审计用例/embedder；cleanup 释放 runner）、`NewSessionService`（框架 session/postgres：schema agent + agent_ 表前缀 + SkipDBInit，TTL 按 conf 注入，连接复用 master DSN——框架强制自建池）、`NewMemorySessionService`（单测/本地体验用进程内实现）、`NewVectorStore`（biz 向量契约的框架适配器；wire 侧向量供给经 data 层留痕装饰器两跳组合，见 [../data/pg_sqlc/provider.go](../data/pg_sqlc/provider.go)）。 |'''),
 # hunk3: HEAD 的 B3 测试段 + THEIRS 的 D3 段
 ('''<<<<<<< HEAD
模型层可靠性单测在 [llm_reliability_test.go](llm_reliability_test.go)（TODO B3，替身模型不发真实网络请求；超时项用回环 httptest）：全缺省装配断言（failover/hedge/logging 全关返回裸 OpenAI 适配器，零包装）、failover 主故障切换与全失败聚合报错、hedge 先到获胜与落败副本取消（delay 取 1ms 极短固定值，胜者判定由慢/快候选的数量级差决定，无需时钟抽象）、hedge+failover 组合次序（failover 在外、hedge 只包主模型）、超时经 conf 真实生效、回调日志面（请求/响应元数据、提示词/补全按 conf 开关且默认不记、超长截断、错误响应升 WARN、ctx 带 SpanContext 时 trace_id 由 extractor 展开、api_key 恒不入日志、failover 每次候选尝试各记一条）。配置模板守卫（新段默认关闭）在 [../../cmd/configinit/main_test.go](../../cmd/configinit/main_test.go)。
=======
D3 留痕事实载体单测在 [retrieval_context_test.go](retrieval_context_test.go)：会话 ID 从框架 Invocation ctx 提取、非框架链路不伪造、空白查询不注入。检索→留痕行的集成验证（query/top_k/命中与实际一致、RLS 两口径、留痕失败不影响检索、管线摄取来源=pipeline）在 [../data/pg_sqlc/retrieval_pg_test.go](../data/pg_sqlc/retrieval_pg_test.go)（`GRAPE_D3_TEST_ADMIN_DSN` 门控跳过，专属库 grape_retrieval_d3_test）。
>>>>>>> feat/retrieval-log-d3''',
  '''模型层可靠性单测在 [llm_reliability_test.go](llm_reliability_test.go)（TODO B3，替身模型不发真实网络请求；超时项用回环 httptest）：全缺省装配断言（failover/hedge/logging 全关返回裸 OpenAI 适配器，零包装）、failover 主故障切换与全失败聚合报错、hedge 先到获胜与落败副本取消（delay 取 1ms 极短固定值，胜者判定由慢/快候选的数量级差决定，无需时钟抽象）、hedge+failover 组合次序（failover 在外、hedge 只包主模型）、超时经 conf 真实生效、回调日志面（请求/响应元数据、提示词/补全按 conf 开关且默认不记、超长截断、错误响应升 WARN、ctx 带 SpanContext 时 trace_id 由 extractor 展开、api_key 恒不入日志、failover 每次候选尝试各记一条）。配置模板守卫（新段默认关闭）在 [../../cmd/configinit/main_test.go](../../cmd/configinit/main_test.go)。

D3 留痕事实载体单测在 [retrieval_context_test.go](retrieval_context_test.go)：会话 ID 从框架 Invocation ctx 提取、非框架链路不伪造、空白查询不注入。检索→留痕行的集成验证（query/top_k/命中与实际一致、RLS 两口径、留痕失败不影响检索、管线摄取来源=pipeline）在 [../data/pg_sqlc/retrieval_pg_test.go](../data/pg_sqlc/retrieval_pg_test.go)（`GRAPE_D3_TEST_ADMIN_DSN` 门控跳过，专属库 grape_retrieval_d3_test）。'''),
])

# internal/service/README.md：HEAD 侧无 agent_retrieval 行（空），直接追加 THEIRS 的行
p = 'internal/service/README.md'
s = open(p, encoding='utf-8').read()
i = s.find('<<<<<<< HEAD')
j = s.find('=======', i)
k = s.find('>>>>>>> feat/retrieval-log-d3', j)
theirs = s[j+len('=======\n'):k]
s = s[:i] + theirs.rstrip('\n') + '\n' + s[k+len('>>>>>>> feat/retrieval-log-d3'):]
assert '<<<<<<<' not in s
open(p, 'w', encoding='utf-8', newline='').write(s)
print(p, 'OK')
print('ALL RESOLVED')
