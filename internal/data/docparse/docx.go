package docparse

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// docxToMarkdown 手解 OOXML（archive/zip + encoding/xml，零外部设施）：
// word/document.xml 的段落与表格按文档顺序产出 markdown——标题样式段落
// 转 ATX 标题（Heading1–6，Title/Subtitle 归一），编号段落转无序列表项
// （不重建编号层级），表格转 markdown 表格（首行表头，单元格内段落以
// 空格相连）。正文之外的部分（页眉页脚、脚注、批注）不解析——页眉页脚
// 由 biz 内容识别处理的规则级兜底对解析产物生效，本解析器不把它们引进
// 正文。token 流按本地名匹配（忽略命名空间 URI 与前缀），兼容 OOXML
// 各方言的元素命名。
func docxToMarkdown(body []byte) (string, error) {
	reader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return "", fmt.Errorf("docparse: open docx container: %w", err)
	}
	entry, err := docxEntry(reader, "word/document.xml")
	if err != nil {
		return "", err
	}
	p := &docxParser{dec: xml.NewDecoder(entry)}
	if err := p.parseBody(); err != nil {
		return "", fmt.Errorf("docparse: decode word/document.xml: %w", err)
	}
	return strings.Join(p.blocks, "\n\n"), nil
}

// docxEntry 按 path 读取 zip entry（缺失按容器损坏报错）。
func docxEntry(reader *zip.Reader, name string) (io.Reader, error) {
	for _, f := range reader.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("docparse: open %s: %w", name, err)
			}
			return rc, nil
		}
	}
	return nil, fmt.Errorf("docparse: docx container misses %s", name)
}

// docxParser 是 document.xml 的流式解析器：块级产物按文档顺序收集。
type docxParser struct {
	dec    *xml.Decoder
	blocks []string
}

// localName 取元素本地名（忽略命名空间前缀）。
func localName(name xml.Name) string {
	if at := strings.IndexByte(name.Local, ':'); at >= 0 {
		return name.Local[at+1:]
	}
	return name.Local
}

// parseBody 解析 body 的块级子元素：p 段落、tbl 表格，其余元素整体跳过
// （sectPr 等版式事实不进正文）。
func (p *docxParser) parseBody() error {
	for {
		tok, err := p.dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch localName(t.Name) {
			case "p":
				line, err := p.parseParagraph()
				if err != nil {
					return err
				}
				if line != "" {
					p.blocks = append(p.blocks, line)
				}
			case "tbl":
				rows, err := p.parseTable()
				if err != nil {
					return err
				}
				if rows != nil {
					p.blocks = append(p.blocks, mdTable(rows))
				}
			case "document", "body":
				// 宿主容器透明处理（子元素即块级序列）。
			default:
				if err := p.skipElement(&t); err != nil {
					return err
				}
			}
		case xml.EndElement:
			// body 结束即完成；document 结束同样收敛。
			switch localName(t.Name) {
			case "body", "document":
				return nil
			}
		}
	}
}

// skipElement 消费到与 start 匹配的结束元素（含嵌套）。
func (p *docxParser) skipElement(start *xml.StartElement) error {
	depth := 1
	for depth > 0 {
		tok, err := p.dec.Token()
		if err != nil {
			return err
		}
		switch tok.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		}
	}
	return nil
}

// headingStylePattern 匹配 OOXML 内置标题样式 id：Heading1–6（大小写不
// 敏感）。
var headingStylePattern = regexp.MustCompile(`(?i)^heading([1-6])$`)

// docxHeadingLevel 由段落样式 id 求标题层级：Heading1–6 直接映射；Title
// 归一级、Subtitle 归二级；其余样式（正文、列表等）返回 0。
func docxHeadingLevel(style string) int {
	if m := headingStylePattern.FindStringSubmatch(strings.TrimSpace(style)); m != nil {
		level, _ := strconv.Atoi(m[1])
		return level
	}
	switch strings.TrimSpace(style) {
	case "Title":
		return 1
	case "Subtitle":
		return 2
	}
	return 0
}

// parseParagraph 解析单个段落，产出 markdown 行：标题样式转 ATX 前缀、
// 编号段落转无序列表项、其余为普通文本行；空段落产空串。文本之外的
// 段落属性在 pPr 内读取。
func (p *docxParser) parseParagraph() (string, error) {
	var text strings.Builder
	style := ""
	numbered := false
	for {
		tok, err := p.dec.Token()
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch localName(t.Name) {
			case "pPr":
				if err := p.parseParagraphProps(&style, &numbered); err != nil {
					return "", err
				}
			case "r":
				if err := p.parseRun(&text); err != nil {
					return "", err
				}
			case "hyperlink":
				// 超链接取锚文本（链接目标不进正文——检索不消费 URL，
				// 正文纯净优先）。
				if err := p.parseRuns(&text); err != nil {
					return "", err
				}
			default:
				if err := p.skipElement(&t); err != nil {
					return "", err
				}
			}
		case xml.EndElement:
			if localName(t.Name) == "p" {
				trimmed := strings.TrimSpace(text.String())
				if trimmed == "" {
					return "", nil
				}
				if level := docxHeadingLevel(style); level > 0 {
					return strings.Repeat("#", level) + " " + mdHeadingText(trimmed), nil
				}
				if numbered {
					return "- " + trimmed, nil
				}
				return trimmed, nil
			}
		}
	}
}

// parseParagraphProps 读取段落属性：样式 id 与编号标记，至 pPr 结束。
func (p *docxParser) parseParagraphProps(style *string, numbered *bool) error {
	for {
		tok, err := p.dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch localName(t.Name) {
			case "pStyle":
				*style = attrVal(t, "val")
				if err := p.skipElement(&t); err != nil {
					return err
				}
			case "numPr":
				*numbered = true
				if err := p.skipElement(&t); err != nil {
					return err
				}
			default:
				if err := p.skipElement(&t); err != nil {
					return err
				}
			}
		case xml.EndElement:
			if localName(t.Name) == "pPr" {
				return nil
			}
		}
	}
}

// parseRuns 消费运行内容至宿主结束元素：r 文本、hyperlink 锚文本，以及
// 表格单元格内的段落（p 以空格连接进单元格文本）。
func (p *docxParser) parseRuns(text *strings.Builder) error {
	for {
		tok, err := p.dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch localName(t.Name) {
			case "r":
				if err := p.parseRun(text); err != nil {
					return err
				}
			case "p":
				// 单元格内的段落：段文本以空格相连（markdown 表格单元
				// 格不能跨行）。
				var para strings.Builder
				if err := p.parseRuns(&para); err != nil {
					return err
				}
				if s := strings.TrimSpace(para.String()); s != "" {
					if text.Len() > 0 {
						text.WriteString(" ")
					}
					text.WriteString(s)
				}
			default:
				if err := p.skipElement(&t); err != nil {
					return err
				}
			}
		case xml.EndElement:
			switch localName(t.Name) {
			case "hyperlink", "p", "tc":
				return nil
			}
		}
	}
}

// parseRun 解析单个运行：t 文本逐字保留、br 折行、tab 单空格，至 r 结束。
func (p *docxParser) parseRun(text *strings.Builder) error {
	for {
		tok, err := p.dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch localName(t.Name) {
			case "tab":
				text.WriteString(" ")
				if err := p.skipElement(&t); err != nil {
					return err
				}
			case "br":
				text.WriteString("\n")
				if err := p.skipElement(&t); err != nil {
					return err
				}
			case "t":
				var chars strings.Builder
				if err := p.parseChars(&chars, "t"); err != nil {
					return err
				}
				text.WriteString(chars.String())
			default:
				if err := p.skipElement(&t); err != nil {
					return err
				}
			}
		case xml.EndElement:
			if localName(t.Name) == "r" {
				return nil
			}
		}
	}
}

// parseChars 收集 start 元素内的字符数据，至同名结束元素。
func (p *docxParser) parseChars(out *strings.Builder, name string) error {
	for {
		tok, err := p.dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.CharData:
			out.WriteString(string(t))
		case xml.EndElement:
			if localName(t.Name) == name {
				return nil
			}
		}
	}
}

// parseTable 解析表格为 markdown 行集：单元格内段落文本以空格相连，
// 无行或全空表返回 nil（不产出空表格）。
func (p *docxParser) parseTable() ([][]string, error) {
	rows := make([][]string, 0, 4)
	for {
		tok, err := p.dec.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch localName(t.Name) {
			case "tr":
				cells, err := p.parseTableRow()
				if err != nil {
					return nil, err
				}
				rows = append(rows, cells)
			default:
				if err := p.skipElement(&t); err != nil {
					return nil, err
				}
			}
		case xml.EndElement:
			if localName(t.Name) == "tbl" {
				if len(rows) == 0 || rowsAllEmpty(rows) {
					return nil, nil
				}
				return rows, nil
			}
		}
	}
}

// parseTableRow 解析表格行：每个 tc 的段落文本拼接为单元格。
func (p *docxParser) parseTableRow() ([]string, error) {
	var cells []string
	for {
		tok, err := p.dec.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch localName(t.Name) {
			case "tc":
				var cell strings.Builder
				if err := p.parseRuns(&cell); err != nil {
					return nil, err
				}
				if parts := strings.Split(cell.String(), "\n"); len(parts) > 0 {
					texts := make([]string, 0, len(parts))
					for _, part := range parts {
						if s := strings.TrimSpace(part); s != "" {
							texts = append(texts, s)
						}
					}
					cells = append(cells, strings.Join(texts, " "))
				} else {
					cells = append(cells, "")
				}
			default:
				if err := p.skipElement(&t); err != nil {
					return nil, err
				}
			}
		case xml.EndElement:
			if localName(t.Name) == "tr" {
				return cells, nil
			}
		}
	}
}

// rowsAllEmpty 报告表格是否全空（每行每单元格都无内容）。
func rowsAllEmpty(rows [][]string) bool {
	for _, row := range rows {
		for _, cell := range row {
			if strings.TrimSpace(cell) != "" {
				return false
			}
		}
	}
	return true
}

// attrVal 取元素属性值（本地名匹配）。
func attrVal(start xml.StartElement, name string) string {
	for _, a := range start.Attr {
		if localName(a.Name) == name {
			return a.Value
		}
	}
	return ""
}
