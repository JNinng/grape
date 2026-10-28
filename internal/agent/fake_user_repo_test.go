package agent

import (
	"context"
	"sync"

	"grape/internal/biz"

	dynq "github.com/jninng/sqlc_gen_dynamic/dynq"
)

// nicknameUpdate 是一次定向改昵称调用的记录。
type nicknameUpdate struct {
	id       int64
	nickname string
}

// fakeUserRepo 是工具测试用的用户仓储替身：内存行集 + 调用记录。
// 只实现工具面触达的读写语义，其余方法返回不可达错误（工具首批不使用）。
type fakeUserRepo struct {
	mu   sync.Mutex
	rows map[int64]*biz.User

	getByIDCalls  []int64
	updateCalls   []*biz.User
	nicknameCalls []nicknameUpdate
	listCalls     []*dynq.Query
}

func newFakeUserRepo(rows ...*biz.User) *fakeUserRepo {
	r := &fakeUserRepo{rows: map[int64]*biz.User{}}
	for _, u := range rows {
		r.rows[u.ID] = u
	}
	return r
}

func (r *fakeUserRepo) GetByID(_ context.Context, _ biz.Identity, id int64) (*biz.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.getByIDCalls = append(r.getByIDCalls, id)
	u, ok := r.rows[id]
	if !ok {
		return nil, biz.ErrUserNotFound
	}
	return cloneUser(u), nil
}

func (r *fakeUserRepo) List(_ context.Context, _ biz.Identity, q *dynq.Query) (*biz.UserList, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.listCalls = append(r.listCalls, q)
	items := make([]*biz.User, 0, len(r.rows))
	for _, u := range r.rows {
		items = append(items, cloneUser(u))
	}
	return &biz.UserList{Items: items}, nil
}

func (r *fakeUserRepo) Update(_ context.Context, _ biz.Identity, u *biz.User) (*biz.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updateCalls = append(r.updateCalls, cloneUser(u))
	if _, ok := r.rows[u.ID]; !ok {
		return nil, biz.ErrUserNotFound
	}
	r.rows[u.ID] = cloneUser(u)
	return cloneUser(u), nil
}

func (r *fakeUserRepo) UpdateNickname(_ context.Context, _ biz.Identity, id int64, nickname string) (*biz.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nicknameCalls = append(r.nicknameCalls, nicknameUpdate{id: id, nickname: nickname})
	u, ok := r.rows[id]
	if !ok {
		return nil, biz.ErrUserNotFound
	}
	updated := cloneUser(u)
	updated.Nickname = nickname
	r.rows[id] = updated
	return cloneUser(updated), nil
}

func (r *fakeUserRepo) Create(context.Context, biz.Identity, *biz.User) (*biz.User, error) {
	return nil, biz.ErrUserInvalidArgument
}

func (r *fakeUserRepo) SetPassword(context.Context, biz.Identity, int64, string) error {
	return biz.ErrUserInvalidArgument
}

func (r *fakeUserRepo) SoftDelete(context.Context, biz.Identity, int64) error {
	return biz.ErrUserInvalidArgument
}

func (r *fakeUserRepo) AssignRoles(context.Context, biz.Identity, int64, []int64) (int64, error) {
	return 0, biz.ErrUserInvalidArgument
}

func (r *fakeUserRepo) TransferOwner(context.Context, biz.Identity, int64, biz.OwnerTriple) (*biz.User, error) {
	return nil, biz.ErrUserInvalidArgument
}

func (r *fakeUserRepo) GetCredentialForLogin(context.Context, string, string) (*biz.UserCredential, error) {
	return nil, biz.ErrUserNotFound
}

// snapshot 返回 Update 收到的调用副本。
func (r *fakeUserRepo) updates() []*biz.User {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*biz.User, len(r.updateCalls))
	for i, u := range r.updateCalls {
		out[i] = cloneUser(u)
	}
	return out
}

// nicknames 返回定向改昵称调用的副本。
func (r *fakeUserRepo) nicknames() []nicknameUpdate {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]nicknameUpdate(nil), r.nicknameCalls...)
}

// queries 返回 List 收到的查询协议副本。
func (r *fakeUserRepo) queries() []*dynq.Query {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*dynq.Query(nil), r.listCalls...)
}

// cloneUser 深拷贝可变面（指针字段），避免替身内部状态被外部改写。
func cloneUser(u *biz.User) *biz.User {
	out := *u
	return &out
}
