package agent

import (
	"context"

	"grape/internal/biz"
)

// AuditHook 是 Agent 运行面的审计写入挂点（TODO 流 E 的 E2 接口预留）：
// 工具执行路径与 ack 决定路径各有一个写入点——
//
//   - 工具执行：每次工具调用产生终态后记一条工具调用审计（tool_call_id、
//     参数/结果摘要、重试次数、耗时、状态；参数/结果摘要的脱敏由
//     biz.AuditUsecase 在落库前完成，挂点传原文即可）；
//   - ack 决定：Decide 结算后记一条授权确认审计（confirmation_id、决定、
//     remember、路径 live|recovery、操作者）。
//
// C1 接线义务（并入后的小修，本窗口不实施以避免触碰 internal/agent
// 既有文件——见并行冲突纪律）：在 Stack 上增加可选注入点（字段 +
// NewStackWithOptions 的可选参数，缺省 nil 即不审计），工具执行与
// gateway.Decide 返回后调用本接口；写入失败只记日志、不中断运行
// （审计是旁路观察面，不得反噬业务主流程）。
//
// 方法签名与 biz.AuditUsecase 的同名方法一致：接线时 wire 装配的
// *biz.AuditUsecase 实例直接满足本接口（下方编译期断言），无需适配器。
type AuditHook interface {
	RecordToolCall(ctx context.Context, in *biz.ToolCallRecordInput) error
	RecordConfirmation(ctx context.Context, in *biz.ConfirmationRecordInput) error
}

// biz 审计用例满足挂点接口的编译期断言：接线义务的形状约束在此固定，
// C1 接线时若用例签名漂移将在此处编译失败。
var _ AuditHook = (*biz.AuditUsecase)(nil)
