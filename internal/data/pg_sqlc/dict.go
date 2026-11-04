package pg_sqlc

import (
	"context"
	"errors"

	"grape/internal/biz"
	"grape/internal/data/pg_sqlc/gen"

	"github.com/jackc/pgx/v5"
	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

// dictRepo 是 biz.DictRepo 的 PostgreSQL 实现。字典是租户内公共资源：
// 表无 @scope，行级无范围，租户边界由 IdentityTx 的 RLS 会话变量承担。
// 类型与数据两级收敛在同一仓储：数据创建路径先锁宿主类型行再写入，
// 类型删除路径在同一事务内校验活跃数据后软删。
type dictRepo struct {
	data *Data
}

// NewDictRepo 构造字典仓储。
func NewDictRepo(data *Data) biz.DictRepo {
	return &dictRepo{data: data}
}

// toBizDictType 将持久化字典类型转换为领域模型。
func toBizDictType(po *gen.SysDictType) *biz.DictType {
	if po == nil {
		return nil
	}
	return &biz.DictType{
		ID:        po.ID,
		Status:    int32(po.Status),
		Name:      po.Name,
		Code:      po.Code,
		Remark:    po.Remark,
		CreatedBy: po.CreatedBy,
		UpdatedBy: po.UpdatedBy,
		CreatedAt: tsToTime(po.CreatedAt),
		UpdatedAt: tsToTimePtr(po.UpdatedAt),
		DeletedAt: tsToTimePtr(po.DeletedAt),
	}
}

// toBizDictData 将持久化字典数据转换为领域模型。
func toBizDictData(po *gen.SysDictDatum) *biz.DictData {
	if po == nil {
		return nil
	}
	return &biz.DictData{
		ID:         po.ID,
		Status:     int32(po.Status),
		DictTypeID: po.DictTypeID,
		Label:      po.Label,
		Value:      po.Value,
		ValueType:  po.ValueType,
		SortOrder:  po.SortOrder,
		Remark:     po.Remark,
		CreatedBy:  po.CreatedBy,
		UpdatedBy:  po.UpdatedBy,
		CreatedAt:  tsToTime(po.CreatedAt),
		UpdatedAt:  tsToTimePtr(po.UpdatedAt),
		DeletedAt:  tsToTimePtr(po.DeletedAt),
	}
}

// GetTypeByID 按主键读取字典类型。租户边界由 RLS 承担，跨租户等同不存在。
func (r *dictRepo) GetTypeByID(ctx context.Context, auth biz.Identity, id int64) (*biz.DictType, error) {
	var out *biz.DictType
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().GetSysDictTypeByID(ctx, id)
		if err != nil {
			return err
		}
		out = toBizDictType(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// ListTypes 解析动态查询协议并返回分页信封；表无 @scope，无需注入守卫，
// 软删过滤由表级 @filter 声明在解析期作为服务端固定不变量注入，字段、
// 操作符、投影与游标合法性由生成代码校验，协议错误映射为
// ErrDictTypeInvalidArgument。
func (r *dictRepo) ListTypes(ctx context.Context, auth biz.Identity, q *dynq.Query) (*biz.DictTypeList, error) {
	var out *biz.DictTypeList
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		params, err := gen.ParseSysDictTypeQuery(q)
		if err != nil {
			return err
		}
		res, err := gen.ListSysDictType(ctx, s.Tx(), params)
		if err != nil {
			return err
		}
		list := &biz.DictTypeList{
			Items:      make([]*biz.DictType, 0, len(res.Items)),
			HasMore:    res.HasMore,
			NextCursor: res.NextCursor,
		}
		for i := range res.Items {
			list.Items = append(list.Items, toBizDictType(&res.Items[i]))
		}
		// total 与翻页位置无关，计数前剥离游标：游标会把行集收窄到
		// 某一页位置之后，导致各页 total 递减。
		if dynq.IncludeTotalOf(q.Pagination) {
			countParams := params
			countParams.After = nil
			total, err := gen.CountSysDictType(ctx, s.Tx(), countParams)
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

// CreateType 创建字典类型：主键由应用层发号（newID），租户取自会话，
// 审计列记操作者；租户内未删除类型间 code 唯一冲突映射为
// ErrDictTypeAlreadyExists。
func (r *dictRepo) CreateType(ctx context.Context, auth biz.Identity, t *biz.DictType) (*biz.DictType, error) {
	var out *biz.DictType
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().InsertSysDictType(ctx, gen.InsertSysDictTypeParams{
			ID:        newID(),
			Status:    int16(t.Status),
			Name:      t.Name,
			Code:      t.Code,
			Remark:    t.Remark,
			CreatedBy: auth.UserID,
			UpdatedBy: auth.UserID,
		})
		if err != nil {
			return err
		}
		out = toBizDictType(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// UpdateType 覆盖可变字段（name、status、remark）。目标行先经
// LockSysDictTypeByID 锁定：不存在或跨租户等同不存在（NotFound），锁持有
// 到提交。
func (r *dictRepo) UpdateType(ctx context.Context, auth biz.Identity, t *biz.DictType) (*biz.DictType, error) {
	var out *biz.DictType
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		if _, err := s.Q().LockSysDictTypeByID(ctx, t.ID); err != nil {
			return err
		}
		updated, err := s.Q().UpdateSysDictType(ctx, gen.UpdateSysDictTypeParams{
			Name:      t.Name,
			Status:    int16(t.Status),
			Remark:    t.Remark,
			UpdatedBy: auth.UserID,
			ID:        t.ID,
		})
		if err != nil {
			return err
		}
		out = toBizDictType(&updated)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// SoftDeleteType 软删除字典类型。目标行先经 LockSysDictTypeByID 锁定，
// 同一事务内统计未删除字典数据：存在即拒绝（ErrDictTypeHasData）不写入，
// 判定与写入同事务，杜绝并发窗口下类型先删、数据后插产生的孤儿。
func (r *dictRepo) SoftDeleteType(ctx context.Context, auth biz.Identity, id int64) error {
	return r.mapErr(r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		if _, err := s.Q().LockSysDictTypeByID(ctx, id); err != nil {
			return err
		}
		active, err := s.Q().CountSysDictDataByType(ctx, id)
		if err != nil {
			return err
		}
		if active > 0 {
			return biz.ErrDictTypeHasData
		}
		rows, err := s.Q().SoftDeleteSysDictType(ctx, gen.SoftDeleteSysDictTypeParams{
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

// GetDataByID 按主键读取字典数据。租户边界由 RLS 承担，跨租户等同不存在。
func (r *dictRepo) GetDataByID(ctx context.Context, auth biz.Identity, id int64) (*biz.DictData, error) {
	var out *biz.DictData
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().GetSysDictDataByID(ctx, id)
		if err != nil {
			return err
		}
		out = toBizDictData(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapDataErr(err)
	}
	return out, nil
}

// ListDatas 解析动态查询协议并返回分页信封，口径同 ListTypes，协议错误
// 映射为 ErrDictDataInvalidArgument。
func (r *dictRepo) ListDatas(ctx context.Context, auth biz.Identity, q *dynq.Query) (*biz.DictDataList, error) {
	var out *biz.DictDataList
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		params, err := gen.ParseSysDictDataQuery(q)
		if err != nil {
			return err
		}
		res, err := gen.ListSysDictData(ctx, s.Tx(), params)
		if err != nil {
			return err
		}
		list := &biz.DictDataList{
			Items:      make([]*biz.DictData, 0, len(res.Items)),
			HasMore:    res.HasMore,
			NextCursor: res.NextCursor,
		}
		for i := range res.Items {
			list.Items = append(list.Items, toBizDictData(&res.Items[i]))
		}
		// total 与翻页位置无关，计数前剥离游标：游标会把行集收窄到
		// 某一页位置之后，导致各页 total 递减。
		if dynq.IncludeTotalOf(q.Pagination) {
			countParams := params
			countParams.After = nil
			total, err := gen.CountSysDictData(ctx, s.Tx(), countParams)
			if err != nil {
				return err
			}
			list.Total = &total
		}
		out = list
		return nil
	})
	if err != nil {
		return nil, r.mapDataErr(err)
	}
	return out, nil
}

// CreateData 创建字典数据：先锁宿主类型行（不存在或跨租户或已删除等同
// 未知，返回 ErrDictDataInvalidArgument 不写入），再同事务插入数据行；
// 同类型下未删除数据间 value 唯一冲突映射为 ErrDictDataAlreadyExists。
func (r *dictRepo) CreateData(ctx context.Context, auth biz.Identity, d *biz.DictData) (*biz.DictData, error) {
	var out *biz.DictData
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		if _, err := s.Q().LockSysDictTypeByID(ctx, d.DictTypeID); err != nil {
			// 宿主类型不存在/跨租户/已删除是客户端传入引用无效：映射为
			// 入参错误（INVALID_ARGUMENT）而非 NotFound。
			if errors.Is(err, pgx.ErrNoRows) {
				return biz.ErrDictDataInvalidArgument
			}
			return err
		}
		row, err := s.Q().InsertSysDictData(ctx, gen.InsertSysDictDataParams{
			ID:         newID(),
			Status:     int16(d.Status),
			DictTypeID: d.DictTypeID,
			Label:      d.Label,
			Value:      d.Value,
			ValueType:  d.ValueType,
			SortOrder:  d.SortOrder,
			Remark:     d.Remark,
			CreatedBy:  auth.UserID,
			UpdatedBy:  auth.UserID,
		})
		if err != nil {
			return err
		}
		out = toBizDictData(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapDataErr(err)
	}
	return out, nil
}

// UpdateData 覆盖可变字段（label、status、sort_order、remark）。目标行先经
// LockSysDictDataByID 锁定：不存在或跨租户等同不存在（NotFound），锁持有
// 到提交。
func (r *dictRepo) UpdateData(ctx context.Context, auth biz.Identity, d *biz.DictData) (*biz.DictData, error) {
	var out *biz.DictData
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		if _, err := s.Q().LockSysDictDataByID(ctx, d.ID); err != nil {
			return err
		}
		updated, err := s.Q().UpdateSysDictData(ctx, gen.UpdateSysDictDataParams{
			Label:     d.Label,
			Status:    int16(d.Status),
			SortOrder: d.SortOrder,
			Remark:    d.Remark,
			UpdatedBy: auth.UserID,
			ID:        d.ID,
		})
		if err != nil {
			return err
		}
		out = toBizDictData(&updated)
		return nil
	})
	if err != nil {
		return nil, r.mapDataErr(err)
	}
	return out, nil
}

// SoftDeleteData 软删除字典数据。目标行先经 LockSysDictDataByID 锁定；
// 宿主类型不受影响。
func (r *dictRepo) SoftDeleteData(ctx context.Context, auth biz.Identity, id int64) error {
	return r.mapDataErr(r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		if _, err := s.Q().LockSysDictDataByID(ctx, id); err != nil {
			return err
		}
		rows, err := s.Q().SoftDeleteSysDictData(ctx, gen.SoftDeleteSysDictDataParams{
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

// mapErr 将驱动与协议错误映射为字典资源的领域错误：无行（含跨租户等同
// 不存在）→ NotFound；dynq 协议错误 → InvalidArgument；唯一约束冲突 →
// AlreadyExists（两级各自的目标错误经 notFound/invalidArg/alreadyExists
// 按调用路径区分）。ErrDictTypeHasData 是业务拒绝，非驱动错误，原样透传。
func (r *dictRepo) mapErr(err error) error {
	if err == biz.ErrDictTypeHasData {
		return err
	}
	return mapStorageError(err, biz.ErrDictTypeNotFound, biz.ErrDictTypeInvalidArgument, biz.ErrDictTypeAlreadyExists)
}

// mapDataErr 与 mapErr 同型，目标错误换成字典数据侧；ErrDictTypeHasData
// 不可能出现在数据路径，无须透传。
func (r *dictRepo) mapDataErr(err error) error {
	return mapStorageError(err, biz.ErrDictDataNotFound, biz.ErrDictDataInvalidArgument, biz.ErrDictDataAlreadyExists)
}
