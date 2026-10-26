package biz

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// 文档摄取管线（TODO D2）的错误语义：
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
	maxSourceIDChars      = 96
	defaultChunkSize      = 800
	defaultChunkOverlap   = 100
	defaultEmbedDimension = 1024
)

// Document 是摄取管线的领域文档来源。文档来源先限内部（运维导入、
// 后续受权端点等可信生产方），ID 由调用方生成且租户内唯一；对外部
// 网页抓取等不可信来源开放前，需先补来源校验与脱敏切面（TODO E1 对齐）。
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

// DocumentUsecase 是摄取管线用例：切分 → embed → 经 VectorStore 契约
// 落库。租户过滤由 VectorStore 实现强制（决策 6），管线不接触身份以外
// 的存储细节、绝不直连 SQL；身份从认证 ctx 提取（管线由受权入口驱动，
// 身份缺失 fail-closed）。
type DocumentUsecase struct {
	store        VectorStore
	embedder     Embedder
	dimension    int
	chunkSize    int
	chunkOverlap int
}

// NewDocumentUsecase 构造摄取管线。dimension 是 conf.vector.dimension 的
// 透传（装配期由 wire 提供方从配置解出）：与 embedder.Dimensions() 不
// 一致即拒绝构造——维度链路（embedder 模型 → conf → DDL 列）的中间一
// 环在启动期对账，与 D1 的 conf vs DDL 探针同口径，不一致启动即失败。
func NewDocumentUsecase(store VectorStore, embedder Embedder, dimension int, opts ...DocumentOption) (*DocumentUsecase, error) {
	if store == nil {
		return nil, errors.New("biz: document usecase requires a vector store")
	}
	if embedder == nil {
		return nil, errors.New("biz: document usecase requires an embedder")
	}
	if dimension <= 0 {
		return nil, errors.New("biz: document usecase requires a positive configured dimension")
	}
	if embedder.Dimensions() != dimension {
		return nil, fmt.Errorf("biz: embedder dimension %d does not match configured dimension %d; align conf.vector.dimension with the embedding model",
			embedder.Dimensions(), dimension)
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
		dimension:    dimension,
		chunkSize:    o.chunkSize,
		chunkOverlap: o.chunkOverlap,
	}, nil
}

// Ingest 摄取一篇内部文档：切分 → 逐块 embed → 写入向量存储。幂等语义
// 为「同来源覆盖」（决策声明）：切片 ID 由来源 ID 确定性派生
// （"<sourceID>#<index>"），摄取前先按 metadata 的 source_id 锚点删除
// 旧行再整批写入，同 ID 重复摄取等价于整体替换——文档更新后重摄不会
// 留下旧版本的残块。三步不在同一事务（契约无跨文档批写），中途失败
// 存在短暂缺文档窗口，内部来源的重摄可重入补偿，可接受。
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
	if len(sourceID) > maxSourceIDChars {
		return nil, fmt.Errorf("%w: got %d chars, max %d", ErrDocumentSourceTooLong, len(sourceID), maxSourceIDChars)
	}
	content := doc.Content
	if strings.TrimSpace(content) == "" {
		return nil, errors.New("biz: document content is required")
	}
	chunks := chunkText(content, u.chunkSize, u.chunkOverlap)
	if len(chunks) == 0 {
		return nil, errors.New("biz: document content chunks to nothing")
	}

	// 逐块向量化在删除旧行之前完成：embed 失败（端点故障、维度漂移）
	// 时不破坏存量数据，重摄即可补偿。
	docs := make([]*VectorDocument, 0, len(chunks))
	for i, c := range chunks {
		embedding, err := u.embedder.Embed(ctx, c.content)
		if err != nil {
			return nil, fmt.Errorf("biz: embed chunk %d of document %s: %w", i, sourceID, err)
		}
		if len(embedding) != u.dimension {
			return nil, fmt.Errorf("biz: embedder returned dimension %d, configured %d", len(embedding), u.dimension)
		}
		docs = append(docs, &VectorDocument{
			ID:        chunkID(sourceID, i),
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
