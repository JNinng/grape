package biz

import (
	"context"
	"errors"
	"time"
)

// ErrRerankUnavailable 是 rerank 模型键完全未配置的哨兵（租户与全局
// sys_config 均无 ai.rerank.model 值）：重排是检索质量增强面，调用方按
// 跳过该层处理（行为与无重排一致），不视为故障。「键已配置但行停用/
// 软删/类别不符/密钥损坏」返回普通错误，按调用失败降级并记服务日志，
// 与本哨兵相区分（错误语义总表见 model.go）。
var ErrRerankUnavailable = errors.New("biz: reranker unavailable")

// RerankResult 是一次重排调用的结果：分数与提供该分数的模型键。ModelKey
// 供调用方（检索编排层）在阶段留痕（stages 的 rerank 阶段）记录重排模型
// ——实现在解析出端点事实后即随结果报告，端点调用失败时结果与错误同时
// 返回（分数为空），调用方按错误降级、按模型键留痕。
type RerankResult struct {
	// Scores 是与输入等长、顺序对齐的相关性分数切片（分数越高越相关，
	// 跨调用不可比）；调用失败为 nil。
	Scores []float64
	// ModelKey 是本次调用的重排模型键（解析现场原样）；未解析出端点
	// 事实（ErrRerankUnavailable）为空。
	ModelKey string
}

// Reranker 是相关性重排设施的框架无关契约：对 query 与 documents 逐段
// 打相关性分，供检索管线在召回融合之后收窄排序。实现由 data 层按依赖
// 反转提供（OpenAI 兼容 /rerank 端点），模型在每次调用时经
// RerankModelCatalog 按当前身份解析（与 Completer 的文本模型解析同
// 口径），biz 不感知端点协议与模型目录——解析出的模型键随结果返回，
// 供留痕面记录。
//
// 这是有偿外部调用且只做质量增强：消费方（检索管线）必须把失败降级为
// 融合序继续检索，不得把 Rerank 的错误当作正确性依赖上抛；唯一例外是
// ErrRerankUnavailable——未配置按跳过该层处理，属正常形态而非故障。
type Reranker interface {
	// Rerank 对 query 与 documents 逐段打相关性分：返回结果携带与输入
	// 等长、顺序对齐的分数切片（不重排输入，排序由调用方做）与重排模
	// 型键；documents 为空返回空分数不调用端点；空白 query 拒绝；未配
	// 置 rerank 模型返回 ErrRerankUnavailable（调用方按跳过该层处理，
	// 与调用失败相区分）。
	Rerank(ctx context.Context, query string, documents []string) (*RerankResult, error)
}

// RerankModelSpec 是解析后的重排模型端点事实，字段对齐 TextModelSpec：
// provider/base_url/model/api_key/timeout 来自 sys_model 租户行或平台行
// （rerank 无 conf 兜底条目，键有值而无行按配置错误拒绝）。APIKey 已解密。
type RerankModelSpec struct {
	Key      string
	Provider string
	BaseURL  string
	Model    string
	APIKey   string
	Timeout  time.Duration
}

// RerankModelCatalog 解析当前身份的重排模型。实现从认证 ctx 取身份，按
// 「租户配置 > 全局配置」选 ai.rerank.model 键，再按「租户行 > 平台行」
// 找端点；键完全未配置报 ErrRerankUnavailable，键有值而行停用、软删或
// 类别不符返回普通错误（不静默回落）。
type RerankModelCatalog interface {
	ResolveRerankModel(ctx context.Context) (RerankModelSpec, error)
}
