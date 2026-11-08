package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"grape/internal/biz"

	framagent "trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// 工具执行终态审计：wrapper 在装配点包一层工具清单，对运行内执行与恢复
// 路径代执行是同一挂点并自带计时；目前只转发 CallableTool 形状。摘要收
// 未脱敏原文，脱敏与截断由 biz.AuditUsecase 落库前完成。

// AuditedTools 给工具清单包上审计/观测 wrapper：仅包装 CallableTool 形状
// 的工具（当前全量），其余形状原样透传。hook 与 observer 都可为 nil
// （各自缺省零行为），两者全 nil 时不应调用本函数（装配侧判空后整段不
// 装配，保证缺省零行为变化）。
func AuditedTools(tools []tool.Tool, hook Hook, observer biz.AgentRunObserver, logger *slog.Logger) []tool.Tool {
	wrapped := make([]tool.Tool, 0, len(tools))
	for _, t := range tools {
		ct, ok := t.(tool.CallableTool)
		if !ok {
			wrapped = append(wrapped, t)
			continue
		}
		wrapped = append(wrapped, &auditTool{CallableTool: ct, hook: hook, observer: observer, logger: logger})
	}
	return wrapped
}

// auditTool 是工具执行终态的审计与观测 wrapper：嵌入内部工具透传声明，
// Call 计时并记终态（hook 写审计行，observer 推送计数/耗时，两个旁路面
// 共用单一包装点）。调用 ID 从执行 ctx 取——运行内执行由框架处理器注入
// 模型生成的 tool call ID（tool.ContextKeyToolCallID），恢复路径代执行由
// ack 决定审计面注入确认 ID（ack Payload 中确认 ID 与工具调用 ID 同源）。
type auditTool struct {
	tool.CallableTool
	hook     Hook
	observer biz.AgentRunObserver
	logger   *slog.Logger
}

// Call 执行内部工具并在终态后记审计：审计写入不改变工具结果与错误。
// 工具自身 panic 同样是执行终态：recover 后先落一条 status=error 的审计
// 行（结果摘要记 panic 值），再原样 re-panic——不改框架对 panic 的处理
// 语义，只补齐审计面（框架执行边界吞掉 panic 时工具行不再缺失）。
func (t *auditTool) Call(ctx context.Context, jsonArgs []byte) (result any, err error) {
	start := time.Now()
	defer func() {
		if r := recover(); r != nil {
			t.record(ctx, jsonArgs, nil, fmt.Errorf("tool panicked: %v", r), time.Since(start))
			panic(r)
		}
		t.record(ctx, jsonArgs, result, err, time.Since(start))
	}()
	return t.CallableTool.Call(ctx, jsonArgs)
}

// record 组装一条工具调用审计并写入。无调用 ID 的执行无法构造合法审计行
// （biz 侧 ToolCallID 必填），只记日志跳过；命中恢复路径捕获器（目标调用
// ID 一致）时只归集不落库，由 ack 决定审计面在决定结算后与确认行组合
// 批量写入。观测推送在归集分支之前：代执行同样是一次工具调用终态。
func (t *auditTool) record(ctx context.Context, jsonArgs []byte, result any, err error, d time.Duration) {
	name := toolNameOf(t.CallableTool)
	callID, _ := tool.ToolCallIDFromContext(ctx)
	if strings.TrimSpace(callID) == "" {
		t.logger.Warn("tool call id missing in context, skip tool call audit", "tool", name)
		return
	}
	status := biz.ToolCallStatusSuccess
	if err != nil {
		status = biz.ToolCallStatusError
	}
	if t.observer != nil {
		t.observer.ToolCallObserved(name, status, d)
	}
	in := &biz.ToolCallRecordInput{
		SessionID:        sessionIDOf(ctx),
		ToolCallID:       callID,
		ToolName:         name,
		ArgumentsSummary: string(jsonArgs),
		ResultSummary:    summarizeToolResult(result),
		// 框架不对外暴露重试次数（重试在框架执行面内部收敛，终态只见最终
		// 一次调用），按 biz 契约恒记 0；最终口径见根目录 operations.md
		// 「工具重试次数口径」，框架透出计数时在本 wrapper 单点补采。
		RetryCount: 0,
		DurationMS: d.Milliseconds(),
		Status:     status,
	}
	if err != nil {
		in.ResultSummary = err.Error()
	}
	if t.hook == nil {
		// 仅观测形态（审计挂点缺省）：工具行不写，观测推送已完成。
		return
	}
	if c := captureFromContext(ctx); c != nil && c.toolCallID == callID {
		c.Add(in)
		return
	}
	WriteAudit(ctx, t.logger, "record tool call", func(wctx context.Context) error {
		return t.hook.RecordToolCall(wctx, in)
	})
}

// RecoveryCapture 是恢复路径代执行的审计归集器：ack 决定审计面在决定
// 请求 ctx 放入捕获器并注入确认 ID（ContextWithCapture），代执行经审计
// wrapper 命中目标 ID 时归集于此，决定结算后与授权确认行组合批量落库
// （同事务原子）。续跑运行内的后续工具调用 ID 与目标不同，不被捕获，
// 仍走 wrapper 直写。
type RecoveryCapture struct {
	toolCallID string
	sessionID  string

	mu   sync.Mutex
	tool *biz.ToolCallRecordInput
}

// NewRecoveryCapture 构造以 toolCallID 为归集目标的捕获器；sessionID
// 用于补齐代执行行的会话 ID（恢复路径代执行的 ctx 无框架 Invocation）。
func NewRecoveryCapture(toolCallID, sessionID string) *RecoveryCapture {
	return &RecoveryCapture{toolCallID: toolCallID, sessionID: sessionID}
}

// captureKeyType 是捕获器的 ctx 键（防碰撞用私有类型）。
type captureKeyType struct{}

// ContextWithCapture 把捕获器放入 ctx，并以其目标 ID 注入框架工具调用
// ID 键：两者恒成对注入（确认 ID 即工具调用 ID，ack Payload 两字段同源），
// 代执行经审计 wrapper 以该 ID 命中捕获器。
func ContextWithCapture(ctx context.Context, c *RecoveryCapture) context.Context {
	ctx = context.WithValue(ctx, captureKeyType{}, c)
	return context.WithValue(ctx, tool.ContextKeyToolCallID{}, c.toolCallID)
}

func captureFromContext(ctx context.Context) *RecoveryCapture {
	c, _ := ctx.Value(captureKeyType{}).(*RecoveryCapture)
	return c
}

// Add 归集一次代执行（首个为准——同一确认 ID 只应代执行一次）。
func (c *RecoveryCapture) Add(in *biz.ToolCallRecordInput) {
	in.SessionID = c.sessionID
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tool == nil {
		c.tool = in
	}
}

// Take 取走归集的代执行行；无代执行（拒绝或降级拒绝）返回 nil。nil 接收
// 者安全：决定审计面的回退单边写不假设捕获器必然存在。
func (c *RecoveryCapture) Take() *biz.ToolCallRecordInput {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tool
}

// auditWriteTimeout 是单次审计写入的短超时预算：审计是旁路观察面，存储
// 悬挂不得反噬业务主流程——超时后放弃该行并记日志。变量形态供测试注入
// 极短预算（生产恒为 5s，无热路径取用）。
var auditWriteTimeout = 5 * time.Second

// WriteAudit 安全执行一次审计写入：写入走脱离请求生命周期的独立短超时
// ctx（WithoutCancel 保留身份与链路事实——IdentityTx 取身份、审计行取
// trace_id 都依赖 ctx 值；超时与取消不再跟随请求），hook 错误与 panic 都
// 只记日志、不向上传播——审计不得反噬业务主流程。三个写入面（工具终态、
// 决定路由、授权终态观察）共用这一纪律。
func WriteAudit(ctx context.Context, logger *slog.Logger, action string, write func(ctx context.Context) error) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("audit hook panicked", "action", action, "panic", r)
		}
	}()
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), auditWriteTimeout)
	defer cancel()
	if err := write(wctx); err != nil {
		logger.Error("audit write failed", "action", action, "error", err)
	}
}

// sessionIDOf 从工具执行 ctx 取框架会话 ID：运行内执行的 ctx 携带
// Invocation（框架处理器注入），其 Session.ID 即会话 ID；恢复路径代执行
// 的 ctx 无 Invocation，会话 ID 由捕获器补齐。取不到留空——biz 契约允许
// 恢复路径外的场景为空，落库为 NULL。
func sessionIDOf(ctx context.Context) string {
	if inv, ok := framagent.InvocationFromContext(ctx); ok && inv.Session != nil {
		return inv.Session.ID
	}
	return ""
}

// summarizeToolResult 把工具结果投影为审计摘要原文：与模型看到的形状一致
// （JSON）。序列化失败退化为 fmt 形状——不因摘要失败丢审计行，长度与
// 脱敏由 biz 侧兜底。
func summarizeToolResult(result any) string {
	if result == nil {
		return ""
	}
	b, err := json.Marshal(result)
	if err != nil {
		return fmt.Sprintf("%v", result)
	}
	return string(b)
}

// toolNameOf 安全读取工具声明名：声明缺失返回空串（biz 侧校验拒绝空名，
// 审计行按写入失败留日志）。
func toolNameOf(t tool.Tool) string {
	if d := t.Declaration(); d != nil {
		return d.Name
	}
	return ""
}
