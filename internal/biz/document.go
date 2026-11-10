package biz

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// 文档摄取管线的错误语义：
//   - ErrDocumentSourceTooLong：来源文档 ID 超长，切片 ID 无法落在向量表
//     的 id 列宽（varchar(128)）内；
//   - 其余入参问题（ID/正文为空等）以普通错误返回；设施侧错误
//     （ErrVectorStoreDisabled / ErrEmbedderDisabled / ErrVectorDocNotFound）
//     由契约透传。
var ErrDocumentSourceTooLong = errors.New("biz: document source id too long")

// 摄取管线的入参边界与切片缺省：
//   - maxSourceIDChars 限制来源 ID 长度，为切片 ID 后缀 "#<index>" 留出
//     列宽余量（agent.agent_document.id 为 varchar(128)）；
//   - defaultChunkSize/defaultChunkOverlap 是固定窗口切分的缺省（rune
//     计），与主流 embedding 模型的输入窗口量级匹配；
//   - defaultChunkDelimiter 是分隔符切分的缺省区分串：段落空行是文档最
//     普遍的结构边界，切出的块语义完整度最高（单换行过碎）；空串表示不按
//     分隔符切分。maxChunkDelimiterRunes 与 kb_library 的长度 CHECK 同值。
//     替换为其他语义切分时改 DocumentOption 注入，接口面不变。
const (
	maxSourceIDChars       = 96
	defaultChunkSize       = 800
	defaultChunkOverlap    = 100
	defaultChunkDelimiter  = "\n\n"
	maxChunkDelimiterRunes = 32
)

// Document 是摄取管线的领域文档来源。来源限内部可信生产方；对外部网页
// 抓取等不可信来源开放前，须先补来源校验与脱敏切面（对齐 pkg/redact）。
// ID 由调用方生成且租户内唯一。
//
// Kind 选择切分策略（见 DocumentKind* 常量）：text 按区分串/固定窗口切分，
// markdown / file 按标题切分；空值视为 text，未知取值在摄取时拒绝。
// ChunkDelimiter 仅对 text 生效；ChunkSize/ChunkOverlap 两种切分共用。
// Embedder 非空时指定摄取空间（知识库级 embedding_model_key）；空值走
// 默认空间解析。ChunkSize/ChunkOverlap 非零时覆盖管线缺省（知识库级切分
// 参数），零值取构造期缺省。ChunkDelimiter 非 nil 时覆盖管线缺省区分串
// （空串即关闭分隔符切分），nil 取构造期缺省。
type Document struct {
	ID             string
	Kind           string
	Name           string
	Content        string
	Metadata       map[string]any
	Embedder       string
	ChunkSize      int
	ChunkOverlap   int
	ChunkDelimiter *string
}

// IngestResult 是摄取结果：Chunks 为本次写入的切片数，Replaced 为覆盖
// 语义下先行删除的旧切片数（首次摄取为 0）；Embedder 是本次落库的空间键。
type IngestResult struct {
	DocumentID string
	Chunks     int
	Replaced   int64
	Embedder   string
}

// chunk 是切分产物：content 为切片正文，anchor 为原文中的 rune 区间
// （引用可追溯的锚点），start/end 是闭开区间偏移。headingPath 是标题切分
// 的标题链（" > " 拼接），仅 markdown 切分填充、首个标题之前的内容为空串；
// 固定窗口切分恒为空。
type chunk struct {
	content     string
	start       int
	end         int
	headingPath string
}

// DocumentOption 定制摄取管线参数。
type DocumentOption func(*documentOptions)

type documentOptions struct {
	chunkSize      int
	chunkOverlap   int
	chunkDelimiter string
}

// WithChunking 覆盖切分参数：size 为单块 rune 上限，overlap 为相邻块
// 重叠 rune 数（0 ≤ overlap < size）。
func WithChunking(size, overlap int) DocumentOption {
	return func(o *documentOptions) { o.chunkSize, o.chunkOverlap = size, overlap }
}

// WithChunkDelimiter 覆盖缺省区分串：空串关闭分隔符切分（纯固定窗口），
// 非空至多 32 rune。
func WithChunkDelimiter(delimiter string) DocumentOption {
	return func(o *documentOptions) { o.chunkDelimiter = delimiter }
}

// DocumentUsecase 是摄取管线用例：切分 → embed → 经 VectorStore 契约落库。
// 租户过滤由 VectorStore 实现强制，管线不接触存储细节、不直连 SQL；身份
// 从认证 ctx 提取（管线由受权入口驱动），缺失 fail-closed。嵌入空间在
// 每次 Ingest 现场解析，默认空间切换即迁移路径（见 README「关键文件导览」）。
type DocumentUsecase struct {
	store          VectorStore
	embedder       Embedder
	resolver       EmbedderResolver
	chunkSize      int
	chunkOverlap   int
	chunkDelimiter string
}

// NewDocumentUsecase 构造摄取管线。维度链路（embedder 空间声明 → conf
// → DDL 索引）的中间一环改在写路径逐次对账：解析出的默认空间必须能
// 取到维度声明，且每次 Embed 响应长度与之一致（Embedder 实现自身亦
// 复核），存储层再按注册表对账——声明与实际向量三方一致才落库。
func NewDocumentUsecase(store VectorStore, embedder Embedder, resolver EmbedderResolver, opts ...DocumentOption) (*DocumentUsecase, error) {
	if store == nil {
		return nil, errors.New("biz: document usecase requires a vector store")
	}
	if embedder == nil {
		return nil, errors.New("biz: document usecase requires an embedder")
	}
	if resolver == nil {
		return nil, errors.New("biz: document usecase requires an embedder resolver")
	}
	o := &documentOptions{chunkSize: defaultChunkSize, chunkOverlap: defaultChunkOverlap, chunkDelimiter: defaultChunkDelimiter}
	for _, opt := range opts {
		opt(o)
	}
	if o.chunkSize <= 0 {
		return nil, errors.New("biz: chunk size must be positive")
	}
	if o.chunkOverlap < 0 || o.chunkOverlap >= o.chunkSize {
		return nil, errors.New("biz: chunk overlap must be in [0, chunk size)")
	}
	if utf8.RuneCountInString(o.chunkDelimiter) > maxChunkDelimiterRunes {
		return nil, fmt.Errorf("biz: chunk delimiter must be at most %d runes", maxChunkDelimiterRunes)
	}
	return &DocumentUsecase{
		store:          store,
		embedder:       embedder,
		resolver:       resolver,
		chunkSize:      o.chunkSize,
		chunkOverlap:   o.chunkOverlap,
		chunkDelimiter: o.chunkDelimiter,
	}, nil
}

// Ingest 摄取一篇内部文档：切分 → 逐块 embed → 写入向量存储。幂等语义为
// 同来源覆盖：切片 ID 确定性派生，摄取前按 source_id 跨空间清旧行再整批
// 写入。三步不在同一事务，中途失败存在短暂缺文档窗口，重摄可补偿。
func (u *DocumentUsecase) Ingest(ctx context.Context, doc *Document) (*IngestResult, error) {
	if _, ok := IdentityFromContext(ctx); !ok {
		return nil, errors.New("biz: document ingest requires an authenticated identity")
	}
	if doc == nil {
		return nil, errors.New("biz: document is required")
	}
	sourceID := strings.TrimSpace(doc.ID)
	if sourceID == "" {
		return nil, errors.New("biz: document id is required")
	}
	// 以字节计（保守侧失效）：非 ASCII 来源 ID 会提前拒绝，varchar(128)
	// 按字符计故该上限留有余量。
	if len(sourceID) > maxSourceIDChars {
		return nil, fmt.Errorf("%w: got %d bytes, max %d", ErrDocumentSourceTooLong, len(sourceID), maxSourceIDChars)
	}
	content := doc.Content
	if strings.TrimSpace(content) == "" {
		return nil, errors.New("biz: document content is required")
	}
	chunkSize, chunkOverlap := docChunking(u.chunkSize, u.chunkOverlap, doc.ChunkSize, doc.ChunkOverlap)
	delimiter := docChunkDelimiter(u.chunkDelimiter, doc.ChunkDelimiter)
	chunks, err := chunkByKind(doc.Kind, content, chunkSize, chunkOverlap, delimiter)
	if err != nil {
		return nil, err
	}
	if len(chunks) == 0 {
		return nil, errors.New("biz: document content chunks to nothing")
	}

	// 摄取空间在删除旧行之前解析：库级指定空间优先，未指定走默认空间。
	// 旧切片（可能属于其他空间）由覆盖语义跨空间清除，重摄取即换空间
	// 迁移路径。
	var space string
	var dimension int
	if strings.TrimSpace(doc.Embedder) != "" {
		spaceInfo, err := u.resolver.ResolveEmbeddingSpace(ctx, doc.Embedder)
		if err != nil {
			return nil, fmt.Errorf("biz: resolve embedding space %s for document %s: %w", doc.Embedder, sourceID, err)
		}
		space, dimension = spaceInfo.Key, spaceInfo.Dimension
	} else {
		var err error
		space, dimension, err = u.resolver.ResolveDefaultEmbedder(ctx)
		if err != nil {
			return nil, fmt.Errorf("biz: resolve default embedder for document %s: %w", sourceID, err)
		}
	}

	// 逐块向量化在删除旧行之前完成：embed 失败（端点故障、维度漂移）
	// 时不破坏存量数据，重摄即可补偿。
	docs := make([]*VectorDocument, 0, len(chunks))
	for i, c := range chunks {
		embedding, err := u.embedder.Embed(ctx, space, c.content)
		if err != nil {
			return nil, fmt.Errorf("biz: embed chunk %d of document %s: %w", i, sourceID, err)
		}
		if len(embedding) != dimension {
			return nil, fmt.Errorf("biz: embedder returned dimension %d for space %s, expected %d", len(embedding), space, dimension)
		}
		docs = append(docs, &VectorDocument{
			ID:        chunkID(sourceID, i),
			Embedder:  space,
			Dimension: dimension,
			Name:      doc.Name,
			Content:   c.content,
			Embedding: embedding,
			Metadata:  chunkMetadata(doc, i, len(chunks), c),
		})
	}

	// 覆盖语义：先按来源锚点清除本租户内的旧切片（跨租户行由存储层
	// 租户过滤天然隔离），再整批写入。
	replaced, err := u.store.DeleteByFilter(ctx, &VectorFilter{Metadata: map[string]any{"source_id": sourceID}})
	if err != nil {
		return nil, fmt.Errorf("biz: clear previous chunks of document %s: %w", sourceID, err)
	}
	for _, d := range docs {
		if err := u.store.Add(ctx, d); err != nil {
			return nil, fmt.Errorf("biz: add chunk %s: %w", d.ID, err)
		}
	}
	return &IngestResult{DocumentID: sourceID, Chunks: len(docs), Replaced: replaced, Embedder: space}, nil
}

// docChunking 合并管线缺省与文档级切分参数：文档值为 0 时取缺省；非法
// 覆盖值（负重叠、重叠不小于块大小）同样回落缺省，由构造期校验过的缺省
// 兜底，保证切分窗口始终有效。
func docChunking(defSize, defOverlap, size, overlap int) (int, int) {
	if size <= 0 || overlap < 0 || overlap >= size {
		return defSize, defOverlap
	}
	return size, overlap
}

// docChunkDelimiter 合并管线缺省与文档级区分串：nil 取缺省；超长的非法
// 覆盖值同样回落缺省（与 docChunking 同一静默回落口径）。空串是合法覆盖，
// 表示关闭分隔符切分。
func docChunkDelimiter(def string, override *string) string {
	if override == nil || utf8.RuneCountInString(*override) > maxChunkDelimiterRunes {
		return def
	}
	return *override
}

// chunkID 派生切片 ID："<sourceID>#<index>"，确定性保证重摄的覆盖语义；
// 长度在来源 ID 上限与列宽之间留有余量。
func chunkID(sourceID string, index int) string {
	return fmt.Sprintf("%s#%d", sourceID, index)
}

// chunkMetadata 组装切片元数据：来源标识（source_id/source_name）与
// 原文锚点（anchor，rune 区间）保证引用可追溯——检索命中可反查来源
// 文档与原文位置。结构化切分的切片附 heading_path（标题链），供按章节
// 过滤与整节聚合。管线保留键以服务端产出为准覆盖调用方同名键，调用方
// 自定义键原样保留。
func chunkMetadata(doc *Document, index, total int, c chunk) map[string]any {
	metadata := make(map[string]any, len(doc.Metadata)+6)
	for k, v := range doc.Metadata {
		metadata[k] = v
	}
	metadata["source_id"] = strings.TrimSpace(doc.ID)
	metadata["source_name"] = doc.Name
	metadata["chunk_index"] = index
	metadata["chunk_total"] = total
	metadata["anchor"] = fmt.Sprintf("runes %d-%d", c.start, c.end)
	// heading_path 只随结构化切分落库，text 切片保持既有 metadata 形状
	// 不变；首个标题之前的内容为空串。
	if doc.Kind == DocumentKindMarkdown || doc.Kind == DocumentKindFile {
		metadata["heading_path"] = c.headingPath
	}
	return metadata
}

// chunkByKind 按 kind 分派切分实现：text（含空值）走区分串/固定窗口，
// markdown / file 走标题切分（file 的正文是解析产物 markdown，切分口径
// 同 markdown）。未知 kind 显式拒绝：kind 是切分策略的分派依据，静默
// 回落会产出与目录口径不符的切片。取值常量见 knowledge.go 的 DocumentKind*。
func chunkByKind(kind, content string, size, overlap int, delimiter string) ([]chunk, error) {
	switch kind {
	case "", DocumentKindText:
		return chunkText(content, size, overlap, delimiter), nil
	case DocumentKindMarkdown, DocumentKindFile:
		return chunkMarkdown(content, size, overlap), nil
	default:
		return nil, fmt.Errorf("biz: unknown document kind %q", kind)
	}
}

// chunkMarkdown 是 Markdown 标题切分实现（chunkText 旁的替换点，产出同
// 「内容 + 原文 rune 锚点」形状）。按标题层级切节：ATX（#–######）与
// Setext（=== / --- 下划线行）都识别，节含节头标题行、止于下一标题行；
// 首个标题之前的内容自成一节（heading path 空串）。节全文不超过 size 时
// 整节一块；超长节回退块级二次切分——段落等普通块按块边界装箱，围栏
// 代码块与表格块保持原子不跨块截断，单个块自身超过 size 时再按固定窗口
// 切开（嵌入输入上限优先于原子性）。ChunkOverlap 只作用于固定窗口回退，
// 块边界是结构边界不重叠。节之间按标题层级维护标题链，ATX 取 # 数量、
// Setext 下划线 === 为一级、--- 为二级，链按 " > " 拼接。
//
// 简化口径：Setext 下划线只挂到紧邻的非空行段（含多行段落，段内行以空格
// 拼接为标题文本），不排除列表/引用等容器上下文；表格识别按表头含 "|" 且
// 下一行是分隔行（列数一致），连续含 "|" 的非空行为表格行；制表符缩进与
// 4 空格缩进的行不视为标题/围栏；CRLF 输入按 LF 同口径切行（行尾 '\r'
// 不入行区间与行文本，标题/围栏判定不受影响，锚点仍为原文 rune 区间）；
// 空 ATX 标题（###### 后无文字）只切节、不入标题链。
func chunkMarkdown(content string, size, overlap int) []chunk {
	runes := []rune(content)
	var out []chunk
	for _, sec := range markdownSections(runes) {
		if sec.to-sec.from <= size {
			out = append(out, chunk{
				content:     string(runes[sec.from:sec.to]),
				start:       sec.from,
				end:         sec.to,
				headingPath: sec.path,
			})
			continue
		}
		out = packMarkdownBlocks(out, runes, sec, size, overlap)
	}
	return out
}

// mdSection 是一个标题节：path 为标题链，from/to 是原文 rune 闭开区间
// （含节头标题行，首尾空白行剔除）。
type mdSection struct {
	path     string
	from, to int
}

// mdLine 是原文的一个行：start/end 为 rune 区间（不含换行符与紧邻其前的
// 行尾 '\r'），text 为行文本。CRLF 输入的行文本因此与 LF 输入一致，切片
// 仍取原文区间，锚点不变式不受影响。
type mdLine struct {
	start, end int
	text       string
}

// markdownSections 扫描全文行，按标题行切节。标题链按层级入栈出栈：新
// 标题弹出层级不小于自身的链尾再入栈。标题行本身不开新块状态，围栏内
// 不识别标题（未闭合围栏吞掉其后全部行）。
func markdownSections(runes []rune) []mdSection {
	lines := mdLines(runes)
	var (
		sections  []mdSection
		titles    []string
		levels    []int
		secFrom   int
		inFence   bool
		fenceChar rune
		fenceLen  int
		paraFrom  = -1
	)
	closeSection := func(toLine int) {
		from, to := trimBlankLines(lines, secFrom, toLine)
		if from < to {
			sections = append(sections, mdSection{
				path: strings.Join(titles, " > "),
				from: lines[from].start,
				to:   lines[to-1].end,
			})
		}
	}
	for i := 0; i < len(lines); i++ {
		stripped := stripIndent(lines[i].text)
		if inFence {
			if isFenceClose(stripped, fenceChar, fenceLen) {
				inFence = false
				paraFrom = -1
			}
			continue
		}
		if char, n, ok := fenceOpen(stripped); ok {
			inFence, fenceChar, fenceLen = true, char, n
			paraFrom = -1
			continue
		}
		if level, title, ok := atxHeading(stripped); ok {
			closeSection(i)
			pushHeading(&titles, &levels, level, title)
			secFrom = i
			paraFrom = -1
			continue
		}
		if level, ok := setextUnderline(stripped); ok && paraFrom >= 0 {
			closeSection(paraFrom)
			pushHeading(&titles, &levels, level, headingText(lines[paraFrom:i]))
			secFrom = paraFrom
			paraFrom = -1
			continue
		}
		if strings.TrimSpace(lines[i].text) == "" {
			paraFrom = -1
			continue
		}
		if paraFrom < 0 {
			paraFrom = i
		}
	}
	closeSection(len(lines))
	return sections
}

// packMarkdownBlocks 对超长节做块级二次切分：块按原文顺序装箱，块间空白
// 属于原文、随装箱区间保留（切片正文恒为原文连续区间）；单块超过 size 时
// 固定窗口切开（overlap 生效处），其余块边界不重叠。
func packMarkdownBlocks(out []chunk, runes []rune, sec mdSection, size, overlap int) []chunk {
	blocks := markdownBlocks(runes, sec.from, sec.to)
	appendChunk := func(from, to int) {
		if from >= 0 && from < to {
			out = append(out, chunk{
				content:     string(runes[from:to]),
				start:       from,
				end:         to,
				headingPath: sec.path,
			})
		}
	}
	cur, curEnd := -1, -1
	for _, b := range blocks {
		switch {
		case b.to-b.from > size:
			appendChunk(cur, curEnd)
			cur, curEnd = -1, -1
			out = windowChunks(out, runes, b.from, b.to, size, overlap, sec.path)
		case cur < 0:
			cur, curEnd = b.from, b.to
		case b.to-cur <= size:
			curEnd = b.to
		default:
			appendChunk(cur, curEnd)
			cur, curEnd = b.from, b.to
		}
	}
	appendChunk(cur, curEnd)
	return out
}

// mdBlock 是节内的一个块。atomic 块（围栏代码块 / 表格块）装箱时不跨块
// 截断；段落块超长同样回退固定窗口。
type mdBlock struct {
	from, to int
	atomic   bool
}

// markdownBlocks 把节内 [from,to) 扫描为块序列：围栏代码块（开行到闭行，
// 未闭合到节尾）与表格块（表头行起到最后一个连续含 "|" 的行）为原子块，
// 其余连续非空行为段落块；块间空白行不入块。
func markdownBlocks(runes []rune, from, to int) []mdBlock {
	lines := mdLines(runes[from:to])
	var blocks []mdBlock
	var (
		inFence   bool
		fenceChar rune
		fenceLen  int
		fenceFrom = -1
		paraFrom  = -1
	)
	flushPara := func(toLine int) {
		if paraFrom >= 0 {
			blocks = append(blocks, mdBlock{
				from: from + lines[paraFrom].start,
				to:   from + lines[toLine-1].end,
			})
			paraFrom = -1
		}
	}
	for i := 0; i < len(lines); i++ {
		stripped := stripIndent(lines[i].text)
		if inFence {
			if isFenceClose(stripped, fenceChar, fenceLen) {
				blocks = append(blocks, mdBlock{
					from:   from + lines[fenceFrom].start,
					to:     from + lines[i].end,
					atomic: true,
				})
				inFence, fenceFrom = false, -1
			}
			continue
		}
		if char, n, ok := fenceOpen(stripped); ok {
			flushPara(i)
			inFence, fenceChar, fenceLen, fenceFrom = true, char, n, i
			continue
		}
		if strings.TrimSpace(lines[i].text) == "" {
			flushPara(i)
			continue
		}
		if tableRowStart(lines, i) {
			flushPara(i)
			j := i + 1
			for j < len(lines) && isTableRow(lines[j]) {
				j++
			}
			blocks = append(blocks, mdBlock{
				from:   from + lines[i].start,
				to:     from + lines[j-1].end,
				atomic: true,
			})
			i = j - 1
			continue
		}
		if paraFrom < 0 {
			paraFrom = i
		}
	}
	flushPara(len(lines))
	if inFence {
		blocks = append(blocks, mdBlock{
			from:   from + lines[fenceFrom].start,
			to:     from + lines[len(lines)-1].end,
			atomic: true,
		})
	}
	return blocks
}

// mdLines 把 rune 序列按 '\n' 切行（行区间不含换行符与紧邻其前的行尾
// '\r'，CRLF 输入因此与 LF 同口径切行；孤立的 '\r' 不切行、留在行内。
// 末行无换行时照常收尾，行尾 '\r' 同样排除）。
func mdLines(runes []rune) []mdLine {
	lines := make([]mdLine, 0, len(runes)/16+1)
	start := 0
	appendLine := func(rawEnd int) {
		end := rawEnd
		// 行尾 '\r' 排除出行区间（空行的 '\r\n' 同理），行文本与 LF
		// 输入一致；切片仍按原文区间取，锚点不变式不受影响。
		if end > start && runes[end-1] == '\r' {
			end--
		}
		lines = append(lines, mdLine{start: start, end: end, text: string(runes[start:end])})
	}
	for i, r := range runes {
		if r == '\n' {
			appendLine(i)
			start = i + 1
		}
	}
	appendLine(len(runes))
	return lines
}

// trimBlankLines 把行区间 [from,to) 首尾的空白行剔除，返回仍在 [from,to)
// 内的区间；全空白时 from == to。
func trimBlankLines(lines []mdLine, from, to int) (int, int) {
	for from < to && strings.TrimSpace(lines[from].text) == "" {
		from++
	}
	for to > from && strings.TrimSpace(lines[to-1].text) == "" {
		to--
	}
	return from, to
}

// stripIndent 去除至多 3 个前导空格（CommonMark 的标题/围栏缩进上限）；
// 4 空格起头的缩进码块保留前导空格，不会命中标题/围栏判定。
func stripIndent(text string) string {
	rs := []rune(text)
	i := 0
	for i < 3 && i < len(rs) && rs[i] == ' ' {
		i++
	}
	return string(rs[i:])
}

// fenceOpen 判定围栏开行：``` 或 ~~~ 起（入参已经 stripIndent 去除至多
// 3 空格缩进，4 空格起的缩进码块不会命中），后续信息串任意；反引号围栏
// 的信息串不得再含反引号。
func fenceOpen(stripped string) (char rune, length int, ok bool) {
	rs := []rune(stripped)
	if len(rs) < 3 || (rs[0] != '`' && rs[0] != '~') {
		return 0, 0, false
	}
	n := 0
	for n < len(rs) && rs[n] == rs[0] {
		n++
	}
	if n < 3 {
		return 0, 0, false
	}
	if rs[0] == '`' && strings.ContainsRune(string(rs[n:]), '`') {
		return 0, 0, false
	}
	return rs[0], n, true
}

// isFenceClose 判定围栏闭行：行尾空白去除后只剩不短于开行长度的同一
// 围栏字符。
func isFenceClose(stripped string, char rune, minLen int) bool {
	rs := []rune(strings.TrimRight(stripped, " \t"))
	if len(rs) < minLen {
		return false
	}
	for _, r := range rs {
		if r != char {
			return false
		}
	}
	return true
}

// atxHeading 判定 ATX 标题行：1–6 个 # 后跟空白或行尾；标题文本去除首尾
// 空白与「空格 + # 串」闭合序列。
func atxHeading(stripped string) (level int, title string, ok bool) {
	rs := []rune(stripped)
	n := 0
	for n < len(rs) && rs[n] == '#' {
		n++
	}
	if n == 0 || n > 6 {
		return 0, "", false
	}
	if n < len(rs) && rs[n] != ' ' && rs[n] != '\t' {
		return 0, "", false
	}
	title = strings.TrimSpace(string(rs[n:]))
	return n, trimClosingHashes(title), true
}

// trimClosingHashes 去除标题尾部的可选闭合序列（空白 + # 串结尾）。
func trimClosingHashes(title string) string {
	rs := []rune(title)
	i := len(rs)
	for i > 0 && rs[i-1] == '#' {
		i--
	}
	if i == len(rs) {
		return title
	}
	if i == 0 || rs[i-1] == ' ' || rs[i-1] == '\t' {
		return strings.TrimRight(string(rs[:i]), " \t")
	}
	return title
}

// setextUnderline 判定 Setext 下划线行：=== 为一级、--- 为二级（单一字符
// 的 ≥1 长度横线，至多 3 空格缩进，行尾空白允许）。
func setextUnderline(stripped string) (level int, ok bool) {
	rs := []rune(stripped)
	if len(rs) == 0 || (rs[0] != '=' && rs[0] != '-') {
		return 0, false
	}
	for _, r := range rs {
		if r != rs[0] {
			return 0, false
		}
	}
	if rs[0] == '=' {
		return 1, true
	}
	return 2, true
}

// headingText 拼接 Setext 标题文本：段内各行去首尾空白后以空格相连。
func headingText(lines []mdLine) string {
	parts := make([]string, 0, len(lines))
	for _, l := range lines {
		parts = append(parts, strings.TrimSpace(l.text))
	}
	return strings.Join(parts, " ")
}

// pushHeading 维护标题链栈：弹出层级不小于新标题的链尾后入栈。空标题
// （如 "######" 后无文字的空 ATX 行）不入栈也不弹出链尾，避免标题链产出
// " > " 悬空分隔符；空标题行的切节行为不受影响。
func pushHeading(titles *[]string, levels *[]int, level int, title string) {
	if title == "" {
		return
	}
	for n := len(*levels); n > 0 && (*levels)[n-1] >= level; n-- {
		*titles = (*titles)[:n-1]
		*levels = (*levels)[:n-1]
	}
	*titles = append(*titles, title)
	*levels = append(*levels, level)
}

// tableRowStart 判定 lines[i:] 是否以表格块开始：表头行含 "|" 且下一行是
// 分隔行（对齐单元格列数与表头一致）。
func tableRowStart(lines []mdLine, i int) bool {
	if i+1 >= len(lines) {
		return false
	}
	header := stripIndent(lines[i].text)
	if !strings.Contains(header, "|") {
		return false
	}
	delimiter := strings.TrimSpace(stripIndent(lines[i+1].text))
	return isTableDelimiterRow(delimiter) && tableCellCount(header) == tableCellCount(delimiter)
}

// isTableRow 判定表格行：非空且含 "|"。表格块吸收到第一个非表格行或空行。
func isTableRow(line mdLine) bool {
	text := strings.TrimSpace(line.text)
	return text != "" && strings.Contains(text, "|")
}

// isTableDelimiterRow 判定表格分隔行：可选首尾 "|"，单元格为 :?-+:? 对齐
// 标记且非空。
func isTableDelimiterRow(line string) bool {
	s := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(line), "|"), "|")
	if s == "" {
		return false
	}
	for _, cell := range strings.Split(s, "|") {
		if !isAlignmentCell(strings.TrimSpace(cell)) {
			return false
		}
	}
	return true
}

// isAlignmentCell 判定对齐单元格：至多首尾各一个冒号、至少一个连字符。
func isAlignmentCell(cell string) bool {
	rs := []rune(cell)
	i := 0
	if i < len(rs) && rs[i] == ':' {
		i++
	}
	dashes := 0
	for i < len(rs) && rs[i] == '-' {
		i++
		dashes++
	}
	if dashes == 0 {
		return false
	}
	if i < len(rs) && rs[i] == ':' {
		i++
	}
	return i == len(rs)
}

// tableCellCount 数表格行的单元格（去可选首尾 "|" 后按 "|" 分段）。
func tableCellCount(line string) int {
	s := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(line), "|"), "|")
	if s == "" {
		return 0
	}
	return len(strings.Split(s, "|"))
}

// chunkText 是切分实现（可替换点）。delimiter 为空时按 rune 固定窗口滑过
// 全文；非空时先按区分串切出段落块——区分串本身不入块，全空白块（含连续
// 区分串之间的空块）丢弃——超过 size 的段落块再按固定窗口二次切分。相邻
// 窗口重叠 overlap 个 rune 保住边界语义；锚点始终是原文 rune 闭开区间。
// 替换为递归/结构化切分时保持「内容 + 原文锚点」产出形状即可，管线其余
// 环节无感。
//
// 区分串按字节查找：合法 UTF-8 自同步，字节匹配必落在 rune 边界上，与
// rune 序列匹配等价（正文与区分串均来自 proto string，已是合法 UTF-8）。
func chunkText(content string, size, overlap int, delimiter string) []chunk {
	runes := []rune(content)
	if delimiter == "" {
		return windowChunks(nil, runes, 0, len(runes), size, overlap, "")
	}
	delimiterRunes := utf8.RuneCountInString(delimiter)
	var out []chunk
	segStart := 0
	rest := content
	for {
		idx := strings.Index(rest, delimiter)
		if idx < 0 {
			break
		}
		segEnd := segStart + utf8.RuneCountInString(rest[:idx])
		out = segmentChunks(out, runes, segStart, segEnd, size, overlap)
		segStart = segEnd + delimiterRunes
		rest = rest[idx+len(delimiter):]
	}
	return segmentChunks(out, runes, segStart, len(runes), size, overlap)
}

// segmentChunks 产出段落块 runes[from:to)：全空白丢弃，不超 size 整块产出，
// 超长按固定窗口二次切分。
func segmentChunks(out []chunk, runes []rune, from, to, size, overlap int) []chunk {
	if from >= to {
		return out
	}
	text := string(runes[from:to])
	if strings.TrimSpace(text) == "" {
		return out
	}
	if to-from <= size {
		return append(out, chunk{content: text, start: from, end: to})
	}
	return windowChunks(out, runes, from, to, size, overlap, "")
}

// windowChunks 在 runes[from:to) 内按 size 窗口、size-overlap 步长滑切，
// 全空白窗口跳过；末窗口推进到 to 为止。headingPath 随窗口落入切片
// （固定窗口切分传空串）。
func windowChunks(out []chunk, runes []rune, from, to, size, overlap int, headingPath string) []chunk {
	step := size - overlap
	for start := from; start < to; start += step {
		end := start + size
		if end > to {
			end = to
		}
		if text := string(runes[start:end]); strings.TrimSpace(text) != "" {
			out = append(out, chunk{content: text, start: start, end: end, headingPath: headingPath})
		}
		if end == to {
			break
		}
	}
	return out
}
