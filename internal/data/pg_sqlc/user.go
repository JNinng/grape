package pg_sqlc

import (
	"context"
	"encoding/json"
	"time"

	"grape/internal/biz"
	"grape/internal/data/pg_sqlc/gen"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

// toBizUser 将持久化的用户转换为领域模型。Password 随行携带以供创建路径
// 写入；读取方必须丢弃，不得上送传输层。
func toBizUser(po *gen.User) (*biz.User, error) {
	if po == nil {
		return nil, nil
	}
	meta, err := jsonbToMap(po.Meta)
	if err != nil {
		return nil, err
	}
	return &biz.User{
		ID:        po.ID,
		Name:      po.Name,
		Email:     po.Email,
		Password:  po.Password,
		Status:    biz.UserStatus(po.Status),
		Role:      po.Role,
		Age:       po.Age,
		Score:     po.Score,
		Balance:   numericToString(po.Balance),
		BirthDate: dateToString(po.BirthDate),
		OrgID:     uuidToString(po.OrgID),
		Tags:      po.Tags,
		Meta:      meta,
		CreatedAt: tsToTime(po.CreatedAt),
		UpdatedAt: tsToTimePtr(po.UpdatedAt),
		DeletedAt: tsToTimePtr(po.DeletedAt),
	}, nil
}

// userRepo 是 biz.UserRepo 的 PostgreSQL 实现，负责可空列的驱动类型转换
// 与驱动错误到领域错误的映射。
type userRepo struct {
	data *Data
}

// NewUserRepo 构造用户仓储。
func NewUserRepo(data *Data) biz.UserRepo {
	return &userRepo{data: data}
}

// GetByID 按主键读取用户；未命中映射为 ErrUserNotFound。
func (r *userRepo) GetByID(ctx context.Context, id int64) (*biz.User, error) {
	po, err := gen.New(r.data.db).GetUser(ctx, id)
	if err != nil {
		return nil, mapStorageError(err, biz.ErrUserNotFound, biz.ErrUserInvalidArgument, biz.ErrUserAlreadyExists)
	}
	return toBizUser(&po)
}

// GetByEmail 按唯一邮箱读取用户；未命中映射为 ErrUserNotFound。
func (r *userRepo) GetByEmail(ctx context.Context, email string) (*biz.User, error) {
	po, err := gen.New(r.data.db).GetUserByEmail(ctx, email)
	if err != nil {
		return nil, mapStorageError(err, biz.ErrUserNotFound, biz.ErrUserInvalidArgument, biz.ErrUserAlreadyExists)
	}
	return toBizUser(&po)
}

// List 解析动态查询协议并返回分页信封；字段、操作符、投影与游标合法性
// 由生成代码校验，协议错误映射为 ErrUserInvalidArgument。
func (r *userRepo) List(ctx context.Context, q *dynq.Query) (*biz.UserList, error) {
	params, err := gen.ParseUsersQuery(q)
	if err != nil {
		return nil, mapStorageError(err, biz.ErrUserNotFound, biz.ErrUserInvalidArgument, biz.ErrUserAlreadyExists)
	}
	res, err := gen.ListUsers(ctx, r.data.db, params)
	if err != nil {
		return nil, mapStorageError(err, biz.ErrUserNotFound, biz.ErrUserInvalidArgument, biz.ErrUserAlreadyExists)
	}
	out := &biz.UserList{
		Items:      make([]*biz.User, 0, len(res.Items)),
		HasMore:    res.HasMore,
		NextCursor: res.NextCursor,
	}
	for i := range res.Items {
		u, err := toBizUser(&res.Items[i])
		if err != nil {
			return nil, err
		}
		out.Items = append(out.Items, u)
	}
	// total 与翻页位置无关，计数前剥离游标：游标会把行集收窄到某一页
	// 位置之后，导致各页 total 递减。
	if dynq.IncludeTotalOf(q.Pagination) {
		countParams := params
		countParams.After = nil
		total, err := gen.CountUsers(ctx, r.data.db, countParams)
		if err != nil {
			return nil, mapStorageError(err, biz.ErrUserNotFound, biz.ErrUserInvalidArgument, biz.ErrUserAlreadyExists)
		}
		out.Total = &total
	}
	return out, nil
}

// userOptionalColumns 将可空的字符串/日期/UUID/JSON 字段转换为驱动类型。
// 任一转失败均视为请求参数非法。
func userOptionalColumns(u *biz.User) (pgtype.Numeric, pgtype.Date, pgtype.UUID, []byte, error) {
	balance, err := stringToNumeric(u.Balance)
	if err != nil {
		return pgtype.Numeric{}, pgtype.Date{}, pgtype.UUID{}, nil, err
	}
	birthDate, err := stringToDate(u.BirthDate)
	if err != nil {
		return pgtype.Numeric{}, pgtype.Date{}, pgtype.UUID{}, nil, err
	}
	orgID, err := stringToUUID(u.OrgID)
	if err != nil {
		return pgtype.Numeric{}, pgtype.Date{}, pgtype.UUID{}, nil, err
	}
	meta, err := mapToJSONB(u.Meta)
	if err != nil {
		return pgtype.Numeric{}, pgtype.Date{}, pgtype.UUID{}, nil, err
	}
	return balance, birthDate, orgID, meta, nil
}

// Create 插入用户并返回持久化行。password 由用例层传入已哈希值；
// 唯一邮箱冲突映射为 ErrUserAlreadyExists。
func (r *userRepo) Create(ctx context.Context, u *biz.User) (*biz.User, error) {
	balance, birthDate, orgID, meta, err := userOptionalColumns(u)
	if err != nil {
		return nil, biz.ErrUserInvalidArgument
	}
	po, err := gen.New(r.data.db).CreateUser(ctx, gen.CreateUserParams{
		Name:      u.Name,
		Email:     u.Email,
		Password:  u.Password,
		Status:    gen.UserStatus(u.Status),
		Role:      u.Role,
		Age:       u.Age,
		Score:     u.Score,
		Balance:   balance,
		BirthDate: birthDate,
		OrgID:     orgID,
		Tags:      u.Tags,
		Meta:      meta,
	})
	if err != nil {
		return nil, mapStorageError(err, biz.ErrUserNotFound, biz.ErrUserInvalidArgument, biz.ErrUserAlreadyExists)
	}
	return toBizUser(&po)
}

// Update 全量覆盖用户可变字段（含 email）。status、password 与时间戳
// 不由本方法写入；未命中返回 ErrUserNotFound。
func (r *userRepo) Update(ctx context.Context, u *biz.User) (*biz.User, error) {
	balance, birthDate, orgID, meta, err := userOptionalColumns(u)
	if err != nil {
		return nil, biz.ErrUserInvalidArgument
	}
	po, err := gen.New(r.data.db).UpdateUser(ctx, gen.UpdateUserParams{
		ID:        u.ID,
		Name:      u.Name,
		Email:     u.Email,
		Role:      u.Role,
		Age:       u.Age,
		Score:     u.Score,
		Balance:   balance,
		BirthDate: birthDate,
		OrgID:     orgID,
		Tags:      u.Tags,
		Meta:      meta,
	})
	if err != nil {
		return nil, mapStorageError(err, biz.ErrUserNotFound, biz.ErrUserInvalidArgument, biz.ErrUserAlreadyExists)
	}
	return toBizUser(&po)
}

// SetPassword 替换已哈希密码；目标不存在时返回 ErrUserNotFound。
func (r *userRepo) SetPassword(ctx context.Context, id int64, password string) error {
	rows, err := gen.New(r.data.db).SetUserPassword(ctx, gen.SetUserPasswordParams{ID: id, Password: password})
	if err != nil {
		return mapStorageError(err, biz.ErrUserNotFound, biz.ErrUserInvalidArgument, biz.ErrUserAlreadyExists)
	}
	if rows == 0 {
		return biz.ErrUserNotFound
	}
	return nil
}

// Delete 物理删除用户；未命中时返回 ErrUserNotFound。
func (r *userRepo) Delete(ctx context.Context, id int64) error {
	rows, err := gen.New(r.data.db).DeleteUser(ctx, id)
	if err != nil {
		return mapStorageError(err, biz.ErrUserNotFound, biz.ErrUserInvalidArgument, biz.ErrUserAlreadyExists)
	}
	if rows == 0 {
		return biz.ErrUserNotFound
	}
	return nil
}

// ---- pgtype 转换 ----

// tsToTime 渲染 NOT NULL 的 timestamptz；NULL 以零值代替。
func tsToTime(t pgtype.Timestamptz) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time
}

// tsToTimePtr 渲染可空 timestamptz；NULL 返回 nil。
func tsToTimePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	tt := t.Time
	return &tt
}

// numericToString 将 NUMERIC 渲染为规范十进制字符串。
func numericToString(n pgtype.Numeric) *string {
	if !n.Valid {
		return nil
	}
	v, err := n.Value()
	if err != nil {
		return nil
	}
	s, _ := v.(string)
	return &s
}

// stringToNumeric 将十进制字符串解析为 NUMERIC；nil/空串返回非 valid 零值。
func stringToNumeric(s *string) (pgtype.Numeric, error) {
	if s == nil || *s == "" {
		return pgtype.Numeric{}, nil
	}
	var n pgtype.Numeric
	if err := n.Scan(*s); err != nil {
		return pgtype.Numeric{}, err
	}
	return n, nil
}

// dateToString 将 DATE 渲染为 YYYY-MM-DD 字符串。
func dateToString(d pgtype.Date) *string {
	if !d.Valid {
		return nil
	}
	s := d.Time.Format("2006-01-02")
	return &s
}

// stringToDate 将 YYYY-MM-DD 字符串解析为 DATE；nil/空串返回非 valid 零值。
func stringToDate(s *string) (pgtype.Date, error) {
	if s == nil || *s == "" {
		return pgtype.Date{}, nil
	}
	t, err := time.ParseInLocation("2006-01-02", *s, time.UTC)
	if err != nil {
		return pgtype.Date{}, err
	}
	return pgtype.Date{Time: t, Valid: true}, nil
}

// uuidToString 将 UUID 渲染为标准连字符字符串。
func uuidToString(u pgtype.UUID) *string {
	if !u.Valid {
		return nil
	}
	s := uuid.UUID(u.Bytes).String()
	return &s
}

// stringToUUID 将 UUID 字符串解析为 pgtype.UUID；nil/空串返回非 valid 零值。
func stringToUUID(s *string) (pgtype.UUID, error) {
	if s == nil || *s == "" {
		return pgtype.UUID{}, nil
	}
	id, err := uuid.Parse(*s)
	if err != nil {
		return pgtype.UUID{}, err
	}
	return pgtype.UUID{Bytes: id, Valid: true}, nil
}

// jsonbToMap 将 JSONB 字节反序列化为 map；空字节返回 nil。
func jsonbToMap(b []byte) (map[string]any, error) {
	if len(b) == 0 {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// mapToJSONB 将 map 序列化为 JSONB 字节；nil 返回 nil。
func mapToJSONB(m map[string]any) ([]byte, error) {
	if m == nil {
		return nil, nil
	}
	return json.Marshal(m)
}
