package jsoncodec

import (
	"encoding/json"
	"testing"
	"time"

	queryv1 "grape/api/query/v1"

	"github.com/go-kratos/kratos/v3/encoding"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// TestMarshalFollowsProtoJSONMapping 验证响应字段形态与 api 契约
// 一致：小驼峰、RFC3339 时间戳、字符串型 int64、符号化枚举名。
func TestMarshalFollowsProtoJSONMapping(t *testing.T) {
	q := &queryv1.Query{
		Pagination: &queryv1.Pagination{
			Page:         proto.Int32(2),
			PageSize:     proto.Int32(20),
			IncludeTotal: proto.Bool(true),
		},
	}

	b, err := codec{}.Marshal(q)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	pg := m["pagination"].(map[string]any)
	if pg["pageSize"] != float64(20) {
		t.Fatalf("int32 should be encoded as number under camelCase name, got %#v", pg["pageSize"])
	}
	if pg["includeTotal"] != true {
		t.Fatalf("bool should be encoded under camelCase name, got %#v", pg)
	}
	if _, ok := pg["page_size"]; ok {
		t.Fatal("output should use camelCase field names")
	}

	tb, err := codec{}.Marshal(timestamppb.New(time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatalf("Marshal timestamp: %v", err)
	}
	if string(tb) != `"2026-10-19T12:00:00Z"` {
		t.Fatalf("timestamp should be RFC3339, got %s", tb)
	}

	ib, err := codec{}.Marshal(wrapperspb.Int64(1))
	if err != nil {
		t.Fatalf("Marshal int64: %v", err)
	}
	if string(ib) != `"1"` {
		t.Fatalf("int64 should be encoded as string, got %s", ib)
	}

	eb, err := codec{}.Marshal(&descriptorpb.FieldDescriptorProto{
		Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
	})
	if err != nil {
		t.Fatalf("Marshal enum: %v", err)
	}
	var em map[string]any
	if err := json.Unmarshal(eb, &em); err != nil {
		t.Fatalf("unmarshal enum output: %v", err)
	}
	if em["type"] != "TYPE_STRING" {
		t.Fatalf("enum should be symbolic name, got %#v", em["type"])
	}
}

// TestUnmarshalAcceptsBothNames 验证请求同时接受小驼峰与 proto 原名。
func TestUnmarshalAcceptsBothNames(t *testing.T) {
	var camel queryv1.Pagination
	if err := (codec{}).Unmarshal([]byte(`{"pageSize":5,"after":"cursor-1"}`), &camel); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if camel.GetPageSize() != 5 || camel.GetAfter() != "cursor-1" {
		t.Fatalf("camelCase not decoded: %+v", &camel)
	}

	var snake queryv1.Pagination
	if err := (codec{}).Unmarshal([]byte(`{"page_size":3,"include_total":false}`), &snake); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if snake.GetPageSize() != 3 || snake.IncludeTotal == nil || *snake.IncludeTotal {
		t.Fatalf("snake_case not decoded: %+v", &snake)
	}
}

// TestUnmarshalNestedMessagePointer 验证生成的 HTTP handler 传入的
// **T 形态能被解引用后按 protojson 解析。
func TestUnmarshalNestedMessagePointer(t *testing.T) {
	var q *queryv1.Query
	if err := (codec{}).Unmarshal([]byte(`{"fields":["createdAt"],"pagination":{"page":"2"}}`), &q); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if q == nil ||
		len(q.GetFields()) != 1 || q.GetFields()[0] != "createdAt" ||
		q.GetPagination().GetPage() != 2 {
		t.Fatalf("unexpected message: %+v", q)
	}
}

// TestInstallOverridesDefault 验证 Install 覆盖注册的 "json" codec。
func TestInstallOverridesDefault(t *testing.T) {
	Install()
	if _, ok := encoding.GetCodec(Name).(codec); !ok {
		t.Fatalf("json codec not overridden: %T", encoding.GetCodec(Name))
	}
}
