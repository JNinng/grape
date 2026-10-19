package service

import (
	"context"
	"time"

	v1 "grape/api/order/v1"
	"grape/internal/biz"

	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// orderEnumAliases 是订单查询中枚举字段的线上枚举名到存储值映射。
var orderEnumAliases = buildOrderStatusAliases()

// buildOrderStatusAliases 从 proto 枚举派生映射，避免词表手写漂移。
func buildOrderStatusAliases() enumAliases {
	mapping := make(map[string]string, len(v1.OrderStatus_name))
	for num, name := range v1.OrderStatus_name {
		status, ok := convertOrderStatusDO(v1.OrderStatus(num))
		if !ok || status == "" {
			continue
		}
		mapping[name] = string(status)
	}
	return enumAliases{"status": mapping}
}

// OrderService 是订单服务。
type OrderService struct {
	v1.UnimplementedOrderServiceServer

	uc *biz.OrderUsecase
}

// NewOrderService 构造订单服务。
func NewOrderService(uc *biz.OrderUsecase) *OrderService {
	return &OrderService{uc: uc}
}

// CreateOrder 创建订单。
func (s *OrderService) CreateOrder(ctx context.Context, req *v1.CreateOrderRequest) (*v1.Order, error) {
	o, err := convertOrder(req.GetOrder())
	if err != nil {
		return nil, err
	}
	o, err = s.uc.CreateOrder(ctx, o)
	if err != nil {
		return nil, err
	}
	return convertOrderReply(o), nil
}

// GetOrder 按 ID 查询订单。
func (s *OrderService) GetOrder(ctx context.Context, req *v1.GetOrderRequest) (*v1.Order, error) {
	o, err := s.uc.GetOrder(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	return convertOrderReply(o), nil
}

// ListOrders 经动态查询协议列出订单。
func (s *OrderService) ListOrders(ctx context.Context, req *v1.ListOrdersRequest) (*v1.OrderSet, error) {
	q, err := convertQuery(req.GetQuery(), orderEnumAliases)
	if err != nil {
		return nil, invalidQueryError(biz.ErrOrderInvalidArgument, err)
	}
	list, err := s.uc.ListOrders(ctx, q)
	if err != nil {
		return nil, err
	}
	set := &v1.OrderSet{
		Items:      make([]*v1.Order, 0, len(list.Items)),
		HasMore:    list.HasMore,
		NextCursor: list.NextCursor,
		Total:      list.Total,
		Page:       pageOf(req.GetQuery()),
	}
	for _, o := range list.Items {
		set.Items = append(set.Items, convertOrderReply(o))
	}
	return set, nil
}

// UpdateOrder 覆盖订单的可变字段。
func (s *OrderService) UpdateOrder(ctx context.Context, req *v1.UpdateOrderRequest) (*v1.Order, error) {
	o, err := convertOrder(req.GetOrder())
	if err != nil {
		return nil, err
	}
	o, err = s.uc.UpdateOrder(ctx, o)
	if err != nil {
		return nil, err
	}
	return convertOrderReply(o), nil
}

// DeleteOrder 物理删除订单。
func (s *OrderService) DeleteOrder(ctx context.Context, req *v1.DeleteOrderRequest) (*emptypb.Empty, error) {
	if err := s.uc.DeleteOrder(ctx, req.GetId()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// convertOrder 将入站 proto 解析为 DO。created_at 不转换：
// 由服务端生成且只读。未定义的枚举值视为请求非法。
func convertOrder(in *v1.Order) (*biz.Order, error) {
	if in == nil {
		return nil, nil
	}
	status, ok := convertOrderStatusDO(in.GetStatus())
	if !ok {
		return nil, biz.ErrOrderInvalidArgument
	}
	return &biz.Order{
		ID:          in.GetId(),
		OrderNo:     in.GetOrderNo(),
		UserID:      in.GetUserId(),
		AmountCents: in.GetAmountCents(),
		Status:      status,
	}, nil
}

// convertOrderReply 将 DO 渲染为 API 订单。
func convertOrderReply(in *biz.Order) *v1.Order {
	if in == nil {
		return nil
	}
	return &v1.Order{
		Id:          in.ID,
		OrderNo:     in.OrderNo,
		UserId:      in.UserID,
		AmountCents: in.AmountCents,
		Status:      convertOrderStatus(in.Status),
		CreatedAt:   timestamppb.New(in.CreatedAt),
	}
}

// convertOrderStatus 将领域状态映射到 api 枚举。
func convertOrderStatus(in biz.OrderStatus) v1.OrderStatus {
	switch in {
	case biz.OrderStatusPending:
		return v1.OrderStatus_ORDER_STATUS_PENDING
	case biz.OrderStatusPaid:
		return v1.OrderStatus_ORDER_STATUS_PAID
	case biz.OrderStatusShipped:
		return v1.OrderStatus_ORDER_STATUS_SHIPPED
	case biz.OrderStatusClosed:
		return v1.OrderStatus_ORDER_STATUS_CLOSED
	default:
		return v1.OrderStatus_ORDER_STATUS_UNSPECIFIED
	}
}

// convertOrderStatusDO 将 api 枚举映射到领域状态。零值枚举映射为空状态，
// 便于用例层施加默认值；未定义枚举返回 false。
func convertOrderStatusDO(in v1.OrderStatus) (biz.OrderStatus, bool) {
	switch in {
	case v1.OrderStatus_ORDER_STATUS_UNSPECIFIED:
		return "", true
	case v1.OrderStatus_ORDER_STATUS_PENDING:
		return biz.OrderStatusPending, true
	case v1.OrderStatus_ORDER_STATUS_PAID:
		return biz.OrderStatusPaid, true
	case v1.OrderStatus_ORDER_STATUS_SHIPPED:
		return biz.OrderStatusShipped, true
	case v1.OrderStatus_ORDER_STATUS_CLOSED:
		return biz.OrderStatusClosed, true
	default:
		return "", false
	}
}

// timestampOf 渲染可空时间戳；nil 保持 nil。
func timestampOf(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}
