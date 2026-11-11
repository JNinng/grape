package biz

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/unicode"
)

// 内容识别处理的用例边界：编码判定（BOM 优先 / UTF-8 严格性 / GB18030
// 回落）、乱码兜底（不可读占比阈值拒绝）、换行规范化、规则级清洗（围栏
// 保留 / 孤立数字行 / 重复行）、标题层级推断（编号模式 + 孤行特征 + 跳级
// 钳制）与最短阈值拒绝。

func TestPrepareFileContent_utf8_passthrough_with_normalization(t *testing.T) {
	raw := "\uFEFF第一段内容，用于满足最短阈值的要求，补充更多文字确保达标。\r\n\r\n\r\n\r\n第二段内容，同样有足够长度。\n"
	out, err := prepareFileContent([]byte(raw))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(out, "\r") || strings.Contains(out, "\uFEFF") {
		t.Fatalf("output keeps CR or BOM: %q", out)
	}
	if !strings.Contains(out, "\n\n第二段内容，同样有足够长度。") {
		t.Fatalf("blank run not folded to paragraph break: %q", out)
	}
}

func TestPrepareFileContent_utf8_bom_and_utf16_bom_decoded(t *testing.T) {
	utf8BOM := append([]byte{0xEF, 0xBB, 0xBF}, []byte("带 BOM 的 UTF-8 正文文本，这一段长度满足最短阈值要求，可被直接收编。")...)
	out, err := prepareFileContent(utf8BOM)
	if err != nil || strings.Contains(out, "�") {
		t.Fatalf("err = %v, out = %q", err, out)
	}

	utf16 := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewEncoder()
	raw, err := utf16.Bytes([]byte("带 BOM 的 UTF-16 正文文本，这一段长度满足最短阈值要求，解码后应完整还原。"))
	if err != nil {
		t.Fatalf("encode utf16: %v", err)
	}
	out, err = prepareFileContent(raw)
	if err != nil || !strings.Contains(out, "UTF-16 正文") {
		t.Fatalf("err = %v, out = %q", err, out)
	}
}

func TestPrepareFileContent_utf16be_bom_decoded(t *testing.T) {
	// UTF-16BE BOM（FE FF）：编码链按 BOM 优先判定 BigEndian，BE 与 LE
	// 同为白名单形态。
	enc := unicode.UTF16(unicode.BigEndian, unicode.UseBOM).NewEncoder()
	raw, err := enc.Bytes([]byte("带 BOM 的 UTF-16BE 正文文本，这一段长度满足最短阈值要求，解码后应完整还原。"))
	if err != nil {
		t.Fatalf("encode utf16be: %v", err)
	}
	out, err := prepareFileContent(raw)
	if err != nil || !strings.Contains(out, "UTF-16BE 正文") {
		t.Fatalf("err = %v, out = %q", err, out)
	}
}

func TestPrepareFileContent_gb18030_without_bom_decoded(t *testing.T) {
	// 无 BOM 且非法 UTF-8：charset 判定不确定时按中文场景回落 GB18030
	//（GBK / GB2312 的超集）。
	raw, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte("中文内容：这是一段用于验证编码判定链路的正文，没有字节序标记也能正确转码。"))
	if err != nil {
		t.Fatalf("encode gb18030: %v", err)
	}
	if utf8.Valid(raw) {
		t.Fatalf("fixture is valid utf-8; encoding case broken")
	}
	out, err := prepareFileContent(raw)
	if err != nil || !strings.Contains(out, "编码判定链路") {
		t.Fatalf("err = %v, out = %q", err, out)
	}
}

func TestPrepareFileContent_garbled_text_rejected_by_unreadable_ratio(t *testing.T) {
	// 构造乱码字节：UTF-16LE BOM + 未配对代理项（解码产出成串 U+FFFD），
	// 不可读占比超过阈值按 ErrFileContentUnreadable 拒绝。
	raw := []byte{0xFF, 0xFE, 0x00, 0xD8, 0x00, 0xD8, 0x00, 0xD8, 0x00, 0xD8,
		0x00, 0xD8, 0x00, 0xD8, 0x00, 0xD8, 0x00, 0xD8, 0x00, 0xD8}
	if _, err := prepareFileContent(raw); !errors.Is(err, ErrFileContentUnreadable) {
		t.Fatalf("err = %v, want unreadable rejection", err)
	}
}

func TestPrepareFileContent_too_short_cleaned_content_rejected(t *testing.T) {
	if _, err := prepareFileContent([]byte("短文本")); !errors.Is(err, ErrFileContentTooShort) {
		t.Fatalf("err = %v, want too short rejection", err)
	}
}

func TestPrepareFileContent_cleans_orphan_numbers_and_duplicate_lines_outside_fences(t *testing.T) {
	raw := strings.Join([]string{
		"页眉标题文本",
		"页眉标题文本", // 连续重复行剔除
		"12",     // 孤立数字行（页码）剔除
		"- 3 -",  // 装饰页码剔除
		"正文段落，提供足够的可读字符以满足最短阈值要求。",
		"正文段落，提供足够的可读字符以满足最短阈值要求。", // 重复行剔除
		"", "", "", "",
		"后续正文，保证清洗后仍有足够长度不被最短阈值拒绝。",
	}, "\n")
	out, err := prepareFileContent([]byte(raw))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got := strings.Count(out, "页眉标题文本"); got != 1 {
		t.Fatalf("duplicate header lines kept %d times:\n%s", got, out)
	}
	if strings.Contains(out, "\n12\n") || strings.Contains(out, "- 3 -") {
		t.Fatalf("orphan number lines survived:\n%s", out)
	}
	if got := strings.Count(out, "正文段落"); got != 1 {
		t.Fatalf("duplicate body lines kept %d times:\n%s", got, out)
	}
}

func TestPrepareFileContent_keeps_closed_fence_content_verbatim(t *testing.T) {
	codeLine := "if (x == 12) { return; }"
	raw := strings.Join([]string{
		" fenced text before, with enough length to pass the threshold check",
		"```text",
		codeLine,
		"12",
		"```",
		"封闭围栏之后的正文，长度同样满足最短阈值的要求。",
	}, "\n")
	out, err := prepareFileContent([]byte(raw))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(out, codeLine) || !strings.Contains(out, "\n12\n") {
		t.Fatalf("fenced content was cleaned:\n%s", out)
	}
}

func TestPrepareFileContent_unclosed_fence_degrades_to_cleaned_text(t *testing.T) {
	raw := strings.Join([]string{
		"```text",
		"12",
		"围栏未闭合：其后文本降级参与清洗，孤立数字行应被剔除。",
		"后续正文，长度满足最短阈值。",
	}, "\n")
	out, err := prepareFileContent([]byte(raw))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(out, "\n12\n") {
		t.Fatalf("orphan number line survived unclosed fence:\n%s", out)
	}
}

func TestPrepareFileContent_infers_numbered_headings(t *testing.T) {
	raw := strings.Join([]string{
		"第一章 总体要求",
		"",
		"一、指导思想",
		"",
		"（一）基本原则",
		"",
		"本节正文，长度满足最短阈值。",
		"",
		"2.3 数据口径",
		"",
		"正文继续，长度满足最短阈值。",
	}, "\n")
	out, err := prepareFileContent([]byte(raw))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{
		"# 第一章 总体要求",
		"## 一、指导思想",
		"### （一）基本原则",
		"## 2.3 数据口径",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing inferred heading %q:\n%s", want, out)
		}
	}
}

func TestPrepareFileContent_clamps_skipped_heading_levels(t *testing.T) {
	raw := strings.Join([]string{
		"第一章 总体要求",
		"",
		"（一）基本原则",
		"",
		"正文，长度满足最短阈值。",
	}, "\n")
	out, err := prepareFileContent([]byte(raw))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	// 跳级钳制：第X章（一级）之后的（一）（推断三级）钳到二级。
	if !strings.Contains(out, "## （一）基本原则") {
		t.Fatalf("skip level not clamped:\n%s", out)
	}
}

func TestPrepareFileContent_infers_consecutive_chinese_headings_without_blank_lines(t *testing.T) {
	// 中文公文最常见形态：三级标题行间无空行连排，上一行是标题即满足孤行
	// 特征（heading_path 为 第X章 → 一、 → （一））。
	raw := strings.Join([]string{
		"第一章 总则",
		"一、背景与目标",
		"（一）总体要求",
		"连排正文紧随其后，长度满足最短阈值要求，保证不被拒绝。",
	}, "\n")
	out, err := prepareFileContent([]byte(raw))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{
		"# 第一章 总则",
		"## 一、背景与目标",
		"### （一）总体要求",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing inferred heading %q:\n%s", want, out)
		}
	}
}

func TestPrepareFileContent_infers_consecutive_numeric_path_headings_without_blank_lines(t *testing.T) {
	// 多级数字路径连排：2.3 与 2.3.1 行间无空行，层级随数字组数递进
	// （首行 第X章 作一级锚点，避免跳级钳制把首个推断标题钳到一级）。
	raw := strings.Join([]string{
		"第一章 数据说明",
		"2.3 数据口径",
		"2.3.1 适用范围",
		"连排正文紧随其后，长度满足最短阈值要求，保证不被拒绝。",
	}, "\n")
	out, err := prepareFileContent([]byte(raw))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{
		"# 第一章 数据说明",
		"## 2.3 数据口径",
		"### 2.3.1 适用范围",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing inferred heading %q:\n%s", want, out)
		}
	}
}

func TestPrepareFileContent_rejects_decimal_line_but_keeps_multi_level_path(t *testing.T) {
	// 全个位组的多级路径（2.3.1）识别；含多位数字组的小数形态（3.14）
	// 不识别（首行为孤行位置且无句读，拒绝只能来自数字组收紧）。识别的
	// 2.3.1 位于 第X章 之后按跳级钳制渲染为二级。
	raw := strings.Join([]string{
		"3.14 是圆周率的常用近似值",
		"",
		"第一章 总体说明",
		"2.3.1 适用范围说明",
		"结尾正文，长度满足最短阈值要求。",
	}, "\n")
	out, err := prepareFileContent([]byte(raw))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(out, "# 3.14") {
		t.Fatalf("decimal line inferred as heading:\n%s", out)
	}
	if !strings.Contains(out, "## 2.3.1 适用范围说明") {
		t.Fatalf("multi-level path not inferred:\n%s", out)
	}
}

func TestPrepareFileContent_does_not_infer_list_like_or_body_lines(t *testing.T) {
	raw := strings.Join([]string{
		"1. 列表项一（单级数字与有序列表同形，不推断）",
		"2. 列表项二",
		"",
		"这是带句读的陈述行，不应识别为标题。",
		"这是紧邻上一行的编号行：2.3 不满足孤行特征。",
		"",
		"结尾正文，长度满足最短阈值。",
	}, "\n")
	out, err := prepareFileContent([]byte(raw))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(out, "# 1. 列表项一") {
		t.Fatalf("ordered list item inferred as heading:\n%s", out)
	}
	if strings.Contains(out, "# 这是带句读的陈述行") {
		t.Fatalf("sentence line inferred as heading:\n%s", out)
	}
	if strings.Contains(out, "# 这是紧邻上一行的编号行") {
		t.Fatalf("non-orphan line inferred as heading:\n%s", out)
	}
}

func TestPrepareFileContent_keeps_existing_markdown_headings_verbatim(t *testing.T) {
	raw := strings.Join([]string{
		"# 已有一级标题",
		"",
		"一、推断标题",
		"",
		"正文，长度满足最短阈值。",
	}, "\n")
	out, err := prepareFileContent([]byte(raw))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(out, "# 已有一级标题") || !strings.Contains(out, "## 一、推断标题") {
		t.Fatalf("existing heading or inferred sibling wrong:\n%s", out)
	}
}

func TestPrepareFileContent_does_not_infer_inside_fences_or_indent_blocks(t *testing.T) {
	raw := strings.Join([]string{
		"围栏外的说明文本，长度满足最短阈值。",
		"",
		"```",
		"一、代码块内的文本不是标题",
		"```",
		"",
		"    一、缩进码块内的文本不是标题",
		"",
		"结尾正文，长度满足最短阈值。",
	}, "\n")
	out, err := prepareFileContent([]byte(raw))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(out, "# 一、") {
		t.Fatalf("fenced or indent-block line inferred as heading:\n%s", out)
	}
}
