package docparse

import (
	"archive/zip"
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"grape/internal/biz"
	"grape/internal/conf"

	"github.com/xuri/excelize/v2"
	"google.golang.org/protobuf/types/known/durationpb"
)

// 解析器单测的边界：三档内档解析（xlsx / docx / html 的 markdown 产出
// 形态）、外部档的 HTTP 协议与未配置拒绝、纯文本类型不经解析器。容器
// 字节一律在测试内构造（excelize 建簿 / zip 写 OOXML），外部服务以
// httptest 替身承接。

func buildTestXLSX(t *testing.T) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	const sheet = "库存"
	if err := f.SetSheetName("Sheet1", sheet); err != nil {
		t.Fatalf("rename sheet: %v", err)
	}
	for i, row := range [][]interface{}{
		{"名称", "数量", "备注"},
		{"苹果", 12, "新鲜|上市"},
		{"香蕉", 30, "两行\n备注"},
		{"", "", ""},
	} {
		if err := f.SetSheetRow(sheet, "A"+strconv.Itoa(i+1), &row); err != nil {
			t.Fatalf("set row %d: %v", i+1, err)
		}
	}
	body, err := f.WriteToBuffer()
	if err != nil {
		t.Fatalf("write xlsx: %v", err)
	}
	return body.Bytes()
}

func TestXlsxToMarkdown_sheets_become_heading_and_table(t *testing.T) {
	out, err := xlsxToMarkdown(buildTestXLSX(t))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{
		"## 库存",
		"| 名称 | 数量 | 备注 |",
		"| 苹果 | 12 | 新鲜\\|上市 |",
		"| 香蕉 | 30 | 两行 备注 |",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in output:\n%s", want, out)
		}
	}
	// 全空行不入表格（解析侧丢弃，不产空行占位）。
	if strings.Contains(out, "|  |  |  |") {
		t.Fatalf("empty row not dropped:\n%s", out)
	}
	if !strings.Contains(out, "| --- | --- | --- |") {
		t.Fatalf("missing table delimiter row:\n%s", out)
	}
}

func TestXlsxToMarkdown_empty_workbook_yields_empty_output(t *testing.T) {
	f := excelize.NewFile()
	defer f.Close()
	body, err := f.WriteToBuffer()
	if err != nil {
		t.Fatalf("write xlsx: %v", err)
	}
	out, err := xlsxToMarkdown(body.Bytes())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("out = %q, want empty", out)
	}
}

func TestXlsxToMarkdown_corrupt_container_rejected(t *testing.T) {
	if _, err := xlsxToMarkdown([]byte("not a zip")); err == nil {
		t.Fatalf("want error for corrupt container")
	}
}

// buildTestDOCX 构造最小 docx 容器：word/document.xml 按测试给定内容。
func buildTestDOCX(t *testing.T, documentXML string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	entry, err := w.Create("word/document.xml")
	if err != nil {
		t.Fatalf("create entry: %v", err)
	}
	if _, err := entry.Write([]byte(documentXML)); err != nil {
		t.Fatalf("write entry: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

const testDocumentXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>
<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>项目说明</w:t></w:r></w:p>
<w:p><w:pPr><w:numPr/></w:pPr><w:r><w:t>第一项</w:t></w:r></w:p>
<w:p><w:r><w:t>正文含</w:t></w:r><w:hyperlink w:anchor="x"><w:r><w:t>链接文本</w:t></w:r></w:hyperlink><w:r><w:t>，含</w:t></w:r><w:r><w:tab/><w:t>制表位</w:t></w:r></w:p>
<w:p><w:pPr><w:pStyle w:val="Heading2"/></w:pPr><w:r><w:t>数据表</w:t></w:r></w:p>
<w:tbl>
<w:tr><w:tc><w:p><w:r><w:t>字段</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>说明</w:t></w:r></w:p></w:tc></w:tr>
<w:tr><w:tc><w:p><w:r><w:t>id</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>主|键</w:t></w:r></w:p></w:tc></w:tr>
</w:tbl>
<w:sectPr/>
</w:body>
</w:document>`

func TestDocxToMarkdown_headings_lists_and_tables(t *testing.T) {
	out, err := docxToMarkdown(buildTestDOCX(t, testDocumentXML))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{
		"# 项目说明",
		"- 第一项",
		"正文含链接文本，含 制表位",
		"## 数据表",
		"| 字段 | 说明 |",
		"| id | 主\\|键 |",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestDocxToMarkdown_empty_body_yields_empty_output(t *testing.T) {
	out, err := docxToMarkdown(buildTestDOCX(t, `<w:document xmlns:w="x"><w:body><w:p/></w:body></w:document>`))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("out = %q, want empty", out)
	}
}

func TestDocxToMarkdown_missing_document_xml_rejected(t *testing.T) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	if _, err := w.Create("other.xml"); err != nil {
		t.Fatalf("create entry: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	if _, err := docxToMarkdown(buf.Bytes()); err == nil || !strings.Contains(err.Error(), "document.xml") {
		t.Fatalf("err = %v, want missing document.xml", err)
	}
}

func TestHtmlToMarkdown_block_conversion(t *testing.T) {
	page := `<!doctype html><html><head><style>.x{color:red}</style><title>页</title></head><body>
<div class="nav"><script>ignore()</script><span>导航文本</span></div>
<h2>章节标题</h2>
<p>段落，含<strong>加粗</strong>、<em>斜体</em>与<a href="https://example.com/a|b">链接</a>。</p>
<ul><li>项目甲</li><li>项目乙</li></ul>
<ol><li>第一</li><li>第二</li></ol>
<table><thead><tr><th>列一</th><th>列二</th></tr></thead><tbody>
<tr><td>值 1</td><td>值 2</td></tr></tbody></table>
<pre><code>keep   spacing
  lines</code></pre>
<blockquote>引述文本</blockquote>
<hr>
</body></html>`
	out, err := htmlToMarkdown([]byte(page))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{
		"导航文本",
		"## 章节标题",
		"段落，含**加粗**、*斜体*与[链接](https://example.com/a|b)。",
		"- 项目甲\n- 项目乙",
		"1. 第一\n2. 第二",
		"| 列一 | 列二 |",
		"| 值 1 | 值 2 |",
		"```\nkeep   spacing\n  lines\n```",
		"> 引述文本",
		"---",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in output:\n%s", want, out)
		}
	}
	if strings.Contains(out, "color:red") || strings.Contains(out, "ignore()") {
		t.Fatalf("non-content elements leaked:\n%s", out)
	}
}

func TestHtmlToMarkdown_script_only_page_yields_empty(t *testing.T) {
	out, err := htmlToMarkdown([]byte(`<html><head><script>var x=1;</script></head><body></body></html>`))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("out = %q, want empty", out)
	}
}

func TestParse_dispatches_by_mime(t *testing.T) {
	p, cleanup, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()
	if _, err := p.Parse(t.Context(), FileParseInputOf(mimePlainText, []byte("a.txt"))); err == nil || !errors.Is(err, biz.ErrFileTypeUnsupported) {
		t.Fatalf("plain text err = %v, want unsupported", err)
	}
	if _, err := p.Parse(t.Context(), FileParseInputOf("application/x-unknown", []byte("a.bin"))); err == nil || !errors.Is(err, biz.ErrFileTypeUnsupported) {
		t.Fatalf("unknown mime err = %v, want unsupported", err)
	}
	out, err := p.Parse(t.Context(), FileParseInputOf("text/html; charset=utf-8", []byte("<h1>标题</h1>")))
	if err != nil || !strings.Contains(out, "# 标题") {
		t.Fatalf("html parse err = %v out = %q", err, out)
	}
	out, err = p.Parse(t.Context(), FileParseInputOf(mimeXLSX, buildTestXLSX(t)))
	if err != nil || !strings.Contains(out, "## 库存") {
		t.Fatalf("xlsx parse err = %v out = %q", err, out)
	}
}

// docParseConfOf 构造解析服务配置（timeout 非正表示不设置，取实现缺省）。
func docParseConfOf(endpoint string, timeout time.Duration) *conf.DocParse {
	c := &conf.DocParse{Endpoint: endpoint}
	if timeout > 0 {
		c.Timeout = durationpb.New(timeout)
	}
	return c
}

// FileParseInputOf 构造解析输入（测试别名）。
func FileParseInputOf(mime string, body []byte) *biz.FileParseInput {
	return &biz.FileParseInput{MIME: mime, FileName: "fixture", Body: body}
}

func TestParse_external_unconfigured_rejected_with_combined_error(t *testing.T) {
	p, cleanup, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()
	_, err = p.Parse(t.Context(), FileParseInputOf(mimePDF, []byte("%PDF-1.7")))
	// 组合错误形态（biz 契约）：errors.Is 命中「类型不支持」，未配置
	// 事实在文案中（文案可区分，供入口层区分提示）。
	if !errors.Is(err, biz.ErrFileTypeUnsupported) || !strings.Contains(err.Error(), biz.ErrFileParseUnavailable.Error()) {
		t.Fatalf("err = %v, want unsupported with unconfigured cause", err)
	}
}

func TestParse_external_service_roundtrip_and_failures(t *testing.T) {
	var gotMime, gotName, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMime, gotName = r.Header.Get("X-Grape-Mime"), r.Header.Get("X-Grape-Filename")
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		gotBody = string(b)
		if strings.Contains(gotBody, "boom") {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte("parse failed"))
			return
		}
		_, _ = w.Write([]byte("# 解析产物\n\n正文段落，满足阈值。"))
	}))
	defer srv.Close()

	p, cleanup, err := New(docParseConfOf(srv.URL, 0))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()

	out, err := p.Parse(t.Context(), &biz.FileParseInput{
		MIME: mimePDF, FileName: "paper.pdf", Body: []byte("%PDF-1.7 payload"),
	})
	if err != nil || !strings.Contains(out, "解析产物") {
		t.Fatalf("err = %v out = %q", err, out)
	}
	if gotMime != mimePDF || gotName != "paper.pdf" || gotBody != "%PDF-1.7 payload" {
		t.Fatalf("protocol facts wrong: mime=%s name=%s body=%q", gotMime, gotName, gotBody)
	}

	_, err = p.Parse(t.Context(), &biz.FileParseInput{MIME: mimePDF, FileName: "bad.pdf", Body: []byte("boom")})
	if err == nil || !strings.Contains(err.Error(), "422") {
		t.Fatalf("err = %v, want non-200 parse failure", err)
	}
}

func TestParse_external_service_unreachable_fails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close() // 端口已释放，必然连接失败
	p, cleanup, err := New(docParseConfOf(srv.URL, 0))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()
	if _, err := p.Parse(t.Context(), FileParseInputOf(mimePPTX, []byte("bytes"))); err == nil {
		t.Fatalf("want transport error for unreachable service")
	}
}
