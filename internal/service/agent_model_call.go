package service

import (
	"context"

	agentv1 "grape/api/agent/v1"
	"grape/internal/biz"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// AgentModelCallService 是模型调用留痕查询面的传输层适配器：DTO ↔ DO 转换、
// 动态查询协议转换，委托给 biz 用例。表 append-only，本服务无写端点（写入
// 由 biz.ModelCallObserver 观察面旁路落库）；租户边界由存储层 RLS 承担。
// 输入/输出摘要与错误为落库时已脱敏形态，本层不二次脱敏、不回传原文。
type AgentModelCallService struct {
	agentv1.UnimplementedAgentModelCallServiceServer
	uc *biz.ModelCallLogUsecase
}

// NewAgentModelCallService 构造模型调用留痕查询服务。
func NewAgentModelCallService(uc *biz.ModelCallLogUsecase) *AgentModelCallService {
	return &AgentModelCallService{uc: uc}
}

// ListModelCalls 按动态查询协议返回一页模型调用留痕；协议非法映射为
// INVALID_ARGUMENT。
func (s *AgentModelCallService) ListModelCalls(ctx context.Context, req *agentv1.ListModelCallsRequest) (*agentv1.ModelCallLogSet, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	q, err := convertQuery(req.GetQuery(), nil)
	if err != nil {
		return nil, invalidQueryError(biz.ErrModelCallInvalidArgument, err)
	}
	out, err := s.uc.List(ctx, auth, q)
	if err != nil {
		return nil, err
	}
	items := make([]*agentv1.ModelCallLog, 0, len(out.Items))
	for _, row := range out.Items {
		items = append(items, toProtoModelCallLog(row))
	}
	return &agentv1.ModelCallLogSet{
		Items:      items,
		Total:      out.Total,
		HasMore:    out.HasMore,
		NextCursor: out.NextCursor,
		Page:       pageOf(req.GetQuery()),
	}, nil
}

// strOrNil 将 DO 的空串语义渲染为字段缺省（NULL），非空原样返回。
func strOrNil(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

// toProtoModelCallLog 将领域模型转换为 DTO。可空列 NULL 渲染为字段缺省
//（token 与文档 ID 的 0 值语义为「未上报 / 无文档事实」，不以 0 冒充）。
func toProtoModelCallLog(row *biz.ModelCallLog) *agentv1.ModelCallLog {
	out := &agentv1.ModelCallLog{
		Id:        row.ID,
		UserId:    row.UserID,
		Scenario:  row.Scenario,
		Status:    row.Status,
		CreatedBy: row.CreatedBy,
		CreatedAt: timestamppb.New(row.CreatedAt),
	}
	out.TraceId = strOrNil(row.TraceID)
	out.ModelKey = strOrNil(row.ModelKey)
	out.Endpoint = strOrNil(row.Endpoint)
	if row.DocumentID != 0 {
		v := row.DocumentID
		out.DocumentId = &v
	}
	out.InputSummary = strOrNil(row.InputSummary)
	out.OutputSummary = strOrNil(row.OutputSummary)
	if row.InputTokens != 0 {
		v := row.InputTokens
		out.InputTokens = &v
	}
	if row.OutputTokens != 0 {
		v := row.OutputTokens
		out.OutputTokens = &v
	}
	if row.DurationMS != 0 {
		v := int32(row.DurationMS)
		out.DurationMs = &v
	}
	out.Error = strOrNil(row.Error)
	return out
}
