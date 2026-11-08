// Package audit 是 Agent 运行面的审计写入面：写入挂点接口 Hook、工具
// 执行终态审计与观测 wrapper、恢复路径代执行的归集器、ack 授权终态观察
// 转写器，以及三者共用的旁路写入纪律（WriteAudit：独立短超时预算、错误
// 与 panic 只记日志）。ack 决定路由的 HTTP 面拦截器在 httpapi 包，经本包
// 导出的 WriteAudit / RecoveryCapture 接入同一纪律；装配点在根包骨架。
package audit

import (
	"context"

	"grape/internal/biz"
)

// Hook 是 Agent 运行面的审计写入挂点：工具执行终态、人工决定确认与
// 自动授权终态三类写入（两类授权行互斥，每次工具调用恰好一条）。挂点
// 缺省 nil 即不审计；写入失败只记日志、不中断运行。方法签名与
// biz.AuditUsecase 一致，wire 装配的用例实例直接满足本接口。接线形态与
// 写入纪律见 README「关键约束」与 tool_audit.go 的 WriteAudit。
type Hook interface {
	RecordToolCall(ctx context.Context, in *biz.ToolCallRecordInput) error
	RecordConfirmation(ctx context.Context, in *biz.ConfirmationRecordInput) error
	// RecordToolCallWithConfirmation 同事务写入工具调用与授权确认的组合
	//（两参至少一个非空）：恢复路径代执行的工具行与确认行组合批量落库，
	// 无代执行（拒绝或降级拒绝）时以单边批量 (nil, conf) 只写确认行。
	RecordToolCallWithConfirmation(ctx context.Context, tool *biz.ToolCallRecordInput, confirmation *biz.ConfirmationRecordInput) error
}

// biz 审计用例满足挂点接口的编译期断言：接线义务的形状约束在此固定，
// 用例签名漂移将在此处编译失败。
var _ Hook = (*biz.AuditUsecase)(nil)
