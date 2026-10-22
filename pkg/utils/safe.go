// Package utils 提供与业务无关的通用工具函数。
package utils

import (
	"strings"
	"unicode/utf8"
)

// maxLogSize 是单个日志体的最大字节数，超出即截断，防止单条日志体积失控。
const maxLogSize = 4 * 1024

// truncateMark 追加在被截断的日志体末尾，明示内容不完整。
const truncateMark = "... [truncated]"

// truncateAtRuneBoundary 将 s 截断到至多 n 字节，截断点回退到最近的 rune 边界，
// 保证不因截断产生非法 UTF-8 序列。n 不小于 len(s) 时原样返回。
func truncateAtRuneBoundary(s string, n int) string {
	if n >= len(s) {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// SafeString 按日志上限截断字符串：字节数不超过 maxLogSize 时原样返回；
// 超长则截断到 rune 边界并追加 truncateMark。输入为合法 UTF-8 时返回值必为合法 UTF-8。
func SafeString(s string) string {
	if len(s) <= maxLogSize {
		return s
	}
	return truncateAtRuneBoundary(s, maxLogSize) + truncateMark
}

// SafeBody 将字节体转为可安全记录的日志字符串，截断契约同 SafeString。
func SafeBody(body []byte) string {
	return SafeString(string(body))
}

// MaskString 掩饰字符串中段：保留前 front 个与后 back 个 rune，中段以 mask 填充。
// 按 rune 计数与切分，Unicode 安全；front 或 back 为负时按 0 处理。
// 边界契约（n 为 s 的 rune 数）：
//   - n < front+back：两端无法同时保全，整体掩码，返回 mask；
//   - n == front+back：前后保留恰好覆盖全串、无中段可掩，原样返回；
//   - 其余：前 front 个 rune + mask + 后 back 个 rune。
func MaskString(s string, front, back int, mask string) string {
	if front < 0 {
		front = 0
	}
	if back < 0 {
		back = 0
	}
	runes := []rune(s)
	n := len(runes)
	switch {
	case n < front+back:
		return mask
	case n == front+back:
		return s
	}
	var b strings.Builder
	// 每 rune 至多 4 字节，据此预分配以免多次扩容
	b.Grow(front*utf8.UTFMax + len(mask) + back*utf8.UTFMax)
	for _, r := range runes[:front] {
		b.WriteRune(r)
	}
	b.WriteString(mask)
	for _, r := range runes[n-back:] {
		b.WriteRune(r)
	}
	return b.String()
}

// MaskString4 是 MaskString 的便捷封装，掩码固定为四个星号。
func MaskString4(s string, front, back int) string {
	return MaskString(s, front, back, "****")
}
