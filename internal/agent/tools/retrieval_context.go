package tools

import (
	"context"

	"grape/internal/biz"

	frameagent "trpc.group/trpc-go/trpc-agent-go/agent"
)

// 检索留痕的 ctx 事实接线：会话 ID（框架 Invocation）与查询文本只在
// agent 层可见，本函数把它们放入 biz 中性 ctx 载体，data 层留痕装饰器
// 读取；事实缺失时载体为空，留痕如实落 NULL，不伪造。

// withRetrievalLogFacts 把留痕事实（会话 ID + 查询文本）注入 ctx：
// 会话 ID 取框架 Invocation 的会话段（与 AG-UI threadId 同源），非框架
// 链路取不到时跳过；查询文本原样注入，脱敏在装饰器落库前执行。
func withRetrievalLogFacts(ctx context.Context, query string) context.Context {
	if inv, ok := frameagent.InvocationFromContext(ctx); ok && inv != nil && inv.Session != nil {
		ctx = biz.WithSessionID(ctx, inv.Session.ID)
	}
	return biz.WithRetrievalQuery(ctx, query)
}
