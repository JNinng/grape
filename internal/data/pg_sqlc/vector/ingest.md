# ingest — 知识入库逻辑

知识入库的落库面在本目录（`Add` / `Update` / `MarkSuperseded` /
`DeleteByFilter` / `UpdateMetadata`），管线本体在 biz 层。本文记录从条目
写入到向量落库的完整链路与各环节的不变量。

## 入库链路

文件条目（kind=file）在目录行之前多两步——原件落介质与对象存储、解析产
markdown，之后与本链路汇合（切分按标题、metadata 带 heading_path）：

```
service/knowledge_file.go（POST /v1/knowledge/documents/upload，multipart 手写端点）
  → KnowledgeUsecase.UploadDocument（[../../../biz/knowledge_file.go](../../../biz/knowledge_file.go)）
      1. 文件识别三层校验（identifyFile，[../../../biz/fileidentify.go](../../../biz/fileidentify.go)）：
         扩展名白名单 → 内容嗅探（路由与落库 MIME 的唯一依据）→ 20MB +
         zip 炸弹预检；不在白名单按 FILE_TYPE_UNSUPPORTED 拒绝（介质成立
         前，无任何落库副作用）
      2. 介质行落 kb_file（对象键 kb-file/<发号 ID>）→ 原件字节写内嵌 OSS
      3. 目录行落 kb_document（kind=file，content 空——解析失败留痕可重解析）
      4. reparseDocument：direct 档（text/plain / markdown）免解析直接内容
         识别处理；native / external 档经 FileParser 解析产 markdown
         （[../../../data/docparse/](../../../data/docparse/)，外部档走
         conf.doc_parse 端点，未配置按 FILE_TYPE_UNSUPPORTED 拒绝）
      5. 内容识别处理（prepareFileContent，[../../../biz/filecontent.go](../../../biz/filecontent.go)）：
         编码统一 UTF-8（BOM / UTF-8 严格性 / charset 判定 / GB18030 回落，
         乱码占比超阈值拒绝）→ 规范化与规则级清洗 → 标题层级推断
         （heading_path 的产出点）→ 解析产物回写目录行
      6. 摄取（ingestDocument）：kind=file 按标题切分（direct 档按原文件
         形态——.md 走标题切分、其余走 text 切分）
  解析 / 内容识别 / 摄取失败：目录行与介质行保留（content 空），管理页
  重建索引即重解析（ReindexDocument 的 file 分支重取原件重走 4-6）。
  删除条目随宿主软删介质行并尽力删除 OSS 对象。
```

```
service/knowledge.go（CreateDocument / UpdateDocument（正文变更时）/ ReindexDocument）
  → KnowledgeUsecase.reindex（[../../../biz/knowledge.go](../../../biz/knowledge.go)）
      组装 effectiveChunkMetadata（库/条目状态取与、有效期交集），条目 kind 透传，
      库级上下文检索开关（contextual_chunking）透传
  → DocumentUsecase.Ingest（[../../../biz/document.go](../../../biz/document.go)）
      1. 入参校验（ID ≤96 字节、正文非空、kind 未知拒绝）
      2. 按 kind 分派切分（chunkByKind）：text 走 chunkText，markdown /
         file 走 chunkMarkdown（库级 ChunkSize/ChunkOverlap 两种切分共用，
         ChunkDelimiter 仅对 text 生效；缺省 800/100/段落空行）
      3. 空间解析（库级 embedding_model_key 优先，否则默认空间）
      4. 上下文前缀生成（开关开启时，见下节）
      5. 逐块 embed.Embed（输入 = 前缀 + 块正文（有前缀时）；响应长度与
         维度声明一致性复核）
      6. 差量对账（reconcileChunks，按新批 ID 对账本来源现存切片）：
         仍在新批 → Update 整行刷新（清退役标记）；不在新批的现行行 →
         MarkSuperseded 标记退役；全新 ID → Add
      7. 写路径经本目录契约实现落库 + 留痕装饰器（Add/Update/MarkSuperseded）
  → MarkDocumentIndexed 回写最近摄取时间与空间键（kb_document 行）
```

## 上下文前缀（Contextual Retrieval）

库级开关 `kb_library.contextual_chunking`（管理面贯通 proto `Library.contextual_chunking`
与前端库表单）开启时，摄取在切分之后、逐块向量化之前为每块生成上下文前缀
（本文出处 + 本块要旨，50–100 token 量级）：

- **成本控制**：文档级摘要只调用一次模型，逐块前缀共享这份摘要（prompt 补
  各块正文，不做逐块全量文档调用）。进摘要 prompt 的文档正文按 rune 截断
  （`maxContextDocRunes`），前缀输出再按 rune 硬截断（`maxContextPrefixRunes`）。
- **生成实现**：经 biz.Completer 契约由
  [openaicompleter](../../openaicompleter/) 单轮调用租户文本模型
  （OpenAI 兼容 chat completions，按当前身份解析端点）；prompt 在 biz
  管线组装，实现不感知业务语义。
- **作用点唯一**：前缀只以段落空行拼进 embed 输入——块正文、锚点
  （anchor）、metadata、目录正文全部不变，落库正文与切分产物逐字一致。
- **失败降级**：前缀是质量增强非正确性依赖——模型失败、端点未配置
  （租户未配置文本模型）或产出清洗后为空时，整篇降级为无前缀继续摄取，
  服务日志记 WARN（document_id + prompt 概要 + 错误），不整体失败；摄取
  留痕行照常由装饰器写入。
- **生效时机**：开关在摄取现场读取，对条目执行重建索引后生效；存量切片
  的向量输入不带前缀，不自动重嵌。

## 切分与切片 ID

kind 是切分策略的分派依据（`chunkByKind`）：`text` 走区分串/固定窗口，
`markdown` / `file` 走标题切分（file 的正文是解析产物 markdown；直传的
纯文本文件按原文件形态分派——.md 走标题切分、其余走 text 切分）；未知
kind 在摄取时显式拒绝，不静默回落。

- **区分串切分**（[chunkText](../../../biz/document.go)，text 条目）：缺省
  区分串是段落空行（`\n\n`），先按区分串切出段落块——区分串本身不入块、
  全空白块丢弃——超过 `ChunkSize` 的段落块再按 rune 固定窗口二次切分，相邻
  窗口重叠 `ChunkOverlap` 个 rune 保住边界语义；区分串置空（空串是合法
  覆盖）即纯固定窗口。切分参数按调用方覆盖构造期缺省 800/100/段落空行
  （知识摄取取库级值，非法已在目录层拒绝），管线级覆盖值非法（重叠不
  小于块大小、区分串超 32 rune）静默回落缺省。
- **标题切分**（[chunkMarkdown](../../../biz/document.go)，markdown /
  file 条目）：按标题层级切节——ATX（`#`–`######`）与 Setext（`===` 一级 /
  `---` 二级下划线行）都识别，节含节头标题行、止于下一标题行，首个标题
  之前的内容自成一节。节全文不超过 `ChunkSize` 时整节一块；超长节回退
  块级二次切分：段落等普通块按块边界装箱（块间空白属原文、随装箱区间
  保留），围栏代码块与表格块保持原子不跨块截断，单个块自身超过
  `ChunkSize` 时再按固定窗口切开（嵌入输入上限优先于原子性，此处
  `ChunkOverlap` 生效）。`ChunkDelimiter` 不参与标题切分。识别口径按
  CommonMark 简化：Setext 下划线挂到紧邻的非空行段（多行段落的标题文本
  以空格拼接），不排除列表/引用容器；表格按「表头含 `|` 且下一行是列数
  一致的分隔行」识别、连续含 `|` 的非空行为表格行；制表符缩进与 4 空格
  缩进不构成标题/围栏。
- **heading_path**：标题切分的每块 metadata 附 `heading_path`（标题链按
  `" > "` 拼接，如 `部署 > 数据库 > 升级`；首个标题之前的内容为空串）。
  metadata 是 jsonb，`MetadataCondition` 可按该键过滤，等于获得「按章节
  过滤」；text 切片不带该键，metadata 形状不变。两实现产出同一「内容 +
  原文 rune 锚点」形状，切片正文恒等于原文对应 rune 区间，管线其余环节
  无感。
- **切片 ID 确定性派生**：`"<sourceID>#<index>#<hash8>"`。hash8 取
  `sha256(嵌入空间键 + "\x00" + 切片内容)` 的前 4 字节十六进制（8 字符）：

  - 内容变化或换空间即出新 ID，历史 ID 永不指向新内容（留痕反查的前提）；
  - 空间键参与哈希，避免 PK `(tenant_id, id)` 下同内容跨空间撞 ID；
  - NUL 分隔保证「空间键 + 内容」拼接无歧义（两者都不含 NUL）；
  - 长度：来源 ID 上限 96 + `#` + 7 位序号 + `#` + 8 = 113，在
    `agent_document.id` 的 varchar(128) 内；
  - 确定性保证同内容同空间重摄复用同一批 ID（差量对账的匹配依据）。
    存量旧形态 ID `"<sourceID>#<index>"` 在下次重摄时自然换新，不做一次性
    迁移。

## 幂等差量对账

同来源重摄按新批 ID 对账本来源现存切片（biz
[reconcileChunks](../../../biz/document.go)），**不再先删后写**——旧切片
保留为退役行，留痕行在保留期内仍可反查命中当时的原文：

- **仍在新批的 ID**：`Update` 整行刷新（content / embedding / metadata）。
  metadata 是整列替换，`superseded_at` 键因此被清掉——内容改回旧版本时
  旧 ID 复活。
- **现存但不在新批的现行行**：`MarkSuperseded` 写入 `superseded_at`
  （RFC3339 文本），物理行保留。已退役且不在新批的行不重复标记，保留
  首次退役时间（清理任务以它为保留期依据）。
- **全新 ID**：`Add`。
- **读面排除退役行**：检索固定条件、管理页 `ListDocumentChunks`、`Count`
  以 `superseded_at IS NULL`（`VectorMetadataIsNull`）排除；`Get` 是复现
  反查读面，不过滤（跨租户仍不可见）。
- **显式下线仍物理删除**：删条目 `DeleteByFilter(document_id)`、删库
  `DeleteByFilter(kb_id)`。条目删除后复现页按 `kb_document.deleted_at`
  标注，不依赖切片行。
- **顺序不变量**：空间解析与逐块向量化都在对账写**之前**完成——embed
  失败（端点故障、维度漂移）不破坏存量数据。
- **无跨步事务**：标记退役与写入分步执行、不在同一事务，中途失败存在
  短暂缺文档窗口，重摄可补偿（不引入分布式事务换取瞬时一致性）。并发
  重摄同一来源沿用既有口径（无源级锁），结果收敛到最后一批 ID。

## chunk metadata 契约

切片 metadata 三层来源，检索侧固定条件与本契约逐键对应：

| 键 | 写入方 | 语义 |
| --- | --- | --- |
| `source_id` / `source_name` / `chunk_index` / `chunk_total` / `anchor` | 管线保留键（[chunkMetadata](../../../biz/document.go)） | 来源标识与原文锚点（rune 区间），引用可追溯；以服务端产出覆盖调用方同名键 |
| `heading_path` | 管线保留键（仅 markdown / file 条目） | 标题链（`" > "` 拼接，节前内容为空串），支撑按章节过滤与整节聚合 |
| `kb_id` / `document_id` / `doc_status` / `valid_from_unix` / `valid_to_unix` | 库过滤事实（[effectiveChunkMetadata](../../../biz/knowledge.go)） | 库与条目状态取与（0/1）、有效期取交集（NULL 视为无界） |
| `superseded_at` | 重摄差量对账（`MarkSuperseded`，[vector_repo.go](vector_repo.go)） | 退役时间（RFC3339）；键存在且非 JSON null 即非现行（JSON null 与键缺失同判），检索/管理页/计数以 is-null 排除；整行 Update 替换 metadata 即清除（复活） |
| 调用方自定义键 | 原样保留 | — |

存储层再注入身份与空间事实：`tenant_id` / `user_id` / `embedder` /
`embed_dim` 以服务端产出为准（见 [vector_repo.go](vector_repo.go)
`vectorMetadata`），列是本体、metadata 是过滤语义冗余。

## 目录状态与删除的同步

- **状态/有效期变更**（条目停用、库停用、有效期调整）：先把新值经
  `UpdateMetadata` 合并进命中 chunk（jsonb `||`，只改 metadata 列），再写
  目录行——同步失败使本次更新失败，避免「目录停用但检索仍命中」。向量
  保留，检索由 `doc_status` / 有效期条件排除（见
  [../../../agent/tools/README.md](../../../agent/tools/README.md) 检索链路）。
- **删条目**：`DeleteByFilter(document_id)` 清切片并留一行条件摘要
  （filter_summary 记条件，不记被删行）。
- **删库**：`DeleteByFilter(kb_id)` 清整库切片；库内有条目时先拒绝
  （`ErrLibraryHasDocuments`）。

## 留痕

摄取写动作都经本目录留痕装饰器落一行（来源 pipeline，失败行同样留痕）：
`Add`/`Update` 作用文档进 hits 列（单元素，anchor 取切片元数据）；
`MarkSuperseded`/`DeleteByFilter` 条件摘要进 filter_summary。写入失败只记
日志，不中断摄取。
