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
// Embedder 非空时指定摄取空间（知识库级 embedding_model_key）；空值走
// 默认空间解析。ChunkSize/ChunkOverlap 非零时覆盖管线缺省（知识库级切分
// 参数），零值取构造期缺省。ChunkDelimiter 非 nil 时覆盖管线缺省区分串
// （空串即关闭分隔符切分），nil 取构造期缺省。
type Document struct {
	ID             string
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
// （引用可追溯的锚点），start/end 是闭开区间偏移。
type chunk struct {
	content string
	start   int
	end     int
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
	chunks := chunkText(content, chunkSize, chunkOverlap, delimiter)
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
// 文档与原文位置。管线保留键以服务端产出为准覆盖调用方同名键，调用方
// 自定义键原样保留。
func chunkMetadata(doc *Document, index, total int, c chunk) map[string]any {
	metadata := make(map[string]any, len(doc.Metadata)+5)
	for k, v := range doc.Metadata {
		metadata[k] = v
	}
	metadata["source_id"] = strings.TrimSpace(doc.ID)
	metadata["source_name"] = doc.Name
	metadata["chunk_index"] = index
	metadata["chunk_total"] = total
	metadata["anchor"] = fmt.Sprintf("runes %d-%d", c.start, c.end)
	return metadata
}

// chunkText 是切分实现（可替换点）。delimiter 为空时按 rune 固定窗口滑过
// 全文；非空时先按区分串（rune 序列匹配）切出段落块——区分串本身不入块，
// 全空白块（含连续区分串之间的空块）丢弃——超过 size 的段落块再按固定
// 窗口二次切分。相邻窗口重叠 overlap 个 rune 保住边界语义；锚点始终是
// 原文 rune 闭开区间。替换为递归/结构化切分时保持「内容 + 原文锚点」
// 产出形状即可，管线其余环节无感。
func chunkText(content string, size, overlap int, delimiter string) []chunk {
	runes := []rune(content)
	if delimiter == "" {
		return windowChunks(nil, runes, 0, len(runes), size, overlap)
	}
	sep := []rune(delimiter)
	var out []chunk
	segStart := 0
	for i := 0; i <= len(runes)-len(sep); {
		if !runesHavePrefix(runes[i:], sep) {
			i++
			continue
		}
		out = segmentChunks(out, runes, segStart, i, size, overlap)
		i += len(sep)
		segStart = i
	}
	return segmentChunks(out, runes, segStart, len(runes), size, overlap)
}

// segmentChunks 产出段落块 runes[from:to)：全空白丢弃，不超 size 整块产出，
// 超长按固定窗口二次切分。
func segmentChunks(out []chunk, runes []rune, from, to, size, overlap int) []chunk {
	if from >= to || strings.TrimSpace(string(runes[from:to])) == "" {
		return out
	}
	if to-from <= size {
		return append(out, chunk{content: string(runes[from:to]), start: from, end: to})
	}
	return windowChunks(out, runes, from, to, size, overlap)
}

// windowChunks 在 runes[from:to) 内按 size 窗口、size-overlap 步长滑切，
// 全空白窗口跳过；末窗口推进到 to 为止。
func windowChunks(out []chunk, runes []rune, from, to, size, overlap int) []chunk {
	step := size - overlap
	for start := from; start < to; start += step {
		end := start + size
		if end > to {
			end = to
		}
		if text := string(runes[start:end]); strings.TrimSpace(text) != "" {
			out = append(out, chunk{content: text, start: start, end: end})
		}
		if end == to {
			break
		}
	}
	return out
}

// runesHavePrefix 判断 s 是否以 prefix 开头（rune 序列比较）。
func runesHavePrefix(s, prefix []rune) bool {
	if len(s) < len(prefix) {
		return false
	}
	for i, r := range prefix {
		if s[i] != r {
			return false
		}
	}
	return true
}
