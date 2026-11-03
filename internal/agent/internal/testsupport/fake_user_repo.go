// Package testsupport 是 agent 树各包测试共享的替身与夹具：内存用户
// 仓储、审计挂点替身、embedder 替身、运行观测替身、骨架配置、PG 套件
// 专属库部署助手。本包只依赖 biz / conf / identity / pg_sqlc 与框架类型，
// 绝不 import 根包与 tools / audit / httpapi 等被测生产包（identity 是
// 无环叶子，仅复用其 userKey 编码）——否则引入「包内测试 → testsupport →
// 被测包」的导入环。仅供 _test.go 文件导入。
package testsupport

import (
	"context"
	"sync"

	"grape/internal/biz"

	dynq "github.com/jninng/sqlc_gen_dynamic/dynq"
)

// NicknameUpdate 是一次定向改昵称调用的记录。
type NicknameUpdate struct {
	ID       int64
	Nickname string
}

// FakeUserRepo 是工具与骨架测试用的用户仓储替身：内存行集 + 调用记录。
// 只实现工具面触达的读写语义，其余方法返回不可达错误（工具首批不使用）。
type FakeUserRepo struct {
	mu   sync.Mutex
	rows map[int64]*biz.User

	getByIDCalls  []int64
	updateCalls   []*biz.User
	nicknameCalls []NicknameUpdate
	listCalls     []*dynq.Query
}

// NewFakeUserRepo 以给定用户行构造替身。
func NewFakeUserRepo(rows ...*biz.User) *FakeUserRepo {
	r := &FakeUserRepo{rows: map[int64]*biz.User{}}
	for _, u := range rows {
		r.rows[u.ID] = u
	}
	return r
}

func (r *FakeUserRepo) GetByID(_ context.Context, _ biz.Identity, id int64) (*biz.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.getByIDCalls = append(r.getByIDCalls, id)
	u, ok := r.rows[id]
	if !ok {
		return nil, biz.ErrUserNotFound
	}
	return cloneUser(u), nil
}

func (r *FakeUserRepo) List(_ context.Context, _ biz.Identity, q *dynq.Query) (*biz.UserList, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.listCalls = append(r.listCalls, q)
	items := make([]*biz.User, 0, len(r.rows))
	for _, u := range r.rows {
		items = append(items, cloneUser(u))
	}
	return &biz.UserList{Items: items}, nil
}

func (r *FakeUserRepo) Update(_ context.Context, _ biz.Identity, u *biz.User) (*biz.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updateCalls = append(r.updateCalls, cloneUser(u))
	if _, ok := r.rows[u.ID]; !ok {
		return nil, biz.ErrUserNotFound
	}
	r.rows[u.ID] = cloneUser(u)
	return cloneUser(u), nil
}

func (r *FakeUserRepo) UpdateNickname(_ context.Context, _ biz.Identity, id int64, nickname string) (*biz.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nicknameCalls = append(r.nicknameCalls, NicknameUpdate{ID: id, Nickname: nickname})
	u, ok := r.rows[id]
	if !ok {
		return nil, biz.ErrUserNotFound
	}
	updated := cloneUser(u)
	updated.Nickname = nickname
	r.rows[id] = updated
	return cloneUser(updated), nil
}

func (r *FakeUserRepo) Create(context.Context, biz.Identity, *biz.User) (*biz.User, error) {
	return nil, biz.ErrUserInvalidArgument
}

func (r *FakeUserRepo) SetPassword(context.Context, biz.Identity, int64, string) error {
	return biz.ErrUserInvalidArgument
}

func (r *FakeUserRepo) SoftDelete(context.Context, biz.Identity, int64) error {
	return biz.ErrUserInvalidArgument
}

func (r *FakeUserRepo) AssignRoles(context.Context, biz.Identity, int64, []int64) (int64, error) {
	return 0, biz.ErrUserInvalidArgument
}

func (r *FakeUserRepo) TransferOwner(context.Context, biz.Identity, int64, biz.OwnerTriple) (*biz.User, error) {
	return nil, biz.ErrUserInvalidArgument
}

func (r *FakeUserRepo) GetCredentialForLogin(context.Context, string, string) (*biz.UserCredential, error) {
	return nil, biz.ErrUserNotFound
}

// Updates 返回 Update 收到的调用副本。
func (r *FakeUserRepo) Updates() []*biz.User {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*biz.User, len(r.updateCalls))
	for i, u := range r.updateCalls {
		out[i] = cloneUser(u)
	}
	return out
}

// Nicknames 返回定向改昵称调用的副本。
func (r *FakeUserRepo) Nicknames() []NicknameUpdate {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]NicknameUpdate(nil), r.nicknameCalls...)
}

// Queries 返回 List 收到的查询协议副本。
func (r *FakeUserRepo) Queries() []*dynq.Query {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*dynq.Query(nil), r.listCalls...)
}

// cloneUser 深拷贝可变面（指针字段），避免替身内部状态被外部改写。
func cloneUser(u *biz.User) *biz.User {
	out := *u
	return &out
}
