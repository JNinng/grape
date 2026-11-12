package biz

import (
	"context"
	"errors"
	"time"
)

// 向量存储设施的错误语义：
//   - ErrVectorStoreDisabled：设施未启用（conf.vector.enabled=false），
//     所有方法一律拒绝，调用方按能力缺失处理而非系统故障；
//   - ErrVectorDocNotFound：文档不存在或不在调用者租户范围内——两者
//     不区分，不泄露存在性；
//   - ErrEmbedderUnknown：文档/过滤声明的嵌入空间键未在设施注册表中
//     声明（与 Embedder 契约同语义，透传复用）。
var (
	ErrVectorStoreDisabled = errors.New("biz: vector store disabled")
	ErrVectorDocNotFound   = errors.New("biz: vector document not found")
)

// VectorDocument 是向量存储的领域文档。ID 由调用方生成、租户内唯一；
// 归属不是文档字段——实现从认证 ctx 提取，杜绝请求侧伪造。
//
// Embedder + Dimension 成对声明嵌入空间身份与维度：写入侧必填并经注册表
// 对账（空间已声明、维度等于声明值且等于向量实际长度），读取侧按行回填。
type VectorDocument struct {
	ID        string
	Embedder  string
	Dimension int
	Name      string
	Content   string
	Embedding []float32
	Metadata  map[string]any
	CreatedAt time.Time
	UpdatedAt time.Time
}

// VectorMetadataOp 是 metadata 条件比较操作符的封闭集合：翻译层只接受
// 这里的取值，操作符不来自请求侧输入（列名与操作符都不进 SQL 文本，
// 仅作为翻译决策）。
type VectorMetadataOp string

const (
	// VectorMetadataEq 文本等值：metadata->>key = value。
	VectorMetadataEq VectorMetadataOp = "eq"
	// VectorMetadataNe 文本不等：metadata->>key <> value。
	VectorMetadataNe VectorMetadataOp = "ne"
	// VectorMetadataIn 文本集合成员：metadata->>key = ANY(values)。
	VectorMetadataIn VectorMetadataOp = "in"
	// VectorMetadataLt / Lte / Gt / Gte 数值比较：metadata->>key 显式
	// ::bigint 后与 value::bigint 比较。
	VectorMetadataLt  VectorMetadataOp = "lt"
	VectorMetadataLte VectorMetadataOp = "lte"
	VectorMetadataGt  VectorMetadataOp = "gt"
	VectorMetadataGte VectorMetadataOp = "gte"
)

// MetadataCondition 是 metadata 的单条比较条件。Key 与 Value 都经占位符
// 下发（键名与值都不拼进 SQL 文本）；Op 只接受 VectorMetadataOp 常量集。
// Eq/Ne/Lt/Lte/Gt/Gte 取 Values[0]；In 取全部 Values。
type MetadataCondition struct {
	Key    string
	Op     VectorMetadataOp
	Values []any
}

// VectorFilter 是向量读面共用的过滤与分页参数。IDs、Metadata 与
// Conditions 叠加时取交集；均空表示不过滤（Count/GetMetadata 视为租户
// 全量，DeleteByFilter 与 UpdateMetadata 拒绝执行，见各方法注释）。
// Embedder 为可空过滤：Search/SearchLexical 必填（与 Dimension 成对声明
// 检索空间并对账），Count/DeleteByFilter/GetMetadata/UpdateMetadata 非
// 空时收敛到该空间、空即跨空间操作；Dimension 与 MinScore 仅检索两方法
//（Search/SearchLexical）消费。Limit 在检索两方法为 0 时由实现取默认页
// 大小、在 GetMetadata 非正为不限量（此时 Offset 必须为 0）；Offset 仅
// GetMetadata 消费（分页枚举）。
type VectorFilter struct {
	IDs        []string
	Embedder   string
	Dimension  int
	Metadata   map[string]any
	Conditions []MetadataCondition
	MinScore   float64
	Limit      int
	Offset     int
}

// ScoredVectorDocument 是检索命中。Score 的语义随检索方法不同：Search
// 产出归一化相似度（[0,1]，越大越相似，1.0 即同向，与框架 pgvector
// 实现的分数语义一致）；SearchLexical 产出 ts_rank_cd 相关性（未归一，
// 跨查询不可比）。同方法内分数可比，跨方法（混合检索融合）只应使用
// 排名。
type ScoredVectorDocument struct {
	Document *VectorDocument
	Score    float64
}

// RetrievalFilter 是检索路径在 ctx 上携带的附加条件：框架的 SearchQuery
// 类型不携带条件 DSL，检索工具在执行现场组装 §3.4 的固定条件（状态/有效期/
// 可选知识库），由框架适配器读取并并入 VectorFilter。缺失时适配器不加条件
// （摄取/清理路径不消费本载体）。
type RetrievalFilter struct {
	Conditions []MetadataCondition
}

type retrievalFilterKey struct{}

// WithRetrievalFilter 注入本次检索的附加 metadata 条件。
func WithRetrievalFilter(ctx context.Context, filter RetrievalFilter) context.Context {
	if len(filter.Conditions) == 0 {
		return ctx
	}
	return context.WithValue(ctx, retrievalFilterKey{}, filter)
}

// RetrievalFilterFromContext 读取检索附加条件；缺失返回零值与 false。
func RetrievalFilterFromContext(ctx context.Context) (RetrievalFilter, bool) {
	f, ok := ctx.Value(retrievalFilterKey{}).(RetrievalFilter)
	return f, ok
}

// VectorStore 是向量检索设施的框架无关契约，也是租户隔离的强制点：
// 声明在 biz、由 data 以参数化 SQL 实现、由 agent 适配层包装成框架
// knowledge/vectorstore 接口供检索工具消费。
//
// 契约不接收身份参数：实现从认证 ctx 提取租户/用户身份并在每条读写强制
// 租户过滤，身份缺失即拒绝（fail-closed），绝不回落默认租户。
// 嵌入空间纪律与方法边界见 README「关键约束」与 data/README.md「向量介质」。
type VectorStore interface {
	// Add 写入文档与向量。ID 必填且租户内唯一，重复即报错；嵌入空间
	// 身份与维度必填且经注册表对账（见 VectorDocument 注释）。
	Add(ctx context.Context, doc *VectorDocument) error
	// Get 按 ID 取文档（含向量、嵌入空间身份与 metadata）；不存在或
	// 跨租户返回 ErrVectorDocNotFound。
	Get(ctx context.Context, id string) (*VectorDocument, error)
	// Update 整体覆盖文档的可变部分（嵌入空间身份/维度/content/
	// embedding/metadata——空间变更即显式重嵌入，无隐式换形）；不存在
	// 或跨租户返回 ErrVectorDocNotFound。ID 与归属不可借本方法变更。
	Update(ctx context.Context, doc *VectorDocument) error
	// Delete 按 ID 删除；不存在或跨租户返回 ErrVectorDocNotFound。
	Delete(ctx context.Context, id string) error
	// Search 以查询向量在 filter.Embedder 声明的空间内做余弦相似检索，
	// 按相似度降序返回至多 Limit 条；过滤条件与租户边界取交集。
	// MinScore 过滤归一化分数。Embedder/Dimension 必填且与查询向量
	// 长度、注册表三方对账，任一不符拒绝。
	Search(ctx context.Context, embedding []float32, filter *VectorFilter) ([]*ScoredVectorDocument, error)
	// SearchLexical 以查询文本在 filter.Embedder 声明的空间内做词法检索
	//（tsquery 匹配 + ts_rank_cd 排序），按相关性降序返回至多 Limit 条；
	// Conditions/IDs/Metadata/MinScore/Limit 语义与 Search 一致，过滤条件
	// 与租户边界取交集。Embedder/Dimension 必填且经注册表对账——词法路
	// 与向量路必须共享同一文档宇宙（同租户同空间同条件），实现内查询
	// 分词与写入建索引用同一 Tokenizer 实例（口径漂移即静默漏召回）。
	//
	// Score 是 ts_rank_cd 相关性（未归一、跨查询不可比，仅同一查询内
	// 排序有意义）——与 Search 的归一化相似度 [0,1] 语义不同，跨方法
	// 比较分数没有意义；融合消费方只应使用排名。查询分词为空（无有效
	// 词元）返回空切片不触库。
	SearchLexical(ctx context.Context, query string, filter *VectorFilter) ([]*ScoredVectorDocument, error)
	// DeleteByFilter 按 IDs/Embedder/Metadata/Conditions 交集删除，返回
	// 删除行数；所有条件均空时拒绝执行——租户级全量清理不设普通入口。
	DeleteByFilter(ctx context.Context, filter *VectorFilter) (int64, error)
	// UpdateMetadata 按 IDs/Embedder/Metadata/Conditions 交集把 patch 合并
	// 进命中行的 metadata（jsonb ||），返回更新行数；只改 metadata 列，
	// 不动正文与向量。所有条件均空时拒绝执行。知识库目录的状态/有效期
	// 变更加载在 chunk metadata 上，失败必须让上层事务回滚。
	UpdateMetadata(ctx context.Context, filter *VectorFilter, patch map[string]any) (int64, error)
	// Count 统计租户内满足过滤条件的文档数；条件为空即租户全量，
	// Embedder 非空则限该空间。
	Count(ctx context.Context, filter *VectorFilter) (int64, error)
	// GetMetadata 按 IDs/Embedder/Metadata 过滤枚举文档 metadata（不含
	// 向量与正文），按写入时间升序分页；返回值以文档 ID 为键。Limit
	// 非正为不限量，此时 Offset 必须为 0。
	GetMetadata(ctx context.Context, filter *VectorFilter) (map[string]map[string]any, error)
	// List 按 IDs/Embedder/Metadata/Conditions 交集枚举文档（含正文与
	// metadata，不回填 Embedding——枚举读面不需要向量，避免批量拉取），
	// 按写入时间升序；Limit 非正为不限量，此时 Offset 必须为 0。
	List(ctx context.Context, filter *VectorFilter) ([]*VectorDocument, error)
}
