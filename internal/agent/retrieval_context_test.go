package agent

import (
	"context"
	"testing"

	"grape/internal/biz"

	frameagent "trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// D3 留痕 ctx 事实载体单测：会话 ID 从框架执行 ctx 提取、查询文本原样
// 注入、事实缺失不伪造（载体留空，留痕侧落 NULL）。

// invocationContext 模拟框架运行时：把带会话的 Invocation 放进 ctx。
func invocationContext(sessionID string) context.Context {
	ctx := context.Background()
	return frameagent.NewInvocationContext(ctx, &frameagent.Invocation{
		Session: &session.Session{ID: sessionID},
	})
}

func TestWithRetrievalLogFacts_session_and_query_injected_from_invocation(t *testing.T) {
	ctx := withRetrievalLogFacts(invocationContext("sess-run-1"), "grape retrieval")
	if got := biz.SessionIDFromContext(ctx); got != "sess-run-1" {
		t.Fatalf("session id = %q, want sess-run-1", got)
	}
	if got := biz.RetrievalQueryFromContext(ctx); got != "grape retrieval" {
		t.Fatalf("query text = %q, want original", got)
	}
}

func TestWithRetrievalLogFacts_without_invocation_only_query_injected(t *testing.T) {
	// 非框架链路（直连契约调用）：会话事实不可得，载体不得伪造。
	ctx := withRetrievalLogFacts(context.Background(), "grape retrieval")
	if got := biz.SessionIDFromContext(ctx); got != "" {
		t.Fatalf("session id = %q, want empty outside framework invocations", got)
	}
	if got := biz.RetrievalQueryFromContext(ctx); got != "grape retrieval" {
		t.Fatalf("query text = %q, want original", got)
	}
}

func TestWithRetrievalLogFacts_blank_query_not_injected(t *testing.T) {
	ctx := withRetrievalLogFacts(invocationContext("sess-run-1"), "   ")
	if got := biz.RetrievalQueryFromContext(ctx); got != "" {
		t.Fatalf("query text = %q, want empty (blank queries are not facts)", got)
	}
	if got := biz.SessionIDFromContext(ctx); got != "sess-run-1" {
		t.Fatalf("session id = %q, want sess-run-1 (unaffected by blank query)", got)
	}
}
