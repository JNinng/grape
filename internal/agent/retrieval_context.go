package agent

import (
	"context"

	"grape/internal/biz"

	frameagent "trpc.group/trpc-go/trpc-agent-go/agent"
)

// 检索留痕的 ctx 事实接线：data 层留痕装饰器
// （pg_sqlc.retrieval_log.go）写 agent_retrieval_log 需要会话 ID 与查询
// 文本两项事实，它们只在本层可见——会话 ID 在框架执行 ctx 携带的
// Invocation 里（biz/data 禁止导入框架包），查询文本只在检索工具的入参
// 里。本函数在检索执行现场把两项事实放进 biz 的中性 ctx 载体，装饰器
// 从载体读取；事实缺失（非框架链路的直连契约调用）载体为空，留痕如实
// 落 NULL，不伪造。

// withRetrievalLogFacts 把留痕事实（会话 ID + 查询文本）注入 ctx：
// 会话 ID 取框架 Invocation 的会话段（userKey 编码的会话键的会话段，
// 与 AG-UI threadId 同源），非框架链路取不到时跳过；查询文本原样注入
// （脱敏在装饰器落库前执行，载体只传递事实）。空白值一律不注入。
func withRetrievalLogFacts(ctx context.Context, query string) context.Context {
	if inv, ok := frameagent.InvocationFromContext(ctx); ok && inv != nil && inv.Session != nil {
		ctx = biz.WithSessionID(ctx, inv.Session.ID)
	}
	return biz.WithRetrievalQuery(ctx, query)
}
