package pg_sqlc

import (
	"context"
	"errors"

	"grape/internal/biz"
	"grape/internal/data/pg_sqlc/gen"

	"github.com/jackc/pgx/v5"
)

// auditRepo 是 biz.AuditRepo 的 PostgreSQL 实现（TODO 流 E 的 E2）。
// 写入与查询全部经身份事务门面 IdentityTx 执行（每事务
// set_config('app.tenant_id', ..., true)）：写入侧租户由门面注入、
// 查询侧租户由 RLS 兜底（schema/95_agent_audit.sql 的策略），读侧
// WHERE 再显式带同源租户谓词（queries/agent_audit.sql 文件头声明的
// 应用层兜底，RLS 被关闭时仍不越租户）。表为追加写，仓储只提供
// 写入与读取，没有更新/删除面。
type auditRepo struct {
	data *Data
}

// NewAuditRepo 构造审计仓储。
func NewAuditRepo(data *Data) biz.AuditRepo {
	return &auditRepo{data: data}
}

// auditIdentity 从认证 ctx 提取身份：缺失或非法一律拒绝。身份只来自
// 服务端认证中间件，永不受请求侧输入影响。
func auditIdentity(ctx context.Context) (Identity, error) {
	id, ok := biz.IdentityFromContext(ctx)
	if !ok {
		return Identity{}, errors.New("pg_sqlc: audit requires an authenticated identity")
	}
	auth := pgAuth(id)
	if err := auth.validate(); err != nil {
		return Identity{}, err
	}
	return auth, nil
}

// AppendToolCall 写入一条工具调用审计。租户取门面注入的会话变量，
// 创建者与记录用户同值（审计行由该用户的运行产生）。
func (r *auditRepo) AppendToolCall(ctx context.Context, rec *biz.ToolCallAudit) error {
	if rec == nil {
		return errors.New("pg_sqlc: tool call audit record is required")
	}
	auth, err := auditIdentity(ctx)
	if err != nil {
		return err
	}
	return r.data.IdentityTx(ctx, auth, func(s *IdentityTx) error {
		_, err := s.Q().InsertAgentToolCallLog(ctx, gen.InsertAgentToolCallLogParams{
			UserID:           auth.UserID,
			TraceID:          textPtr(rec.TraceID),
			SessionID:        textPtr(rec.SessionID),
			ToolCallID:       rec.ToolCallID,
			ToolName:         rec.ToolName,
			ArgumentsSummary: textPtr(rec.ArgumentsSummary),
			ResultSummary:    textPtr(rec.ResultSummary),
			RetryCount:       rec.RetryCount,
			DurationMs:       int32Ptr(int(rec.DurationMS)),
			Status:           rec.Status,
		})
		return err
	})
}

// AppendConfirmation 写入一条授权确认审计。
func (r *auditRepo) AppendConfirmation(ctx context.Context, rec *biz.ConfirmationAudit) error {
	if rec == nil {
		return errors.New("pg_sqlc: confirmation audit record is required")
	}
	auth, err := auditIdentity(ctx)
	if err != nil {
		return err
	}
	return r.data.IdentityTx(ctx, auth, func(s *IdentityTx) error {
		_, err := s.Q().InsertAgentConfirmationLog(ctx, gen.InsertAgentConfirmationLogParams{
			UserID:         auth.UserID,
			TraceID:        textPtr(rec.TraceID),
			ConfirmationID: rec.ConfirmationID,
			SessionID:      rec.SessionID,
			ToolCallID:     textPtr(rec.ToolCallID),
			ToolName:       rec.ToolName,
			Decision:       rec.Decision,
			Remember:       textPtr(rec.Remember),
			Path:           rec.Path,
			DecidedBy:      rec.DecidedBy,
		})
		return err
	})
}

// AppendBatch 在同一身份事务内原子写入批量组合：工具调用与其授权确认
// 成对产生，要么都落库要么都不落库；两参至少一个非空（用例已校验，
// 仓储再守一道）。唯一约束冲突（重复写入同一 tool_call_id /
// confirmation_id）按已存在错误上抛，暴露重复埋点缺陷而非静默吞并。
func (r *auditRepo) AppendBatch(ctx context.Context, tool *biz.ToolCallAudit, confirmation *biz.ConfirmationAudit) error {
	if tool == nil && confirmation == nil {
		return errors.New("pg_sqlc: audit batch requires at least one record")
	}
	auth, err := auditIdentity(ctx)
	if err != nil {
		return err
	}
	return r.data.IdentityTx(ctx, auth, func(s *IdentityTx) error {
		if tool != nil {
			if _, err := s.Q().InsertAgentToolCallLog(ctx, gen.InsertAgentToolCallLogParams{
				UserID:           auth.UserID,
				TraceID:          textPtr(tool.TraceID),
				SessionID:        textPtr(tool.SessionID),
				ToolCallID:       tool.ToolCallID,
				ToolName:         tool.ToolName,
				ArgumentsSummary: textPtr(tool.ArgumentsSummary),
				ResultSummary:    textPtr(tool.ResultSummary),
				RetryCount:       tool.RetryCount,
				DurationMs:       int32Ptr(int(tool.DurationMS)),
				Status:           tool.Status,
			}); err != nil {
				return err
			}
		}
		if confirmation != nil {
			_, err := s.Q().InsertAgentConfirmationLog(ctx, gen.InsertAgentConfirmationLogParams{
				UserID:         auth.UserID,
				TraceID:        textPtr(confirmation.TraceID),
				ConfirmationID: confirmation.ConfirmationID,
				SessionID:      confirmation.SessionID,
				ToolCallID:     textPtr(confirmation.ToolCallID),
				ToolName:       confirmation.ToolName,
				Decision:       confirmation.Decision,
				Remember:       textPtr(confirmation.Remember),
				Path:           confirmation.Path,
				DecidedBy:      confirmation.DecidedBy,
			})
			return err
		}
		return nil
	})
}

// ListToolCalls 分页查询工具调用审计（时间倒序，同秒以 id tie-break）。
func (r *auditRepo) ListToolCalls(ctx context.Context, filter *biz.AuditFilter) (*biz.ToolCallAuditList, error) {
	auth, err := auditIdentity(ctx)
	if err != nil {
		return nil, err
	}
	sessionID := textPtr(filter.SessionID)
	var out *biz.ToolCallAuditList
	err = r.data.IdentityTx(ctx, auth, func(s *IdentityTx) error {
		rows, err := s.Q().ListAgentToolCallLogs(ctx, gen.ListAgentToolCallLogsParams{
			SessionID: sessionID,
			Limit:     int64(filter.Limit),
			Offset:    int64(filter.Offset),
		})
		if err != nil {
			return err
		}
		total, err := s.Q().CountAgentToolCallLogs(ctx, sessionID)
		if err != nil {
			return err
		}
		items := make([]*biz.ToolCallAudit, 0, len(rows))
		for i := range rows {
			items = append(items, toolCallAuditToBiz(&rows[i]))
		}
		out = &biz.ToolCallAuditList{Items: items, Total: total}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ListConfirmations 分页查询授权确认审计（时间倒序，同秒以 id tie-break）。
func (r *auditRepo) ListConfirmations(ctx context.Context, filter *biz.AuditFilter) (*biz.ConfirmationAuditList, error) {
	auth, err := auditIdentity(ctx)
	if err != nil {
		return nil, err
	}
	sessionID := textPtr(filter.SessionID)
	var out *biz.ConfirmationAuditList
	err = r.data.IdentityTx(ctx, auth, func(s *IdentityTx) error {
		rows, err := s.Q().ListAgentConfirmationLogs(ctx, gen.ListAgentConfirmationLogsParams{
			SessionID: sessionID,
			Limit:     int64(filter.Limit),
			Offset:    int64(filter.Offset),
		})
		if err != nil {
			return err
		}
		total, err := s.Q().CountAgentConfirmationLogs(ctx, sessionID)
		if err != nil {
			return err
		}
		items := make([]*biz.ConfirmationAudit, 0, len(rows))
		for i := range rows {
			items = append(items, confirmationAuditToBiz(&rows[i]))
		}
		out = &biz.ConfirmationAuditList{Items: items, Total: total}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// GetToolCall 按工具调用 ID 点查；不存在或跨租户统一
// ErrAuditNotFound（不泄露存在性）。
func (r *auditRepo) GetToolCall(ctx context.Context, toolCallID string) (*biz.ToolCallAudit, error) {
	auth, err := auditIdentity(ctx)
	if err != nil {
		return nil, err
	}
	var out *biz.ToolCallAudit
	err = r.data.IdentityTx(ctx, auth, func(s *IdentityTx) error {
		row, err := s.Q().GetAgentToolCallLogByToolCallID(ctx, toolCallID)
		if err != nil {
			return err
		}
		out = toolCallAuditToBiz(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// GetConfirmation 按确认 ID 点查；不存在或跨租户统一
// ErrAuditNotFound（不泄露存在性）。
func (r *auditRepo) GetConfirmation(ctx context.Context, confirmationID string) (*biz.ConfirmationAudit, error) {
	auth, err := auditIdentity(ctx)
	if err != nil {
		return nil, err
	}
	var out *biz.ConfirmationAudit
	err = r.data.IdentityTx(ctx, auth, func(s *IdentityTx) error {
		row, err := s.Q().GetAgentConfirmationLogByConfirmationID(ctx, confirmationID)
		if err != nil {
			return err
		}
		out = confirmationAuditToBiz(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// mapErr 把无行（不存在/跨租户不可见）统一映射为 ErrAuditNotFound。
func (r *auditRepo) mapErr(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return biz.ErrAuditNotFound
	}
	return err
}

// toolCallAuditToBiz 转换为领域模型；可空列 NULL 渲染为零值。
func toolCallAuditToBiz(po *gen.AgentToolCallLog) *biz.ToolCallAudit {
	return &biz.ToolCallAudit{
		ID:               po.ID,
		TenantID:         po.TenantID,
		UserID:           po.UserID,
		TraceID:          textValue(po.TraceID),
		SessionID:        textValue(po.SessionID),
		ToolCallID:       po.ToolCallID,
		ToolName:         po.ToolName,
		ArgumentsSummary: textValue(po.ArgumentsSummary),
		ResultSummary:    textValue(po.ResultSummary),
		RetryCount:       po.RetryCount,
		DurationMS:       int64(int32Value(po.DurationMs)),
		Status:           po.Status,
		CalledAt:         tsToTime(po.CalledAt),
	}
}

// confirmationAuditToBiz 转换为领域模型；可空列 NULL 渲染为零值。
func confirmationAuditToBiz(po *gen.AgentConfirmationLog) *biz.ConfirmationAudit {
	return &biz.ConfirmationAudit{
		ID:             po.ID,
		TenantID:       po.TenantID,
		UserID:         po.UserID,
		TraceID:        textValue(po.TraceID),
		ConfirmationID: po.ConfirmationID,
		SessionID:      po.SessionID,
		ToolCallID:     textValue(po.ToolCallID),
		ToolName:       po.ToolName,
		Decision:       po.Decision,
		Remember:       textValue(po.Remember),
		Path:           po.Path,
		DecidedBy:      po.DecidedBy,
		DecidedAt:      tsToTime(po.DecidedAt),
	}
}

// textPtr 是空串 → NULL 的可空文本渲染：领域侧空串语义即「未记录」，
// 落库为 NULL（列注释与查询语义一致）。
func textPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// textValue 是 NULL → 空串的读侧渲染（与 textPtr 互逆）。
func textValue(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// int32Ptr 是 0 → NULL 的可空整数渲染：耗时、top_k 等可空整数共用的
// 单一渲染点（0 语义统一为「未记录 / 未指定」，落 NULL）。
func int32Ptr(v int) *int32 {
	if v == 0 {
		return nil
	}
	out := int32(v)
	return &out
}

// int32Value 是 NULL → 0 的读侧渲染（与 int32Ptr 互逆）。
func int32Value(v *int32) int32 {
	if v == nil {
		return 0
	}
	return *v
}
