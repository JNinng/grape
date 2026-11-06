package pg_sqlc

import (
	"context"

	"grape/internal/biz"
	"grape/internal/data/pg_sqlc/gen"

	"github.com/jackc/pgx/v5"
	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

// userGroupRepo 是 biz.UserGroupRepo 的 PostgreSQL 实现。用户组是租户内
// 资源：表无 @scope，行级无范围，租户边界由 IdentityTx 的 RLS 会话变量
// 承担。成员映射 sys_user_group_mapping 不单独立资源，随本仓储的成员
// 方法维护：写路径先锁组行再操作映射表，映射行移除走物理删除。
type userGroupRepo struct {
	data *Data
}

// NewUserGroupRepo 构造用户组仓储。
func NewUserGroupRepo(data *Data) biz.UserGroupRepo {
	return &userGroupRepo{data: data}
}

// toBizUserGroup 将持久化用户组转换为领域模型。
func toBizUserGroup(po *gen.SysUserGroup) *biz.UserGroup {
	if po == nil {
		return nil
	}
	return &biz.UserGroup{
		ID:          po.ID,
		Status:      int32(po.Status),
		Name:        po.Name,
		Code:        po.Code,
		Remark:      po.Remark,
		OwnerUserID: po.OwnerUserID,
		CreatedBy:   po.CreatedBy,
		UpdatedBy:   po.UpdatedBy,
		CreatedAt:   tsToTime(po.CreatedAt),
		UpdatedAt:   tsToTimePtr(po.UpdatedAt),
		DeletedAt:   tsToTimePtr(po.DeletedAt),
	}
}

// toBizUserGroupMember 将成员联结行转换为领域模型。
func toBizUserGroupMember(row *gen.ListSysUserGroupMembersRow) *biz.UserGroupMember {
	return &biz.UserGroupMember{
		UserID:   row.UserID,
		Name:     row.Name,
		Nickname: row.Nickname,
		Status:   int32(row.Status),
		AddedBy:  row.AddedBy,
		AddedAt:  tsToTime(row.AddedAt),
	}
}

// GetByID 按主键读取用户组。租户边界由 RLS 承担，跨租户等同不存在。
func (r *userGroupRepo) GetByID(ctx context.Context, auth biz.Identity, id int64) (*biz.UserGroup, error) {
	var out *biz.UserGroup
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().GetSysUserGroupByID(ctx, id)
		if err != nil {
			return err
		}
		out = toBizUserGroup(&row)
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
// 校验，协议错误映射为 ErrUserGroupInvalidArgument。
func (r *userGroupRepo) List(ctx context.Context, auth biz.Identity, q *dynq.Query) (*biz.UserGroupList, error) {
	var out *biz.UserGroupList
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		params, err := gen.ParseSysUserGroupQuery(q)
		if err != nil {
			return err
		}
		res, err := gen.ListSysUserGroup(ctx, s.Tx(), params)
		if err != nil {
			return err
		}
		list := &biz.UserGroupList{
			Items:      make([]*biz.UserGroup, 0, len(res.Items)),
			HasMore:    res.HasMore,
			NextCursor: res.NextCursor,
		}
		for i := range res.Items {
			list.Items = append(list.Items, toBizUserGroup(&res.Items[i]))
		}
		// total 与翻页位置无关，计数前剥离游标：游标会把行集收窄到
		// 某一页位置之后，导致各页 total 递减。
		if dynq.IncludeTotalOf(q.Pagination) {
			countParams := params
			countParams.After = nil
			total, err := gen.CountSysUserGroup(ctx, s.Tx(), countParams)
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

// Create 创建用户组：主键由应用层发号（newID），租户取自会话，审计列记
// 操作者；租户内未删除用户组间 code/name 唯一冲突映射为
// ErrUserGroupAlreadyExists。组长归属（owner_user_id，可空）先经
// fn_can_set_owner 校验（存在、启用且在操作人可见范围内），不通过返回
// ErrUserGroupInvalidArgument。
func (r *userGroupRepo) Create(ctx context.Context, auth biz.Identity, g *biz.UserGroup) (*biz.UserGroup, error) {
	var out *biz.UserGroup
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		if g.OwnerUserID != nil {
			ok, err := s.Q().CanSetOwner(ctx, gen.CanSetOwnerParams{
				Operator:  auth.UserID,
				OwnerUser: g.OwnerUserID,
			})
			if err != nil {
				return err
			}
			if !ok {
				return biz.ErrUserGroupInvalidArgument
			}
		}
		row, err := s.Q().InsertSysUserGroup(ctx, gen.InsertSysUserGroupParams{
			ID:          newID(),
			Status:      int16(g.Status),
			Name:        g.Name,
			Code:        g.Code,
			Remark:      g.Remark,
			OwnerUserID: g.OwnerUserID,
			CreatedBy:   auth.UserID,
			UpdatedBy:   auth.UserID,
		})
		if err != nil {
			return err
		}
		out = toBizUserGroup(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// Update 覆盖可变字段（name、status、remark）。目标行先经
// LockSysUserGroupByID 锁定：不存在或跨租户等同不存在（NotFound），
// 锁持有到提交。
func (r *userGroupRepo) Update(ctx context.Context, auth biz.Identity, g *biz.UserGroup) (*biz.UserGroup, error) {
	var out *biz.UserGroup
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		if _, err := s.Q().LockSysUserGroupByID(ctx, g.ID); err != nil {
			return err
		}
		updated, err := s.Q().UpdateSysUserGroup(ctx, gen.UpdateSysUserGroupParams{
			Name:      g.Name,
			Status:    int16(g.Status),
			Remark:    g.Remark,
			UpdatedBy: auth.UserID,
			ID:        g.ID,
		})
		if err != nil {
			return err
		}
		out = toBizUserGroup(&updated)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// SoftDelete 软删除用户组。目标行先经 LockSysUserGroupByID 锁定；成员
// 映射行物理残留（映射表无外键级联），权限推导与成员列表的组表/用户表
// 活跃过滤联结使残留行不产生可见副作用。
func (r *userGroupRepo) SoftDelete(ctx context.Context, auth biz.Identity, id int64) error {
	return r.mapErr(r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		if _, err := s.Q().LockSysUserGroupByID(ctx, id); err != nil {
			return err
		}
		rows, err := s.Q().SoftDeleteSysUserGroup(ctx, gen.SoftDeleteSysUserGroupParams{
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

// ListMembers 分页返回组成员：先锁组行（不存在或跨租户等同不存在），
// 再在同一事务内取成员页与总数（List 与 Count 共用活跃联结，total 与
// items 视图一致）。
func (r *userGroupRepo) ListMembers(ctx context.Context, auth biz.Identity, groupID int64, page, pageSize int) (*biz.UserGroupMemberList, error) {
	var out *biz.UserGroupMemberList
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		if _, err := s.Q().LockSysUserGroupByID(ctx, groupID); err != nil {
			return err
		}
		offset := int64(page-1) * int64(pageSize)
		limit := int64(pageSize)
		rows, err := s.Q().ListSysUserGroupMembers(ctx, gen.ListSysUserGroupMembersParams{
			GroupID: groupID,
			Offset:  offset,
			Limit:   limit,
		})
		if err != nil {
			return err
		}
		total, err := s.Q().CountSysUserGroupMembers(ctx, groupID)
		if err != nil {
			return err
		}
		items := make([]*biz.UserGroupMember, 0, len(rows))
		for i := range rows {
			items = append(items, toBizUserGroupMember(&rows[i]))
		}
		out = &biz.UserGroupMemberList{Items: items, Total: total}
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// AddMembers 批量添加成员：先锁组行，再校验候选用户存在（命中数与请求数
// 不等即存在未知/跨租户/已删除用户，整批拒绝不写入），最后批量写入映射，
// 重复添加经主键冲突跳过；判定与写入同一事务，任一步失败整体回滚。
func (r *userGroupRepo) AddMembers(ctx context.Context, auth biz.Identity, groupID int64, userIDs []int64) (int64, error) {
	var added int64
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		if _, err := s.Q().LockSysUserGroupByID(ctx, groupID); err != nil {
			return err
		}
		found, err := s.Q().CountSysUserGroupMemberCandidates(ctx, userIDs)
		if err != nil {
			return err
		}
		if found != int64(len(userIDs)) {
			return biz.ErrUserGroupInvalidArgument
		}
		added, err = s.Q().AddSysUserGroupMembers(ctx, gen.AddSysUserGroupMembersParams{
			GroupID:   groupID,
			CreatedBy: auth.UserID,
			UserIds:   userIDs,
		})
		return err
	})
	if err != nil {
		return 0, r.mapErr(err)
	}
	return added, nil
}

// RemoveMembers 批量移除成员：先锁组行（不存在或跨租户等同不存在），
// 再物理删除映射行；移除非成员更新 0 行（幂等，不报错）。
func (r *userGroupRepo) RemoveMembers(ctx context.Context, auth biz.Identity, groupID int64, userIDs []int64) (int64, error) {
	var removed int64
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		if _, err := s.Q().LockSysUserGroupByID(ctx, groupID); err != nil {
			return err
		}
		n, err := s.Q().RemoveSysUserGroupMembers(ctx, gen.RemoveSysUserGroupMembersParams{
			GroupID: groupID,
			UserIds: userIDs,
		})
		removed = n
		return err
	})
	if err != nil {
		return 0, r.mapErr(err)
	}
	return removed, nil
}

// mapErr 将驱动与协议错误映射为用户组资源的领域错误：无行（含跨租户
// 等同不存在）→ NotFound；dynq 协议错误 → InvalidArgument；唯一约束
// 冲突 → AlreadyExists；其余原样透传。
func (r *userGroupRepo) mapErr(err error) error {
	return mapStorageError(err, biz.ErrUserGroupNotFound, biz.ErrUserGroupInvalidArgument, biz.ErrUserGroupAlreadyExists)
}
