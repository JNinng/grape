package oss

import (
	"encoding/json"
	"testing"
)

// TestLikePrefixPattern 验证 LIKE 模式转义：通配符与转义符按字面量处理，
// 空前缀匹配全部键。
func TestLikePrefixPattern(t *testing.T) {
	cases := []struct {
		name   string
		prefix string
		want   string
	}{
		{"empty prefix matches all", "", "%"},
		{"plain prefix", "docs/", "docs/%"},
		{"percent escaped", "100%", `100\%%`},
		{"underscore escaped", "a_b", `a\_b%`},
		{"backslash escaped first", `a\b%`, `a\\b\%%`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := likePrefixPattern(tc.prefix); got != tc.want {
				t.Fatalf("likePrefixPattern(%q) = %q, want %q", tc.prefix, got, tc.want)
			}
		})
	}
}

// TestMetadataJSONRoundTrip 验证元数据 jsonb 编解码：空映射落 NULL，
// 非空映射原样往返。
func TestMetadataJSONRoundTrip(t *testing.T) {
	if got := metadataJSON(nil); got != nil {
		t.Fatalf("metadataJSON(nil) = %v, want nil", got)
	}
	if got := metadataJSON(map[string]string{}); got != nil {
		t.Fatalf("metadataJSON(empty) = %v, want nil", got)
	}
	meta := map[string]string{"Content-Type": "text/plain", "X-Amz-Meta-K": "v"}
	encoded := metadataJSON(meta)
	if encoded == nil {
		t.Fatal("metadataJSON(meta) = nil, want encoded json")
	}
	decoded := decodeMetadata(encoded)
	if len(decoded) != 2 || decoded["Content-Type"] != "text/plain" || decoded["X-Amz-Meta-K"] != "v" {
		t.Fatalf("decodeMetadata = %+v, want roundtrip of %v", decoded, meta)
	}
}

// TestDecodeMetadataInvalid 验证损坏的 jsonb 解码退化空映射而非报错。
func TestDecodeMetadataInvalid(t *testing.T) {
	if got := decodeMetadata([]byte("not-json")); len(got) != 0 {
		t.Fatalf("decodeMetadata(corrupt) = %+v, want empty", got)
	}
	if got := decodeMetadata(nil); len(got) != 0 {
		t.Fatalf("decodeMetadata(nil) = %+v, want empty", got)
	}
}

// TestDecodeJSONString 验证配置值解码：JSON 字符串字面量解出原文，其他
// JSON 形态与非法 JSON 拒绝。
func TestDecodeJSONString(t *testing.T) {
	got, err := decodeJSONString(`"local"`)
	if err != nil || got != "local" {
		t.Fatalf("decodeJSONString(`\"local\"`) = (%q, %v), want (local, nil)", got, err)
	}
	if _, err := decodeJSONString("local"); err == nil {
		t.Fatal("decodeJSONString(bare word) = nil error, want invalid json error")
	}
	if _, err := decodeJSONString("42"); err == nil {
		t.Fatal("decodeJSONString(number) = nil error, want non-string error")
	}
	if _, err := decodeJSONString(`"local`); err == nil {
		t.Fatal("decodeJSONString(truncated) = nil error, want invalid json error")
	}
}

// TestQuotedETag 验证 S3 响应口径的引号包装。
func TestQuotedETag(t *testing.T) {
	if got := quotedETag("abc123"); got != `"abc123"` {
		t.Fatalf("quotedETag = %q, want quoted form", got)
	}
}

// TestOssConfigJSONValueAlignment 防止 oss_config.go 的解析与本包漂移：
// 两处对同一 value_type=json 约定必须产出一致结果。
func TestOssConfigJSONValueAlignment(t *testing.T) {
	raw := `"http://127.0.0.1:8000/s3"`
	var direct string
	if err := json.Unmarshal([]byte(raw), &direct); err != nil {
		t.Fatalf("baseline unmarshal: %v", err)
	}
	got, err := decodeJSONString(raw)
	if err != nil || got != direct {
		t.Fatalf("decodeJSONString = (%q, %v), want (%q, nil)", got, err, direct)
	}
}
