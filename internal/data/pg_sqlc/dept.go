package pg_sqlc

import (
	"context"

	"grape/internal/biz"
	"grape/internal/data/pg_sqlc/gen"

	"github.com/jackc/pgx/v5"
	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

// deptRepo 是 biz.DeptRepo 的 PostgreSQL 实现。组织树是租户内资源：表无
// @scope，行级无范围，租户边界由 IdentityTx 的 RLS 会话变量承担。闭包表
// sys_dept_closure 是派生数据，全部由 trg_maintain_dept_closure 触发器在
// 写事务内自动维护（建立/软删清理/移动重写与父级、防环校验），本仓储不
// 直接读写闭包表；触发器的拒绝是并发窗口的兜底防线。
type deptRepo struct {
	data *Data
}

// NewDeptRepo 构造部门仓储。
func NewDeptRepo(data *Data) biz.DeptRepo {
	return &deptRepo{data: data}
}

// toBizDept 将持久化部门转换为领域模型。
func toBizDept(po *gen.SysDept) *biz.Dept {
	if po == nil {
		return nil
	}
	return &biz.Dept{
		ID:               po.ID,
		Status:           int32(po.Status),
		Name:             po.Name,
		ParentID:         po.ParentID,
		OwnerUserID:      po.OwnerUserID,
		OwnerUserGroupID: po.OwnerUserGroupID,
		CreatedBy:        po.CreatedBy,
		UpdatedBy:        po.UpdatedBy,
		CreatedAt:        tsToTime(po.CreatedAt),
		UpdatedAt:        tsToTimePtr(po.UpdatedAt),
		DeletedAt:        tsToTimePtr(po.DeletedAt),
	}
}

// GetByID 按主键读取部门。租户边界由 RLS 承担，跨租户等同不存在。
func (r *deptRepo) GetByID(ctx context.Context, auth biz.Identity, id int64) (*biz.Dept, error) {
	var out *biz.Dept
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().GetSysDeptByID(ctx, id)
		if err != nil {
			return err
		}
		out = toBizDept(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// List 解析动态查询协议并返回分页信封；表无 @scope，无需注入守卫，
// 软删过滤由表级 @filter 声明在解析期作为服务端固定不变量注入（协议
// 不可见、调用方不可控），字段、操作符、投影与游标合法性由生成代码
// 校验，协议错误映射为 ErrDeptInvalidArgument。
func (r *deptRepo) List(ctx context.Context, auth biz.Identity, q *dynq.Query) (*biz.DeptList, error) {
	var out *biz.DeptList
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		params, err := gen.ParseSysDeptQuery(q)
		if err != nil {
			return err
		}
		res, err := gen.ListSysDept(ctx, s.Tx(), params)
		if err != nil {
			return err
		}
		list := &biz.DeptList{
			Items:      make([]*biz.Dept, 0, len(res.Items)),
			HasMore:    res.HasMore,
			NextCursor: res.NextCursor,
		}
		for i := range res.Items {
			list.Items = append(list.Items, toBizDept(&res.Items[i]))
		}
		// total 与翻页位置无关，计数前剥离游标：游标会把行集收窄到
		// 某一页位置之后，导致各页 total 递减。
		if dynq.IncludeTotalOf(q.Pagination) {
			countParams := params
			countParams.After = nil
			total, err := gen.CountSysDept(ctx, s.Tx(), countParams)
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

// ListAll 返回租户内全部未删除部门（组织树装配与环检测的全量输入）。
func (r *deptRepo) ListAll(ctx context.Context, auth biz.Identity) ([]*biz.Dept, error) {
	var out []*biz.Dept
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		rows, err := s.Q().ListSysDeptAll(ctx)
		if err != nil {
			return err
		}
		out = make([]*biz.Dept, 0, len(rows))
		for i := range rows {
			out = append(out, toBizDept(&rows[i]))
		}
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// Create 创建部门：主键由应用层发号（newID），租户取自会话，审计列记
// 操作者；租户内未删除部门间 name 唯一冲突映射为 ErrDeptAlreadyExists。
// 父级合法性由触发器在同一事务内兜底校验。
func (r *deptRepo) Create(ctx context.Context, auth biz.Identity, d *biz.Dept) (*biz.Dept, error) {
	var out *biz.Dept
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().InsertSysDept(ctx, gen.InsertSysDeptParams{
			ID:               newID(),
			Status:           int16(d.Status),
			Name:             d.Name,
			ParentID:         d.ParentID,
			OwnerUserID:      d.OwnerUserID,
			OwnerUserGroupID: d.OwnerUserGroupID,
			CreatedBy:        auth.UserID,
			UpdatedBy:        auth.UserID,
		})
		if err != nil {
			return err
		}
		out = toBizDept(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// Update 覆盖可变字段（name、status）。目标行先经 LockSysDeptByID 锁定：
// 不存在或跨租户等同不存在（NotFound），锁持有到提交。
func (r *deptRepo) Update(ctx context.Context, auth biz.Identity, d *biz.Dept) (*biz.Dept, error) {
	var out *biz.Dept
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		if _, err := s.Q().LockSysDeptByID(ctx, d.ID); err != nil {
			return err
		}
		updated, err := s.Q().UpdateSysDept(ctx, gen.UpdateSysDeptParams{
			Name:      d.Name,
			Status:    int16(d.Status),
			UpdatedBy: auth.UserID,
			ID:        d.ID,
		})
		if err != nil {
			return err
		}
		out = toBizDept(&updated)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// Move 移动部门到新父级（parentID 为 nil 表示移为根部门）。目标行先经
// LockSysDeptByID 锁定；parent_id 变更触发 trg_maintain_dept_closure 在
// 同一事务内校验父级与防环并重写闭包路径，触发器拒绝（并发窗口的兜底）
// 原样透传为存储错误并整体回滚。
func (r *deptRepo) Move(ctx context.Context, auth biz.Identity, id int64, parentID *int64) (*biz.Dept, error) {
	var out *biz.Dept
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		if _, err := s.Q().LockSysDeptByID(ctx, id); err != nil {
			return err
		}
		moved, err := s.Q().MoveSysDept(ctx, gen.MoveSysDeptParams{
			ParentID:  parentID,
			UpdatedBy: auth.UserID,
			ID:        id,
		})
		if err != nil {
			return err
		}
		out = toBizDept(&moved)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// SoftDelete 软删除部门。同一事务内先锁行、再数未删除直接子部门：有子
// 部门返回 ErrDeptHasChildren（触发器兜底同口径），否则置位 deleted_at
// 并由触发器清理闭包路径（保留子树内部关系）。
func (r *deptRepo) SoftDelete(ctx context.Context, auth biz.Identity, id int64) error {
	return r.mapErr(r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		if _, err := s.Q().LockSysDeptByID(ctx, id); err != nil {
			return err
		}
		children, err := s.Q().CountSysDeptActiveChildren(ctx, &id)
		if err != nil {
			return err
		}
		if children > 0 {
			return biz.ErrDeptHasChildren
		}
		rows, err := s.Q().SoftDeleteSysDept(ctx, gen.SoftDeleteSysDeptParams{
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

// mapErr 将驱动与协议错误映射为部门资源的领域错误：无行（含跨租户
// 等同不存在）→ NotFound；dynq 协议错误 → InvalidArgument；唯一约束
// 冲突 → AlreadyExists；其余（含触发器拒绝）原样透传。
func (r *deptRepo) mapErr(err error) error {
	return mapStorageError(err, biz.ErrDeptNotFound, biz.ErrDeptInvalidArgument, biz.ErrDeptAlreadyExists)
}
