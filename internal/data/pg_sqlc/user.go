package pg_sqlc

import (
	"context"
	"time"

	"grape/internal/biz"
	"grape/internal/data/pg_sqlc/gen"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

// userRepo 是 biz.UserRepo 的 PostgreSQL 实现。全部操作经会话门面在
// 租户事务内执行；写路径按「锁行 → 库内判定 → 写入」先检后写，判定
// 与写入同一事务，判定不通过整体回滚。
type userRepo struct {
	data *Data
}

// NewUserRepo 构造用户仓储。
func NewUserRepo(data *Data) biz.UserRepo {
	return &userRepo{data: data}
}

// scopeDimsAll 是 fn_check_visible 的全维度掩码：1 归属人、2 归属部门、
// 4 归属用户组。读写同权下等价于 fn_can_operate_row 的判定口径。
const scopeDimsAll int16 = 7

// toBizUser 将持久化用户转换为领域模型。密码哈希不随行：biz.User 的
// Password 仅在创建与改密路径由用例填充。
func toBizUser(po *gen.SysUser) *biz.User {
	if po == nil {
		return nil
	}
	return &biz.User{
		ID:               po.ID,
		Status:           int32(po.Status),
		Name:             po.Name,
		Nickname:         po.Nickname,
		Email:            po.Email,
		Phone:            po.Phone,
		Remark:           po.Remark,
		Locale:           po.Locale,
		OwnerUserID:      po.OwnerUserID,
		OwnerDeptID:      po.OwnerDeptID,
		OwnerUserGroupID: po.OwnerUserGroupID,
		CreatedBy:        po.CreatedBy,
		UpdatedBy:        po.UpdatedBy,
		CreatedAt:        tsToTime(po.CreatedAt),
		UpdatedAt:        tsToTimePtr(po.UpdatedAt),
		DeletedAt:        tsToTimePtr(po.DeletedAt),
	}
}

// GetByID 按主键读取用户。租户内存在但数据范围不可见的行等同不存在，
// 一律返回 ErrUserNotFound，不泄露存在性。
func (r *userRepo) GetByID(ctx context.Context, auth biz.Identity, id int64) (*biz.User, error) {
	var out *biz.User
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().GetSysUserByID(ctx, id)
		if err != nil {
			return err
		}
		visible, err := s.Q().CheckVisible(ctx, gen.CheckVisibleParams{
			Operator:   auth.UserID,
			Dims:       scopeDimsAll,
			OwnerUser:  row.OwnerUserID,
			OwnerDept:  row.OwnerDeptID,
			OwnerGroup: row.OwnerUserGroupID,
		})
		if err != nil {
			return err
		}
		if !visible {
			return pgx.ErrNoRows
		}
		out = toBizUser(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// List 解析动态查询协议并返回分页信封；@scope 守卫由会话注入，
// 软删过滤由表级 @filter 声明在解析期作为服务端固定不变量注入
// （协议不可见、调用方不可控），字段、操作符、投影与游标合法性由
// 生成代码校验，协议错误映射为 ErrUserInvalidArgument。
func (r *userRepo) List(ctx context.Context, auth biz.Identity, q *dynq.Query) (*biz.UserList, error) {
	var out *biz.UserList
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		params, err := gen.ParseSysUserQuery(q)
		if err != nil {
			return err
		}
		params.Scope = s.SysUserScope()
		res, err := gen.ListSysUser(ctx, s.Tx(), params)
		if err != nil {
			return err
		}
		list := &biz.UserList{
			Items:      make([]*biz.User, 0, len(res.Items)),
			HasMore:    res.HasMore,
			NextCursor: res.NextCursor,
		}
		for i := range res.Items {
			list.Items = append(list.Items, toBizUser(&res.Items[i]))
		}
		// total 与翻页位置无关，计数前剥离游标：游标会把行集收窄到
		// 某一页位置之后，导致各页 total 递减。
		if dynq.IncludeTotalOf(q.Pagination) {
			countParams := params
			countParams.After = nil
			total, err := gen.CountSysUser(ctx, s.Tx(), countParams)
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

// Create 创建用户：主键由应用层发号（newID，发号能力目录落地前过渡），
// 租户取自会话，审计列记操作者；租户内 name/email/phone 唯一冲突映射
// 为 ErrUserAlreadyExists。
func (r *userRepo) Create(ctx context.Context, auth biz.Identity, u *biz.User) (*biz.User, error) {
	var out *biz.User
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().InsertSysUser(ctx, gen.InsertSysUserParams{
			ID:               newID(),
			Status:           int16(u.Status),
			Name:             u.Name,
			Nickname:         u.Nickname,
			Password:         u.Password,
			Email:            u.Email,
			Phone:            u.Phone,
			Remark:           u.Remark,
			Locale:           u.Locale,
			OwnerUserID:      u.OwnerUserID,
			OwnerDeptID:      u.OwnerDeptID,
			OwnerUserGroupID: u.OwnerUserGroupID,
			CreatedBy:        auth.UserID,
			UpdatedBy:        auth.UserID,
		})
		if err != nil {
			return err
		}
		out = toBizUser(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// Update 覆盖可变字段。目标行先锁行并经 fn_can_operate_row 判定：
// 不可见等同不存在（读写同权下可见即可操作）。
func (r *userRepo) Update(ctx context.Context, auth biz.Identity, u *biz.User) (*biz.User, error) {
	var out *biz.User
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().GetSysUserForUpdate(ctx, u.ID)
		if err != nil {
			return err
		}
		if err := requireOperable(ctx, s, auth.UserID, &row); err != nil {
			return err
		}
		updated, err := s.Q().UpdateSysUser(ctx, gen.UpdateSysUserParams{
			Status:    int16(u.Status),
			Name:      u.Name,
			Nickname:  u.Nickname,
			Email:     u.Email,
			Phone:     u.Phone,
			Remark:    u.Remark,
			Locale:    u.Locale,
			UpdatedBy: auth.UserID,
			ID:        u.ID,
		})
		if err != nil {
			return err
		}
		out = toBizUser(&updated)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// SetPassword 替换密码哈希，先经行操作判定。
func (r *userRepo) SetPassword(ctx context.Context, auth biz.Identity, id int64, hashedPassword string) error {
	return r.mapErr(r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().GetSysUserForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if err := requireOperable(ctx, s, auth.UserID, &row); err != nil {
			return err
		}
		rows, err := s.Q().SetSysUserPassword(ctx, gen.SetSysUserPasswordParams{
			Password: hashedPassword, UpdatedBy: auth.UserID, ID: id,
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

// SoftDelete 软删除用户，先经行操作判定。
func (r *userRepo) SoftDelete(ctx context.Context, auth biz.Identity, id int64) error {
	return r.mapErr(r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().GetSysUserForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if err := requireOperable(ctx, s, auth.UserID, &row); err != nil {
			return err
		}
		rows, err := s.Q().SoftDeleteSysUser(ctx, gen.SoftDeleteSysUserParams{
			UpdatedBy: auth.UserID, ID: id,
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

// AssignRoles 批量分配角色：fn_can_assign_role 逐角色判定，任一不通过
// 整体拒绝（不写入任何映射）；目标/角色/操作者无效同样表现为拒绝。
func (r *userRepo) AssignRoles(ctx context.Context, auth biz.Identity, userID int64, roleIDs []int64) (int64, error) {
	var added int64
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		rows, err := s.Q().CanAssignRoles(ctx, gen.CanAssignRolesParams{
			Operator: auth.UserID, RoleIds: roleIDs, Target: userID,
		})
		if err != nil {
			return err
		}
		for _, row := range rows {
			if !row.Ok {
				return biz.ErrUserAccessDenied
			}
		}
		added, err = s.Q().AssignUserRoles(ctx, gen.AssignUserRolesParams{
			UserID: userID, RoleIds: roleIDs, CreatedBy: auth.UserID,
		})
		return err
	})
	if err != nil {
		return 0, r.mapErr(err)
	}
	return added, nil
}

// TransferOwner 修改归属三元组：两头校验——该用户本身有权操作（不可见
// 等同不存在），新归属的人/部门/组在可见范围内（不可达即拒绝）。
func (r *userRepo) TransferOwner(ctx context.Context, auth biz.Identity, id int64, to biz.OwnerTriple) (*biz.User, error) {
	var out *biz.User
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().GetSysUserForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if err := requireOperable(ctx, s, auth.UserID, &row); err != nil {
			return err
		}
		ok, err := s.Q().CanTransferOwner(ctx, gen.CanTransferOwnerParams{
			Operator: auth.UserID,
			OldUser:  row.OwnerUserID, OldDept: row.OwnerDeptID, OldGroup: row.OwnerUserGroupID,
			NewUser: to.UserID, NewDept: to.DeptID, NewGroup: to.GroupID,
		})
		if err != nil {
			return err
		}
		if !ok {
			return biz.ErrUserAccessDenied
		}
		if err := s.Q().UpdateSysUserOwner(ctx, gen.UpdateSysUserOwnerParams{
			OwnerUserID:      to.UserID,
			OwnerDeptID:      to.DeptID,
			OwnerUserGroupID: to.GroupID,
			UpdatedBy:        auth.UserID,
			ID:               id,
		}); err != nil {
			return err
		}
		updated, err := s.Q().GetSysUserByID(ctx, id)
		if err != nil {
			return err
		}
		out = toBizUser(&updated)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// requireOperable 对已锁定的行执行行操作判定：判定不通过说明该行在
// 当前用户的数据范围之外，等同不存在（读写同权：可见即可操作）。
func requireOperable(ctx context.Context, s *IdentityTx, operator int64, row *gen.SysUser) error {
	ok, err := s.Q().CanOperateRow(ctx, gen.CanOperateRowParams{
		Operator:   operator,
		OwnerUser:  row.OwnerUserID,
		OwnerDept:  row.OwnerDeptID,
		OwnerGroup: row.OwnerUserGroupID,
	})
	if err != nil {
		return err
	}
	if !ok {
		return pgx.ErrNoRows
	}
	return nil
}

// pgAuth 将 biz 层身份事实映射为存储层会话门面入参。
func pgAuth(auth biz.Identity) Identity {
	return Identity{TenantID: auth.TenantID, UserID: auth.UserID}
}

// tsToTime 渲染 NOT NULL 的 timestamp；NULL 以零值代替。
func tsToTime(t pgtype.Timestamp) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time
}

// tsToTimePtr 渲染可空 timestamp；NULL 返回 nil。
func tsToTimePtr(t pgtype.Timestamp) *time.Time {
	if !t.Valid {
		return nil
	}
	tt := t.Time
	return &tt
}

// mapErr 将驱动与协议错误映射为用户资源的领域错误：无行（含不可见
// 等同不存在）→ NotFound；dynq 协议错误 → InvalidArgument；唯一约束
// 冲突 → AlreadyExists；判定拒绝等业务错误原样透传。
func (r *userRepo) mapErr(err error) error {
	return mapStorageError(err, biz.ErrUserNotFound, biz.ErrUserInvalidArgument, biz.ErrUserAlreadyExists)
}

// GetCredentialByName 登录路径专用：按租户内用户名读取口令哈希与状态。
// 经 TenantTx 执行（操作者身份尚未成立，只注入租户）；软删除行
// 等同不存在。哈希仅供 biz 口令校验单点使用。
func (r *userRepo) GetCredentialByName(ctx context.Context, tenantID, name string) (*biz.UserCredential, error) {
	var out *biz.UserCredential
	err := r.data.TenantTx(ctx, tenantID, func(s *TenantTx) error {
		row, err := s.Q().GetUserCredentialByName(ctx, name)
		if err != nil {
			return err
		}
		out = &biz.UserCredential{
			UserID:       row.ID,
			Status:       int32(row.Status),
			Name:         row.Name,
			Nickname:     row.Nickname,
			PasswordHash: row.PasswordHash,
		}
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}
