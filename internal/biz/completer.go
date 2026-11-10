package biz

import "context"

// Completer 是单次文本生成设施的框架无关契约：一次 prompt 进、一段文本
// 出，供摄取管线的上下文前缀生成等轻量文本加工消费。实现由 data 层按
// 依赖反转提供（OpenAI 兼容 chat completions），模型在每次调用时经
// TextModelCatalog 按当前身份解析（与 Embedder 的空间解析同口径），biz
// 不感知端点协议与模型目录。
//
// 这是有偿外部调用且只做质量增强：消费方（摄取管线）必须把失败降级为
// 无生成结果继续主流程，不得把 Complete 的错误当作正确性依赖上抛。
type Completer interface {
	// Complete 按单个 prompt 生成一段文本。空白 prompt 拒绝；返回的文本
	// 是端点原文（去协议包装后的 message content），长度与格式由调用方
	// 自行收敛。
	Complete(ctx context.Context, prompt string) (string, error)
}
