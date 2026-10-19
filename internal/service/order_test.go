package service

import (
	"context"
	"errors"
	"testing"

	v1 "grape/api/order/v1"
	queryv1 "grape/api/query/v1"
	"grape/internal/biz"

	dynq "github.com/jninng/sqlc_gen_dynamic/dynq"
	"google.golang.org/protobuf/types/known/structpb"
)

// fakeOrderRepo 以内存版假仓储替换 biz 仓储接口。
type fakeOrderRepo struct {
	biz.OrderRepo

	query   *dynq.Query
	created *biz.Order
}

func (f *fakeOrderRepo) Create(ctx context.Context, o *biz.Order) (*biz.Order, error) {
	o.ID = 10
	o.Status = biz.OrderStatusPending
	f.created = o
	return o, nil
}

func (f *fakeOrderRepo) List(ctx context.Context, q *dynq.Query) (*biz.OrderList, error) {
	f.query = q
	return &biz.OrderList{Items: []*biz.Order{{ID: 10, OrderNo: "A-1", Status: biz.OrderStatusPaid}}}, nil
}

// TestCreateOrderDefaultsStatus 验证创建路径 status 缺省为 pending。
func TestCreateOrderDefaultsStatus(t *testing.T) {
	repo := &fakeOrderRepo{}
	svc := NewOrderService(biz.NewOrderUsecase(repo))

	reply, err := svc.CreateOrder(context.Background(), &v1.CreateOrderRequest{
		Order: &v1.Order{OrderNo: "A-1", UserId: 1, AmountCents: 1000},
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	if reply.GetId() != 10 || reply.GetStatus() != v1.OrderStatus_ORDER_STATUS_PENDING {
		t.Fatalf("unexpected reply: %+v", reply)
	}
}

// TestListOrdersConvertsQueryProtocol 验证条件值经 protobuf JSON 值
// 转换、枚举名归一化后到达 dynq.Query，且游标模式页码为 0。
func TestListOrdersConvertsQueryProtocol(t *testing.T) {
	repo := &fakeOrderRepo{}
	svc := NewOrderService(biz.NewOrderUsecase(repo))

	status, _ := structpb.NewValue("ORDER_STATUS_PAID")
	after := "cursor-9"
	set, err := svc.ListOrders(context.Background(), &v1.ListOrdersRequest{
		Query: &queryv1.Query{
			Where: []*queryv1.Condition{
				{Field: "status", Op: "eq", Value: status},
			},
			Pagination: &queryv1.Pagination{After: &after},
		},
	})
	if err != nil {
		t.Fatalf("ListOrders: %v", err)
	}
	q := repo.query
	if len(q.Where) != 1 || q.Where[0].Field != "status" || q.Where[0].Value != "paid" {
		t.Fatalf("unexpected where: %+v", q.Where)
	}
	if set.GetPage() != 0 {
		t.Fatalf("cursor mode should report page 0, got %d", set.GetPage())
	}
	if len(set.GetItems()) != 1 || set.GetItems()[0].GetOrderNo() != "A-1" {
		t.Fatalf("unexpected items: %+v", set.GetItems())
	}
}

// TestCreateOrderRejectsUnknownStatus 验证未定义的枚举值不会被静默
// 改写成 pending。
func TestCreateOrderRejectsUnknownStatus(t *testing.T) {
	svc := NewOrderService(biz.NewOrderUsecase(&fakeOrderRepo{}))

	_, err := svc.CreateOrder(context.Background(), &v1.CreateOrderRequest{
		Order: &v1.Order{
			OrderNo: "A-1", UserId: 1, AmountCents: 100,
			Status: v1.OrderStatus(99),
		},
	})
	if !errors.Is(err, biz.ErrOrderInvalidArgument) {
		t.Fatalf("want ErrOrderInvalidArgument, got %v", err)
	}
}

// TestUpdateOrderRequiresStatus 验证更新覆盖语义下缺失 status 在
// 用例层被拒绝，而不是落到数据库枚举转换错误。
func TestUpdateOrderRequiresStatus(t *testing.T) {
	svc := NewOrderService(biz.NewOrderUsecase(&fakeOrderRepo{}))

	_, err := svc.UpdateOrder(context.Background(), &v1.UpdateOrderRequest{
		Order: &v1.Order{Id: 1, OrderNo: "A-1", UserId: 1, AmountCents: 100},
	})
	if !errors.Is(err, biz.ErrOrderInvalidArgument) {
		t.Fatalf("want ErrOrderInvalidArgument, got %v", err)
	}
}

// TestListOrdersRejectsPageAndAfter 验证分页互斥参数被拒绝。
func TestListOrdersRejectsPageAndAfter(t *testing.T) {
	svc := NewOrderService(biz.NewOrderUsecase(&fakeOrderRepo{}))

	page, after := int32(1), "cursor-1"
	_, err := svc.ListOrders(context.Background(), &v1.ListOrdersRequest{
		Query: &queryv1.Query{Pagination: &queryv1.Pagination{Page: &page, After: &after}},
	})
	if !errors.Is(err, biz.ErrOrderInvalidArgument) {
		t.Fatalf("want ErrOrderInvalidArgument, got %v", err)
	}
}

// TestListOrdersNormalizesEnumList 验证 in 条件中的枚举名逐个归一化。
func TestListOrdersNormalizesEnumList(t *testing.T) {
	repo := &fakeOrderRepo{}
	svc := NewOrderService(biz.NewOrderUsecase(repo))

	status, _ := structpb.NewValue([]any{"ORDER_STATUS_PAID", "ORDER_STATUS_SHIPPED"})
	_, err := svc.ListOrders(context.Background(), &v1.ListOrdersRequest{
		Query: &queryv1.Query{Where: []*queryv1.Condition{
			{Field: "status", Op: "in", Value: status},
		}},
	})
	if err != nil {
		t.Fatalf("ListOrders: %v", err)
	}
	got, ok := repo.query.Where[0].Value.([]any)
	if !ok || len(got) != 2 || got[0] != "paid" || got[1] != "shipped" {
		t.Fatalf("unexpected normalized value: %#v", repo.query.Where[0].Value)
	}
}

// TestListOrdersRejectsNumericEnum 验证枚举过滤值只接受符号名。
func TestListOrdersRejectsNumericEnum(t *testing.T) {
	svc := NewOrderService(biz.NewOrderUsecase(&fakeOrderRepo{}))

	status, _ := structpb.NewValue(float64(2))
	_, err := svc.ListOrders(context.Background(), &v1.ListOrdersRequest{
		Query: &queryv1.Query{Where: []*queryv1.Condition{
			{Field: "status", Op: "eq", Value: status},
		}},
	})
	if !errors.Is(err, biz.ErrOrderInvalidArgument) {
		t.Fatalf("want ErrOrderInvalidArgument, got %v", err)
	}
}

// TestListOrdersRejectsUnknownEnum 验证未定义的枚举名被拒绝。
func TestListOrdersRejectsUnknownEnum(t *testing.T) {
	svc := NewOrderService(biz.NewOrderUsecase(&fakeOrderRepo{}))

	status, _ := structpb.NewValue("ORDER_STATUS_UNKNOWN")
	_, err := svc.ListOrders(context.Background(), &v1.ListOrdersRequest{
		Query: &queryv1.Query{Where: []*queryv1.Condition{
			{Field: "status", Op: "eq", Value: status},
		}},
	})
	if !errors.Is(err, biz.ErrOrderInvalidArgument) {
		t.Fatalf("want ErrOrderInvalidArgument, got %v", err)
	}
}
