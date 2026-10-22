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
		{name: "mask whole string when runes < front+back", in: "ab", front: 2, back: 2, mask: "*", want: "*"},
		{name: "return as-is when runes == front+back", in: "abcd", front: 2, back: 2, mask: "*", want: "abcd"},
		{name: "keep both ends and mask middle when runes > front+back", in: "abcdef", front: 2, back: 2, mask: "***", want: "ab***ef"},
		// front/back 为 0
		{name: "keep only tail when front is 0", in: "abcdef", front: 0, back: 2, mask: "..", want: "..ef"},
		{name: "keep only head when back is 0", in: "abcdef", front: 2, back: 0, mask: "..", want: "ab.."},
		{name: "mask whole string when front and back are 0", in: "abcdef", front: 0, back: 0, mask: "#", want: "#"},
		// 负值按 0 处理
		{name: "negative front treated as 0", in: "abcdef", front: -1, back: 2, mask: "..", want: "..ef"},
		{name: "negative back treated as 0", in: "abcdef", front: 2, back: -3, mask: "..", want: "ab.."},
		// Unicode
		{name: "count by rune with CJK and ASCII mixed", in: "中文abcdef", front: 2, back: 3, mask: "*", want: "中文*def"},
		{name: "single-rune emoji kept intact", in: "a\U0001F44Db", front: 1, back: 1, mask: "***", want: "a***b"},
		{name: "multi-rune emoji kept intact", in: family + "OK", front: 5, back: 1, mask: "###", want: family + "###K"},
		// 空串
		{name: "empty string returned as-is with nothing kept", in: "", front: 0, back: 0, mask: "*", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MaskString(tt.in, tt.front, tt.back, tt.mask)
			if got != tt.want {
				t.Fatalf("MaskString(%q, %d, %d, %q) = %q, want %q", tt.in, tt.front, tt.back, tt.mask, got, tt.want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("masked result is not valid UTF-8: %q", got)
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
		{name: "phone-number style mask", in: "13812345678", front: 3, back: 4, want: "138****5678"},
		{name: "mask whole string when too short", in: "ab", front: 3, back: 3, want: "****"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MaskString4(tt.in, tt.front, tt.back)
			if got != tt.want {
				t.Fatalf("MaskString4(%q, %d, %d) = %q, want %q", tt.in, tt.front, tt.back, got, tt.want)
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
		{name: "returned as-is within limit", in: strings.Repeat("a", maxLogSize-1), want: strings.Repeat("a", maxLogSize-1)},
		{name: "not truncated when exactly at limit", in: strings.Repeat("a", maxLogSize), want: strings.Repeat("a", maxLogSize)},
		{name: "truncated with mark when one byte over", in: strings.Repeat("a", maxLogSize+1), want: strings.Repeat("a", maxLogSize) + truncateMark},
		{
			name: "fall back to rune boundary when multi-byte rune straddles the cut",
			in:   strings.Repeat("a", maxLogSize-1) + "中文",
			want: strings.Repeat("a", maxLogSize-1) + truncateMark,
		},
		{
			name: "fall back when the cut lands inside a 3-byte rune sequence",
			in:   strings.Repeat("中", maxLogSize/3+1),
			want: strings.Repeat("中", maxLogSize/3) + truncateMark,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SafeString(tt.in)
			if got != tt.want {
				t.Fatalf("SafeString(len %d) = len %d, want len %d", len(tt.in), len(got), len(tt.want))
			}
			if !utf8.ValidString(got) {
				t.Fatalf("truncated result is not valid UTF-8")
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
		{name: "returned as-is within limit", in: []byte("hello"), want: "hello"},
		{name: "not truncated when exactly at limit", in: bytes.Repeat([]byte("a"), maxLogSize), want: strings.Repeat("a", maxLogSize)},
		{name: "truncated with mark when over limit", in: bytes.Repeat([]byte("a"), maxLogSize+1), want: strings.Repeat("a", maxLogSize) + truncateMark},
		{
			name: "no invalid UTF-8 when multi-byte rune straddles the cut",
			in:   append(bytes.Repeat([]byte("a"), maxLogSize-1), []byte("中文")...),
			want: strings.Repeat("a", maxLogSize-1) + truncateMark,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SafeBody(tt.in)
			if got != tt.want {
				t.Fatalf("SafeBody(len %d) = len %d, want len %d", len(tt.in), len(got), len(tt.want))
			}
			if !utf8.ValidString(got) {
				t.Fatalf("truncated result is not valid UTF-8")
			}
		})
	}
}
