// Package jsoncodec 提供按 protobuf JSON 映射编解码的 json codec。
//
// Kratos 默认的 json codec 直接调用 encoding/json，产出 snake_case 字段、
// 数字型 int64 与 {seconds,nanos} 时间戳，与 proto 及 OpenAPI 契约不一致。
// 这里以 protojson 覆盖 "json" 子类型，使 HTTP 与 gRPC 共享同一套字段映射。
package jsoncodec

import (
	"encoding/json"
	"reflect"

	"github.com/go-kratos/kratos/v3/encoding"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Name 是覆盖的 content-subtype，与 Kratos 默认 json codec 同名。
const Name = "json"

var (
	// MarshalOptions 输出小驼峰字段、RFC3339 时间戳、字符串型 int64
	// 与符号化枚举名。
	MarshalOptions = protojson.MarshalOptions{}
	// UnmarshalOptions 同时接受小驼峰与 proto 原名（snake_case），
	// 未知字段忽略，与旧 codec 的宽松行为保持一致。
	UnmarshalOptions = protojson.UnmarshalOptions{
		DiscardUnknown: true,
	}
)

// Install 覆盖注册 "json" codec。须在开始接收请求前调用。
func Install() {
	encoding.RegisterCodec(codec{})
}

type codec struct{}

var protoMessageType = reflect.TypeOf((*proto.Message)(nil)).Elem()

func (codec) Marshal(v any) ([]byte, error) {
	m, ok := v.(proto.Message)
	if !ok {
		return json.Marshal(v)
	}
	return MarshalOptions.Marshal(m)
}

func (codec) Unmarshal(data []byte, v any) error {
	if len(data) == 0 {
		return nil
	}
	if m, ok := v.(proto.Message); ok {
		return UnmarshalOptions.Unmarshal(data, m)
	}
	// 生成的 HTTP handler 以 &in.Field 绑定嵌套 body，目标形态是 **T；
	// kratos 对 proto/protojson codec 有同样的解引用逻辑，这里需要对齐。
	if m := indirectProtoMessage(v); m != nil {
		return UnmarshalOptions.Unmarshal(data, m)
	}
	return json.Unmarshal(data, v)
}

// indirectProtoMessage 将 **T（T 为 proto.Message）解引用为可写入的 *T。
func indirectProtoMessage(v any) proto.Message {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() || rv.Kind() != reflect.Pointer || rv.IsNil() {
		return nil
	}
	elem := rv.Type().Elem()
	if elem.Kind() != reflect.Pointer || !elem.Implements(protoMessageType) {
		return nil
	}
	target := rv.Elem()
	if target.IsNil() {
		target.Set(reflect.New(elem.Elem()))
	}
	return target.Interface().(proto.Message)
}

func (codec) Name() string { return Name }
