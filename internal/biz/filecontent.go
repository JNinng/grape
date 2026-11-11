package biz

import (
	"bytes"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	gmtext "github.com/yuin/goldmark/text"
	"golang.org/x/net/html/charset"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/simplifiedchinese"
	textunicode "golang.org/x/text/encoding/unicode"
)

// 内容识别处理的边界（解析产物入库前、切分之前，纯文本处理）：
//   - 编码与规范化：BOM 优先（UTF-8 / UTF-16 LE / BE）；无 BOM 先以 UTF-8
//     严格性判定（合法 UTF-8 直接收编），非法时经 x/net/html/charset 判定
//     （meta 声明 certain 即采用），不确定按中文场景优先 GB18030（GBK /
//     GB2312 的超集）转码 → 统一 UTF-8；转码后统计不可读字符（U+FFFD、
//     控制符）占比，超阈值按乱码拒绝（乱码文本 / OCR 失败 / 加密 PDF 的
//     兜底信号）；换行规范化（CRLF / 孤 CR → LF）、行尾空白、3+ 连续空行
//     折叠为段落空行。
//   - 正文清洗：页眉页脚页码水印以解析器产出为准，Go 侧只做规则级兜底——
//     围栏内代码逐字保留（闭合围栏由扫描定界；残缺围栏降级普通文本参与
//     清洗），围栏外剔除连续重复行与孤立数字行。
//   - 标题层级识别：解析器已带 # 层级的行直接采用（goldmark AST 遍历收集，
//     不重排已有层级）；缺失处按编号模式（第X章 / 一、 / （一） / 1.1 多级
//     数字）与孤行特征推断补齐，跳级按运行栈钳制——这是 heading_path 的
//     真正产出点。单级数字前缀（"1. "/"1、"）与 Markdown 有序列表同形，
//     不参与推断，避免吞列表项。
//   - 清洗后低于最短阈值按摄取失败处理（空文档或解析产物退化为噪声）。
const (
	// maxUnreadableRatio 是转码后不可读字符占比上限（超过即判乱码）。
	maxUnreadableRatio = 0.05
	// minFileContentRunes 是清洗后正文的最短 rune 阈值。
	minFileContentRunes = 32
	// maxHeadingRunes 是孤行标题的长度上限（超长编号行按正文处理）。
	maxHeadingRunes = 64
)

// prepareFileContent 是内容识别处理的主入口：解码 → 规范化 → 清洗 →
// 标题推断 → 最短阈值。输入是原始文件字节（direct 档位）或解析器的
// markdown 产物（解析产物已是 UTF-8，解码为无伤直通，规范化 / 清洗 /
// 推断照常生效）。任一步失败以错误上抛，由摄取编排折叠为失败留痕
// （目录行保留、可重解析）。
func prepareFileContent(raw []byte) (string, error) {
	text, err := decodeText(raw)
	if err != nil {
		return "", err
	}
	text = normalizeLines(text)
	text = cleanLines(text)
	text = strings.TrimSpace(inferHeadings(text))
	if runes := utf8.RuneCountInString(text); runes < minFileContentRunes {
		return "", fmt.Errorf("%w: cleaned content has %d runes, min %d", ErrFileContentTooShort, runes, minFileContentRunes)
	}
	return text, nil
}

// decodeText 把原始字节统一为 UTF-8 文本并做乱码兜底：不可读字符占比超
// maxUnreadableRatio 按 ErrFileContentUnreadable 拒绝。
func decodeText(raw []byte) (string, error) {
	text, err := decodeToUTF8(raw)
	if err != nil {
		return "", fmt.Errorf("biz: decode file content: %w", err)
	}
	if ratio := unreadableRatio(text); ratio > maxUnreadableRatio {
		return "", fmt.Errorf("%w: unreadable rune ratio %.2f exceeds %.2f", ErrFileContentUnreadable, ratio, maxUnreadableRatio)
	}
	return text, nil
}

// byte-order marks（按序优先判定）。
var (
	bomUTF8    = []byte{0xEF, 0xBB, 0xBF}
	bomUTF16LE = []byte{0xFF, 0xFE}
	bomUTF16BE = []byte{0xFE, 0xFF}
)

// decodeToUTF8 选出解码器并转码；decoderFor 返回 nil 表示字节已是
// UTF-8（直通）。UTF-16 的 BOM 声明与内容不符属构造数据，解码产出按
// 乱码率兜底，不做二次判定。
func decodeToUTF8(raw []byte) (string, error) {
	dec, body := decoderFor(raw)
	if dec == nil {
		return string(body), nil
	}
	return decodeBytes(dec, body)
}

// decoderFor 按 BOM 优先、UTF-8 严格性、charset 判定的顺序选解码器：
// UTF-8 BOM 剥离后内容仍须合法，否则按无 BOM 字节继续判定；无 BOM 且
// 非法 UTF-8 时 charset 判定（meta 声明 certain 即采用），不确定回落
// GB18030（中文场景优先）。
func decoderFor(raw []byte) (encoding.Encoding, []byte) {
	if bytes.HasPrefix(raw, bomUTF8) {
		raw = raw[len(bomUTF8):]
		if utf8.Valid(raw) {
			return nil, raw
		}
	} else if bytes.HasPrefix(raw, bomUTF16LE) {
		return textunicode.UTF16(textunicode.LittleEndian, textunicode.IgnoreBOM), raw[len(bomUTF16LE):]
	} else if bytes.HasPrefix(raw, bomUTF16BE) {
		return textunicode.UTF16(textunicode.BigEndian, textunicode.IgnoreBOM), raw[len(bomUTF16BE):]
	}
	if utf8.Valid(raw) {
		return nil, raw
	}
	enc, _, certain := charset.DetermineEncoding(raw, "")
	if !certain {
		enc = simplifiedchinese.GB18030
	}
	return enc, raw
}

// decodeBytes 以替换模式转码（非法序列产出 U+FFFD 而非报错），不可读
// 占比由调用方统计裁决。
func decodeBytes(enc encoding.Encoding, raw []byte) (string, error) {
	out, err := enc.NewDecoder().Bytes(raw)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// unreadableRatio 统计不可读字符占比：U+FFFD（转码替换产物）、C0 控制符
// （常用空白除外）与 C1 控制符（错误转码的典型残留）。
func unreadableRatio(text string) float64 {
	total := utf8.RuneCountInString(text)
	if total == 0 {
		return 0
	}
	unreadable := 0
	for _, r := range text {
		if isUnreadableRune(r) {
			unreadable++
		}
	}
	return float64(unreadable) / float64(total)
}

// isUnreadableRune 判定单个 rune 是否不可读。
func isUnreadableRune(r rune) bool {
	switch r {
	case '\t', '\n', '\f', '\r':
		return false
	}
	return r == unicode.ReplacementChar || (r >= 0x00 && r < 0x20) || r == 0x7F || (r >= 0x80 && r <= 0x9F)
}

// normalizeLines 规范化换行与空白：CRLF / 孤 CR → LF、剥离残留 BOM 字符、
// 去行尾空白、3+ 连续空行折叠为段落空行（保留段落边界语义）。
func normalizeLines(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.TrimPrefix(text, "\uFEFF")
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	var b strings.Builder
	b.Grow(len(text))
	blank := 0
	for i, line := range lines {
		if line == "" {
			blank++
			if blank > 2 {
				continue
			}
		} else {
			blank = 0
		}
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
	}
	return b.String()
}

// cleanLines 规则级正文清洗（页眉页脚页码水印的兜底）：闭合围栏内逐字
// 保留；残缺围栏（无闭行）降级普通文本参与清洗；围栏外剔除孤立数字行
// （页码）与连续重复行（重复页眉页脚）。
func cleanLines(text string) string {
	lines := strings.Split(text, "\n")
	fenced := fencedRegions(lines)
	out := make([]string, 0, len(lines))
	for i, line := range lines {
		if fenced[i] {
			out = append(out, line)
			continue
		}
		if isOrphanNumberLine(line) {
			continue
		}
		if trimmed := strings.TrimSpace(line); trimmed != "" && len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == trimmed {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// fencedRegions 标记闭合围栏覆盖的行（含开闭行）。围栏须能向后找到闭行
// 才生效——残缺围栏（常见于清洗目标本身：解析器把版面元素误作围栏）不
// 吞掉其后全部正文，整段降级普通文本参与清洗。
func fencedRegions(lines []string) []bool {
	mask := make([]bool, len(lines))
	inFence := false
	var fenceChar rune
	var fenceLen int
	for i, line := range lines {
		stripped := stripIndent(line)
		if inFence {
			mask[i] = true
			if isFenceClose(stripped, fenceChar, fenceLen) {
				inFence = false
			}
			continue
		}
		if char, n, ok := fenceOpen(stripped); ok && closesAhead(lines, i+1, char, n) {
			inFence, fenceChar, fenceLen = true, char, n
			mask[i] = true
		}
	}
	return mask
}

// closesAhead 判定 from 起是否存在给定围栏的闭行。
func closesAhead(lines []string, from int, char rune, minLen int) bool {
	for j := from; j < len(lines); j++ {
		if isFenceClose(stripIndent(lines[j]), char, minLen) {
			return true
		}
	}
	return false
}

// isOrphanNumberLine 判定孤立数字行：至多由一组至多 8 位数字与装饰符
// （- – — 空格）组成且至少含一个数字（页码水印的兜底信号；正文数字
// 行总是带有其他文字或标点）。
func isOrphanNumberLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	digits := 0
	for _, r := range trimmed {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r == '-' || r == '–' || r == '—' || r == ' ':
		default:
			return false
		}
	}
	return digits > 0 && digits <= 8
}

// inferHeadings 标题层级识别：goldmark AST 收集既有标题行（直接采用、
// 不重排层级），其余行按编号模式 + 孤行特征推断补齐，层级入运行栈钳制
// 跳级。孤行特征 = 上一行为空行或标题（既有或推断，中文公文的三级连排
// 行间常无空行）、行长 ≤ maxHeadingRunes、行尾无句读；4 空格起头的缩进
// 码块行不参与。
func inferHeadings(text string) string {
	lines := strings.Split(text, "\n")
	fenced := fencedRegions(lines)
	existing := goldmarkHeadingLines(lines)
	levels := make([]int, 0, 8)
	out := make([]string, len(lines))
	copy(out, lines)
	prevBlank, prevHeading := true, false
	for i, line := range lines {
		switch {
		case fenced[i]:
			prevBlank, prevHeading = false, false
			continue
		case strings.TrimSpace(line) == "":
			prevBlank, prevHeading = true, false
			continue
		case existing[i]:
			pushHeadingLevel(&levels, headingLevelOfLine(lines, i), false)
			prevBlank, prevHeading = false, true
			continue
		case strings.HasPrefix(line, "    "):
			// 缩进码块行（4 空格起头）不参与推断。
			prevBlank, prevHeading = false, false
			continue
		}
		trimmed := strings.TrimSpace(line)
		level, ok := inferredHeadingLevel(trimmed)
		if !ok || !(prevBlank || prevHeading) || utf8.RuneCountInString(trimmed) > maxHeadingRunes || endsWithSentencePunct(trimmed) {
			prevBlank, prevHeading = false, false
			continue
		}
		out[i] = strings.Repeat("#", pushHeadingLevel(&levels, level, true)) + " " + trimmed
		prevBlank, prevHeading = false, true
	}
	return strings.Join(out, "\n")
}

// headingLevelOfLine 取既有标题行的层级：ATX 取 # 数量；Setext 取下划线
// 档位（=== 一级 / --- 二级）；形状不符按一级兜底（运行栈只求单调可用，
// 不追求精确）。
func headingLevelOfLine(lines []string, i int) int {
	if level, _, ok := atxHeading(stripIndent(lines[i])); ok {
		return level
	}
	if i+1 < len(lines) {
		if level, ok := setextUnderline(stripIndent(lines[i+1])); ok {
			return level
		}
	}
	return 1
}

// pushHeadingLevel 维护标题运行栈：弹出层级不小于新层级的栈尾后入栈。
// clamp 为 true 时把推断层级钳制到栈深 + 1（缺失或跳级补齐），既有标题
// 不钳制（解析器产出原样采用）；层级上限 6（ATX 上限）。
func pushHeadingLevel(levels *[]int, level int, clamp bool) int {
	if clamp {
		if max := len(*levels) + 1; level > max {
			level = max
		}
	}
	if level > 6 {
		level = 6
	}
	for len(*levels) > 0 && (*levels)[len(*levels)-1] >= level {
		*levels = (*levels)[:len(*levels)-1]
	}
	*levels = append(*levels, level)
	return level
}

// goldmarkHeadingLines 用 goldmark 解析并遍历 AST，收集被识别为标题的行
// 序号（ATX 与 Setext 一并覆盖）。goldmark 与切分实现的容器口径差异不影
// 响本用途：这里只回答「哪些行已是标题」，推断只处理剩余行。
func goldmarkHeadingLines(lines []string) map[int]bool {
	source := []byte(strings.Join(lines, "\n"))
	starts := make(map[int]bool)
	doc := goldmark.New().Parser().Parse(gmtext.NewReader(source))
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if h, ok := n.(*ast.Heading); ok && h.Lines() != nil && h.Lines().Len() > 0 {
			starts[lineIndexOf(source, h.Lines().At(0).Start)] = true
		}
		return ast.WalkContinue, nil
	})
	return starts
}

// lineIndexOf 把字节偏移换算为行序号（偏移落在换行符上取其所在行）。
func lineIndexOf(source []byte, offset int) int {
	if offset > len(source) {
		offset = len(source)
	}
	line := 0
	for i := 0; i < offset; i++ {
		if source[i] == '\n' {
			line++
		}
	}
	return line
}

// inferredHeadingLevel 识别编号模式与对应层级：
//   - 第X章 / 第X节 / 第X篇 / 第X部（中文数字或阿拉伯数字）→ 1；
//   - 一、（中文数字 + 顿号）→ 2；
//   - （一）/ (一)（括号中文数字）→ 3；
//   - 多级数字路径 1.1 / 2.3.4（≥2 组数字）→ 数字组数；
//   - 单级数字前缀（"1. "/"1、"）与 Markdown 有序列表同形，不识别。
func inferredHeadingLevel(line string) (int, bool) {
	if chapterHeading(line) {
		return 1, true
	}
	if chineseEnumHeading(line) {
		return 2, true
	}
	if parenChineseHeading(line) {
		return 3, true
	}
	if groups, ok := numericPathHeading(line); ok && groups >= 2 {
		return groups, true
	}
	return 0, false
}

// chapterHeading 判定「第X章/节/篇/部」模式（X 为中文数字或 ASCII 数字，
// 后随标题文字）。
func chapterHeading(line string) bool {
	rs := []rune(line)
	if len(rs) < 3 || rs[0] != '第' {
		return false
	}
	i := 1
	for i < len(rs) && (isChineseNumeral(rs[i]) || (rs[i] >= '0' && rs[i] <= '9')) {
		i++
	}
	if i == 1 || i >= len(rs) {
		return false
	}
	switch rs[i] {
	case '章', '节', '篇', '部':
		return i+1 < len(rs) && rs[i+1] != '、'
	}
	return false
}

// chineseEnumHeading 判定「一、」模式（中文数字 + 顿号；ASCII 数字不算，
// "1、" 与有序列表同形）。
func chineseEnumHeading(line string) bool {
	rs := []rune(line)
	i := 0
	for i < len(rs) && isChineseNumeral(rs[i]) {
		i++
	}
	return i > 0 && i < len(rs) && rs[i] == '、'
}

// parenChineseHeading 判定「（一）」/「(一)」模式（括号中文数字）。
func parenChineseHeading(line string) bool {
	rs := []rune(line)
	if len(rs) < 3 || (rs[0] != '（' && rs[0] != '(') {
		return false
	}
	i := 1
	for i < len(rs) && isChineseNumeral(rs[i]) {
		i++
	}
	if i == 1 || i >= len(rs) {
		return false
	}
	return rs[i] == '）' || rs[i] == ')'
}

// numericPathHeading 判定多级数字路径（1.1 / 2.3.1，组间以单点相连），
// 返回数字组数。多级编号的每段都是个位序号：含多位数字组的形态按小数
// 等正文处理（"3.14 是圆周率的常用近似值" 不识别），两位章节号（10.2）
// 因此也不再识别；路径尾紧随数字时视为截断的小数路径同样拒绝（"3.14"
// 的路径前缀 3.1 后残留 4）。组数超过 5 按编号异常拒绝。剩余已知误判
// 面：全个位组的小数与数量前缀（"3.5 ..." / "1.5TB ..."）仍与二级标题
// 同形，行级无法区分，按标题识别。
func numericPathHeading(line string) (int, bool) {
	rs := []rune(line)
	i := 0
	groups := 0
	for groups < 5 {
		if i >= len(rs) || rs[i] < '0' || rs[i] > '9' {
			return 0, false
		}
		i++
		groups++
		// 组后必须单点相连且下一字符仍是数字，否则路径结束。
		if i < len(rs) && rs[i] == '.' && i+1 < len(rs) && rs[i+1] >= '0' && rs[i+1] <= '9' {
			i++
			continue
		}
		break
	}
	if i < len(rs) && rs[i] >= '0' && rs[i] <= '9' {
		return 0, false
	}
	return groups, groups >= 2
}

// isChineseNumeral 判定中文数字字符（不含「第」等结构字）。
func isChineseNumeral(r rune) bool {
	switch r {
	case '一', '二', '三', '四', '五', '六', '七', '八', '九', '十', '百', '千', '万', '零', '〇':
		return true
	}
	return false
}

// endsWithSentencePunct 判定行尾句读（有句读的行是正文陈述，不是标题）。
func endsWithSentencePunct(line string) bool {
	rs := []rune(strings.TrimRight(line, " \t"))
	if len(rs) == 0 {
		return false
	}
	switch rs[len(rs)-1] {
	case '。', '！', '？', '；', '，', '…', '.', '!', '?', ';', ',':
		return true
	}
	return false
}
