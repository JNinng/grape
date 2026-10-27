package agent

import (
	"context"

	"grape/internal/biz"
)

// AuditHook 是 Agent 运行面的审计写入挂点：工具执行路径与 ack 决定路径各有一个写入点——
//
//   - 工具执行：每次工具调用产生终态后记一条工具调用审计（tool_call_id、
//     参数/结果摘要、重试次数、耗时、状态；参数/结果摘要的脱敏由
//     biz.AuditUsecase 在落库前完成，挂点传原文即可）；
//   - ack 决定：Decide 结算后记一条授权确认审计（confirmation_id、决定、
//     remember、路径 live|recovery、操作者）。
//
// 接线形态（见 tool_audit.go / ack_audit.go / stack.go）：Stack 经
// WithAuditHook 可选注入挂点，wire 生产装配传 *biz.AuditUsecase；工具
// 清单包终态审计 wrapper（同时覆盖运行内执行与恢复路径代执行），ack
// 决定路由装 HTTP 面拦截器（live 只写确认行、recovery 与代执行工具行
// 组合批量、deny 归一 Remember=""）；挂点写入失败只记日志、不中断运行
// （审计是旁路观察面，不得反噬业务主流程），缺省 nil 即不审计。
//
// 方法签名与 biz.AuditUsecase 的同名方法一致：接线处 wire 装配的
// *biz.AuditUsecase 实例直接满足本接口（下方编译期断言），无需适配器。
type AuditHook interface {
	RecordToolCall(ctx context.Context, in *biz.ToolCallRecordInput) error
	RecordConfirmation(ctx context.Context, in *biz.ConfirmationRecordInput) error
	// RecordToolCallWithConfirmation 同事务写入工具调用与授权确认的组合
	//（两参至少一个非空）：恢复路径代执行的工具行与确认行组合批量落库，
	// 无代执行（拒绝或降级拒绝）时以单边批量 (nil, conf) 只写确认行。
	RecordToolCallWithConfirmation(ctx context.Context, tool *biz.ToolCallRecordInput, confirmation *biz.ConfirmationRecordInput) error
}

// biz 审计用例满足挂点接口的编译期断言：接线义务的形状约束在此固定，
// C1 接线时若用例签名漂移将在此处编译失败。
var _ AuditHook = (*biz.AuditUsecase)(nil)
