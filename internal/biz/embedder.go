package biz

import (
	"context"
	"errors"
)

// ErrEmbedderDisabled 是 embedding 设施未启用（conf.vector.enabled=false）
// 时的统一错误：所有调用按能力缺失拒绝，调用方按设施缺失处理而非系统
// 故障（与 ErrVectorStoreDisabled 同语义）。
var ErrEmbedderDisabled = errors.New("biz: embedding facility disabled")

// Embedder 是文本向量化设施的框架无关契约：摄取管线把文档
// 切块后逐块向量化，检索侧把查询文本向量化后送 VectorStore.Search。
// 接口只声明 grape 需要的最小面；具体实现（OpenAI 兼容端点客户端，见
// internal/data/openaiembedder）由 data 层按依赖反转提供，biz 不感知
// 端点协议。框架 knowledge/embedder 的形状不经本接口进入 biz——需要
// 接框架 embedder 时在适配层转换。
//
// 维度纪律：Dimensions 声明产出向量的维度，装配期与 conf.vector.dimension
// 对账（DocumentUsecase 构造期校验，不一致拒绝启动）；实现还必须在每次
// Embed 响应上复核长度，端点维度漂移在调用点显式报错而非落库。
type Embedder interface {
	// Embed 为单段文本生成向量。文本为空白时拒绝；返回向量长度必须
	// 与 Dimensions 一致。
	Embed(ctx context.Context, text string) ([]float32, error)
	// Dimensions 声明产出向量的维度；未启用的占位实现返回 0。
	Dimensions() int
}
