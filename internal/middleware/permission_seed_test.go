package middleware

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"grape/internal/biz"
	"grape/internal/data/pg_sqlc/sqlc/seed"
)

// menuInsertRE 提取种子包中 sys_menu 插入语句的权限码（每行 VALUES
// 的第二列是 status、第三列是 name、第四列是 permission_code）。
var menuInsertRE = regexp.MustCompile(`(?s)INSERT INTO public\.sys_menu \([^;]*?VALUES \(\d+, 1, '[^']*', '([^']+)'`)

// pagePermissionCodes 是前端设置面模块导航与路由守卫使用的页面级权限码：
// 它们不在入口层操作码表内（页面本身不发请求码），但必须与种子包的
// 页面菜单一一对应，否则前端会隐藏整个模块或直接拒绝路由访问。
var pagePermissionCodes = []string{
	"sys:manage",
	"sys:user",
	"sys:dept",
	"sys:user-group",
	"sys:role",
	"sys:menu",
	"sys:dict",
	"sys:audit",
	"sys:session",
	"sys:config",
	"sys:tenant",
}

// TestSeedMenuCodes_cover_operation_and_page_codes 守卫生产种子与入口层
// 码表的漂移：入口层每个受控操作码、前端每个页面级权限码都必须能在
// 种子包的 sys_menu 中找到承载行，且种子内权限码不得重复（租户内
// 唯一由部分唯一索引兜底，此处把冲突前移到单元测试）。新增管理端点或
// 页面时，先补种子菜单再改码表，本测试是二者的对齐闸门。
func TestSeedMenuCodes_cover_operation_and_page_codes(t *testing.T) {
	dir := filepath.Join("..", "data", "pg_sqlc", "sqlc")

	counts := make(map[string]int)
	for _, name := range seed.Files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, match := range menuInsertRE.FindAllStringSubmatch(string(content), -1) {
			counts[match[1]]++
		}
	}
	if len(counts) == 0 {
		t.Fatalf("no sys_menu permission codes parsed from seed package under %s", dir)
	}

	for operation, code := range operationPermissionCodes {
		if code == "" {
			t.Errorf("operation %s declares an empty permission code", operation)
			continue
		}
		if counts[code] == 0 {
			t.Errorf("operation %s requires %q, but seed package has no menu row for it", operation, code)
		}
	}
	for _, code := range pagePermissionCodes {
		if counts[code] == 0 {
			t.Errorf("frontend page code %q is missing from seed package menu tree", code)
		}
	}
	for code, n := range counts {
		if n > 1 {
			t.Errorf("permission code %q appears %d times in seed package, want exactly one menu row", code, n)
		}
	}

	// 业务常量必须全部被入口层码表消费，避免种子/常量/中间件三处各自
	// 漂移。这里列举当前全部管理面常量。
	bizCodes := []string{
		biz.PermUserQuery, biz.PermUserCreate, biz.PermUserUpdate, biz.PermUserDelete,
		biz.PermUserResetPwd, biz.PermUserAssignRole, biz.PermUserTransferOwner,
		biz.PermDeptQuery, biz.PermDeptCreate, biz.PermDeptUpdate, biz.PermDeptDelete,
		biz.PermUserGroupQuery, biz.PermUserGroupCreate, biz.PermUserGroupUpdate,
		biz.PermUserGroupDelete, biz.PermUserGroupAssignMember,
		biz.PermRoleQuery, biz.PermRoleCreate, biz.PermRoleUpdate, biz.PermRoleDelete,
		biz.PermRoleSetDataScope, biz.PermRoleSetMenus,
		biz.PermMenuQuery, biz.PermMenuCreate, biz.PermMenuUpdate, biz.PermMenuDelete,
		biz.PermDictQuery, biz.PermDictCreate, biz.PermDictUpdate, biz.PermDictDelete,
		biz.PermConfigGlobalQuery, biz.PermConfigGlobalCreate, biz.PermConfigGlobalUpdate,
		biz.PermConfigGlobalDelete, biz.PermConfigTenantQuery, biz.PermConfigTenantCreate,
		biz.PermConfigTenantUpdate, biz.PermConfigTenantDelete,
		biz.PermConfigFeatureFlagQuery, biz.PermConfigFeatureFlagCreate,
		biz.PermConfigFeatureFlagUpdate, biz.PermConfigFeatureFlagDelete,
		biz.PermConfigTranslationQuery, biz.PermConfigTranslationCreate,
		biz.PermConfigTranslationUpdate, biz.PermConfigTranslationDelete,
		biz.PermAuditOperationLog, biz.PermAuditLoginLog,
		biz.PermSessionQuery, biz.PermSessionRevoke,
		biz.PermTenantQuery, biz.PermTenantCreate, biz.PermTenantUpdate, biz.PermTenantDelete,
	}
	gated := make(map[string]bool, len(operationPermissionCodes))
	for _, code := range operationPermissionCodes {
		gated[code] = true
	}
	for _, code := range bizCodes {
		if !gated[code] {
			t.Errorf("biz permission constant %q is not gated by any operation", code)
		}
		if counts[code] == 0 {
			t.Errorf("biz permission constant %q is missing from seed package menu tree", code)
		}
	}
}
