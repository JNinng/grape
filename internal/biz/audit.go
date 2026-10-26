package biz

import (
	"context"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"go.opentelemetry.io/otel/trace"

	"grape/pkg/redact"
)

// Agent 业务审计（TODO 流 E 的 E2）：工具调用审计与授权确认审计的领域
// 契约。写入侧（RecordToolCall / RecordConfirmation / 组合批量）供 Agent
// 运行面（internal/agent 的工具执行与 ack 决定路径，接线归 C1 合并后的
// 小修，接口预留见 agent.AuditHook）调用；查询侧供运维复现视图（A3）
// 的手写端点消费。摘要脱敏在用例内完成：参数摘要走 pkg/redact 的
// tool_param 类别、结果摘要走 retrieval 类别，与日志、复现视图共用同一
// 注册表（E1 单一代码点）。

// 审计记录的领域取值。与 ack / 框架语义一一对应：
//   - 工具调用终态：成功 / 失败（一次调用一行，重试只记最终态与次数）；
//   - 确认决定：approved / denied（pending 是决定前状态，不入审计）；
//   - 决定路径：live（原运行存活直接唤醒）/ recovery（孤儿确认由网关
//     代执行或合成拒绝后续跑，ack PathLive / PathRecovery 同名）；
//   - 记住作用域：session / global（空串语义为「未记住」，落库为 NULL）。
const (
	ToolCallStatusSuccess = "success"
	ToolCallStatusError   = "error"

	ConfirmationDecisionApproved = "approved"
	ConfirmationDecisionDenied   = "denied"

	ConfirmationPathLive     = "live"
	ConfirmationPathRecovery = "recovery"

	ConfirmationRememberSession = "session"
	ConfirmationRememberGlobal  = "global"
)

// 审计列宽与摘要上限：与 schema/95_agent_audit.sql 的列注释一致。
// 摘要列是 text，长度约束由应用层在本文件执行（超限截断），不设数据库
// CHECK——上限调整不伴随存量行迁移。
const (
	// auditIDColumnWidth 是 ID 类字符串列的统一上限（tool_call_id /
	// confirmation_id / decided_by）。
	auditIDColumnWidth = 128
	// auditSessionColumnWidth 是 session_id 列宽上限。
	auditSessionColumnWidth = 255
	// AuditSummaryMaxRunes 是参数/结果摘要的截断上限（rune 计）。脱敏
	// 之后的兜底截断：tool_param 键名过滤不改变长度、retrieval 类别在
	// 标准注册表下自带 512 rune 截断，本上限只防未按标准装配的注册表
	// 或非截断型实现把超长原文送进审计表。
	AuditSummaryMaxRunes = 4096
)

// 审计查询的分页口径：默认页大小与单页上限对齐表注释的 @page 惯例
// （20/200）；offset 非负。
const (
	auditDefaultPageSize = 20
	auditMaxPageSize     = 200
)

// 审计域的类型化错误。审计端点为手写 JSON 端点（无 proto reason 枚举），
// reason 使用域内字面量；语义与生成端点的枚举错误一致。
var (
	// ErrAuditInvalidArgument 表示审计写入/查询的入参非法。
	ErrAuditInvalidArgument = kratoserrors.BadRequest("AGENT_AUDIT_INVALID_ARGUMENT", "invalid audit argument")
	// ErrAuditNotFound 表示审计记录不存在或不在调用者租户范围内——两者
	// 不区分（与 permission.md 的 NotFound 口径一致，不泄露存在性）。
	ErrAuditNotFound = kratoserrors.NotFound("AGENT_AUDIT_NOT_FOUND", "audit record not found")
)

// ToolCallRecordInput 是一次工具调用审计的写入入参：摘要字段收未脱敏
// 原文，由用例在落库前消费脱敏注册表并截断。身份与链路事实不是入参——
// 实现从认证 ctx 提取（与 VectorStore 同一纪律），杜绝请求侧伪造归属。
type ToolCallRecordInput struct {
	// SessionID 是框架会话 ID；agent 恢复路径外的场景可为空。
	SessionID string
	// ToolCallID 是模型生成的工具调用 ID，租户内唯一。
	ToolCallID string
	// ToolName 是工具名。
	ToolName string
	// ArgumentsSummary 是参数摘要原文（写入前脱敏 + 截断）。
	ArgumentsSummary string
	// ResultSummary 是结果摘要原文（写入前脱敏 + 截断）。
	ResultSummary string
	// RetryCount 是重试次数：框架可采集时记实际值，不可采集时记 0
	//（TODO E3 评估回调接线后补真实采集）。
	RetryCount int32
	// DurationMS 是最终一次执行的耗时（毫秒）；未知留 0，落库为 NULL。
	DurationMS int64
	// Status 是调用终态：ToolCallStatusSuccess / ToolCallStatusError。
	Status string
}

// ConfirmationRecordInput 是一次授权确认审计的写入入参（终态决定）。
// 身份与链路事实同上从 ctx 提取；DecidedBy 收 ack 网关的决定人字符串
// （userKey 编码），live 与 recovery 路径都必须提供。
type ConfirmationRecordInput struct {
	// ConfirmationID 是确认 ID（ack Payload，与工具调用 ID 同源）。
	ConfirmationID string
	// SessionID 是确认所属会话 ID，必填。
	SessionID string
	// ToolCallID 是关联的工具调用 ID；可空（ack Payload 未携带时）。
	ToolCallID string
	// ToolName 是被确认的工具名。
	ToolName string
	// Decision 是决定：approved / denied。
	Decision string
	// Remember 是记住作用域：空串=未记住、session / global；拒绝时恒空。
	Remember string
	// Path 是决定生效路径：live / recovery。
	Path string
	// DecidedBy 是决定人（ack 身份字符串，userKey 编码）。
	DecidedBy string
}

// ToolCallAudit 是工具调用审计的领域模型（读侧完整行）。
type ToolCallAudit struct {
	ID               int64
	TenantID         string
	UserID           int64
	TraceID          string
	SessionID        string
	ToolCallID       string
	ToolName         string
	ArgumentsSummary string
	ResultSummary    string
	RetryCount       int32
	DurationMS       int64 // 0 表示未记录
	Status           string
	CalledAt         time.Time
}

// ConfirmationAudit 是授权确认审计的领域模型（读侧完整行）。
type ConfirmationAudit struct {
	ID             int64
	TenantID       string
	UserID         int64
	TraceID        string
	ConfirmationID string
	SessionID      string
	ToolCallID     string
	ToolName       string
	Decision       string
	Remember       string // 空串表示未记住
	Path           string
	DecidedBy      string
	DecidedAt      time.Time
}

// AuditFilter 是审计查询的过滤与分页参数：租户边界不在此列（实现从
// 认证 ctx 提取，与写入侧同一纪律）。SessionID 为空表示不过滤；
// Limit 为 0 时取默认页大小。
type AuditFilter struct {
	SessionID string
	Limit     int
	Offset    int
}

// ToolCallAuditList 是工具调用审计的分页信封。
type ToolCallAuditList struct {
	Items []*ToolCallAudit
	Total int64
}

// ConfirmationAuditList 是授权确认审计的分页信封。
type ConfirmationAuditList struct {
	Items []*ConfirmationAudit
	Total int64
}

// AuditRepo 是审计读写的仓储契约，由 data 层实现；写入与查询都绑定
// 认证身份（IdentityTx，每事务 set_config('app.tenant_id', ..., true)），
// 身份从 ctx 提取，契约不接收身份参数。
type AuditRepo interface {
	// AppendToolCall 写入一条工具调用审计。
	AppendToolCall(ctx context.Context, rec *ToolCallAudit) error
	// AppendConfirmation 写入一条授权确认审计。
	AppendConfirmation(ctx context.Context, rec *ConfirmationAudit) error
	// AppendBatch 在同一事务内原子写入批量组合（两参至少一个非空，校验在
	// 用例侧完成）；单行写入 AppendToolCall / AppendConfirmation 是其退化形。
	AppendBatch(ctx context.Context, tool *ToolCallAudit, confirmation *ConfirmationAudit) error
	// ListToolCalls 分页查询工具调用审计（时间倒序）。
	ListToolCalls(ctx context.Context, filter *AuditFilter) (*ToolCallAuditList, error)
	// ListConfirmations 分页查询授权确认审计（时间倒序）。
	ListConfirmations(ctx context.Context, filter *AuditFilter) (*ConfirmationAuditList, error)
	// GetToolCall 按工具调用 ID 点查；不存在或跨租户返回 ErrAuditNotFound。
	GetToolCall(ctx context.Context, toolCallID string) (*ToolCallAudit, error)
	// GetConfirmation 按确认 ID 点查；不存在或跨租户返回 ErrAuditNotFound。
	GetConfirmation(ctx context.Context, confirmationID string) (*ConfirmationAudit, error)
}

// TraceIDFromContext 从 ctx 的 SpanContext 提取链路 ID（链路中间件注入，
// E1 已打通；遥测开关关闭时 SpanContext 仍恒在）。无链路事实返回空串，
// 落库为 NULL——链路缺失如实留空，不伪造。
func TraceIDFromContext(ctx context.Context) string {
	if sc := trace.SpanContextFromContext(ctx); sc.HasTraceID() {
		return sc.TraceID().String()
	}
	return ""
}

// AuditUsecase 是审计写入/查询的领域用例：入参校验、身份与链路事实
// 提取、摘要脱敏与截断都在这里落地，仓储只见可落库的形态。
type AuditUsecase struct {
	repo     AuditRepo
	registry *redact.Registry
}

// NewAuditUsecase 构造审计用例：registry 是与日志/复现视图共用的脱敏
// 注册表（E1 单一代码点，经 wire 提供同一实例）。
func NewAuditUsecase(repo AuditRepo, registry *redact.Registry) *AuditUsecase {
	return &AuditUsecase{repo: repo, registry: registry}
}

// RecordToolCall 记录一次工具调用审计。身份缺失按未认证 fail-closed；
// 脱敏类别未登记时放行原值但保留截断兜底（见 redactSummary）。
func (uc *AuditUsecase) RecordToolCall(ctx context.Context, in *ToolCallRecordInput) error {
	rec, err := uc.buildToolCall(ctx, in)
	if err != nil {
		return err
	}
	return uc.repo.AppendToolCall(ctx, rec)
}

// RecordConfirmation 记录一次授权确认审计（终态决定）。
func (uc *AuditUsecase) RecordConfirmation(ctx context.Context, in *ConfirmationRecordInput) error {
	rec, err := uc.buildConfirmation(ctx, in)
	if err != nil {
		return err
	}
	return uc.repo.AppendConfirmation(ctx, rec)
}

// RecordToolCallWithConfirmation 在同一事务内写入工具调用与授权确认的
// 组合（写工具触发确认卡的典型场景）；两者至少一个非空，审计行与工具
// 执行结果要么都落库要么都不落库。
func (uc *AuditUsecase) RecordToolCallWithConfirmation(ctx context.Context, tool *ToolCallRecordInput, confirmation *ConfirmationRecordInput) error {
	if tool == nil && confirmation == nil {
		return ErrAuditInvalidArgument
	}
	var toolRec *ToolCallAudit
	var confRec *ConfirmationAudit
	if tool != nil {
		rec, err := uc.buildToolCall(ctx, tool)
		if err != nil {
			return err
		}
		toolRec = rec
	}
	if confirmation != nil {
		rec, err := uc.buildConfirmation(ctx, confirmation)
		if err != nil {
			return err
		}
		confRec = rec
	}
	return uc.repo.AppendBatch(ctx, toolRec, confRec)
}

// ListToolCalls 分页查询工具调用审计（时间倒序）。身份缺失按未认证
// fail-closed：读面与写面同一纪律，查询只以认证身份发生。
func (uc *AuditUsecase) ListToolCalls(ctx context.Context, filter *AuditFilter) (*ToolCallAuditList, error) {
	if _, ok := IdentityFromContext(ctx); !ok {
		return nil, ErrSessionInvalid
	}
	f, err := auditFilterOf(filter)
	if err != nil {
		return nil, err
	}
	return uc.repo.ListToolCalls(ctx, f)
}

// ListConfirmations 分页查询授权确认审计（时间倒序）。
func (uc *AuditUsecase) ListConfirmations(ctx context.Context, filter *AuditFilter) (*ConfirmationAuditList, error) {
	if _, ok := IdentityFromContext(ctx); !ok {
		return nil, ErrSessionInvalid
	}
	f, err := auditFilterOf(filter)
	if err != nil {
		return nil, err
	}
	return uc.repo.ListConfirmations(ctx, f)
}

// GetToolCall 按工具调用 ID 点查。
func (uc *AuditUsecase) GetToolCall(ctx context.Context, toolCallID string) (*ToolCallAudit, error) {
	if _, ok := IdentityFromContext(ctx); !ok {
		return nil, ErrSessionInvalid
	}
	if strings.TrimSpace(toolCallID) == "" {
		return nil, ErrAuditInvalidArgument
	}
	return uc.repo.GetToolCall(ctx, toolCallID)
}

// GetConfirmation 按确认 ID 点查。
func (uc *AuditUsecase) GetConfirmation(ctx context.Context, confirmationID string) (*ConfirmationAudit, error) {
	if _, ok := IdentityFromContext(ctx); !ok {
		return nil, ErrSessionInvalid
	}
	if strings.TrimSpace(confirmationID) == "" {
		return nil, ErrAuditInvalidArgument
	}
	return uc.repo.GetConfirmation(ctx, confirmationID)
}

// buildToolCall 校验写入入参并补齐身份与链路事实、完成摘要脱敏截断。
func (uc *AuditUsecase) buildToolCall(ctx context.Context, in *ToolCallRecordInput) (*ToolCallAudit, error) {
	if in == nil {
		return nil, ErrAuditInvalidArgument
	}
	switch {
	case strings.TrimSpace(in.ToolCallID) == "" || len(in.ToolCallID) > auditIDColumnWidth:
		return nil, ErrAuditInvalidArgument
	case strings.TrimSpace(in.ToolName) == "" || len(in.ToolName) > auditIDColumnWidth:
		return nil, ErrAuditInvalidArgument
	case in.Status != ToolCallStatusSuccess && in.Status != ToolCallStatusError:
		return nil, ErrAuditInvalidArgument
	case in.RetryCount < 0 || in.DurationMS < 0:
		return nil, ErrAuditInvalidArgument
	case in.DurationMS > math.MaxInt32:
		// duration_ms 列是 int4：超列宽的耗时按入参非法拒绝，让
		// 异常输入显式失败而非静默回绕。
		return nil, ErrAuditInvalidArgument
	case len(in.SessionID) > auditSessionColumnWidth:
		return nil, ErrAuditInvalidArgument
	}
	auth, ok := IdentityFromContext(ctx)
	if !ok {
		return nil, ErrSessionInvalid
	}
	return &ToolCallAudit{
		UserID:           auth.UserID,
		TraceID:          TraceIDFromContext(ctx),
		SessionID:        in.SessionID,
		ToolCallID:       in.ToolCallID,
		ToolName:         in.ToolName,
		ArgumentsSummary: uc.redactSummary(redact.CategoryToolParam, in.ArgumentsSummary),
		ResultSummary:    uc.redactSummary(redact.CategoryRetrieval, in.ResultSummary),
		RetryCount:       in.RetryCount,
		DurationMS:       in.DurationMS,
		Status:           in.Status,
	}, nil
}

// buildConfirmation 校验写入入参并补齐身份与链路事实。拒绝决定的
// remember 语义恒为「未记住」，非空即入参非法。
func (uc *AuditUsecase) buildConfirmation(ctx context.Context, in *ConfirmationRecordInput) (*ConfirmationAudit, error) {
	if in == nil {
		return nil, ErrAuditInvalidArgument
	}
	switch {
	case strings.TrimSpace(in.ConfirmationID) == "" || len(in.ConfirmationID) > auditIDColumnWidth:
		return nil, ErrAuditInvalidArgument
	case in.SessionID == "" || len(in.SessionID) > auditSessionColumnWidth:
		return nil, ErrAuditInvalidArgument
	case len(in.ToolCallID) > auditIDColumnWidth:
		return nil, ErrAuditInvalidArgument
	case strings.TrimSpace(in.ToolName) == "" || len(in.ToolName) > auditIDColumnWidth:
		return nil, ErrAuditInvalidArgument
	case in.Decision != ConfirmationDecisionApproved && in.Decision != ConfirmationDecisionDenied:
		return nil, ErrAuditInvalidArgument
	case in.Path != ConfirmationPathLive && in.Path != ConfirmationPathRecovery:
		return nil, ErrAuditInvalidArgument
	case in.DecidedBy == "" || len(in.DecidedBy) > auditIDColumnWidth:
		return nil, ErrAuditInvalidArgument
	case in.Decision == ConfirmationDecisionDenied && in.Remember != "":
		return nil, ErrAuditInvalidArgument
	case in.Remember != "" && in.Remember != ConfirmationRememberSession && in.Remember != ConfirmationRememberGlobal:
		return nil, ErrAuditInvalidArgument
	}
	auth, ok := IdentityFromContext(ctx)
	if !ok {
		return nil, ErrSessionInvalid
	}
	return &ConfirmationAudit{
		UserID:         auth.UserID,
		TraceID:        TraceIDFromContext(ctx),
		ConfirmationID: in.ConfirmationID,
		SessionID:      in.SessionID,
		ToolCallID:     in.ToolCallID,
		ToolName:       in.ToolName,
		Decision:       in.Decision,
		Remember:       in.Remember,
		Path:           in.Path,
		DecidedBy:      in.DecidedBy,
	}, nil
}

// redactSummary 按类别消费脱敏注册表并做长度兜底。未登记类别（Redact
// 返回 false）的选择是【放行原值但保留截断】：拒绝会让这次工具调用
// 完全没有审计行——审计的目的是留痕，因脱敏语义未声明而丢弃记录比
// 落一条已截断的原文危害更大；截断兜底保证不会落超长原文。装配方按
// E1 口径补齐类别后自动全量生效。已登记类别输出脱敏值后同样过上限
// （非截断型实现如掩码/哈希天然短，此处只为统一语义）。
func (uc *AuditUsecase) redactSummary(category, value string) string {
	if value == "" {
		return ""
	}
	if uc.registry != nil {
		redacted, _ := uc.registry.Redact(category, value)
		value = redacted
	}
	return truncateAuditSummary(value)
}

// truncateAuditSummary 按 rune 截断到 AuditSummaryMaxRunes（多字节字符
// 不劈开），尾部以标记说明发生了截断，便于审计侧识别不完整摘要。
func truncateAuditSummary(value string) string {
	if utf8.RuneCountInString(value) <= AuditSummaryMaxRunes {
		return value
	}
	runes := []rune(value)
	return string(runes[:AuditSummaryMaxRunes]) + "...(truncated)"
}

// auditFilterOf 归一查询过滤：页大小缺省、上限与 offset 非负校验在
// biz 落地（fail-closed，不静默截断）。
func auditFilterOf(filter *AuditFilter) (*AuditFilter, error) {
	f := &AuditFilter{}
	if filter != nil {
		*f = *filter
	}
	f.SessionID = strings.TrimSpace(f.SessionID)
	if len(f.SessionID) > auditSessionColumnWidth {
		return nil, ErrAuditInvalidArgument
	}
	if f.Limit == 0 {
		f.Limit = auditDefaultPageSize
	}
	if f.Limit < 0 || f.Limit > auditMaxPageSize || f.Offset < 0 {
		return nil, ErrAuditInvalidArgument
	}
	return f, nil
}
