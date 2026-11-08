package pg_sqlc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"grape/internal/biz"
	"grape/internal/data/pg_sqlc/gen"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

// tenantRepo 是 biz.TenantRepo 的 PostgreSQL 实现。sys_tenant 按租户树模型
// 由 RLS 承担边界：每个租户可读写自己名下的直接子租户与自身，树外行天然
// 不可见。super 命名表达「管理一整个租户实体」，不表示绕过 RLS；删除前置
// 的受控计数函数见 internal/data/README.md「迁移暂存（阶段 2 并入）」。
type tenantRepo struct {
	data *Data
}

// NewTenantRepo 构造租户仓储。
func NewTenantRepo(data *Data) biz.TenantRepo {
	return &tenantRepo{data: data}
}

// platformTenantGuard 是 biz.PlatformTenantGuard 的 PostgreSQL 实现：在
// 调用方自己的租户事务内读取 app.tenant_id 对应的租户行（RLS 放行
// id = app.tenant_id），parent_id 为 NULL 即平台（根）租户。租户不存在、
// 已软删或读取命不中一律 false（fail-closed）；不探测树外租户（RLS 下
// 本就不可见）。读取不写审计列。
type platformTenantGuard struct {
	data *Data
}

// NewPlatformTenantGuard 构造平台租户判定器。
func NewPlatformTenantGuard(data *Data) biz.PlatformTenantGuard {
	return &platformTenantGuard{data: data}
}

// IsPlatformTenant 读取调用方自身租户行并判定 parent_id：无行（不存在/
// 已软删/无身份）返回 (false, nil)，判定语义见 biz.PlatformTenantGuard。
func (g *platformTenantGuard) IsPlatformTenant(ctx context.Context, auth biz.Identity) (bool, error) {
	var platform bool
	err := g.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().GetSysTenantByID(ctx, auth.TenantID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		platform = row.ParentID == nil
		return nil
	})
	if err != nil {
		return false, err
	}
	return platform, nil
}

// tenantIDRandomBytes 是发号随机部分的字节数：7 字节 → 14 位小写十六
// 进制，加前缀共 16 字符，恰好填满 sys_tenant.id 的 varchar(16)。
const tenantIDRandomBytes = 7

// newTenantID 生成字符串主键：`t-` + 14 位小写十六进制（crypto/rand，
// 随机空间 2^56）。碰撞由主键约束兜底：极小概率的碰撞与 code/name 冲突
// 一样映射为 AlreadyExists（可重试创建）。不与 int64 发号（newID）混用
// ——sys_tenant.id 是 varchar，与其余体系表的 int64 主键形态不同。
func newTenantID() (string, error) {
	var b [tenantIDRandomBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "t-" + hex.EncodeToString(b[:]), nil
}

// timeToTS 将可空时间转换为 sqlc 的 pgtype.Timestamp：nil 表示 NULL。
func timeToTS(t *time.Time) pgtype.Timestamp {
	if t == nil {
		return pgtype.Timestamp{}
	}
	return pgtype.Timestamp{Time: *t, Valid: true}
}

// toBizTenant 将持久化租户转换为领域模型。
func toBizTenant(po *gen.SysTenant) *biz.Tenant {
	if po == nil {
		return nil
	}
	return &biz.Tenant{
		ID:            po.ID,
		Status:        int32(po.Status),
		Code:          po.Code,
		Name:          po.Name,
		ParentID:      po.ParentID,
		ContactName:   po.ContactName,
		ContactPhone:  po.ContactPhone,
		ExpiredAt:     tsToTimePtr(po.ExpiredAt),
		MaxUserCount:  po.MaxUserCount,
		DefaultLocale: po.DefaultLocale,
		TenantID:      po.TenantID,
		CreatedBy:     po.CreatedBy,
		UpdatedBy:     po.UpdatedBy,
		CreatedAt:     tsToTime(po.CreatedAt),
		UpdatedAt:     tsToTimePtr(po.UpdatedAt),
		DeletedAt:     tsToTimePtr(po.DeletedAt),
	}
}

// Get 按主键读取租户。租户树边界由 RLS 承担（树外行等同不存在）。
func (r *tenantRepo) Get(ctx context.Context, auth biz.Identity, id string) (*biz.Tenant, error) {
	var out *biz.Tenant
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().GetSysTenantByID(ctx, id)
		if err != nil {
			return err
		}
		out = toBizTenant(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// List 解析动态查询协议并返回分页信封；租户树可见范围由 RLS 承担（表
// 无 @scope，行级无范围），软删过滤由表级 @filter 声明在解析期作为服务端
// 固定不变量注入，字段、操作符、投影与游标合法性由生成代码校验，协议
// 错误映射为 ErrTenantInvalidArgument。
func (r *tenantRepo) List(ctx context.Context, auth biz.Identity, q *dynq.Query) (*biz.TenantList, error) {
	var out *biz.TenantList
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		params, err := gen.ParseSysTenantQuery(q)
		if err != nil {
			return err
		}
		res, err := gen.ListSysTenant(ctx, s.Tx(), params)
		if err != nil {
			return err
		}
		list := &biz.TenantList{
			Items:      make([]*biz.Tenant, 0, len(res.Items)),
			HasMore:    res.HasMore,
			NextCursor: res.NextCursor,
		}
		for i := range res.Items {
			list.Items = append(list.Items, toBizTenant(&res.Items[i]))
		}
		// total 与翻页位置无关，计数前剥离游标：游标会把行集收窄到某一页
		// 位置之后，导致各页 total 递减。
		if dynq.IncludeTotalOf(q.Pagination) {
			countParams := params
			countParams.After = nil
			total, err := gen.CountSysTenant(ctx, s.Tx(), countParams)
			if err != nil {
				return err
			}
			list.Total = &total
		}
		out = list
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// CreateSuper 创建租户实体：主键由本仓储发号（newTenantID），tenant_id 由
// SQL 取会话 app.tenant_id 落值，审计列记操作者；code/name 唯一冲突映射
// ErrTenantAlreadyExists。parent_id 只允许省略或等于调用方租户；插入前在
// 同事务锁定父级行，不存在/已软删返回 ErrTenantCallerMissing，与删除锁
// 串行化，杜绝「父删 → 子插」的孤儿。
func (r *tenantRepo) CreateSuper(ctx context.Context, auth biz.Identity, t *biz.Tenant) (*biz.Tenant, error) {
	var out *biz.Tenant
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		id, err := newTenantID()
		if err != nil {
			return err
		}
		parent := auth.TenantID
		if t.ParentID != nil {
			if *t.ParentID != auth.TenantID {
				return biz.ErrTenantInvalidArgument
			}
			parent = *t.ParentID
		}
		if _, err := s.Q().LockSysTenantByID(ctx, parent); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return biz.ErrTenantCallerMissing
			}
			return err
		}
		row, err := s.Q().InsertSysTenant(ctx, gen.InsertSysTenantParams{
			ID:            id,
			Status:        int16(t.Status),
			Code:          t.Code,
			Name:          t.Name,
			ParentID:      &parent,
			ContactName:   t.ContactName,
			ContactPhone:  t.ContactPhone,
			ExpiredAt:     timeToTS(t.ExpiredAt),
			MaxUserCount:  t.MaxUserCount,
			DefaultLocale: t.DefaultLocale,
			CreatedBy:     auth.UserID,
			UpdatedBy:     auth.UserID,
		})
		if err != nil {
			return err
		}
		out = toBizTenant(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// UpdateSuper 覆盖可变字段（status、name、contact、expired_at、max_user_count、
// default_locale）；code 是创建后不可变的身份列。目标行先经 LockSysTenantByID
// 锁定：不存在、已软删或树外不可见等同不存在。调用方自身租户的 status 变更
// 与当前值不同即 ErrTenantSelfStatusChange（自身停用/恢复须走 Disable/Restore）。
func (r *tenantRepo) UpdateSuper(ctx context.Context, auth biz.Identity, t *biz.Tenant) (*biz.Tenant, error) {
	var out *biz.Tenant
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		locked, err := s.Q().LockSysTenantByID(ctx, t.ID)
		if err != nil {
			return err
		}
		if t.ID == auth.TenantID && int32(locked.Status) != t.Status {
			return biz.ErrTenantSelfStatusChange
		}
		updated, err := s.Q().UpdateSysTenant(ctx, gen.UpdateSysTenantParams{
			Status:        int16(t.Status),
			Name:          t.Name,
			ContactName:   t.ContactName,
			ContactPhone:  t.ContactPhone,
			ExpiredAt:     timeToTS(t.ExpiredAt),
			MaxUserCount:  t.MaxUserCount,
			DefaultLocale: t.DefaultLocale,
			UpdatedBy:     auth.UserID,
			ID:            t.ID,
		})
		if err != nil {
			return err
		}
		out = toBizTenant(&updated)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// DeleteSuper 软删除租户。同一事务内先锁行、再经受控计数函数统计未删除
// 直接子租户：有子返回 ErrTenantHasChildren 不写入，否则置位 deleted_at。
// 孤儿判定口径（祖父看不到孙行、函数只返回计数）见 internal/data/README.md
// 「迁移暂存（阶段 2 并入）」。
func (r *tenantRepo) DeleteSuper(ctx context.Context, auth biz.Identity, id string) error {
	return r.mapErr(r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		if _, err := s.Q().LockSysTenantByID(ctx, id); err != nil {
			return err
		}
		children, err := s.Q().CountSysTenantActiveChildren(ctx, id)
		if err != nil {
			return err
		}
		if children > 0 {
			return biz.ErrTenantHasChildren
		}
		rows, err := s.Q().SoftDeleteSysTenant(ctx, gen.SoftDeleteSysTenantParams{
			UpdatedBy: auth.UserID,
			ID:        id,
		})
		if err != nil {
			return err
		}
		if rows == 0 {
			return pgx.ErrNoRows
		}
		return nil
	}))
}

// DisableSuper 置 status=0（幂等目标状态写：已停用再停用不出错）。目标行
// 先锁后写，不可见等同不存在。
func (r *tenantRepo) DisableSuper(ctx context.Context, auth biz.Identity, id string) (*biz.Tenant, error) {
	return r.setStatus(ctx, auth, id, biz.TenantStatusDisabled)
}

// RestoreSuper 置 status=1（幂等目标状态写）。目标行先锁后写，不可见等同
// 不存在。
func (r *tenantRepo) RestoreSuper(ctx context.Context, auth biz.Identity, id string) (*biz.Tenant, error) {
	return r.setStatus(ctx, auth, id, biz.TenantStatusEnabled)
}

// setStatus 是停用/恢复共用的目标状态写：先锁行再覆盖 status，updated_by/
// updated_at 随之刷新。
func (r *tenantRepo) setStatus(ctx context.Context, auth biz.Identity, id string, status int32) (*biz.Tenant, error) {
	var out *biz.Tenant
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		if _, err := s.Q().LockSysTenantByID(ctx, id); err != nil {
			return err
		}
		row, err := s.Q().SetSysTenantStatus(ctx, gen.SetSysTenantStatusParams{
			Status:    int16(status),
			UpdatedBy: auth.UserID,
			ID:        id,
		})
		if err != nil {
			return err
		}
		out = toBizTenant(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// mapErr 将驱动与协议错误映射为租户资源的领域错误：无行（含树外不可见
// 与已软删等同不存在）→ NotFound；dynq 协议错误 → InvalidArgument；
// 唯一约束冲突 → AlreadyExists；CHECK/列宽截断由 mapStorageError 归入
// InvalidArgument。ErrTenantHasChildren 是业务拒绝，非驱动错误，原样
// 透传。
func (r *tenantRepo) mapErr(err error) error {
	if errors.Is(err, biz.ErrTenantHasChildren) {
		return err
	}
	return mapStorageError(err, biz.ErrTenantNotFound, biz.ErrTenantInvalidArgument, biz.ErrTenantAlreadyExists)
}
