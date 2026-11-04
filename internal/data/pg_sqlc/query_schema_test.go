package pg_sqlc

import (
	"testing"

	"grape/internal/biz"
)

// TestQuerySchemaRegistryCoversExposedResources 验证注册表收录的资源均能
// 产出非空 schema，且资源名与内容 hash 一致（确定性纯函数）。
func TestQuerySchemaRegistryCoversExposedResources(t *testing.T) {
	for resource := range querySchemaRegistry {
		s, err := NewQuerySchemaRepo().ListSchema(resource)
		if err != nil {
			t.Fatalf("ListSchema(%q): %v", resource, err)
		}
		if s.Resource != resource {
			t.Fatalf("schema resource = %q, want %q", s.Resource, resource)
		}
		if len(s.Fields) == 0 {
			t.Fatalf("schema fields of %q is empty", resource)
		}
		if s.Hash() == "" {
			t.Fatalf("schema hash of %q is empty", resource)
		}
		// 再取一次：确定性纯函数，hash 必须字节级一致。
		again, err := NewQuerySchemaRepo().ListSchema(resource)
		if err != nil {
			t.Fatalf("ListSchema(%q) second call: %v", resource, err)
		}
		if again.Hash() != s.Hash() {
			t.Fatalf("hash of %q = %s then %s, want deterministic", resource, s.Hash(), again.Hash())
		}
	}
}

// TestQuerySchemaRegistryIncludesAllDynamicListResources 验证已暴露动态
// List 的四个资源（user/role/dept/user_group）全部收录，防接入遗漏。
func TestQuerySchemaRegistryIncludesAllDynamicListResources(t *testing.T) {
	for _, resource := range []string{"sys_dept", "sys_role", "sys_user", "sys_user_group"} {
		if _, ok := querySchemaRegistry[resource]; !ok {
			t.Fatalf("resource %q not registered", resource)
		}
	}
}

// TestQuerySchemaUnknownResource 验证未收录资源与空白资源名均返回
// ErrQueryResourceUnknown（400 INVALID_ARGUMENT）。
func TestQuerySchemaUnknownResource(t *testing.T) {
	repo := NewQuerySchemaRepo()
	for _, resource := range []string{"basic", "sys_menu", "", "  "} {
		_, err := repo.ListSchema(resource)
		if err == nil {
			t.Fatalf("ListSchema(%q): want error, got nil", resource)
		}
		if err != biz.ErrQueryResourceUnknown {
			t.Fatalf("ListSchema(%q) err = %v, want ErrQueryResourceUnknown", resource, err)
		}
	}
}
