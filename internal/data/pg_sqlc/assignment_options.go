package pg_sqlc

import (
	"context"
	"slices"

	"grape/internal/biz"
	"grape/internal/data/pg_sqlc/gen"
)

// assignmentRepo 是 biz.AssignmentRepo 的 PostgreSQL 实现：用户分配选择器
// 候选与「管理范围内整组替换」的角色写入。全部操作经 IdentityTx 在租户
// 事务内执行（RLS 承担租户边界）；写路径先经 LockSysUserPermitted 锁定
// 目标行，判定、版本校验与写入同一事务完成。
type assignmentRepo struct {
	data *Data
}

// NewAssignmentOptionRepo 构造用户分配仓储。
func NewAssignmentOptionRepo(data *Data) biz.AssignmentRepo {
	return &assignmentRepo{data: data}
}

// ListAssignableRoles 列出目标用户可分配且尚未拥有的活跃角色：目标用户
// 不可见（GetSysUserPermitted 无行）等同不存在；候选范围由库内
// fn_can_assign_role 逐角色判定，分页以 limit+1 判定 has_more。
func (r *assignmentRepo) ListAssignableRoles(ctx context.Context, auth biz.Identity, targetUserID int64, q biz.OptionQuery) (*biz.OptionSet, error) {
	var out *biz.OptionSet
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		if _, err := s.Q().GetSysUserPermitted(ctx, gen.GetSysUserPermittedParams{
			ID: targetUserID, Operator: auth.UserID, PermissionCode: nil,
		}); err != nil {
			return err
		}
		limit, offset := optionLimitOffset(q)
		rows, err := s.Q().ListAssignableRoleOptions(ctx, gen.ListAssignableRoleOptionsParams{
			Operator: auth.UserID, Target: targetUserID, Keyword: q.Keyword,
			Limit: limit, Offset: offset,
		})
		if err != nil {
			return err
		}
		items := make([]*biz.Option, 0, len(rows))
		for _, row := range rows {
			items = append(items, &biz.Option{Value: row.ID, Label: row.Name, Enabled: true})
		}
		out = optionSetOf(items, q)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// ListUserRoles 读取目标用户当前可管理角色集合与版本：currentAll 是全部
// 映射（含停用/软删角色的残留映射），activeCurrent 是其中活跃的角色；
// 按 fn_can_assign_role 分区后 manageable 返回明细、locked 只计数。
func (r *assignmentRepo) ListUserRoles(ctx context.Context, auth biz.Identity, userID int64) (*biz.UserRoleSet, error) {
	var out *biz.UserRoleSet
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		if _, err := s.Q().GetSysUserPermitted(ctx, gen.GetSysUserPermittedParams{
			ID: userID, Operator: auth.UserID, PermissionCode: nil,
		}); err != nil {
			return err
		}
		set, err := readUserRoleSet(ctx, s, auth.UserID, userID)
		if err != nil {
			return err
		}
		out = set
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// SetUserRoles 按「管理范围内整组替换」写入角色集合，全部步骤在同一事务：
//
//  1. LockSysUserPermitted 锁定目标（不可见等同不存在）；
//  2. 读全部映射与活跃角色，按 fn_can_assign_role 分区出 manageable/locked；
//  3. 请求触及 locked、desired 有失活角色、新增角色任一不可分配 → 拒绝；
//  4. expected_version 与当前 manageable 集合不一致 → Conflict；
//  5. 操作目标为本人时，最终集合必须至少保留一个带分配权限码的有效角色；
//  6. 先删 removed（SQL 内再带 fn_can_assign_role 且要求行数相等）、
//     再补 added，最后以最终原集合重算 version 返回。
func (r *assignmentRepo) SetUserRoles(ctx context.Context, auth biz.Identity, in biz.SetUserRolesInput) (*biz.UserRoleSet, error) {
	var out *biz.UserRoleSet
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		if _, err := s.Q().LockSysUserPermitted(ctx, gen.LockSysUserPermittedParams{
			ID: in.UserID, Operator: auth.UserID, PermissionCode: nil,
		}); err != nil {
			return err
		}

		currentAll, err := s.Q().ListUserRoleIDs(ctx, in.UserID)
		if err != nil {
			return err
		}
		active, err := s.Q().ListActiveRoleOptionsByIDs(ctx, currentAll)
		if err != nil {
			return err
		}
		labels := make(map[int64]string, len(active)+len(in.RoleIDs))
		activeIDs := make([]int64, 0, len(active))
		for _, row := range active {
			activeIDs = append(activeIDs, row.ID)
			labels[row.ID] = row.Name
		}
		manageable, err := assignableSet(ctx, s, auth.UserID, activeIDs, in.UserID)
		if err != nil {
			return err
		}
		manageableIDs := make([]int64, 0, len(manageable))
		lockedIDs := make([]int64, 0, len(currentAll))
		for _, id := range activeIDs {
			if manageable[id] {
				manageableIDs = append(manageableIDs, id)
			}
		}
		for _, id := range currentAll {
			if !manageable[id] {
				lockedIDs = append(lockedIDs, id)
			}
		}
		// role_ids 只表达可管理范围：触及 locked（范围外/停用/软删角色的
		// 既有映射）即拒绝，绝不误删调用方看不见的分配。
		for _, id := range in.RoleIDs {
			if slices.Contains(lockedIDs, id) {
				return biz.ErrUserAccessDenied
			}
		}

		// desired 必须全部是活跃角色；命中数不等即存在未知/停用/已删除。
		desiredActive, err := s.Q().ListActiveRoleOptionsByIDs(ctx, in.RoleIDs)
		if err != nil {
			return err
		}
		if len(desiredActive) != len(in.RoleIDs) {
			return biz.ErrUserAccessDenied
		}
		for _, row := range desiredActive {
			labels[row.ID] = row.Name
		}

		added := difference(in.RoleIDs, currentAll)
		if len(added) > 0 {
			ok, err := assignableSet(ctx, s, auth.UserID, added, in.UserID)
			if err != nil {
				return err
			}
			for _, id := range added {
				if !ok[id] {
					return biz.ErrUserAccessDenied
				}
			}
		}

		desiredSet := toIDSet(in.RoleIDs)
		removed := make([]int64, 0, len(manageableIDs))
		for _, id := range manageableIDs {
			if !desiredSet[id] {
				removed = append(removed, id)
			}
		}

		// 版本以可管理集合（升序）为口径；ListUserRoles 与这里共用同一 helper。
		if biz.UserRoleSetVersion(in.UserID, manageableIDs) != in.ExpectedVersion {
			return biz.ErrUserRoleSetChanged
		}

		// 自锁保护：操作目标为本人时，locked 与 desired 的并集必须仍能授予
		// 分配角色权限，防止把自己锁在管理面之外。
		if in.UserID == auth.UserID {
			final := make([]int64, 0, len(lockedIDs)+len(in.RoleIDs))
			final = append(final, lockedIDs...)
			final = append(final, in.RoleIDs...)
			n, err := s.Q().CountRolesGrantingPermission(ctx, gen.CountRolesGrantingPermissionParams{
				RoleIds: final, PermissionCode: biz.PermUserAssignRole,
			})
			if err != nil {
				return err
			}
			if n == 0 {
				return biz.ErrUserAccessDenied
			}
		}

		if len(removed) > 0 {
			affected, err := s.Q().DeleteUserRoles(ctx, gen.DeleteUserRolesParams{
				UserID: in.UserID, RoleIds: removed, Operator: auth.UserID,
			})
			if err != nil {
				return err
			}
			// SQL 内已带 fn_can_assign_role：行数不齐说明并发改动或判定不通过，
			// 整批回滚。
			if affected != int64(len(removed)) {
				return biz.ErrUserAccessDenied
			}
		}
		if len(added) > 0 {
			if _, err := s.Q().AssignUserRoles(ctx, gen.AssignUserRolesParams{
				UserID: in.UserID, RoleIds: added, CreatedBy: auth.UserID,
			}); err != nil {
				return err
			}
		}

		finalIDs := make([]int64, 0, len(manageableIDs)+len(added))
		for _, id := range manageableIDs {
			if !desiredSet[id] {
				continue
			}
			finalIDs = append(finalIDs, id)
		}
		finalIDs = append(finalIDs, added...)
		out = roleSetOf(in.UserID, finalIDs, labels, int64(len(lockedIDs)))
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// ListOwnerUserOptions 列出归属用户候选。
func (r *assignmentRepo) ListOwnerUserOptions(ctx context.Context, auth biz.Identity, q biz.OptionQuery) (*biz.OptionSet, error) {
	var out *biz.OptionSet
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		limit, offset := optionLimitOffset(q)
		rows, err := s.Q().ListOwnerUserOptions(ctx, gen.ListOwnerUserOptionsParams{
			Operator: auth.UserID, Keyword: q.Keyword, Limit: limit, Offset: offset,
		})
		if err != nil {
			return err
		}
		items := make([]*biz.Option, 0, len(rows))
		for _, row := range rows {
			items = append(items, &biz.Option{Value: row.ID, Label: row.Label, Enabled: true})
		}
		out = optionSetOf(items, q)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// ListOwnerDeptOptions 列出归属部门候选。
func (r *assignmentRepo) ListOwnerDeptOptions(ctx context.Context, auth biz.Identity, q biz.OptionQuery) (*biz.OptionSet, error) {
	var out *biz.OptionSet
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		limit, offset := optionLimitOffset(q)
		rows, err := s.Q().ListOwnerDeptOptions(ctx, gen.ListOwnerDeptOptionsParams{
			Operator: auth.UserID, Keyword: q.Keyword, Limit: limit, Offset: offset,
		})
		if err != nil {
			return err
		}
		items := make([]*biz.Option, 0, len(rows))
		for _, row := range rows {
			items = append(items, &biz.Option{Value: row.ID, Label: row.Label, Enabled: true})
		}
		out = optionSetOf(items, q)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// ListOwnerGroupOptions 列出归属用户组候选。
func (r *assignmentRepo) ListOwnerGroupOptions(ctx context.Context, auth biz.Identity, q biz.OptionQuery) (*biz.OptionSet, error) {
	var out *biz.OptionSet
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		limit, offset := optionLimitOffset(q)
		rows, err := s.Q().ListOwnerGroupOptions(ctx, gen.ListOwnerGroupOptionsParams{
			Operator: auth.UserID, Keyword: q.Keyword, Limit: limit, Offset: offset,
		})
		if err != nil {
			return err
		}
		items := make([]*biz.Option, 0, len(rows))
		for _, row := range rows {
			items = append(items, &biz.Option{Value: row.ID, Label: row.Label, Enabled: true})
		}
		out = optionSetOf(items, q)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// readUserRoleSet 在同一事务内读取目标用户的当前角色集合：全部映射 →
// 活跃角色 → 按 fn_can_assign_role 分区；返回可管理明细（assigned=true）、
// locked 计数与版本。调用方须已完成目标可见性校验。
func readUserRoleSet(ctx context.Context, s *IdentityTx, operator, userID int64) (*biz.UserRoleSet, error) {
	currentAll, err := s.Q().ListUserRoleIDs(ctx, userID)
	if err != nil {
		return nil, err
	}
	active, err := s.Q().ListActiveRoleOptionsByIDs(ctx, currentAll)
	if err != nil {
		return nil, err
	}
	labels := make(map[int64]string, len(active))
	activeIDs := make([]int64, 0, len(active))
	for _, row := range active {
		activeIDs = append(activeIDs, row.ID)
		labels[row.ID] = row.Name
	}
	ok, err := assignableSet(ctx, s, operator, activeIDs, userID)
	if err != nil {
		return nil, err
	}
	manageableIDs := make([]int64, 0, len(activeIDs))
	locked := 0
	for _, id := range activeIDs {
		if ok[id] {
			manageableIDs = append(manageableIDs, id)
		}
	}
	for _, id := range currentAll {
		if !ok[id] {
			locked++
		}
	}
	return roleSetOf(userID, manageableIDs, labels, int64(locked)), nil
}

// assignableSet 调 fn_can_assign_roles 批量判定角色集合，返回可分配的
// role_id 集合。
func assignableSet(ctx context.Context, s *IdentityTx, operator int64, roleIDs []int64, target int64) (map[int64]bool, error) {
	out := make(map[int64]bool, len(roleIDs))
	if len(roleIDs) == 0 {
		return out, nil
	}
	verdicts, err := s.Q().CanAssignRoles(ctx, gen.CanAssignRolesParams{
		Operator: operator, RoleIds: roleIDs, Target: target,
	})
	if err != nil {
		return nil, err
	}
	for _, v := range verdicts {
		if v.Ok {
			out[v.RoleID] = true
		}
	}
	return out, nil
}

// roleSetOf 把角色 ID 集合渲染为 UserRoleSet（assigned=true），条目按
// 名称、ID 稳定排序；version 以 ID 升序集合经 biz 的单一 helper 计算。
func roleSetOf(userID int64, ids []int64, labels map[int64]string, unmanaged int64) *biz.UserRoleSet {
	sorted := slices.Clone(ids)
	slices.SortFunc(sorted, func(a, b int64) int {
		if labels[a] != labels[b] {
			if labels[a] < labels[b] {
				return -1
			}
			return 1
		}
		switch {
		case a < b:
			return -1
		case a > b:
			return 1
		default:
			return 0
		}
	})
	items := make([]*biz.Option, 0, len(sorted))
	for _, id := range sorted {
		items = append(items, &biz.Option{Value: id, Label: labels[id], Enabled: true, Assigned: true})
	}
	return &biz.UserRoleSet{Items: items, UnmanagedCount: unmanaged, Version: biz.UserRoleSetVersion(userID, ids)}
}

// optionLimitOffset 把归一化后的选项查询转成 limit+1 / offset：多取一行
// 供调用方判定 has_more，total 不计算。
func optionLimitOffset(q biz.OptionQuery) (limit, offset int32) {
	return q.PageSize + 1, (q.Page - 1) * q.PageSize
}

// optionSetOf 裁掉多取的一行并渲染分页信封。
func optionSetOf(items []*biz.Option, q biz.OptionQuery) *biz.OptionSet {
	hasMore := int32(len(items)) > q.PageSize
	if hasMore {
		items = items[:q.PageSize]
	}
	return &biz.OptionSet{Items: items, HasMore: hasMore, Page: q.Page}
}

// difference 返回 a 中不在 b 里的元素（去重由调用方保证）。
func difference(a, b []int64) []int64 {
	inB := make(map[int64]struct{}, len(b))
	for _, id := range b {
		inB[id] = struct{}{}
	}
	out := make([]int64, 0, len(a))
	for _, id := range a {
		if _, ok := inB[id]; !ok {
			out = append(out, id)
		}
	}
	return out
}

// toIDSet 把 ID 列表转为集合。
func toIDSet(ids []int64) map[int64]bool {
	out := make(map[int64]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out
}

// mapErr 将驱动错误映射为分配面（用户资源）的领域错误：无行（含不可见
// 等同不存在）→ NotFound；协议/约束错误 → InvalidArgument；判定拒绝等
// 业务错误原样透传。
func (r *assignmentRepo) mapErr(err error) error {
	return mapStorageError(err, biz.ErrUserNotFound, biz.ErrUserInvalidArgument, biz.ErrUserAlreadyExists)
}
