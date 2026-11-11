package docparse

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// htmlToMarkdown 把 HTML 解析为 markdown（x/net/html 树遍历的最小转换，
// 零外部设施）：标题元素转 ATX、段落转文本块、列表转无序/有序列表、
// 表格转 markdown 表格、pre 转围栏代码块；行内转换覆盖 strong/em/code/
// a/img/br。script / style / head 等非正文元素整棵跳过；div 等透明容器
// 递归展开。产物粒度是「块级元素一段」，供内容识别处理统一做规范化、
// 清洗与标题推断。纯脚本等非正文页面产出空串，由最短阈值拒绝。
func htmlToMarkdown(body []byte) (string, error) {
	root, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("docparse: parse html: %w", err)
	}
	w := &htmlMD{}
	w.walk(root)
	return strings.Join(w.blocks, "\n\n"), nil
}

// htmlMD 收集块级产物与块间散落的行内文本（body 直下的裸文本）。
type htmlMD struct {
	blocks []string
	inline strings.Builder
}

// htmlSkip 是整棵跳过的非正文元素。
var htmlSkip = map[string]bool{
	"head": true, "script": true, "style": true, "template": true,
	"noscript": true, "svg": true, "canvas": true, "iframe": true,
	"meta": true, "link": true, "title": true,
}

// htmlBlocks 是块级元素词表：进入即先落行内积压、再按元素语义产出。
var htmlBlocks = map[string]bool{
	"p": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true,
	"h6": true, "ul": true, "ol": true, "table": true, "pre": true,
	"blockquote": true, "hr": true, "dl": true, "figure": true,
}

// htmlTransparent 是递归展开的透明容器（不单独成块）。
var htmlTransparent = map[string]bool{
	"div": true, "section": true, "article": true, "main": true,
	"header": true, "footer": true, "nav": true, "aside": true,
	"span": true, "body": true, "html": true, "#document": true,
}

// walk 深度优先遍历：块级元素落块，透明容器递归，其余按行内积压。
func (w *htmlMD) walk(n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		w.node(c)
	}
}

// node 处理单个节点。
func (w *htmlMD) node(n *html.Node) {
	if n.Type == html.CommentNode || htmlSkip[n.Data] {
		return
	}
	if n.Type == html.TextNode {
		w.inline.WriteString(n.Data)
		return
	}
	if n.Type != html.ElementNode {
		return
	}
	name := strings.ToLower(n.Data)
	switch {
	case htmlSkip[name]:
		return
	case name == "br":
		w.inline.WriteString("\n")
		return
	case name == "hr":
		w.flushInline()
		w.blocks = append(w.blocks, "---")
		return
	case htmlHeading(name):
		w.flushInline()
		w.blocks = append(w.blocks, strings.Repeat("#", headingRank[name])+" "+mdHeadingText(htmlInlineOf(n)))
		return
	case name == "p":
		w.flushInline()
		if text := strings.TrimSpace(htmlInlineOf(n)); text != "" {
			w.blocks = append(w.blocks, text)
		}
		return
	case name == "pre":
		w.flushInline()
		if text := htmlRawText(n); strings.TrimSpace(text) != "" {
			w.blocks = append(w.blocks, "```\n"+strings.Trim(text, "\n")+"\n```")
		}
		return
	case name == "ul" || name == "ol":
		w.flushInline()
		if items := htmlListItems(n, name); items != "" {
			w.blocks = append(w.blocks, items)
		}
		return
	case name == "table":
		w.flushInline()
		if table := htmlTable(n); table != "" {
			w.blocks = append(w.blocks, table)
		}
		return
	case name == "blockquote":
		w.flushInline()
		if text := strings.TrimSpace(htmlInlineOf(n)); text != "" {
			w.blocks = append(w.blocks, "> "+text)
		}
		return
	case name == "li":
		// 游离列表项（宿主列表被跳过或结构残缺）按段落产出。
		w.flushInline()
		if text := strings.TrimSpace(htmlInlineOf(n)); text != "" {
			w.blocks = append(w.blocks, text)
		}
		return
	case htmlBlocks[name] || htmlTransparent[name]:
		// 透明容器与其余块级元素：递归展开（dl/figure 等无专门转换，
		// 子元素各自成块）。
		w.walk(n)
		return
	default:
		// 其余行内元素：展开子节点进行内积压（文本/强调/链接等由
		// 子节点路径处理，本元素自身无标记语义）。
		w.walk(n)
	}
}

// flushInline 把行内积压收敛为段落（非空才落块）。
func (w *htmlMD) flushInline() {
	if text := strings.TrimSpace(htmlCollapseSpace(w.inline.String())); text != "" {
		w.blocks = append(w.blocks, text)
	}
	w.inline.Reset()
}

// headingRank 是标题元素的 ATX 层级。
var headingRank = map[string]int{"h1": 1, "h2": 2, "h3": 3, "h4": 4, "h5": 5, "h6": 6}

// htmlHeading 报告元素是否标题。
func htmlHeading(name string) bool { return headingRank[name] > 0 }

// htmlInlineOf 渲染子树为行内 markdown：strong/b 加粗、em/i 斜体、code
// 反引号、a 链接、img 占位、br 换行，文本折叠空白。
func htmlInlineOf(n *html.Node) string {
	var b strings.Builder
	htmlInline(&b, n)
	return htmlCollapseSpace(b.String())
}

// htmlInline 追加单个节点的行内渲染。
func htmlInline(b *strings.Builder, n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch {
		case c.Type == html.CommentNode || htmlSkip[strings.ToLower(c.Data)]:
			continue
		case c.Type == html.TextNode:
			b.WriteString(c.Data)
		case c.Type != html.ElementNode:
			continue
		default:
			htmlInlineElement(b, c)
		}
	}
}

// htmlInlineElement 渲染行内元素及其子树。
func htmlInlineElement(b *strings.Builder, n *html.Node) {
	name := strings.ToLower(n.Data)
	switch name {
	case "strong", "b":
		inner := htmlInlineOf(n)
		if strings.TrimSpace(inner) != "" {
			b.WriteString("**" + inner + "**")
		}
	case "em", "i":
		inner := htmlInlineOf(n)
		if strings.TrimSpace(inner) != "" {
			b.WriteString("*" + inner + "*")
		}
	case "code":
		inner := htmlInlineOf(n)
		if inner != "" {
			b.WriteString("`" + inner + "`")
		}
	case "a":
		text := htmlCollapseSpace(htmlInlineOf(n))
		if href := htmlAttr(n, "href"); href != "" && strings.TrimSpace(text) != "" {
			b.WriteString("[" + text + "](" + href + ")")
		} else {
			b.WriteString(text)
		}
	case "img":
		alt := strings.TrimSpace(htmlAttr(n, "alt"))
		src := strings.TrimSpace(htmlAttr(n, "src"))
		if alt != "" || src != "" {
			b.WriteString("![" + alt + "](" + src + ")")
		}
	case "br":
		b.WriteString("\n")
	default:
		htmlInline(b, n)
	}
}

// htmlListItems 渲染列表：ul 项加 "- " 前缀、ol 项顺序编号；列表项内的
// 嵌套块平铺为项文本。无内容项跳过，全空列表产空串。
func htmlListItems(n *html.Node, kind string) string {
	var b strings.Builder
	index := 0
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || strings.ToLower(c.Data) != "li" {
			continue
		}
		text := strings.TrimSpace(htmlInlineOf(c))
		if text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		index++
		if kind == "ol" {
			b.WriteString(strconv.Itoa(index) + ". " + text)
		} else {
			b.WriteString("- " + text)
		}
	}
	return b.String()
}

// htmlTable 渲染表格为 markdown：首行为表头（thead 内行或首个 tr），
// 列数按表最大列数补齐，单元格文本折叠空白并转义竖线。无行产空串。
func htmlTable(n *html.Node) string {
	var rows [][]string
	var header []string
	collectTableRows(n, &header, &rows)
	if header == nil {
		return ""
	}
	return mdTable(append([][]string{header}, rows...))
}

// collectTableRows 按文档序收集表格内的 tr（直查或经 thead/tbody/tfoot
// 透传；首个 tr 为表头，其后为数据行）。
func collectTableRows(n *html.Node, header *[]string, rows *[][]string) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		switch strings.ToLower(c.Data) {
		case "tr":
			cells := htmlRowCells(c)
			if len(cells) == 0 {
				continue
			}
			if *header == nil {
				*header = cells
			} else {
				*rows = append(*rows, cells)
			}
		case "thead", "tbody", "tfoot":
			collectTableRows(c, header, rows)
		}
	}
}

// htmlRowCells 取一行的单元格文本。
func htmlRowCells(tr *html.Node) []string {
	var cells []string
	for c := tr.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && strings.ToLower(c.Data) == "td" || c.Type == html.ElementNode && strings.ToLower(c.Data) == "th" {
			cells = append(cells, mdCell(htmlCollapseSpace(htmlInlineOf(c))))
		}
	}
	return cells
}

// htmlRawText 取子树的原始文本（pre 不折叠空白）。
func htmlRawText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(c *html.Node) {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
			return
		}
		if c.Type != html.ElementNode || !htmlSkip[strings.ToLower(c.Data)] {
			for x := c.FirstChild; x != nil; x = x.NextSibling {
				walk(x)
			}
		}
	}
	walk(n)
	return b.String()
}

// htmlAttr 取元素属性值。
func htmlAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.ToLower(a.Key) == key {
			return a.Val
		}
	}
	return ""
}

// htmlCollapseSpace 折叠空白字符序列为单空格。
func htmlCollapseSpace(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
