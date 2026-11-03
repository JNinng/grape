package biz

import (
	"context"
	"errors"
	"strings"
)

// embedding 设施的错误语义：
//   - ErrEmbedderDisabled：设施未启用（conf.vector.enabled=false）时的
//     统一错误，所有调用按能力缺失拒绝，调用方按设施缺失处理而非系统
//     故障（与 ErrVectorStoreDisabled 同语义）；
//   - ErrEmbedderUnknown：空间键未在设施注册表中声明——调用方传入了
//     配置之外的嵌入空间（含租户配置指向已下线空间的场景），按配置
//     错误显式拒绝，不静默回落其他空间（静默换空间会让摄取与检索
//     悄悄失配）。
var (
	ErrEmbedderDisabled = errors.New("biz: embedding facility disabled")
	ErrEmbedderUnknown  = errors.New("biz: unknown embedder space")
)

// Embedder 是文本向量化设施的框架无关契约：摄取管线把文档
// 切块后逐块向量化，检索侧把查询文本向量化后送 VectorStore.Search。
// 接口只声明 grape 需要的最小面；具体实现（OpenAI 兼容端点客户端，见
// internal/data/openaiembedder）由 data 层按依赖反转提供，biz 不感知
// 端点协议与空间注册表的落点（conf）。框架 knowledge/embedder 的形状
// 不经本接口进入 biz——需要接框架 embedder 时在适配层转换。
//
// 空间纪律：系统可同时声明多个嵌入空间（模型[@版本]，见
// conf.Vector.embedders），不同空间的向量互不可比，文本必须与其目标
// 空间成对送入。维度契约显式成对出现：Dimension 声明空间的向量长度，
// 实现在每次 Embed 响应上复核长度，端点维度漂移在调用点显式报错而非
// 落库；biz 不 import conf，键→维度注册表是实现侧细节。
type Embedder interface {
	// Embed 为单段文本生成向量。embedder 是嵌入空间键；空白文本与
	// 未知空间拒绝；返回向量长度必须与 Dimension(embedder) 一致。
	Embed(ctx context.Context, embedder string, text string) ([]float32, error)
	// Dimension 返回空间的向量维度；未知空间报 ErrEmbedderUnknown，
	// 未启用的占位实现返回 ErrEmbedderDisabled。
	Dimension(embedder string) (int, error)
}

// EmbedderResolver 解析默认嵌入空间：租户配置（sys_tenant_config 的
// vector.embedder 键）优先，未配置回落全局 conf 兜底；更高一级的请求侧
// 显式指定由调用方入参面承担（当前生产链路恒走解析器，入口扩展时在
// 解析之上叠加）。默认空间决定摄取入库与默认检索落在哪个空间——切换
// 默认不迁移存量，旧文档留在原空间，重摄取（source_id 幂等覆盖）即
// 迁移路径。实现在 data 层（租户配置读取），biz 只声明形状；设施未启用
// 返回 ErrEmbedderDisabled，租户配置指向未声明空间返回
// ErrEmbedderUnknown（不静默回落全局——静默换空间会让摄取/检索悄悄
// 失配）。
type EmbedderResolver interface {
	// ResolveDefaultEmbedder 返回当前身份的默认空间键与维度，身份与
	// 维度成对出现；身份缺失 fail-closed（实现从认证 ctx 提取）。
	ResolveDefaultEmbedder(ctx context.Context) (embedder string, dimension int, err error)
}

// 嵌入空间的 ctx 事实载体：检索工具在执行现场解析出默认空间后经 ctx
// 传入框架适配器——框架的 SearchQuery 类型不可扩展（不携带空间概念），
// 适配器从本载体读取事实填充 biz.VectorFilter。空白键或非正维度不
// 注入（缺失在消费点 fail-closed，不伪造）。

type embedderSpaceKey struct{}

// WithEmbedderSpace 注入本次向量操作的嵌入空间（键 + 维度成对）。
// 仅供框架适配层在执行现场调用；契约适配器消费。
func WithEmbedderSpace(ctx context.Context, embedder string, dimension int) context.Context {
	if strings.TrimSpace(embedder) == "" || dimension <= 0 {
		return ctx
	}
	return context.WithValue(ctx, embedderSpaceKey{}, embedderSpace{embedder: embedder, dimension: dimension})
}

// EmbedderSpaceFromContext 读取嵌入空间事实；缺失以 ok=false 返回，
// 消费方必须拒绝而非猜测空间。
func EmbedderSpaceFromContext(ctx context.Context) (embedder string, dimension int, ok bool) {
	s, _ := ctx.Value(embedderSpaceKey{}).(embedderSpace)
	return s.embedder, s.dimension, s.embedder != ""
}

// embedderSpace 是载体值：键与维度不可分离携带。
type embedderSpace struct {
	embedder  string
	dimension int
}
