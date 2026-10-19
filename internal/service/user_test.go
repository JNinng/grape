package service

import (
	"context"
	"errors"
	"testing"
	"time"

	queryv1 "grape/api/query/v1"
	v1 "grape/api/user/v1"
	"grape/internal/biz"

	dynq "github.com/jninng/sqlc_gen_dynamic/dynq"
	"google.golang.org/protobuf/types/known/structpb"
)

// fakeUserRepo 以内存版假仓储替换 biz 仓储接口，隔离测试 service 层
// 的 DTO 转换与请求语义。
type fakeUserRepo struct {
	biz.UserRepo

	user    *biz.User
	query   *dynq.Query
	created bool
}

func (f *fakeUserRepo) GetByID(ctx context.Context, id int64) (*biz.User, error) {
	if f.user == nil || f.user.ID != id {
		return nil, biz.ErrUserNotFound
	}
	return f.user, nil
}

func (f *fakeUserRepo) Create(ctx context.Context, u *biz.User) (*biz.User, error) {
	f.created = true
	u.ID = 1
	u.Status = biz.UserStatusActive
	u.CreatedAt = time.Now()
	return u, nil
}

func (f *fakeUserRepo) List(ctx context.Context, q *dynq.Query) (*biz.UserList, error) {
	f.query = q
	return &biz.UserList{
		Items:      []*biz.User{{ID: 1, Name: "Alice", Email: "alice@x.com", Status: biz.UserStatusActive}},
		HasMore:    true,
		NextCursor: "cursor-1",
	}, nil
}

// TestCreateUserCarriesPasswordAndDefaultsStatus 验证创建路径携带密码
// 且 status 缺省为 active。
func TestCreateUserCarriesPasswordAndDefaultsStatus(t *testing.T) {
	repo := &fakeUserRepo{}
	svc := NewUserService(biz.NewUserUsecase(repo))

	reply, err := svc.CreateUser(context.Background(), &v1.CreateUserRequest{
		User:     &v1.User{Name: "Alice", Email: "alice@x.com"},
		Password: "secret",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if !repo.created {
		t.Fatal("expected repo create to be called")
	}
	if reply.GetId() != 1 || reply.GetName() != "Alice" {
		t.Fatalf("unexpected reply: %+v", reply)
	}
	if reply.GetStatus() != v1.UserStatus_USER_STATUS_ACTIVE {
		t.Fatalf("expected default status active, got %v", reply.GetStatus())
	}
}

// TestListUsersConvertsQueryProtocol 验证 proto 查询协议到 dynq.Query
// 的字段级转换与分页信封。
func TestListUsersConvertsQueryProtocol(t *testing.T) {
	repo := &fakeUserRepo{}
	svc := NewUserService(biz.NewUserUsecase(repo))

	since, _ := structpb.NewValue("2026-01-01T00:00:00Z")
	page, pageSize := int32(2), int32(5)
	set, err := svc.ListUsers(context.Background(), &v1.ListUsersRequest{
		Query: &queryv1.Query{
			Fields: []string{"name"},
			Where: []*queryv1.Condition{
				{Field: "createdAt", Op: "gte", Value: since},
			},
			Sort:       []*queryv1.SortItem{{Field: "score", Order: "asc"}},
			Pagination: &queryv1.Pagination{Page: &page, PageSize: &pageSize},
		},
	})
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	q := repo.query
	if q == nil {
		t.Fatal("expected repo list to receive a query")
	}
	if len(q.Fields) != 1 || q.Fields[0] != "name" {
		t.Fatalf("unexpected fields: %v", q.Fields)
	}
	if len(q.Where) != 1 || q.Where[0].Field != "createdAt" || q.Where[0].Op != "gte" {
		t.Fatalf("unexpected where: %+v", q.Where)
	}
	if len(q.Sort) != 1 || q.Sort[0].Field != "score" || q.Sort[0].Order != "asc" {
		t.Fatalf("unexpected sort: %+v", q.Sort)
	}
	if q.Pagination == nil || q.Pagination.Page == nil || *q.Pagination.Page != 2 {
		t.Fatalf("unexpected pagination: %+v", q.Pagination)
	}
	if set.GetPage() != 2 || !set.GetHasMore() || set.GetNextCursor() != "cursor-1" {
		t.Fatalf("unexpected envelope: %+v", set)
	}
	if len(set.GetItems()) != 1 || set.GetItems()[0].GetEmail() != "alice@x.com" {
		t.Fatalf("unexpected items: %+v", set.GetItems())
	}
}

// TestGetUserMapsNotFound 验证仓储未命中时透传 ErrUserNotFound。
func TestGetUserMapsNotFound(t *testing.T) {
	svc := NewUserService(biz.NewUserUsecase(&fakeUserRepo{}))

	_, err := svc.GetUser(context.Background(), &v1.GetUserRequest{Id: 42})
	if err != biz.ErrUserNotFound {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
}

// TestCreateUserRejectsMissingUser 验证缺失 user 载荷返回参数错误，
// 而不是空指针崩溃。
func TestCreateUserRejectsMissingUser(t *testing.T) {
	svc := NewUserService(biz.NewUserUsecase(&fakeUserRepo{}))

	_, err := svc.CreateUser(context.Background(), &v1.CreateUserRequest{Password: "secret"})
	if !errors.Is(err, biz.ErrUserInvalidArgument) {
		t.Fatalf("want ErrUserInvalidArgument, got %v", err)
	}
}

// TestListUsersRejectsPageAndAfter 验证分页互斥参数被拒绝。
func TestListUsersRejectsPageAndAfter(t *testing.T) {
	svc := NewUserService(biz.NewUserUsecase(&fakeUserRepo{}))

	page, after := int32(1), "cursor-1"
	_, err := svc.ListUsers(context.Background(), &v1.ListUsersRequest{
		Query: &queryv1.Query{Pagination: &queryv1.Pagination{Page: &page, After: &after}},
	})
	if !errors.Is(err, biz.ErrUserInvalidArgument) {
		t.Fatalf("want ErrUserInvalidArgument, got %v", err)
	}
}

// TestListUsersNormalizesEnumStatus 验证用户状态过滤使用线上枚举名，
// 归一化为存储值后交给 dynq。
func TestListUsersNormalizesEnumStatus(t *testing.T) {
	repo := &fakeUserRepo{}
	svc := NewUserService(biz.NewUserUsecase(repo))

	status, _ := structpb.NewValue("USER_STATUS_BANNED")
	_, err := svc.ListUsers(context.Background(), &v1.ListUsersRequest{
		Query: &queryv1.Query{Where: []*queryv1.Condition{
			{Field: "status", Op: "eq", Value: status},
		}},
	})
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if repo.query.Where[0].Value != "banned" {
		t.Fatalf("unexpected normalized value: %#v", repo.query.Where[0].Value)
	}
}
