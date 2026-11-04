package service

import (
	"context"
	"encoding/json"

	auditv1 "grape/api/audit/v1"
	"grape/internal/biz"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// AuditService 是审计日志只读查询面的传输层适配器：DTO ↔ DO 转换、动态
// 查询协议转换，委托给 biz 用例。两表 append-only，本服务无写端点；租户
// 边界由存储层 RLS 承担。
type AuditService struct {
	auditv1.UnimplementedAuditServiceServer
	uc *biz.AuditLogUsecase
}

// NewAuditService 构造审计日志查询服务。
func NewAuditService(uc *biz.AuditLogUsecase) *AuditService {
	return &AuditService{uc: uc}
}

// ListLoginLogs 按动态查询协议返回一页登录日志；协议非法映射为
// INVALID_ARGUMENT。
func (s *AuditService) ListLoginLogs(ctx context.Context, req *auditv1.ListLoginLogsRequest) (*auditv1.LoginLogSet, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	q, err := convertQuery(req.GetQuery(), nil)
	if err != nil {
		return nil, invalidQueryError(biz.ErrAuditLogInvalidArgument, err)
	}
	out, err := s.uc.ListLoginLogs(ctx, auth, q)
	if err != nil {
		return nil, err
	}
	items := make([]*auditv1.LoginLog, 0, len(out.Items))
	for _, row := range out.Items {
		items = append(items, toProtoLoginLog(row))
	}
	return &auditv1.LoginLogSet{
		Items:      items,
		Total:      out.Total,
		HasMore:    out.HasMore,
		NextCursor: out.NextCursor,
		Page:       pageOf(req.GetQuery()),
	}, nil
}

// GetLoginLog 按 id 查询登录日志。
func (s *AuditService) GetLoginLog(ctx context.Context, req *auditv1.GetLoginLogRequest) (*auditv1.LoginLog, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.uc.GetLoginLog(ctx, auth, req.GetId())
	if err != nil {
		return nil, err
	}
	return toProtoLoginLog(out), nil
}

// ListOperationLogs 按动态查询协议返回一页操作日志；协议非法映射为
// INVALID_ARGUMENT。
func (s *AuditService) ListOperationLogs(ctx context.Context, req *auditv1.ListOperationLogsRequest) (*auditv1.OperationLogSet, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	q, err := convertQuery(req.GetQuery(), nil)
	if err != nil {
		return nil, invalidQueryError(biz.ErrAuditLogInvalidArgument, err)
	}
	out, err := s.uc.ListOperationLogs(ctx, auth, q)
	if err != nil {
		return nil, err
	}
	items := make([]*auditv1.OperationLog, 0, len(out.Items))
	for _, row := range out.Items {
		items = append(items, toProtoOperationLog(row))
	}
	return &auditv1.OperationLogSet{
		Items:      items,
		Total:      out.Total,
		HasMore:    out.HasMore,
		NextCursor: out.NextCursor,
		Page:       pageOf(req.GetQuery()),
	}, nil
}

// GetOperationLog 按 id 查询操作日志。
func (s *AuditService) GetOperationLog(ctx context.Context, req *auditv1.GetOperationLogRequest) (*auditv1.OperationLog, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.uc.GetOperationLog(ctx, auth, req.GetId())
	if err != nil {
		return nil, err
	}
	return toProtoOperationLog(out), nil
}

// toProtoLoginLog 将领域模型转换为 DTO。
func toProtoLoginLog(row *biz.LoginLog) *auditv1.LoginLog {
	return &auditv1.LoginLog{
		Id:            row.ID,
		UserId:        row.UserID,
		Username:      row.Username,
		IpAddress:     row.IPAddress,
		UserAgent:     row.UserAgent,
		LoginType:     row.LoginType,
		IsSuccess:     row.IsSuccess,
		FailureReason: row.FailureReason,
		LoginAt:       timestamppb.New(row.LoginAt),
		CreatedBy:     row.CreatedBy,
		CreatedAt:     timestamppb.New(row.CreatedAt),
	}
}

// toProtoOperationLog 将领域模型转换为 DTO。操作前后数据快照解码为 JSON
// 对象（structpb.Struct）：快照缺失（NULL）即字段未设置；解码失败（写入
// 侧快照非 JSON 对象的历史脏数据）置字段未设置而非让整行/整页失败——
// 日志是只读审计面，展示降级优于查询不可用。
func toProtoOperationLog(row *biz.OperationLog) *auditv1.OperationLog {
	return &auditv1.OperationLog{
		Id:            row.ID,
		UserId:        row.UserID,
		Username:      row.Username,
		OperationType: row.OperationType,
		TargetTable:   row.TargetTable,
		TargetId:      row.TargetID,
		OldValue:      jsonRawToStruct(row.OldValue),
		NewValue:      jsonRawToStruct(row.NewValue),
		IpAddress:     row.IPAddress,
		UserAgent:     row.UserAgent,
		ApiPath:       row.APIPath,
		HttpMethod:    row.HTTPMethod,
		DurationMs:    row.DurationMs,
		ResultCode:    row.ResultCode,
		ResultMsg:     row.ResultMsg,
		RequestId:     row.RequestID,
		OperatedAt:    timestamppb.New(row.OperatedAt),
		CreatedBy:     row.CreatedBy,
		CreatedAt:     timestamppb.New(row.CreatedAt),
	}
}

// jsonRawToStruct 将 JSON 快照解码为 structpb.Struct；空快照与解码失败均
// 返回 nil（proto optional 未设置）。
func jsonRawToStruct(raw json.RawMessage) *structpb.Struct {
	if len(raw) == 0 {
		return nil
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil
	}
	out, err := structpb.NewStruct(value)
	if err != nil {
		return nil
	}
	return out
}
