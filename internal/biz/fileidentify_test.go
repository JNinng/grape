package biz

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

// 文件识别三层校验的用例边界：扩展名白名单快速拒绝、嗅探 MIME 路由分派
// （路由与落库 MIME 一律以嗅探结果为准）、完整性限流（空文件 / 超限 /
// zip 炸弹预检）。嗅探以注入替身的方式进行（生产为 mimetype 薄包装），
// 用例只验证路由与拒绝语义。

const sniffXLSX = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"

func TestIdentifyFile_unknown_extension_rejected_before_sniffing(t *testing.T) {
	sniffed := false
	sniff := func([]byte) string { sniffed = true; return sniffXLSX }

	_, err := identifyFile("malware.exe", []byte("whatever"), sniff)
	if err == nil || !strings.Contains(err.Error(), "extension") {
		t.Fatalf("err = %v, want unsupported extension", err)
	}
	if !isFileTypeUnsupported(err) {
		t.Fatalf("err not ErrFileTypeUnsupported: %v", err)
	}
	if sniffed {
		t.Fatalf("sniffing ran for rejected extension; want quick rejection")
	}
}

func TestIdentifyFile_empty_and_oversized_body_rejected_as_invalid_argument(t *testing.T) {
	_, err := identifyFile("notes.txt", nil, sniffMIME)
	if !isInvalidArgument(err) {
		t.Fatalf("err = %v, want empty file invalid argument", err)
	}
	big := make([]byte, maxFileBytes+1)
	_, err = identifyFile("notes.txt", big, sniffMIME)
	if !isInvalidArgument(err) || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("err = %v, want size limit invalid argument", err)
	}
}

func TestIdentifyFile_sniffed_mime_dispatches_route_and_direct_kind(t *testing.T) {
	textBody := []byte("payload")
	// OOXML 档位的路由用例需要真实 zip 容器（识别期的炸弹预检对容器事实设防）。
	xlsxBody := buildTestZip(t, 1, 8)
	cases := map[string]struct {
		name      string
		sniffed   string
		body      []byte
		wantRoute string
		wantKind  string
	}{
		"plain text":    {"notes.txt", mimePlainText, textBody, FileRouteDirect, DocumentKindText},
		"markdown ext":  {"notes.md", mimePlainText, textBody, FileRouteDirect, DocumentKindMarkdown},
		"markdown mime": {"notes.md", mimeMarkdown, textBody, FileRouteDirect, DocumentKindMarkdown},
		"html":          {"page.html", mimeHTML, textBody, FileRouteNative, ""},
		"xlsx":          {"book.xlsx", sniffXLSX, xlsxBody, FileRouteNative, ""},
		"docx":          {"spec.docx", mimeDOCX, xlsxBody, FileRouteNative, ""},
		"pdf":           {"paper.pdf", mimePDF, textBody, FileRouteExternal, ""},
		"pptx":          {"deck.pptx", mimePPTX, xlsxBody, FileRouteExternal, ""},
		"jpeg":          {"scan.jpg", mimeJPEG, textBody, FileRouteExternal, ""},
		"png":           {"shot.png", mimePNG, textBody, FileRouteExternal, ""},
	}
	for name, tc := range cases {
		identity, err := identifyFile(tc.name, tc.body, func([]byte) string { return tc.sniffed })
		if err != nil {
			t.Fatalf("%s: err = %v", name, err)
		}
		if identity.route != tc.wantRoute {
			t.Fatalf("%s: route = %s, want %s", name, identity.route, tc.wantRoute)
		}
		if identity.directKind != tc.wantKind {
			t.Fatalf("%s: directKind = %q, want %q", name, identity.directKind, tc.wantKind)
		}
		if identity.sniffedMIME != tc.sniffed {
			t.Fatalf("%s: sniffedMIME = %q, want %q", name, identity.sniffedMIME, tc.sniffed)
		}
	}
}

func TestIdentifyFile_sniffed_mime_outside_whitelist_rejected(t *testing.T) {
	// 扩展名通过但内容不在白名单：改扩展名伪装或类型漂移，按不支持拒绝。
	_, err := identifyFile("payload.txt", []byte("MZ..."), func([]byte) string { return "application/zip" })
	if !isFileTypeUnsupported(err) || !strings.Contains(err.Error(), "sniffed mime") {
		t.Fatalf("err = %v, want sniffed mime unsupported", err)
	}
}

func TestIdentifyFile_octet_stream_falls_back_to_extension_route(t *testing.T) {
	// 嗅探回落通配 octet-stream：扩展名指向 OOXML/外部档时按扩展名放行
	//（手工构造容器的嗅探回落），zip 预检照常执行。
	identity, err := identifyFile("book.xlsx", []byte("not really zip"), func([]byte) string { return mimeOctetStream })
	if err == nil || !isFileTypeUnsupported(err) {
		// octet-stream 放行路由后，zip 预检对非 zip 字节按容器事实拒绝。
		t.Fatalf("err = %v, want zip precheck rejection for non-zip bytes", err)
	}

	validZip := buildTestZip(t, 1, 16)
	identity, err = identifyFile("book.xlsx", validZip, func([]byte) string { return mimeOctetStream })
	if err != nil {
		t.Fatalf("err = %v, want octet-stream routed by extension and zip precheck passed", err)
	}
	if identity.route != FileRouteNative {
		t.Fatalf("route = %s, want %s", identity.route, FileRouteNative)
	}
}

func TestIdentifyFile_zip_bomb_precheck_rejects_entry_flood_and_uncompressed_total(t *testing.T) {
	entryBomb := buildTestZip(t, maxArchiveEntries+1, 8)
	_, err := identifyFile("bomb.xlsx", entryBomb, func([]byte) string { return sniffXLSX })
	if !isFileTypeUnsupported(err) || !strings.Contains(err.Error(), "entries") {
		t.Fatalf("err = %v, want entry flood rejection", err)
	}

	ratioBomb := buildTestZip(t, 1, maxArchiveUncompressed+1)
	_, err = identifyFile("bomb.docx", ratioBomb, func([]byte) string { return mimeDOCX })
	if !isFileTypeUnsupported(err) || !strings.Contains(err.Error(), "uncompressed") {
		t.Fatalf("err = %v, want uncompressed total rejection", err)
	}
}

func TestIdentifyFile_corrupt_zip_container_rejected(t *testing.T) {
	_, err := identifyFile("book.docx", []byte("PK\x03\x04 broken"), func([]byte) string { return mimeDOCX })
	if !isFileTypeUnsupported(err) || !strings.Contains(err.Error(), "zip container") {
		t.Fatalf("err = %v, want corrupt container rejection", err)
	}
}

func TestIdentifyFile_mime_normalization_strips_parameters(t *testing.T) {
	identity, err := identifyFile("page.html", []byte("<p>hi</p>"), func([]byte) string { return "text/html; charset=utf-8" })
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if identity.sniffedMIME != mimeHTML {
		t.Fatalf("sniffedMIME = %q, want parameters stripped", identity.sniffedMIME)
	}
}

func TestFileRouteForDirectType_maps_markdown_extensions_only(t *testing.T) {
	if got := fileRouteForDirectType(".md"); got != DocumentKindMarkdown {
		t.Fatalf("kind = %s, want markdown", got)
	}
	if got := fileRouteForDirectType(".markdown"); got != DocumentKindMarkdown {
		t.Fatalf("kind = %s, want markdown", got)
	}
	for _, ext := range []string{".txt", ".text", ".log"} {
		if got := fileRouteForDirectType(ext); got != DocumentKindText {
			t.Fatalf("kind(%s) = %s, want text", ext, got)
		}
	}
}

// isFileTypeUnsupported / isInvalidArgument 是错误语义断言（errors.Is
// 目标哨兵，普通错误不携带 kratos 结构）。
func isFileTypeUnsupported(err error) bool { return errors.Is(err, ErrFileTypeUnsupported) }
func isInvalidArgument(err error) bool     { return errors.Is(err, ErrDocumentInvalidArgument) }

// testZeros 是零字节流（zip 炸弹用例的高压缩比填充）。
type testZeros struct{}

func (testZeros) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

// buildTestZip 构造测试 zip 容器：entries 个 entry、每个 entry 含
// uncompressed 字节未压缩量（零填充经 deflate 高压缩写出，容器声明值即
// 炸弹预检的判定输入）。
func buildTestZip(t *testing.T, entries int, uncompressed int) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for i := 0; i < entries; i++ {
		entry, err := w.Create(fmt.Sprintf("entry%04d.xml", i))
		if err != nil {
			t.Fatalf("create zip entry: %v", err)
		}
		if _, err := io.CopyN(entry, testZeros{}, int64(uncompressed)); err != nil {
			t.Fatalf("write zip entry: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
	return buf.Bytes()
}
