package tools

import (
	"context"

	"grape/internal/biz"

	frameagent "trpc.group/trpc-go/trpc-agent-go/agent"
)

// 检索留痕的 ctx 事实接线：会话 ID（框架 Invocation）与查询文本只在
// agent 层可见，本函数把它们放进 biz 中性 ctx 载体。
//
// 消费方随写入方迁移：工具检索链路经 KnowledgeSearchUsecase 编排，留痕行
// 由编排层在完成处自写（一次操作一行 + stages 阶段时间线），会话 ID 与
// 来源由编排层从 ctx 读取；data 层留痕装饰器被编排层的抑制标记跳过，只有
// 未抑制的单路直调（框架适配器等）才读取查询文本载体。查询文本的注入
// 保留：它使载体独立于抑制开关——装饰器接管的路径仍能读到查询文本，工具
// 链路行为也与引入编排层前一致（编排层以规范化后的查询为准，两者同值）。
// 事实缺失时载体为空，留痕如实落 NULL，不伪造。

// withRetrievalLogFacts 把留痕事实（会话 ID + 查询文本）注入 ctx：
// 会话 ID 取框架 Invocation 的会话段（与 AG-UI threadId 同源），非框架
// 链路取不到时跳过；查询文本原样注入，脱敏在写入方落库前执行。
func withRetrievalLogFacts(ctx context.Context, query string) context.Context {
	if inv, ok := frameagent.InvocationFromContext(ctx); ok && inv != nil && inv.Session != nil {
		ctx = biz.WithSessionID(ctx, inv.Session.ID)
	}
	return biz.WithRetrievalQuery(ctx, query)
}
