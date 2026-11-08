package biz

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
//   - defaultChunkSize/defaultChunkOverlap 是简版固定长度切分的缺省
//     （rune 计），与主流 embedding 模型的输入窗口量级匹配；替换为
//     语义切分（递归/按结构）时改 DocumentOption 注入，接口面不变。
const (
	maxSourceIDChars    = 96
	defaultChunkSize    = 800
	defaultChunkOverlap = 100
)

// Document 是摄取管线的领域文档来源。来源限内部可信生产方；对外部网页
// 抓取等不可信来源开放前，须先补来源校验与脱敏切面（对齐 pkg/redact）。
// ID 由调用方生成且租户内唯一。
type Document struct {
	ID       string
	Name     string
	Content  string
	Metadata map[string]any
}

// IngestResult 是摄取结果：Chunks 为本次写入的切片数，Replaced 为覆盖
// 语义下先行删除的旧切片数（首次摄取为 0）。
type IngestResult struct {
	DocumentID string
	Chunks     int
	Replaced   int64
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
	chunkSize    int
	chunkOverlap int
}

// WithChunking 覆盖切分参数：size 为单块 rune 上限，overlap 为相邻块
// 重叠 rune 数（0 ≤ overlap < size）。
func WithChunking(size, overlap int) DocumentOption {
	return func(o *documentOptions) { o.chunkSize, o.chunkOverlap = size, overlap }
}

// DocumentUsecase 是摄取管线用例：切分 → embed → 经 VectorStore 契约落库。
// 租户过滤由 VectorStore 实现强制，管线不接触存储细节、不直连 SQL；身份
// 从认证 ctx 提取（管线由受权入口驱动），缺失 fail-closed。嵌入空间在
// 每次 Ingest 现场解析，默认空间切换即迁移路径（见 README「关键文件导览」）。
type DocumentUsecase struct {
	store        VectorStore
	embedder     Embedder
	resolver     EmbedderResolver
	chunkSize    int
	chunkOverlap int
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
	o := &documentOptions{chunkSize: defaultChunkSize, chunkOverlap: defaultChunkOverlap}
	for _, opt := range opts {
		opt(o)
	}
	if o.chunkSize <= 0 {
		return nil, errors.New("biz: chunk size must be positive")
	}
	if o.chunkOverlap < 0 || o.chunkOverlap >= o.chunkSize {
		return nil, errors.New("biz: chunk overlap must be in [0, chunk size)")
	}
	return &DocumentUsecase{
		store:        store,
		embedder:     embedder,
		resolver:     resolver,
		chunkSize:    o.chunkSize,
		chunkOverlap: o.chunkOverlap,
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
	chunks := chunkText(content, u.chunkSize, u.chunkOverlap)
	if len(chunks) == 0 {
		return nil, errors.New("biz: document content chunks to nothing")
	}

	// 摄取空间在删除旧行之前解析：本次切片落在解析出的默认空间，旧
	// 切片（可能属于其他空间）由覆盖语义跨空间清除，重摄取即换空间
	// 迁移路径。
	space, dimension, err := u.resolver.ResolveDefaultEmbedder(ctx)
	if err != nil {
		return nil, fmt.Errorf("biz: resolve default embedder for document %s: %w", sourceID, err)
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
	return &IngestResult{DocumentID: sourceID, Chunks: len(docs), Replaced: replaced}, nil
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

// chunkText 是简版固定长度切分（可替换点）：按 rune 窗口滑过原文，相邻
// 块重叠 overlap 个 rune 保住边界语义；全空白块跳过，编号连续、锚点仍
// 指向原文位置。替换为递归/结构化切分时保持「内容 + 原文锚点」产出
// 形状即可，管线其余环节无感。
func chunkText(content string, size, overlap int) []chunk {
	runes := []rune(content)
	var out []chunk
	step := size - overlap
	for start := 0; start < len(runes); start += step {
		end := start + size
		if end > len(runes) {
			end = len(runes)
		}
		if text := string(runes[start:end]); strings.TrimSpace(text) != "" {
			out = append(out, chunk{content: text, start: start, end: end})
		}
		if end == len(runes) {
			break
		}
	}
	return out
}
