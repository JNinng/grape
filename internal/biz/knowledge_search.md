# 知识检索（Knowledge Search）

`KnowledgeSearchUsecase` 是租户内知识检索的统一入口：前端 console 检索
（`internal/service/knowledge.go`，展平回包单元的 Hits）与 agent 检索工具
（`internal/agent/tools/tool_search.go`，逐单元围栏出参；列库工具与
`VisibleLibraries` 共用同一可见库解析实现）都经本用例。混合检索的全部编排
——召回、融合、重排、回包聚合——只在用例层，存储两路互不感知；本文是
管线语义、参数纪律、错误降级与留痕口径的权威说明。代码落点：

| 文件 | 内容 |
| --- | --- |
| [knowledge_search.go](knowledge_search.go) | 用例与编排：入参归一、可见库解析、召回、RRF 融合、重排、错误语义 |
| [knowledge_search_pack.go](knowledge_search_pack.go) | 回包聚合（pack）：来源分档、全量块拉取、预算装配、token 估算 |
| [knowledge_search_stages.go](knowledge_search_stages.go) | 分阶段留痕（stages）：阶段元素形态、收集器、脱敏截断、留痕写入 |

依赖契约见 [vectorstore.go](vectorstore.go)（向量/词法两路与 `List`）、
[embedder.go](embedder.go)、[reranker.go](reranker.go)、
[retrieval_log.go](retrieval_log.go)；摄取侧的切片 metadata 口径见
[document.go](document.go)。

## 管线总览

```
入参归一 → 可见库集合解析 → 默认嵌入空间解析
  └─ 召回集（主查询 + 变体，逐条）
       ├─ 向量路 Search（Limit = 2×top_k）      失败 → 整次失败
       └─ 词法路 SearchLexical（同 Limit）      失败 → 降级仅向量路
  └─ RRF 融合（Σ 1/(k+rank)，取前 2×top_k）
  └─ 重排（可选层，对 2×top_k 候选打分重排）    失败/未配置 → 降级融合序
  └─ 取 top_k → 回包聚合（pack，4K token 预算） 拉取失败 → 降级仅命中块直回
  └─ 完成处自写一行完整留痕（stages 承载阶段时间线）
```

正确性依赖失败让整次检索失败（可见库解析、查询向量化、向量路）；质量增强
失败按降级继续并记服务日志（词法路、重排层、来源全量块拉取）。设施未启用
（`ErrEmbedderDisabled` / `ErrVectorStoreDisabled`）原样透传，不降级为空
结果，由入口层映射为 503。

## 入参与参数纪律

`KnowledgeSearchQuery`：query trim 后非空且 ≤512 rune；`LibraryID` 0 检索
全部可见库、>0 必须命中可见集合（不可见与不存在统一 `ErrLibraryNotFound`，
不区分存在性）、<0 非法；`TopK` 缺省 5、上限 20、负值非法。`Variants` 是
multi-query 的 biz 召回入口——改写由 agent 侧 LLM 决策，用例只防滥用：
逐条 trim（空白条目跳过不占名额）、同 512 上限、条数上限 4，主查询与变体
共同构成召回集、各自独立召回后融合。

参数属代码级默认，不进 conf（与 top_k 硬钳同纪律）：

| 常量 | 值 | 语义 |
| --- | --- | --- |
| `DefaultKnowledgeSearchFusionRRFK` | 60 | RRF 的 k |
| `KnowledgeSearchRecallFactor` | 2 | 每路召回倍数（2×top_k，给融合与重排留边界召回） |
| `KnowledgeSearchPackBudgetTokens` | 4096 | 回包 token 预算（确定性估算，非精确分词） |
| `KnowledgeSearchPackMaxSources` | 5 | 来源数上限，超出整源丢弃（按最高分序） |
| `KnowledgeSearchPackSectionMinHits` | 2 | 同节命中达此数才整节返回 |
| `KnowledgeSearchPackWindowGap` | 2 | 相邻窗口的命中索引差上限 |
| `KnowledgeSearchPackDocumentMaxChunks` | 8 | 整文档档的全量块数上限 |
| `KnowledgeSearchPackListGuardChunks` | 1024 | 来源全量块拉取防线，超线降级直回 |

## 固定检索条件与可见性

向量路与词法路共享同一 conditions 口径与文档宇宙（同租户同嵌入空间）：
条目参与（`doc_status`）+ 有效期 `[valid_from_unix, valid_to_unix)` + 可见
库集合 `kb_id IN` + 排除重摄淘汰行（superseded is-null）。时间为秒级 unix，
与摄取侧写入口径一致。

可见库集合每次检索现场解析（权限运行时可变，不做长生命周期缓存）；解析经
`KnowledgeLibraryDirectory` 接缝，存储层按调用方身份求交 `fn_accessible_*`
三件套——检索与列库工具共用同一实现，避免「列得出、查不到」的口径漂移。
身份缺失 fail-closed（`ErrSessionInvalid`）；无可见库直接返回空集，不触达
嵌入与向量设施，也不产生留痕行。聚合的来源全量块经 `store.List` 取回，与
命中块同宇宙、同样受固定条件收敛——聚合粒度不越过可见性边界。

## 融合与重排

RRF 融合是纯函数：各路按排名累加 1/(k+rank)（rank 从 1 起），同文档跨路
累加、文档记录取首次出现者；得分降序、同分按首次出现序（确定性），nil
命中跳过不计名次。

重排是可选层：reranker 未装配（nil）、候选不足 2 条、重排模型未配置
（`ErrRerankUnavailable`，按跳过处理不告警）时层未运行、不记 rerank 阶段
（不伪造空阶段）；调用失败与契约破坏（分数长度不符）降级为融合序继续，
阶段照常留痕。成功时分数降序稳定重排（同分保持融合序）。

`Score` 口径：装配重排层且本次成功为 rerank 相关性分，否则为 RRF 融合分
——两种分仅同次检索内可比、不代表相似度，跨调用与跨形态不可比。候选不足
2 条跳过重排，Score 同样保持融合分。

## 回包聚合（pack）

命中块按 `source_id` 分组（空 source_id 各自成组，无法取回兄弟块），按
来源最高分截前 `MaxSources`，逐来源经 `store.List` 取全量块（与命中块按
切片 ID 对齐合并，命中分与引用并入；不属于本来源的枚举行丢弃，缺漏的命中
块补进宇宙），然后自动分档：

| Mode | 条件 | 成员 |
| --- | --- | --- |
| `document` | 命中来源唯一且全量块 ≤8 | 整文档（另产出基础档位作预算降级备份） |
| `section` | 有 heading 结构且同节命中 ≥2 | 该节全量块（含未命中兄弟块） |
| `window` | 无 heading 结构 | 命中索引差 ≤2 分窗、两侧各外扩 1 块、重叠相接合并，窗口区间内全部块 |
| `chunk` | 其余（含聚合降级、索引不可定位） | 单个命中块 |

每来源另产出保底直回单元（最高分命中块单块），保证广度段「每源至少一块」。
预算装配两段：广度段按来源序逐源取第一个放得下的单元（整文档放不下自然
降级到窗口/直回档，保底也放不下才丢源并记警告）；深度段剩余单元按全序
逐个尝试、放不下跳过（保底单元不参与）。document 单元入包收编同源其余
单元，非 document 入包使同源 document 降级——同源聚合粒度互斥，内容不
重复出包。返回单元按全序（Score 降序、来源首现序、最小块索引升序）。

`KnowledgeSearchUnit` 语义：Content 是成员正文按 chunk_index 序以 `\n\n`
连接（不做围栏包装——围栏是模型面语义，由 agent 工具层负责）；`Hits` 仅
本次命中成员块，`DocumentIDs` 含全部成员（差集即兄弟块）；`HeadingPath`
仅 section/document 档承载；`Score` 取成员最高分，与 `KnowledgeSearchHit.Score`
同口径。

token 估算（`estimatePackTokens`）：CJK 汉字/假名/谚文每 rune 计 1，其余
每 4 rune 计 1（向上取整）——只求量级封顶，不引入分词器依赖。

## 错误与降级语义

| 故障 | 处置 |
| --- | --- |
| 身份缺失 | fail-closed `ErrSessionInvalid` |
| 入参非法 | `ErrKnowledgeSearchInvalidArgument`（400） |
| 可见库解析失败 | 整次失败 |
| 指定库不可见/不存在 | 统一 `ErrLibraryNotFound` |
| 查询向量化 / 向量路失败 | 整次失败（正确性依赖） |
| 词法路失败 | 降级仅向量路，记服务日志与阶段失败留痕 |
| 重排模型未配置 | 跳过该层（不告警、不记阶段） |
| 重排调用失败 / 分数长度不符 | 降级融合序，记日志与阶段失败留痕 |
| 来源全量块拉取失败 / 超 1024 防线 | 聚合降级仅命中块直回（超线是合法大文档形态，不告警） |
| 嵌入/向量设施未启用 | 错误原样透传，入口层映射 503 |

其余执行故障对外只给固定文案（500），底层原因只进服务端日志；映射规则在
入口层（`knowledgeSearchError`），用例本身不吞设施错误。

## 留痕

编排路径自写一行完整留痕（一次操作一行）：进入召回管线后打
`WithRetrievalLogSuppressed` 让 data 层装饰器跳过逐路留痕，完成处经
`RetrievalLogRepo.Append` 写整行——hits 是最终命中，filter summary 记可见
库集合，status 记操作终态（降级不翻转 status）。留痕写入失败只记日志，
不反噬主流程（旁路观察面纪律）。

stages 数组承载「召回 → 融合 → 重排 → 回包」阶段时间线（落库 jsonb，
schema/97 列注释为权威声明）：数组顺序即执行序，后一阶段输入序即前一阶段
输出序；元素键 `stage/query_text/limit/model/candidates/units/error/duration_ms`
（query_text 仅召回、model 仅 rerank、units 仅 pack）。每阶段候选/单元上限
50（与 hits 列截断口径同源）；rerank 阶段 candidates 记重排输入候选的重排
后序、limit 记输出上限（最终 top_k）；pack 阶段 units 含全部装配候选
（admitted 区分），未入包按装配标记归因——merged（同源收编）> redundant
（来源已有入包单元的保底单元）> truncated（预算截断），优先级与装配期
跳过顺序一致。

脱敏义务随写入方落在本层：主查询、变体与阶段错误摘要按 retrieval 类别
脱敏（注册表 512 rune 截断 + 4096 rune 兜底；registry 为 nil 按「放行
原值 + 截断兜底」降级）。不进入召回管线的路径（无可见库、入参非法、身份
缺失、设施解析失败）不产生留痕行。模型调用留痕场景在编排内声明
（`embed_query`、`rerank`）。

## 测试

用例层以 mock 仓储覆盖：入参校验矩阵与 top_k 钳制、固定条件组装、RRF
融合（多路累加、并列名次按首次出现序、top_k 截断、nil 命中不计名次）、
变体召回（逐条 trim 与上限、词法路失败降级）、重排编排（候选截断、分数
降序还原、无层/未配置/失败降级路径）、回包聚合（分档矩阵、预算两段装配、
整文档超预算降级与同源收编、来源数截断、全量块拉取失败/超防线降级、
token 估算精确值）、阶段留痕（形态、截断、未入包归因三态）。集成侧
（`internal/data/pg_sqlc/vector`）验证真实 PostgreSQL 的两路检索与
装饰器留痕边界。
