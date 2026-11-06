package pg_sqlc

import (
	"context"

	"grape/internal/biz"
	"grape/internal/data/pg_sqlc/gen"

	"github.com/jackc/pgx/v5"
	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

// roleRepo 是 biz.RoleRepo 的 PostgreSQL 实现。角色是租户内资源：租户
// 边界由 IdentityTx 的 RLS 会话变量承担，行级可见性由表级 @scope 守卫
//（动态 List/Count，fn_visible_role_ids）与 fn_can_see_role（详情共用
// 判定）承担。数据范围明细（sys_role_id_scope）、分发部门
//（sys_role_allowed_dept）与角色-菜单映射（sys_role_menu）不单独立
// 资源，随本仓储的方法维护：写路径先锁角色行，差量在事务内读活跃行
// 集合后在应用层算出并逐行增删，角色行锁串行化同一角色的全部配置写。
type roleRepo struct {
	data *Data
}

// NewRoleRepo 构造角色仓储。
func NewRoleRepo(data *Data) biz.RoleRepo {
	return &roleRepo{data: data}
}

// toBizRole 将持久化角色转换为领域模型。
func toBizRole(po *gen.SysRole) *biz.Role {
	if po == nil {
		return nil
	}
	return &biz.Role{
		ID:          po.ID,
		Status:      int32(po.Status),
		Name:        po.Name,
		Code:        po.Code,
		RoleType:    int32(po.RoleType),
		DataScope:   int32(po.DataScope),
		ScopeDeptID: po.ScopeDeptID,
		Remark:      po.Remark,
		CreatedBy:   po.CreatedBy,
		UpdatedBy:   po.UpdatedBy,
		CreatedAt:   tsToTime(po.CreatedAt),
		UpdatedAt:   tsToTimePtr(po.UpdatedAt),
		DeletedAt:   tsToTimePtr(po.DeletedAt),
	}
}

// GetByID 按主键读取角色。租户边界由 RLS 承担，跨租户等同不存在；
// 行级可见性由 fn_can_see_role（A AND G）判定，不可见等同不存在，
// 一律返回 ErrRoleNotFound，不泄露存在性。
func (r *roleRepo) GetByID(ctx context.Context, auth biz.Identity, id int64) (*biz.Role, error) {
	var out *biz.Role
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().GetSysRoleVisibleByID(ctx, gen.GetSysRoleVisibleByIDParams{
			ID: id, Operator: auth.UserID,
		})
		if err != nil {
			return err
		}
		out = toBizRole(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// List 解析动态查询协议并返回分页信封；角色可见性由表级 @scope 守卫
//（fn_visible_role_ids，与 fn_can_see_role 共用 A AND G 口径）注入，
// 软删过滤由表级 @filter 声明在解析期作为服务端固定不变量注入（协议
// 不可见、调用方不可控），字段、操作符、投影与游标合法性由生成代码
// 校验，协议错误映射为 ErrRoleInvalidArgument。
func (r *roleRepo) List(ctx context.Context, auth biz.Identity, q *dynq.Query) (*biz.RoleList, error) {
	var out *biz.RoleList
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		params, err := gen.ParseSysRoleQuery(q)
		if err != nil {
			return err
		}
		params.Scope = s.SysRoleScope()
		res, err := gen.ListSysRole(ctx, s.Tx(), params)
		if err != nil {
			return err
		}
		list := &biz.RoleList{
			Items:      make([]*biz.Role, 0, len(res.Items)),
			HasMore:    res.HasMore,
			NextCursor: res.NextCursor,
		}
		for i := range res.Items {
			list.Items = append(list.Items, toBizRole(&res.Items[i]))
		}
		// total 与翻页位置无关，计数前剥离游标：游标会把行集收窄到
		// 某一页位置之后，导致各页 total 递减。
		if dynq.IncludeTotalOf(q.Pagination) {
			countParams := params
			countParams.After = nil
			total, err := gen.CountSysRole(ctx, s.Tx(), countParams)
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

// Create 创建角色：主键由应用层发号（newID），租户取自会话，审计列记
// 操作者；租户内未删除角色间 code/name 唯一冲突映射为
// ErrRoleAlreadyExists。
func (r *roleRepo) Create(ctx context.Context, auth biz.Identity, g *biz.Role) (*biz.Role, error) {
	var out *biz.Role
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().InsertSysRole(ctx, gen.InsertSysRoleParams{
			ID:          newID(),
			Status:      int16(g.Status),
			Name:        g.Name,
			Code:        g.Code,
			RoleType:    int16(g.RoleType),
			DataScope:   int16(g.DataScope),
			ScopeDeptID: g.ScopeDeptID,
			Remark:      g.Remark,
			CreatedBy:   auth.UserID,
			UpdatedBy:   auth.UserID,
		})
		if err != nil {
			return err
		}
		out = toBizRole(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// Update 覆盖可变字段（name、status、remark）。目标行先经 LockSysRoleByID
// 锁定：不存在或跨租户等同不存在（NotFound），锁持有到提交。
func (r *roleRepo) Update(ctx context.Context, auth biz.Identity, g *biz.Role) (*biz.Role, error) {
	var out *biz.Role
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		if _, err := s.Q().LockSysRoleByID(ctx, g.ID); err != nil {
			return err
		}
		updated, err := s.Q().UpdateSysRole(ctx, gen.UpdateSysRoleParams{
			Name:      g.Name,
			Status:    int16(g.Status),
			Remark:    g.Remark,
			UpdatedBy: auth.UserID,
			ID:        g.ID,
		})
		if err != nil {
			return err
		}
		out = toBizRole(&updated)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// SoftDelete 软删除角色。目标行先经 LockSysRoleByID 锁定；系统预置角色
// （role_type=1）不可删除，拒绝不落任何变更。明细/映射行物理残留，消费
// 侧的活跃过滤使其不产生可见副作用。
func (r *roleRepo) SoftDelete(ctx context.Context, auth biz.Identity, id int64) error {
	return r.mapErr(r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().LockSysRoleByID(ctx, id)
		if err != nil {
			return err
		}
		if row.RoleType == int16(biz.RoleTypePreset) {
			return biz.ErrRolePresetUndeletable
		}
		rows, err := s.Q().SoftDeleteSysRole(ctx, gen.SoftDeleteSysRoleParams{
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

// GetDataScope 读取角色的数据范围配置：角色读面按 fn_can_see_role 判定
// 可见（不可见或跨租户等同不存在），标量取自可见行，两张明细表取活跃
// 行集合，与判定函数的消费口径（仅活跃行参与推导）一致。
func (r *roleRepo) GetDataScope(ctx context.Context, auth biz.Identity, id int64) (*biz.RoleDataScope, error) {
	var out *biz.RoleDataScope
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().GetSysRoleVisibleByID(ctx, gen.GetSysRoleVisibleByIDParams{
			ID: id, Operator: auth.UserID,
		})
		if err != nil {
			return err
		}
		scopes, err := s.Q().ListSysRoleIDScopes(ctx, id)
		if err != nil {
			return err
		}
		depts, err := s.Q().ListSysRoleAllowedDepts(ctx, id)
		if err != nil {
			return err
		}
		cfg := &biz.RoleDataScope{
			RoleID:         id,
			DataScope:      int32(row.DataScope),
			ScopeDeptID:    row.ScopeDeptID,
			IDScopes:       make([]biz.RoleIDScopeEntry, 0, len(scopes)),
			AllowedDeptIDs: make([]int64, 0, len(depts)),
		}
		for _, sc := range scopes {
			cfg.IDScopes = append(cfg.IDScopes, biz.RoleIDScopeEntry{Type: int32(sc.Type), DataID: sc.DataID})
		}
		for _, d := range depts {
			cfg.AllowedDeptIDs = append(cfg.AllowedDeptIDs, d)
		}
		out = cfg
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// SetDataScope 整组替换角色的数据范围配置：先锁角色行，再校验明细与分发
// 部门引用的对象存在且启用（跨租户等同不存在），随后更新角色行标量并按
// 差量同步两张明细表（既有命中行保留原审计，新增记录配置人，移除物理
// 删除）。判定与写入同一事务，任一步失败整体回滚。
func (r *roleRepo) SetDataScope(ctx context.Context, auth biz.Identity, cfg *biz.RoleDataScope) (*biz.RoleDataScope, error) {
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		if _, err := s.Q().LockSysRoleByID(ctx, cfg.RoleID); err != nil {
			return err
		}
		if err := validateDataScopeRefs(ctx, s, cfg); err != nil {
			return err
		}
		if _, err := s.Q().UpdateSysRoleDataScope(ctx, gen.UpdateSysRoleDataScopeParams{
			DataScope:   int16(cfg.DataScope),
			ScopeDeptID: cfg.ScopeDeptID,
			UpdatedBy:   auth.UserID,
			ID:          cfg.RoleID,
		}); err != nil {
			return err
		}
		// 差量同步 ID 范围明细：事务内读活跃行集合，在应用层算出增删。
		current, err := s.Q().ListSysRoleIDScopes(ctx, cfg.RoleID)
		if err != nil {
			return err
		}
		existing := make(map[biz.RoleIDScopeEntry]struct{}, len(current))
		for _, sc := range current {
			existing[biz.RoleIDScopeEntry{Type: int32(sc.Type), DataID: sc.DataID}] = struct{}{}
		}
		for _, e := range cfg.IDScopes {
			if _, ok := existing[e]; ok {
				delete(existing, e)
				continue
			}
			if _, err := s.Q().AddSysRoleIDScope(ctx, gen.AddSysRoleIDScopeParams{
				ID: newID(), Type: int16(e.Type), RoleID: cfg.RoleID, DataID: e.DataID, CreatedBy: auth.UserID,
			}); err != nil {
				return err
			}
		}
		for e := range existing {
			if _, err := s.Q().DeleteSysRoleIDScope(ctx, gen.DeleteSysRoleIDScopeParams{
				RoleID: cfg.RoleID, Type: int16(e.Type), DataID: e.DataID,
			}); err != nil {
				return err
			}
		}
		// 差量同步分发部门映射。
		currentDepts, err := s.Q().ListSysRoleAllowedDepts(ctx, cfg.RoleID)
		if err != nil {
			return err
		}
		existingDepts := make(map[int64]struct{}, len(currentDepts))
		for _, d := range currentDepts {
			existingDepts[d] = struct{}{}
		}
		for _, deptID := range cfg.AllowedDeptIDs {
			if _, ok := existingDepts[deptID]; ok {
				delete(existingDepts, deptID)
				continue
			}
			if _, err := s.Q().AddSysRoleAllowedDept(ctx, gen.AddSysRoleAllowedDeptParams{
				ID: newID(), RoleID: cfg.RoleID, DeptID: deptID, CreatedBy: auth.UserID,
			}); err != nil {
				return err
			}
		}
		for deptID := range existingDepts {
			if _, err := s.Q().DeleteSysRoleAllowedDept(ctx, gen.DeleteSysRoleAllowedDeptParams{
				RoleID: cfg.RoleID, DeptID: deptID,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return cfg, nil
}

// validateDataScopeRefs 校验数据范围配置引用的对象全部存在且启用：明细
// 按类型分组计数，分发部门与 scope_dept_id 并入部门引用一并校验（跨来源
// 重复引用去重后计数；命中数与请求数不等即存在未知/跨租户/停用/已删除
// 对象，整组拒绝）。
func validateDataScopeRefs(ctx context.Context, s *IdentityTx, cfg *biz.RoleDataScope) error {
	users := map[int64]struct{}{}
	depts := map[int64]struct{}{}
	groups := map[int64]struct{}{}
	for _, e := range cfg.IDScopes {
		switch e.Type {
		case biz.RoleIDScopeTypeUser:
			users[e.DataID] = struct{}{}
		case biz.RoleIDScopeTypeDept:
			depts[e.DataID] = struct{}{}
		case biz.RoleIDScopeTypeGroup:
			groups[e.DataID] = struct{}{}
		}
	}
	for _, deptID := range cfg.AllowedDeptIDs {
		depts[deptID] = struct{}{}
	}
	if cfg.ScopeDeptID != nil {
		depts[*cfg.ScopeDeptID] = struct{}{}
	}
	if len(users) > 0 {
		found, err := s.Q().CountSysRoleScopeUserRefs(ctx, keysOf(users))
		if err != nil {
			return err
		}
		if found != int64(len(users)) {
			return biz.ErrRoleInvalidArgument
		}
	}
	if len(depts) > 0 {
		found, err := s.Q().CountSysRoleScopeDeptRefs(ctx, keysOf(depts))
		if err != nil {
			return err
		}
		if found != int64(len(depts)) {
			return biz.ErrRoleInvalidArgument
		}
	}
	if len(groups) > 0 {
		found, err := s.Q().CountSysRoleScopeGroupRefs(ctx, keysOf(groups))
		if err != nil {
			return err
		}
		if found != int64(len(groups)) {
			return biz.ErrRoleInvalidArgument
		}
	}
	return nil
}

// keysOf 提取集合的键（存在性计数的去重入参）。
func keysOf(set map[int64]struct{}) []int64 {
	out := make([]int64, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	return out
}

// ListMenus 返回角色当前的有效授权菜单 ID 集合（停用/软删菜单的残留映射
// 不出现在回显集合，与 fn_has_permission 的活跃口径一致）。角色读面按
// fn_can_see_role 判定可见（不存在或跨租户等同不存在）。
func (r *roleRepo) ListMenus(ctx context.Context, auth biz.Identity, id int64) ([]int64, error) {
	var out []int64
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		if _, err := s.Q().GetSysRoleVisibleByID(ctx, gen.GetSysRoleVisibleByIDParams{
			ID: id, Operator: auth.UserID,
		}); err != nil {
			return err
		}
		rows, err := s.Q().ListSysRoleMenus(ctx, id)
		if err != nil {
			return err
		}
		out = make([]int64, 0, len(rows))
		for _, m := range rows {
			out = append(out, m)
		}
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// SetMenus 整组替换角色的菜单授权：先锁角色行并校验菜单引用存在且启用，
// 再以映射物理行为对账基线差量同步（不在集合内的行物理删除——含失活
// 菜单的残留授权，补建缺失并记录授权人，既有有效授权行保留原授权人/
// 授权时间），返回设置后的有效授权集合。
func (r *roleRepo) SetMenus(ctx context.Context, auth biz.Identity, id int64, menuIDs []int64) ([]int64, error) {
	var out []int64
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		if _, err := s.Q().LockSysRoleByID(ctx, id); err != nil {
			return err
		}
		if len(menuIDs) > 0 {
			found, err := s.Q().CountSysRoleMenuRefs(ctx, menuIDs)
			if err != nil {
				return err
			}
			if found != int64(len(menuIDs)) {
				return biz.ErrRoleInvalidArgument
			}
		}
		// 差量以映射物理行为基线：失活菜单的残留授权不在任何有效回显
		// 集合内，会随不在新集合内的行一并物理清除。
		current, err := s.Q().ListSysRoleMenuGrantsAll(ctx, id)
		if err != nil {
			return err
		}
		existing := make(map[int64]struct{}, len(current))
		for _, m := range current {
			existing[m] = struct{}{}
		}
		for _, menuID := range menuIDs {
			if _, ok := existing[menuID]; ok {
				delete(existing, menuID)
				continue
			}
			if _, err := s.Q().AddSysRoleMenu(ctx, gen.AddSysRoleMenuParams{
				RoleID: id, MenuID: menuID, CreatedBy: auth.UserID,
			}); err != nil {
				return err
			}
		}
		for menuID := range existing {
			if _, err := s.Q().DeleteSysRoleMenu(ctx, gen.DeleteSysRoleMenuParams{
				RoleID: id, MenuID: menuID,
			}); err != nil {
				return err
			}
		}
		rows, err := s.Q().ListSysRoleMenus(ctx, id)
		if err != nil {
			return err
		}
		out = make([]int64, 0, len(rows))
		for _, m := range rows {
			out = append(out, m)
		}
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// mapErr 将驱动与协议错误映射为角色资源的领域错误：无行（含跨租户等同
// 不存在）→ NotFound；dynq 协议错误 → InvalidArgument；唯一约束冲突 →
// AlreadyExists；biz 类型化错误与其余错误原样透传。
func (r *roleRepo) mapErr(err error) error {
	return mapStorageError(err, biz.ErrRoleNotFound, biz.ErrRoleInvalidArgument, biz.ErrRoleAlreadyExists)
}
