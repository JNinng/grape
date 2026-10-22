package utils

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestMaskString(t *testing.T) {
	// family 为 👨+ZWJ+👩+ZWJ+👧，共 5 个 rune 的组合 emoji
	family := string([]rune{0x1F468, 0x200D, 0x1F469, 0x200D, 0x1F467})
	tests := []struct {
		name  string
		in    string
		front int
		back  int
		mask  string
		want  string
	}{
		// 三段边界
		{name: "rune 数小于 front+back 时整体掩码", in: "ab", front: 2, back: 2, mask: "*", want: "*"},
		{name: "rune 数等于 front+back 时原样返回", in: "abcd", front: 2, back: 2, mask: "*", want: "abcd"},
		{name: "rune 数大于 front+back 时保两端掩中段", in: "abcdef", front: 2, back: 2, mask: "***", want: "ab***ef"},
		// front/back 为 0
		{name: "front 为 0 时仅保留后段", in: "abcdef", front: 0, back: 2, mask: "..", want: "..ef"},
		{name: "back 为 0 时仅保留前段", in: "abcdef", front: 2, back: 0, mask: "..", want: "ab.."},
		{name: "front 与 back 均为 0 时整体掩码", in: "abcdef", front: 0, back: 0, mask: "#", want: "#"},
		// 负值按 0 处理
		{name: "front 为负按 0 处理", in: "abcdef", front: -1, back: 2, mask: "..", want: "..ef"},
		{name: "back 为负按 0 处理", in: "abcdef", front: 2, back: -3, mask: "..", want: "ab.."},
		// Unicode
		{name: "中英混排按 rune 计数", in: "中文abcdef", front: 2, back: 3, mask: "*", want: "中文*def"},
		{name: "单 rune emoji 保持完整", in: "a\U0001F44Db", front: 1, back: 1, mask: "***", want: "a***b"},
		{name: "多 rune 组合 emoji 保持完整", in: family + "OK", front: 5, back: 1, mask: "###", want: family + "###K"},
		// 空串
		{name: "空串且无保留时原样返回", in: "", front: 0, back: 0, mask: "*", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MaskString(tt.in, tt.front, tt.back, tt.mask)
			if got != tt.want {
				t.Fatalf("MaskString(%q, %d, %d, %q) = %q, 期望 %q", tt.in, tt.front, tt.back, tt.mask, got, tt.want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("掩码结果不是合法 UTF-8：%q", got)
			}
		})
	}
}

func TestMaskString4(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		front int
		back  int
		want  string
	}{
		{name: "手机号式掩码", in: "13812345678", front: 3, back: 4, want: "138****5678"},
		{name: "过短整体掩码", in: "ab", front: 3, back: 3, want: "****"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MaskString4(tt.in, tt.front, tt.back)
			if got != tt.want {
				t.Fatalf("MaskString4(%q, %d, %d) = %q, 期望 %q", tt.in, tt.front, tt.back, got, tt.want)
			}
		})
	}
}

func TestSafeString(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "上限内原样返回", in: strings.Repeat("a", maxLogSize-1), want: strings.Repeat("a", maxLogSize-1)},
		{name: "恰好等于上限不截断", in: strings.Repeat("a", maxLogSize), want: strings.Repeat("a", maxLogSize)},
		{name: "超一字节即截断并追加标记", in: strings.Repeat("a", maxLogSize+1), want: strings.Repeat("a", maxLogSize) + truncateMark},
		{
			name: "多字节 rune 跨越截断点时回退到边界",
			in:   strings.Repeat("a", maxLogSize-1) + "中文",
			want: strings.Repeat("a", maxLogSize-1) + truncateMark,
		},
		{
			name: "截断点落在三字节 rune 序列内时回退",
			in:   strings.Repeat("中", maxLogSize/3+1),
			want: strings.Repeat("中", maxLogSize/3) + truncateMark,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SafeString(tt.in)
			if got != tt.want {
				t.Fatalf("SafeString(长度 %d) = 长度 %d, 期望长度 %d", len(tt.in), len(got), len(tt.want))
			}
			if !utf8.ValidString(got) {
				t.Fatalf("截断结果不是合法 UTF-8")
			}
		})
	}
}

func TestSafeBody(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want string
	}{
		{name: "上限内原样返回", in: []byte("hello"), want: "hello"},
		{name: "恰好等于上限不截断", in: bytes.Repeat([]byte("a"), maxLogSize), want: strings.Repeat("a", maxLogSize)},
		{name: "超长截断并追加标记", in: bytes.Repeat([]byte("a"), maxLogSize+1), want: strings.Repeat("a", maxLogSize) + truncateMark},
		{
			name: "多字节 rune 跨越截断点时不产生非法 UTF-8",
			in:   append(bytes.Repeat([]byte("a"), maxLogSize-1), []byte("中文")...),
			want: strings.Repeat("a", maxLogSize-1) + truncateMark,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SafeBody(tt.in)
			if got != tt.want {
				t.Fatalf("SafeBody(长度 %d) = 长度 %d, 期望长度 %d", len(tt.in), len(got), len(tt.want))
			}
			if !utf8.ValidString(got) {
				t.Fatalf("截断结果不是合法 UTF-8")
			}
		})
	}
}
