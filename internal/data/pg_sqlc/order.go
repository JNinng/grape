package pg_sqlc

import (
	"context"

	"grape/internal/biz"
	"grape/internal/data/pg_sqlc/gen"

	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

// toBizOrder 将持久化的订单转换为领域模型。
func toBizOrder(po *gen.Order) *biz.Order {
	if po == nil {
		return nil
	}
	return &biz.Order{
		ID:          po.ID,
		OrderNo:     po.OrderNo,
		UserID:      po.UserID,
		AmountCents: po.AmountCents,
		Status:      biz.OrderStatus(po.Status),
		CreatedAt:   tsToTime(po.CreatedAt),
	}
}

// orderRepo 是 biz.OrderRepo 的 PostgreSQL 实现，负责动态查询解析
// 与驱动错误到领域错误的映射。
type orderRepo struct {
	data *Data
}

// NewOrderRepo 构造订单仓储。
func NewOrderRepo(data *Data) biz.OrderRepo {
	return &orderRepo{data: data}
}

// GetByID 按主键读取订单；未命中映射为 ErrOrderNotFound。
func (r *orderRepo) GetByID(ctx context.Context, id int64) (*biz.Order, error) {
	po, err := gen.New(r.data.db).GetOrder(ctx, id)
	if err != nil {
		return nil, mapStorageError(err, biz.ErrOrderNotFound, biz.ErrOrderInvalidArgument, biz.ErrOrderInvalidArgument)
	}
	return toBizOrder(&po), nil
}

// List 解析动态查询协议并返回分页信封；字段、操作符与游标合法性
// 由生成代码校验，协议错误映射为 ErrOrderInvalidArgument。
func (r *orderRepo) List(ctx context.Context, q *dynq.Query) (*biz.OrderList, error) {
	params, err := gen.ParseOrdersQuery(q)
	if err != nil {
		return nil, mapStorageError(err, biz.ErrOrderNotFound, biz.ErrOrderInvalidArgument, biz.ErrOrderInvalidArgument)
	}
	res, err := gen.ListOrders(ctx, r.data.db, params)
	if err != nil {
		return nil, mapStorageError(err, biz.ErrOrderNotFound, biz.ErrOrderInvalidArgument, biz.ErrOrderInvalidArgument)
	}
	out := &biz.OrderList{
		Items:      make([]*biz.Order, 0, len(res.Items)),
		HasMore:    res.HasMore,
		NextCursor: res.NextCursor,
	}
	for i := range res.Items {
		out.Items = append(out.Items, toBizOrder(&res.Items[i]))
	}
	// total 与翻页位置无关，计数前剥离游标：游标会把行集收窄到某一页
	// 位置之后，导致各页 total 递减。
	if dynq.IncludeTotalOf(q.Pagination) {
		countParams := params
		countParams.After = nil
		total, err := gen.CountOrders(ctx, r.data.db, countParams)
		if err != nil {
			return nil, mapStorageError(err, biz.ErrOrderNotFound, biz.ErrOrderInvalidArgument, biz.ErrOrderInvalidArgument)
		}
		out.Total = &total
	}
	return out, nil
}

// Create 插入订单；id 与 created_at 由数据库生成，返回值以持久化行为准。
func (r *orderRepo) Create(ctx context.Context, o *biz.Order) (*biz.Order, error) {
	po, err := gen.New(r.data.db).CreateOrder(ctx, gen.CreateOrderParams{
		OrderNo:     o.OrderNo,
		UserID:      o.UserID,
		AmountCents: o.AmountCents,
		Status:      gen.OrderStatus(o.Status),
	})
	if err != nil {
		return nil, mapStorageError(err, biz.ErrOrderNotFound, biz.ErrOrderInvalidArgument, biz.ErrOrderInvalidArgument)
	}
	return toBizOrder(&po), nil
}

// Update 全量覆盖订单可变字段；未命中由 RETURNING 无行触发 not found。
func (r *orderRepo) Update(ctx context.Context, o *biz.Order) (*biz.Order, error) {
	po, err := gen.New(r.data.db).UpdateOrder(ctx, gen.UpdateOrderParams{
		ID:          o.ID,
		OrderNo:     o.OrderNo,
		UserID:      o.UserID,
		AmountCents: o.AmountCents,
		Status:      gen.OrderStatus(o.Status),
	})
	if err != nil {
		return nil, mapStorageError(err, biz.ErrOrderNotFound, biz.ErrOrderInvalidArgument, biz.ErrOrderInvalidArgument)
	}
	return toBizOrder(&po), nil
}

// Delete 物理删除订单；未命中时返回 ErrOrderNotFound。
func (r *orderRepo) Delete(ctx context.Context, id int64) error {
	rows, err := gen.New(r.data.db).DeleteOrder(ctx, id)
	if err != nil {
		return mapStorageError(err, biz.ErrOrderNotFound, biz.ErrOrderInvalidArgument, biz.ErrOrderInvalidArgument)
	}
	if rows == 0 {
		return biz.ErrOrderNotFound
	}
	return nil
}
