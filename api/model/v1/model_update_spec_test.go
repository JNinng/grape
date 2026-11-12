package v1

import (
	"sort"
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// 协议字段同步守卫：ModelUpdateSpec 是 Model 的可变字段子集，两处重复
// 声明（proto3 无继承/排除机制）。本测试以 protobuf 反射比对字段名集合，
// 防止将来只改一处导致更新协议与响应表示漂移。

// mutableModelFields 是约定的创建后可变字段集合（ModelUpdateSpec 全部字段）。
var mutableModelFields = []string{
	"status",
	"name",
	"provider",
	"base_url",
	"model",
	"dimension",
	"timeout_ms",
	"api_key_env",
	"remark",
}

func TestModelUpdateSpecFieldsMatchMutableModelFields(t *testing.T) {
	got := protoFieldNames((&ModelUpdateSpec{}).ProtoReflect().Descriptor())
	want := append([]string(nil), mutableModelFields...)
	sort.Strings(want)

	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("ModelUpdateSpec fields = %v, want %v", got, want)
	}

	modelFields := make(map[string]bool)
	for _, name := range protoFieldNames((&Model{}).ProtoReflect().Descriptor()) {
		modelFields[name] = true
	}
	for _, name := range want {
		if !modelFields[name] {
			t.Fatalf("Model lacks field %q declared by ModelUpdateSpec", name)
		}
	}
}

// protoFieldNames 返回消息的 proto 字段名（snake_case）并按字典序排序。
func protoFieldNames(desc protoreflect.MessageDescriptor) []string {
	fields := desc.Fields()
	names := make([]string, 0, fields.Len())
	for i := 0; i < fields.Len(); i++ {
		names = append(names, string(fields.Get(i).Name()))
	}
	sort.Strings(names)
	return names
}
