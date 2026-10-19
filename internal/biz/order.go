package biz

import (
	"context"
	"strings"
	"time"

	v1 "grape/api/order/v1"

	"github.com/go-kratos/kratos/v3/errors"
	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

var (
	// ErrOrderNotFound 表示目标订单不存在。
	ErrOrderNotFound = errors.NotFound(v1.ErrorReason_ORDER_NOT_FOUND.String(), "order not found")
	// ErrOrderInvalidArgument 表示订单请求参数非法。
	ErrOrderInvalidArgument = errors.BadRequest(v1.ErrorReason_ORDER_INVALID_ARGUMENT.String(), "invalid order argument")
)

// OrderStatus 是订单的履约状态，取值与存储层枚举一致。
type OrderStatus string

const (
	// OrderStatusPending 表示待支付。
	OrderStatusPending OrderStatus = "pending"
	// OrderStatusPaid 表示已支付。
	OrderStatusPaid OrderStatus = "paid"
	// OrderStatusShipped 表示已发货。
	OrderStatusShipped OrderStatus = "shipped"
	// OrderStatusClosed 表示已关闭。
	OrderStatusClosed OrderStatus = "closed"
)

// Order 是订单领域模型。
type Order struct {
	ID          int64
	OrderNo     string
	UserID      int64
	AmountCents int64
	Status      OrderStatus
	CreatedAt   time.Time
}

// OrderList 是订单仓储 List 返回的分页信封。
type OrderList struct {
	Items      []*Order
	Total      *int64 // include_total=false 时为 nil
	HasMore    bool
	NextCursor string
}

// OrderRepo 是订单仓储接口。List 消费 dynq 动态查询协议。
type OrderRepo interface {
	// GetByID 按主键查询单个订单。
	GetByID(context.Context, int64) (*Order, error)
	// List 按动态查询协议返回分页集合。
	List(context.Context, *dynq.Query) (*OrderList, error)
	// Create 创建订单。
	Create(context.Context, *Order) (*Order, error)
	// Update 覆盖可变字段（id、created_at 除外）。
	Update(context.Context, *Order) (*Order, error)
	// Delete 物理删除订单。
	Delete(context.Context, int64) error
}

// OrderUsecase 是订单用例。
type OrderUsecase struct {
	repo OrderRepo
}

// NewOrderUsecase 构造订单用例。
func NewOrderUsecase(repo OrderRepo) *OrderUsecase {
	return &OrderUsecase{repo: repo}
}

// GetOrder 按 ID 查询订单。
func (uc *OrderUsecase) GetOrder(ctx context.Context, id int64) (*Order, error) {
	if id <= 0 {
		return nil, ErrOrderInvalidArgument
	}
	return uc.repo.GetByID(ctx, id)
}

// ListOrders 经动态查询协议列出订单。
func (uc *OrderUsecase) ListOrders(ctx context.Context, q *dynq.Query) (*OrderList, error) {
	return uc.repo.List(ctx, q)
}

// CreateOrder 创建订单；status 未指定时默认 pending。
func (uc *OrderUsecase) CreateOrder(ctx context.Context, o *Order) (*Order, error) {
	if o == nil {
		return nil, ErrOrderInvalidArgument
	}
	if o.Status == "" {
		o.Status = OrderStatusPending
	}
	if err := validateOrder(o); err != nil {
		return nil, err
	}
	return uc.repo.Create(ctx, o)
}

// UpdateOrder 覆盖订单可变字段。id 与 created_at 由服务端管理，
// 输入中被忽略；status 是可变字段，更新时必填。
func (uc *OrderUsecase) UpdateOrder(ctx context.Context, o *Order) (*Order, error) {
	if o == nil || o.ID <= 0 || o.Status == "" {
		return nil, ErrOrderInvalidArgument
	}
	if err := validateOrder(o); err != nil {
		return nil, err
	}
	return uc.repo.Update(ctx, o)
}

// DeleteOrder 物理删除订单。
func (uc *OrderUsecase) DeleteOrder(ctx context.Context, id int64) error {
	if id <= 0 {
		return ErrOrderInvalidArgument
	}
	return uc.repo.Delete(ctx, id)
}

// Valid 报告状态是否为存储层认可的枚举值。
func (s OrderStatus) Valid() bool {
	switch s {
	case OrderStatusPending, OrderStatusPaid, OrderStatusShipped, OrderStatusClosed:
		return true
	default:
		return false
	}
}

// validateOrder 校验入参：order_no 必填、user_id 正数、金额非负、
// status 为合法枚举值。
func validateOrder(o *Order) error {
	if o == nil ||
		strings.TrimSpace(o.OrderNo) == "" ||
		o.UserID <= 0 ||
		o.AmountCents < 0 ||
		!o.Status.Valid() {
		return ErrOrderInvalidArgument
	}
	return nil
}
