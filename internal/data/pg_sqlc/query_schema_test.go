package pg_sqlc

import (
	"reflect"
	"testing"

	"grape/internal/biz"

	"github.com/jninng/sqlc_gen_dynamic/dynq"
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

// TestQuerySchemaRegistry_excludes_internal_tenant_field 验证 tenant_id 作为
// 服务端内部列不出现在业务资源 schema 中；sys_tenant.tenant_id 是租户树结构
// 例外，仍保留为可查询字段。
func TestQuerySchemaRegistry_excludes_internal_tenant_field(t *testing.T) {
	for resource := range querySchemaRegistry {
		s, err := NewQuerySchemaRepo().ListSchema(resource)
		if err != nil {
			t.Fatalf("ListSchema(%q): %v", resource, err)
		}
		found := false
		for _, f := range s.Fields {
			if f.Field == "tenantId" {
				found = true
				break
			}
		}
		if resource == "sys_tenant" {
			if !found {
				t.Fatal("sys_tenant schema must keep tenantId as structural exception")
			}
			continue
		}
		if found {
			t.Fatalf("resource %q schema exposes internal tenantId field", resource)
		}
	}
}

// TestQuerySchemaRegistry_carries_desc_and_options 验证列注释 DSL 的展示
// 注解经生成 registry 原样进入 schema：整数/字符串选项定型正确、标签顺序
// 稳定、无注解字段保持空 desc 与 nil options。
func TestQuerySchemaRegistry_carries_desc_and_options(t *testing.T) {
	schemaOf := func(t *testing.T, resource string) dynq.ResourceSchema {
		t.Helper()
		s, err := NewQuerySchemaRepo().ListSchema(resource)
		if err != nil {
			t.Fatalf("ListSchema(%q): %v", resource, err)
		}
		return s
	}
	fieldOf := func(t *testing.T, s dynq.ResourceSchema, key string) dynq.FieldSchema {
		t.Helper()
		for _, f := range s.Fields {
			if f.Field == key {
				return f
			}
		}
		t.Fatalf("field %q not found in %q schema", key, s.Resource)
		return dynq.FieldSchema{}
	}

	t.Run("integer options keep int64 values and order", func(t *testing.T) {
		f := fieldOf(t, schemaOf(t, "sys_role"), "dataScope")
		if f.DataType != string(dynq.DataTypeInteger) {
			t.Fatalf("dataScope data type = %q, want integer", f.DataType)
		}
		want := []dynq.FieldOption{
			{Value: int64(1), Label: "仅本人"},
			{Value: int64(2), Label: "本部门"},
			{Value: int64(3), Label: "本部门及子部门"},
			{Value: int64(4), Label: "指定人"},
			{Value: int64(5), Label: "指定部门"},
			{Value: int64(6), Label: "用户组"},
			{Value: int64(7), Label: "全租户"},
		}
		if !reflect.DeepEqual(f.Options, want) {
			t.Fatalf("dataScope options = %+v, want %+v", f.Options, want)
		}
	})

	t.Run("string options keep string values and order", func(t *testing.T) {
		f := fieldOf(t, schemaOf(t, "sys_dict_data"), "valueType")
		if f.DataType != string(dynq.DataTypeString) {
			t.Fatalf("valueType data type = %q, want string", f.DataType)
		}
		want := []dynq.FieldOption{
			{Value: "string", Label: "字符串"},
			{Value: "integer", Label: "整数"},
			{Value: "boolean", Label: "布尔"},
			{Value: "float", Label: "浮点"},
			{Value: "json", Label: "JSON"},
		}
		if !reflect.DeepEqual(f.Options, want) {
			t.Fatalf("valueType options = %+v, want %+v", f.Options, want)
		}
	})

	t.Run("desc is carried", func(t *testing.T) {
		f := fieldOf(t, schemaOf(t, "sys_user"), "lockedUntil")
		if f.Desc != "NULL 表示未锁定" {
			t.Fatalf("lockedUntil desc = %q, want NULL 表示未锁定", f.Desc)
		}
	})

	t.Run("field without annotations stays empty", func(t *testing.T) {
		f := fieldOf(t, schemaOf(t, "sys_user"), "name")
		if f.Desc != "" {
			t.Fatalf("name desc = %q, want empty", f.Desc)
		}
		if f.Options != nil {
			t.Fatalf("name options = %+v, want nil", f.Options)
		}
	})
}

// TestQuerySchemaRegistryIncludesAllDynamicListResources 验证已暴露动态
// List 的资源全部收录，防接入遗漏：字面量期望集合是 registry 的完整镜像
// （缺一即失败），并反向断言 registry 无多余 key（多一即失败）。
func TestQuerySchemaRegistryIncludesAllDynamicListResources(t *testing.T) {
	want := []string{
		"agent_model_call_log",
		"sys_config",
		"sys_dept",
		"sys_dict_data",
		"sys_dict_type",
		"sys_feature_flag",
		"sys_login_log",
		"sys_operation_log",
		"sys_role",
		"sys_session",
		"sys_tenant",
		"sys_tenant_config",
		"sys_translation",
		"sys_user",
		"sys_user_group",
		"sys_model",
		"kb_library",
		"kb_document",
	}
	for _, resource := range want {
		if _, ok := querySchemaRegistry[resource]; !ok {
			t.Fatalf("resource %q not registered", resource)
		}
	}
	if len(querySchemaRegistry) != len(want) {
		t.Fatalf("registry size = %d, want %d (no undeclared resources)", len(querySchemaRegistry), len(want))
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
