package jsoncodec

import (
	"encoding/json"
	"testing"
	"time"

	userv1 "grape/api/user/v1"

	"github.com/go-kratos/kratos/v3/encoding"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestMarshalFollowsProtoJSONMapping 验证响应字段形态与 api/openapi 契约
// 一致：小驼峰、RFC3339 时间戳、字符串型 int64、符号化枚举名。
func TestMarshalFollowsProtoJSONMapping(t *testing.T) {
	u := &userv1.User{
		Id:        1,
		Name:      "Alice",
		Status:    userv1.UserStatus_USER_STATUS_ACTIVE,
		CreatedAt: timestamppb.New(time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)),
	}

	b, err := codec{}.Marshal(u)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if m["id"] != "1" {
		t.Fatalf("int64 should be encoded as string, got %#v", m["id"])
	}
	if m["createdAt"] != "2026-10-19T12:00:00Z" {
		t.Fatalf("timestamp should be RFC3339, got %#v", m["createdAt"])
	}
	if m["status"] != "USER_STATUS_ACTIVE" {
		t.Fatalf("enum should be symbolic name, got %#v", m["status"])
	}
	if _, ok := m["created_at"]; ok {
		t.Fatal("output should use camelCase field names")
	}
}

// TestUnmarshalAcceptsBothNames 验证请求同时接受小驼峰与 proto 原名。
func TestUnmarshalAcceptsBothNames(t *testing.T) {
	var u userv1.User
	if err := (codec{}).Unmarshal([]byte(`{"birthDate":"2000-01-02","org_id":"org-1"}`), &u); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if u.GetBirthDate() != "2000-01-02" {
		t.Fatalf("birthDate not decoded: %q", u.GetBirthDate())
	}
	if u.GetOrgId() != "org-1" {
		t.Fatalf("org_id not decoded: %q", u.GetOrgId())
	}
}

// TestUnmarshalNestedMessagePointer 验证生成的 HTTP handler 传入的
// **T 形态能被解引用后按 protojson 解析。
func TestUnmarshalNestedMessagePointer(t *testing.T) {
	var u *userv1.User
	if err := (codec{}).Unmarshal([]byte(`{"id":"7","birthDate":"2000-01-02"}`), &u); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if u == nil || u.GetId() != 7 || u.GetBirthDate() != "2000-01-02" {
		t.Fatalf("unexpected message: %+v", u)
	}
}

// TestInstallOverridesDefault 验证 Install 覆盖注册的 "json" codec。
func TestInstallOverridesDefault(t *testing.T) {
	Install()
	if _, ok := encoding.GetCodec(Name).(codec); !ok {
		t.Fatalf("json codec not overridden: %T", encoding.GetCodec(Name))
	}
}
