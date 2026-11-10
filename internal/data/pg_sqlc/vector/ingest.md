# ingest — 知识入库逻辑

知识入库的落库面在本目录（`Add` / `DeleteByFilter` / `UpdateMetadata`），
管线本体在 biz 层。本文记录从条目写入到向量落库的完整链路与各环节的
不变量。

## 入库链路

```
service/knowledge.go（CreateDocument / UpdateDocument（正文变更时）/ ReindexDocument）
  → KnowledgeUsecase.reindex（[../../../biz/knowledge.go](../../../biz/knowledge.go)）
      组装 effectiveChunkMetadata（库/条目状态取与、有效期交集），条目 kind 透传
  → DocumentUsecase.Ingest（[../../../biz/document.go](../../../biz/document.go)）
      1. 入参校验（ID ≤96 字节、正文非空、kind 未知拒绝）
      2. 按 kind 分派切分（chunkByKind）：text 走 chunkText，markdown /
         file 走 chunkMarkdown（库级 ChunkSize/ChunkOverlap 两种切分共用，
         ChunkDelimiter 仅对 text 生效；缺省 800/100/段落空行）
      3. 空间解析（库级 embedding_model_key 优先，否则默认空间）
      4. 逐块 embed.Embed（响应长度与维度声明一致性复核）
      5. DeleteByFilter(source_id)（跨空间清除旧切片）
      6. 逐块 store.Add（经本目录契约实现落库 + 留痕装饰器）
  → MarkDocumentIndexed 回写最近摄取时间与空间键（kb_document 行）
```

## 切分与切片 ID

kind 是切分策略的分派依据（`chunkByKind`）：`text` 走区分串/固定窗口，
`markdown` / `file` 走标题切分（file 的正文是解析产物 markdown，摄取入口
由文件解析摄取小节放开）；未知 kind 在摄取时显式拒绝，不静默回落。

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
- **切片 ID 确定性派生**：`"<sourceID>#<index>"`。来源 ID 上限 96 字节，
  为后缀留出 `agent_document.id` 列宽（varchar(128)）余量；确定性保证
  重摄的覆盖语义（同来源重摄写同一批 ID）。

## 幂等覆盖语义

同来源覆盖：摄取前按 `source_id` metadata 清除本租户旧切片（跨空间——
重摄取即换空间迁移路径），再整批写入。

- **顺序不变量**：空间解析与逐块向量化都在删除旧行**之前**完成——
  embed 失败（端点故障、维度漂移）不破坏存量数据，重摄即可补偿。
- **无跨步事务**：清除与写入三步不在同一事务，中途失败存在短暂缺文档
  窗口，重摄可补偿（不引入分布式事务换取瞬时一致性）。

## chunk metadata 契约

切片 metadata 三层来源，检索侧固定条件与本契约逐键对应：

| 键 | 写入方 | 语义 |
| --- | --- | --- |
| `source_id` / `source_name` / `chunk_index` / `chunk_total` / `anchor` | 管线保留键（[chunkMetadata](../../../biz/document.go)） | 来源标识与原文锚点（rune 区间），引用可追溯；以服务端产出覆盖调用方同名键 |
| `heading_path` | 管线保留键（仅 markdown / file 条目） | 标题链（`" > "` 拼接，节前内容为空串），支撑按章节过滤与整节聚合 |
| `kb_id` / `document_id` / `doc_status` / `valid_from_unix` / `valid_to_unix` | 库过滤事实（[effectiveChunkMetadata](../../../biz/knowledge.go)） | 库与条目状态取与（0/1）、有效期取交集（NULL 视为无界） |
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

摄取三动作都经本目录留痕装饰器落一行（来源 pipeline，失败行同样留痕）：
`Add`/`Update` 作用文档进 hits 列（单元素，anchor 取切片元数据）；
`DeleteByFilter` 条件摘要进 filter_summary。写入失败只记日志，不中断
摄取。
