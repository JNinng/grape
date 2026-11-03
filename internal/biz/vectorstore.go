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
//     不区分（与 permission.md 的 NotFound 口径一致，不泄露存在性）；
//   - ErrEmbedderUnknown：文档/过滤声明的嵌入空间键未在设施注册表中
//     声明（与 Embedder 契约同语义，透传复用）。
var (
	ErrVectorStoreDisabled = errors.New("biz: vector store disabled")
	ErrVectorDocNotFound   = errors.New("biz: vector document not found")
)

// VectorDocument 是向量存储的领域文档。ID 由调用方生成（摄取管线或
// 框架侧，字符串形态，租户内唯一）；租户/用户身份不是文档字段——实现
// 必须从认证 ctx 提取（见 VectorStore 注释），文档自身不携带归属，杜绝
// 请求侧伪造归属。
//
// 嵌入空间身份与维度成对显式出现（Embedder + Dimension）：写入侧两者
// 必填，实现对照空间注册表（conf.Vector.embedders 解析，实现侧细节）
// 校验——空间必须已声明、维度必须等于该空间的声明维度、且等于
// embedding 实际长度，任一不符拒绝（声明不可伪造）；读取侧按行回填，
// 文档落在哪个空间由数据说话。
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

// VectorFilter 是向量读面共用的过滤与分页参数。IDs 与 Metadata 叠加时
// 取交集；均空表示不过滤（Count/GetMetadata 视为租户全量，DeleteByFilter
// 拒绝执行，见各方法注释）。Embedder 为可空过滤：Search 必填（与
// Dimension 成对声明检索空间并三方对账），Count/DeleteByFilter/
// GetMetadata 非空时收敛到该空间、空即跨空间操作；Dimension 与 MinScore
// 仅 Search 消费。Limit 在 Search 为 0 时由实现取默认页大小、在
// GetMetadata 非正为不限量（此时 Offset 必须为 0）；Offset 仅 GetMetadata
// 消费（分页枚举）。
type VectorFilter struct {
	IDs       []string
	Embedder  string
	Dimension int
	Metadata  map[string]any
	MinScore  float64
	Limit     int
	Offset    int
}

// ScoredVectorDocument 是检索命中：Score 为归一化相似度（[0,1]，越大越
// 相似，1.0 即同向），与框架 pgvector 实现的分数语义一致。
type ScoredVectorDocument struct {
	Document *VectorDocument
	Score    float64
}

// VectorStore 是向量检索设施的框架无关契约（租户隔离的强制点）：
// 声明在 biz、由 data 以参数化 SQL 实现（pgvector 算子不进 sqlc catalog，
// 见 sqlc/README.md）、由 agent 适配层包装成框架 knowledge/vectorstore
// 接口供检索工具消费。框架 pgvector store 自建连接且启动建表，不
// 引入。
//
// 租户隔离由实现强制，契约不接收身份参数：所有方法从认证 ctx
// （IdentityFromContext）提取 tenant_id/user_id，写入侧身份落到租户列
// 与 metadata，读侧每条 SQL 强制租户过滤；身份缺失即拒绝（fail-closed），
// 绝不回落默认租户。
//
// 嵌入空间纪律：一表承载多空间（不同模型/版本/维度的向量共存，物理
// 上 embedding 列无固定维度、embedder/embed_dim 列逐行声明身份，DB 层
// CHECK 把维度列与实际向量长度绑定），空间是余弦相似可比性的边界——
// 相似度只对同一空间内的向量有意义，跨空间混排检索不存在。Search 以
// filter.Embedder/Dimension 声明检索空间（必填，实现与查询向量长度及
// 注册表三方对账）；写侧以文档字段声明归属；Count/DeleteByFilter/
// GetMetadata 的 Embedder 为可空过滤（空 = 跨空间操作，重摄取迁移正是
// 借此清理旧空间切片）。维度 N 的检索 SQL 以内联常量下钻 vector(N)
// cast——维度来自校验后的注册表值而非请求侧输入，与表名白名单同纪律。
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
	// DeleteByFilter 按 IDs/Embedder/Metadata 交集删除，返回删除行数；
	// 所有条件均空时拒绝执行——租户级全量清理是跨租户管理口径（super
	// 路径，向量层仅留占位，见 permission.md），不设普通入口。
	DeleteByFilter(ctx context.Context, filter *VectorFilter) (int64, error)
	// Count 统计租户内满足过滤条件的文档数；条件为空即租户全量，
	// Embedder 非空则限该空间。
	Count(ctx context.Context, filter *VectorFilter) (int64, error)
	// GetMetadata 按 IDs/Embedder/Metadata 过滤枚举文档 metadata（不含
	// 向量与正文），按写入时间升序分页；返回值以文档 ID 为键。Limit
	// 非正为不限量，此时 Offset 必须为 0。
	GetMetadata(ctx context.Context, filter *VectorFilter) (map[string]map[string]any, error)
}
