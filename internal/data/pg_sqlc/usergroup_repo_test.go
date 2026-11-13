package pg_sqlc

import (
	"context"
	"errors"
	"testing"

	"grape/internal/biz"

	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

// 用户组仓储集成测试（真实 PostgreSQL，未设 GRAPE_TEST_* DSN 时跳过）：覆盖
// sys_user_group 的 CRUD、动态查询、成员映射增删语义与 RLS 租户边界。
// setupTestDB 每次重建数据库；种子用户组属 t-test-01。

const (
	// groupTenantSeeded 是种子用户组所在的租户。
	groupTenantSeeded = "t-test-01"
	// groupTenantEmpty 是无用户组种子的租户，供新建组的用例使用。
	groupTenantEmpty = "t-test-02"
)

// groupAuth 返回指定租户的种子操作者身份（tenantOperators 定义见
// role_repo_test.go）；组长归属校验（fn_can_set_owner）要求有效身份。
func groupAuth(tenant string) biz.Identity {
	return biz.Identity{TenantID: tenant, UserID: tenantOperators[tenant]}
}

// mustCreateUserGroup 在指定租户内创建用户组，失败即终止用例。
func mustCreateUserGroup(t *testing.T, d *Data, auth biz.Identity, g *biz.UserGroup) *biz.UserGroup {
	t.Helper()
	out, err := NewUserGroupRepo(d).Create(context.Background(), auth, g)
	if err != nil {
		t.Fatalf("create user group %s: %v", g.Name, err)
	}
	return out
}

// mustAddMembers 向指定组批量添加成员，失败即终止用例。
func mustAddMembers(t *testing.T, d *Data, auth biz.Identity, groupID int64, userIDs []int64) int64 {
	t.Helper()
	added, err := NewUserGroupRepo(d).AddMembers(context.Background(), auth, groupID, userIDs)
	if err != nil {
		t.Fatalf("add members to group %d: %v", groupID, err)
	}
	return added
}

// mappingUserIDs 读取指定组的成员映射行（物理行，含软删用户），按 user_id
// 升序返回。
func mappingUserIDs(t *testing.T, d *Data, auth biz.Identity, groupID int64) []int64 {
	t.Helper()
	var ids []int64
	err := d.IdentityTx(context.Background(), pgAuth(auth), func(s *IdentityTx) error {
		rows, err := s.Tx().Query(context.Background(),
			`SELECT user_id FROM public.sys_user_group_mapping WHERE group_id = $1 ORDER BY user_id`, groupID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatalf("read mapping rows: %v", err)
	}
	return ids
}

// softDeleteUserRow 置位指定用户的 deleted_at（写侧仓储属用户域，这里用
// 原生 SQL 直接构造软删事实）。
func softDeleteUserRow(t *testing.T, d *Data, auth biz.Identity, id int64) {
	t.Helper()
	err := d.IdentityTx(context.Background(), pgAuth(auth), func(s *IdentityTx) error {
		_, err := s.Tx().Exec(context.Background(),
			`UPDATE public.sys_user SET deleted_at = CURRENT_TIMESTAMP WHERE id = $1`, id)
		return err
	})
	if err != nil {
		t.Fatalf("soft delete user %d: %v", id, err)
	}
}

func TestUserGroupRepo_create_writes_fields_and_tenant(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := groupAuth(groupTenantEmpty)
	owner := int64(5)
	remark := "ops team"

	// 仓储 Create 原样落 status（未指定哨兵的缺省启用发生在用例层），
	// 这里显式携带启用值。
	created := mustCreateUserGroup(t, d, auth, &biz.UserGroup{Name: "运维组", Code: "ops", Status: biz.UserGroupStatusEnabled, Remark: &remark, OwnerUserID: &owner})
	got, err := NewUserGroupRepo(d).GetByID(context.Background(), auth, created.ID)
	if err != nil {
		t.Fatalf("get created group: %v", err)
	}
	if got.Name != "运维组" || got.Code != "ops" || got.Status != biz.UserGroupStatusEnabled {
		t.Fatalf("row = (%s, %s, %d), want (运维组, ops, enabled)", got.Name, got.Code, got.Status)
	}
	if got.Remark == nil || *got.Remark != "ops team" || got.OwnerUserID == nil || *got.OwnerUserID != 5 {
		t.Fatalf("optional fields = (remark %+v, owner %+v), want (ops team, 5)", got.Remark, got.OwnerUserID)
	}
	if got.CreatedBy != auth.UserID || got.UpdatedBy != auth.UserID || got.DeletedAt != nil {
		t.Fatalf("audit = (by %d/%d, deleted %+v), want (%d/%d, nil)", got.CreatedBy, got.UpdatedBy, got.DeletedAt, auth.UserID, auth.UserID)
	}
}

func TestUserGroupRepo_create_duplicate_code_maps_already_exists(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := groupAuth(groupTenantSeeded)

	_, err := NewUserGroupRepo(d).Create(context.Background(), auth, &biz.UserGroup{Name: "新组", Code: "core_dev"})
	if !errors.Is(err, biz.ErrUserGroupAlreadyExists) {
		t.Fatalf("err = %v, want ErrUserGroupAlreadyExists", err)
	}
}

func TestUserGroupRepo_create_duplicate_name_maps_already_exists(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := groupAuth(groupTenantSeeded)

	_, err := NewUserGroupRepo(d).Create(context.Background(), auth, &biz.UserGroup{Name: "核心开发组", Code: "brand_new"})
	if !errors.Is(err, biz.ErrUserGroupAlreadyExists) {
		t.Fatalf("err = %v, want ErrUserGroupAlreadyExists", err)
	}
}

func TestUserGroupRepo_get_by_id_hides_cross_tenant_row(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	// 组 1 属 t-test-01；t-test-02 的身份经 RLS 不可见，等同不存在。
	other := groupAuth(groupTenantEmpty)

	_, err := NewUserGroupRepo(d).GetByID(context.Background(), other, 1)
	if !errors.Is(err, biz.ErrUserGroupNotFound) {
		t.Fatalf("cross-tenant get = %v, want ErrUserGroupNotFound", err)
	}
}

func TestUserGroupRepo_get_by_id_missing_maps_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	_, err := NewUserGroupRepo(d).GetByID(context.Background(), groupAuth(groupTenantSeeded), 424242)
	if !errors.Is(err, biz.ErrUserGroupNotFound) {
		t.Fatalf("err = %v, want ErrUserGroupNotFound", err)
	}
}

func TestUserGroupRepo_list_filters_by_code_prefix_with_total(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := groupAuth(groupTenantSeeded)

	list, err := NewUserGroupRepo(d).List(context.Background(), auth,
		&dynq.Query{Where: []dynq.Condition{{Field: "code", Op: "prefix", Value: "core"}}})
	if err != nil {
		t.Fatalf("list groups: %v", err)
	}
	if len(list.Items) != 1 || list.Items[0].Code != "core_dev" {
		t.Fatalf("items = %+v, want one group code core_dev", list.Items)
	}
	if list.Total == nil || *list.Total != 1 {
		t.Fatalf("total = %+v, want 1", list.Total)
	}
}

func TestUserGroupRepo_list_rejects_unknown_field(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	_, err := NewUserGroupRepo(d).List(context.Background(), groupAuth(groupTenantSeeded),
		&dynq.Query{Where: []dynq.Condition{{Field: "ghost", Op: "eq", Value: 1}}})
	if !errors.Is(err, biz.ErrUserGroupInvalidArgument) {
		t.Fatalf("err = %v, want ErrUserGroupInvalidArgument", err)
	}
}

func TestUserGroupRepo_update_changes_name_status_remark(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := groupAuth(groupTenantEmpty)
	repo := NewUserGroupRepo(d)

	created := mustCreateUserGroup(t, d, auth, &biz.UserGroup{Name: "before", Code: "ug", Status: biz.UserGroupStatusEnabled})
	remark := "after-remark"
	created.Name = "after"
	created.Status = biz.UserGroupStatusDisabled
	created.Remark = &remark
	if _, err := repo.Update(context.Background(), auth, created); err != nil {
		t.Fatalf("update group: %v", err)
	}
	got, err := repo.GetByID(context.Background(), auth, created.ID)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got.Name != "after" || got.Status != biz.UserGroupStatusDisabled || got.Remark == nil || *got.Remark != "after-remark" {
		t.Fatalf("row = (%s, %d, %+v), want (after, disabled, after-remark)", got.Name, got.Status, got.Remark)
	}
}

func TestUserGroupRepo_update_keeps_code_immutable(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := groupAuth(groupTenantEmpty)
	repo := NewUserGroupRepo(d)

	created := mustCreateUserGroup(t, d, auth, &biz.UserGroup{Name: "grp", Code: "keep-code"})
	created.Code = "hacked"
	if _, err := repo.Update(context.Background(), auth, created); err != nil {
		t.Fatalf("update group: %v", err)
	}
	got, err := repo.GetByID(context.Background(), auth, created.ID)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got.Code != "keep-code" {
		t.Fatalf("code = %q, want keep-code (update must not touch identity column)", got.Code)
	}
}

func TestUserGroupRepo_update_missing_maps_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	_, err := NewUserGroupRepo(d).Update(context.Background(), groupAuth(groupTenantSeeded),
		&biz.UserGroup{ID: 424242, Name: "ghost", Code: "ghost", Status: biz.UserGroupStatusEnabled})
	if !errors.Is(err, biz.ErrUserGroupNotFound) {
		t.Fatalf("err = %v, want ErrUserGroupNotFound", err)
	}
}

func TestUserGroupRepo_delete_missing_maps_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	err := NewUserGroupRepo(d).SoftDelete(context.Background(), groupAuth(groupTenantSeeded), 424242)
	if !errors.Is(err, biz.ErrUserGroupNotFound) {
		t.Fatalf("err = %v, want ErrUserGroupNotFound", err)
	}
}

func TestUserGroupRepo_delete_soft_deletes_and_mapping_rows_remain(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := groupAuth(groupTenantSeeded)
	repo := NewUserGroupRepo(d)

	// 组 2 种子成员为用户 3/4；软删组后映射行物理残留（无外键级联）。
	if err := repo.SoftDelete(context.Background(), auth, 2); err != nil {
		t.Fatalf("delete group: %v", err)
	}
	if _, err := repo.GetByID(context.Background(), auth, 2); !errors.Is(err, biz.ErrUserGroupNotFound) {
		t.Fatalf("get after delete = %v, want ErrUserGroupNotFound", err)
	}
	ids := mappingUserIDs(t, d, auth, 2)
	if len(ids) != 2 || ids[0] != 3 || ids[1] != 4 {
		t.Fatalf("mapping rows after delete = %v, want [3 4] (physical residue)", ids)
	}
	// 唯一约束限定未删除组间：软删后 code/name 可复用。
	created := mustCreateUserGroup(t, d, auth, &biz.UserGroup{Name: "产品设计组", Code: "product_design"})
	if created.ID == 0 {
		t.Fatalf("recreate with freed code = %+v, want new row", created)
	}
}

func TestUserGroupRepo_list_members_returns_display_fields_in_added_order(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := groupAuth(groupTenantSeeded)

	// 组 2 种子成员为用户 3（lisi/李四）与 4（wangwu/王五），同刻添加按
	// user_id 升序稳定排序。
	list, err := NewUserGroupRepo(d).ListMembers(context.Background(), auth, 2, 1, 20)
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	if list.Total != 2 || len(list.Items) != 2 {
		t.Fatalf("members = (total %d, len %d), want (2, 2)", list.Total, len(list.Items))
	}
	first, second := list.Items[0], list.Items[1]
	if first.UserID != 3 || first.Name != "lisi" || first.Nickname != "李四" || first.Status != 1 || first.AddedBy != 1 {
		t.Fatalf("first member = %+v, want user 3 lisi 李四 added by 1", first)
	}
	if second.UserID != 4 || second.Name != "wangwu" || second.Nickname != "王五" {
		t.Fatalf("second member = %+v, want user 4 wangwu 王五", second)
	}
}

func TestUserGroupRepo_add_members_returns_added_count(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := groupAuth(groupTenantSeeded)
	repo := NewUserGroupRepo(d)

	// 组 1 种子成员为用户 2；追加 3/4 后组内共 3 人。
	added := mustAddMembers(t, d, auth, 1, []int64{3, 4})
	if added != 2 {
		t.Fatalf("added = %d, want 2", added)
	}
	list, err := repo.ListMembers(context.Background(), auth, 1, 1, 20)
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	if list.Total != 3 {
		t.Fatalf("total = %d, want 3", list.Total)
	}
}

func TestUserGroupRepo_add_members_duplicate_is_idempotent_skip(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := groupAuth(groupTenantSeeded)

	// 用户 2 已在组 1（种子）：跳过，仅新增用户 3。
	added := mustAddMembers(t, d, auth, 1, []int64{2, 3})
	if added != 1 {
		t.Fatalf("added = %d, want 1 (existing member skipped)", added)
	}
	ids := mappingUserIDs(t, d, auth, 1)
	if len(ids) != 2 || ids[0] != 2 || ids[1] != 3 {
		t.Fatalf("mapping rows = %v, want [2 3]", ids)
	}
}

func TestUserGroupRepo_add_members_unknown_user_rejects_and_writes_nothing(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := groupAuth(groupTenantSeeded)

	_, err := NewUserGroupRepo(d).AddMembers(context.Background(), auth, 1, []int64{3, 424242})
	if !errors.Is(err, biz.ErrUserGroupInvalidArgument) {
		t.Fatalf("err = %v, want ErrUserGroupInvalidArgument", err)
	}
	ids := mappingUserIDs(t, d, auth, 1)
	if len(ids) != 1 || ids[0] != 2 {
		t.Fatalf("mapping rows after rejection = %v, want unchanged [2]", ids)
	}
}

func TestUserGroupRepo_add_members_cross_tenant_user_rejects(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	// 用户 5 属 t-test-02；t-test-01 身份经 RLS 不可见，等同不存在。
	auth := groupAuth(groupTenantSeeded)

	_, err := NewUserGroupRepo(d).AddMembers(context.Background(), auth, 1, []int64{5})
	if !errors.Is(err, biz.ErrUserGroupInvalidArgument) {
		t.Fatalf("err = %v, want ErrUserGroupInvalidArgument", err)
	}
	if ids := mappingUserIDs(t, d, auth, 1); len(ids) != 1 {
		t.Fatalf("mapping rows after rejection = %v, want unchanged [2]", ids)
	}
}

func TestUserGroupRepo_add_members_missing_group_maps_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	_, err := NewUserGroupRepo(d).AddMembers(context.Background(), groupAuth(groupTenantSeeded), 424242, []int64{2})
	if !errors.Is(err, biz.ErrUserGroupNotFound) {
		t.Fatalf("err = %v, want ErrUserGroupNotFound", err)
	}
}

func TestUserGroupRepo_remove_members_deletes_rows_and_returns_removed(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := groupAuth(groupTenantSeeded)
	repo := NewUserGroupRepo(d)

	removed, err := repo.RemoveMembers(context.Background(), auth, 2, []int64{3, 4})
	if err != nil {
		t.Fatalf("remove members: %v", err)
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}
	list, err := repo.ListMembers(context.Background(), auth, 2, 1, 20)
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	if list.Total != 0 || len(list.Items) != 0 {
		t.Fatalf("members after remove = (total %d, len %d), want (0, 0)", list.Total, len(list.Items))
	}
	if ids := mappingUserIDs(t, d, auth, 2); len(ids) != 0 {
		t.Fatalf("mapping rows after remove = %v, want empty (physical delete)", ids)
	}
}

func TestUserGroupRepo_remove_non_member_is_idempotent_zero(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := groupAuth(groupTenantSeeded)

	// 用户 9 不在组 1：移除返回 0 行且不报错，既有映射不受影响。
	removed, err := NewUserGroupRepo(d).RemoveMembers(context.Background(), auth, 1, []int64{9})
	if err != nil {
		t.Fatalf("remove non-member: %v", err)
	}
	if removed != 0 {
		t.Fatalf("removed = %d, want 0", removed)
	}
	if ids := mappingUserIDs(t, d, auth, 1); len(ids) != 1 || ids[0] != 2 {
		t.Fatalf("mapping rows = %v, want unchanged [2]", ids)
	}
}

func TestUserGroupRepo_remove_members_missing_group_maps_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	_, err := NewUserGroupRepo(d).RemoveMembers(context.Background(), groupAuth(groupTenantSeeded), 424242, []int64{2})
	if !errors.Is(err, biz.ErrUserGroupNotFound) {
		t.Fatalf("err = %v, want ErrUserGroupNotFound", err)
	}
}

func TestUserGroupRepo_list_members_hides_soft_deleted_user(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := groupAuth(groupTenantSeeded)

	// 用户 2 软删后，组 1 的残留映射行被活跃联结过滤，成员列表为空。
	softDeleteUserRow(t, d, auth, 2)
	list, err := NewUserGroupRepo(d).ListMembers(context.Background(), auth, 1, 1, 20)
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	if list.Total != 0 || len(list.Items) != 0 {
		t.Fatalf("members = (total %d, len %d), want (0, 0) after user soft delete", list.Total, len(list.Items))
	}
}

func TestUserGroupRepo_list_members_hides_cross_tenant_group(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	other := groupAuth(groupTenantEmpty)

	_, err := NewUserGroupRepo(d).ListMembers(context.Background(), other, 1, 1, 20)
	if !errors.Is(err, biz.ErrUserGroupNotFound) {
		t.Fatalf("cross-tenant list members = %v, want ErrUserGroupNotFound", err)
	}
}

func TestUserGroupRepo_list_members_paginates_by_offset(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	seeded := groupAuth(groupTenantSeeded)

	// 组内 3 人（用户 2/3/4），每页 2：第 1 页 [2 3]、第 2 页 [4]、越界页
	// 空集且 total 不变。
	paged := mustCreateUserGroup(t, d, seeded, &biz.UserGroup{Name: "paged-seed", Code: "paged-seed"})
	mustAddMembers(t, d, seeded, paged.ID, []int64{2, 3, 4})
	repo := NewUserGroupRepo(d)

	page1, err := repo.ListMembers(context.Background(), seeded, paged.ID, 1, 2)
	if err != nil {
		t.Fatalf("list members page 1: %v", err)
	}
	if len(page1.Items) != 2 || page1.Items[0].UserID != 2 || page1.Items[1].UserID != 3 || page1.Total != 3 {
		t.Fatalf("page 1 = (len %d, ids %v, total %d), want (2, [2 3], 3)", len(page1.Items), memberIDsOf(page1), page1.Total)
	}
	page2, err := repo.ListMembers(context.Background(), seeded, paged.ID, 2, 2)
	if err != nil {
		t.Fatalf("list members page 2: %v", err)
	}
	if len(page2.Items) != 1 || page2.Items[0].UserID != 4 || page2.Total != 3 {
		t.Fatalf("page 2 = (len %d, ids %v, total %d), want (1, [4], 3)", len(page2.Items), memberIDsOf(page2), page2.Total)
	}
	page9, err := repo.ListMembers(context.Background(), seeded, paged.ID, 9, 2)
	if err != nil {
		t.Fatalf("list members page 9: %v", err)
	}
	if len(page9.Items) != 0 || page9.Total != 3 {
		t.Fatalf("page 9 = (len %d, total %d), want (0, 3)", len(page9.Items), page9.Total)
	}
}

// memberIDsOf 提取成员集合的用户 ID，供分页断言输出。
func memberIDsOf(list *biz.UserGroupMemberList) []int64 {
	out := make([]int64, 0, len(list.Items))
	for _, m := range list.Items {
		out = append(out, m.UserID)
	}
	return out
}
