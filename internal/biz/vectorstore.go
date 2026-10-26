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
//     不区分（与 permission.md 的 NotFound 口径一致，不泄露存在性）。
var (
	ErrVectorStoreDisabled = errors.New("biz: vector store disabled")
	ErrVectorDocNotFound   = errors.New("biz: vector document not found")
)

// VectorDocument 是向量存储的领域文档。ID 由调用方生成（摄取管线或
// 框架侧，字符串形态，租户内唯一）；租户/用户身份不是文档字段——实现
// 必须从认证 ctx 提取（见 VectorStore 注释），文档自身不携带归属，杜绝
// 请求侧伪造归属。
type VectorDocument struct {
	ID        string
	Name      string
	Content   string
	Embedding []float32
	Metadata  map[string]any
	CreatedAt time.Time
	UpdatedAt time.Time
}

// VectorFilter 是向量读面共用的过滤与分页参数。IDs 与 Metadata 叠加时
// 取交集；均空表示不过滤（Count/GetMetadata 视为租户全量，DeleteByFilter
// 拒绝执行，见各方法注释）。MinScore 仅 Search 消费；Limit 为 0 时由
// 实现取默认页大小；Offset 仅 GetMetadata 消费（分页枚举）。
type VectorFilter struct {
	IDs      []string
	Metadata map[string]any
	MinScore float64
	Limit    int
	Offset   int
}

// ScoredVectorDocument 是检索命中：Score 为归一化相似度（[0,1]，越大越
// 相似，1.0 即同向），与框架 pgvector 实现的分数语义一致。
type ScoredVectorDocument struct {
	Document *VectorDocument
	Score    float64
}

// VectorStore 是向量检索设施的框架无关契约（决策 6 的强制隔离点）：
// 声明在 biz、由 data 以参数化 SQL 实现（pgvector 算子不进 sqlc catalog，
// 见 sqlc/README.md）、由 agent 适配层包装成框架 knowledge/vectorstore
// 接口供 D2 检索工具消费。框架 pgvector store 自建连接且启动建表，不
// 引入。
//
// 租户隔离由实现强制，契约不接收身份参数：所有方法从认证 ctx
// （IdentityFromContext）提取 tenant_id/user_id，写入侧身份落到租户列
// 与 metadata，读侧每条 SQL 强制租户过滤；身份缺失即拒绝（fail-closed），
// 绝不回落默认租户。embedding 维度在装配期与 DDL 列校验一致，运行期
// 不再协商。
type VectorStore interface {
	// Add 写入文档与向量。ID 必填且租户内唯一，重复即报错；embedding
	// 维度须与表定义一致。
	Add(ctx context.Context, doc *VectorDocument) error
	// Get 按 ID 取文档（含向量与 metadata）；不存在或跨租户返回
	// ErrVectorDocNotFound。
	Get(ctx context.Context, id string) (*VectorDocument, error)
	// Update 整体覆盖文档的可变部分（name/content/embedding/metadata）；
	// 不存在或跨租户返回 ErrVectorDocNotFound。ID 与归属不可借本方法变更。
	Update(ctx context.Context, doc *VectorDocument) error
	// Delete 按 ID 删除；不存在或跨租户返回 ErrVectorDocNotFound。
	Delete(ctx context.Context, id string) error
	// Search 以查询向量做余弦相似检索，按相似度降序返回至多 Limit 条；
	// 过滤条件与租户边界取交集。MinScore 过滤归一化分数。
	Search(ctx context.Context, embedding []float32, filter *VectorFilter) ([]*ScoredVectorDocument, error)
	// DeleteByFilter 按 IDs/Metadata 交集删除，返回删除行数；两个条件
	// 均空时拒绝执行——租户级全量清理是跨租户管理口径（super 路径，
	// D1 只留占位），不设普通入口。
	DeleteByFilter(ctx context.Context, filter *VectorFilter) (int64, error)
	// Count 统计租户内满足过滤条件的文档数；条件为空即租户全量。
	Count(ctx context.Context, filter *VectorFilter) (int64, error)
	// GetMetadata 按 IDs/Metadata 过滤枚举文档 metadata（不含向量与
	// 正文），按写入时间升序分页；返回值以文档 ID 为键。
	GetMetadata(ctx context.Context, filter *VectorFilter) (map[string]map[string]any, error)
}
