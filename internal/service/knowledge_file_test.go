package service

import (
	"bytes"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sessionv1 "grape/api/session/v1"
	"grape/internal/biz"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
)

// 上传端点的表单解析单测：字段映射与缺省推导（标题取文件名去扩展名）、
// 形态错误统一入参非法、身份缺失 fail-closed。业务链路（识别 / 解析 /
// 摄取）由 biz 用例与 server 路由层测试覆盖。

// multipartUploadRequest 构造 multipart 上传请求（file 部分可选）。
func multipartUploadRequest(t *testing.T, withFile bool, fileName string, fields map[string]string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if withFile {
		part, err := w.CreateFormFile("file", fileName)
		if err != nil {
			t.Fatalf("create form file: %v", err)
		}
		if _, err := part.Write([]byte("file payload bytes")); err != nil {
			t.Fatalf("write form file: %v", err)
		}
	}
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatalf("write field %s: %v", k, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/knowledge/documents/upload", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}

func TestKnowledgeUploadInputOf_parses_fields_and_defaults_title(t *testing.T) {
	req := multipartUploadRequest(t, true, "季度报告.PDF", map[string]string{
		"libraryId":  "7",
		"status":     "1",
		"sourceName": "来源名",
		"validFrom":  "2026-01-01T00:00:00Z",
		"validTo":    "2027-01-01T00:00:00Z",
	})

	in, err := knowledgeUploadInputOf(req)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if in.LibraryID != 7 || in.Status != biz.KnowledgeStatusEnabled {
		t.Fatalf("library/status = %d/%d, want 7/1", in.LibraryID, in.Status)
	}
	if in.Title != "季度报告" || in.FileName != "季度报告.PDF" {
		t.Fatalf("title/file = %q/%q, want derived title with original name", in.Title, in.FileName)
	}
	if in.SourceName == nil || *in.SourceName != "来源名" {
		t.Fatalf("sourceName = %v, want set", in.SourceName)
	}
	if in.ValidFrom == nil || in.ValidTo == nil {
		t.Fatalf("validity = %v/%v, want parsed", in.ValidFrom, in.ValidTo)
	}
	if !strings.HasPrefix(string(in.Body), "file payload") {
		t.Fatalf("body = %q, want file part bytes", in.Body)
	}
}

func TestKnowledgeUploadInputOf_title_defaults_and_explicit_override(t *testing.T) {
	// 文件名缺省标题（无扩展名回落原名）与显式 title 覆盖。
	req := multipartUploadRequest(t, true, "README", map[string]string{"libraryId": "1"})
	in, err := knowledgeUploadInputOf(req)
	if err != nil || in.Title != "README" {
		t.Fatalf("err = %v, title = %v, want README", err, titleOf(in))
	}

	req = multipartUploadRequest(t, true, "notes.md", map[string]string{"libraryId": "1", "title": "显式标题"})
	in, err = knowledgeUploadInputOf(req)
	if err != nil || in.Title != "显式标题" {
		t.Fatalf("err = %v, title = %v, want explicit title wins", err, titleOf(in))
	}
}

func TestKnowledgeUploadInputOf_form_errors_rejected_as_invalid_argument(t *testing.T) {
	cases := map[string]*http.Request{
		"no file part":  multipartUploadRequest(t, false, "", map[string]string{"libraryId": "1"}),
		"bad library":   multipartUploadRequest(t, true, "a.txt", map[string]string{"libraryId": "x"}),
		"zero library":  multipartUploadRequest(t, true, "a.txt", map[string]string{"libraryId": "0"}),
		"bad status":    multipartUploadRequest(t, true, "a.txt", map[string]string{"libraryId": "1", "status": "9"}),
		"bad validFrom": multipartUploadRequest(t, true, "a.txt", map[string]string{"libraryId": "1", "validFrom": "yesterday"}),
		"not multipart": httptest.NewRequest(http.MethodPost, "/v1/knowledge/documents/upload", strings.NewReader("plain")),
	}
	for name, req := range cases {
		if _, err := knowledgeUploadInputOf(req); !isReason(err, "DOCUMENT_INVALID_ARGUMENT") {
			t.Fatalf("%s: err = %v, want DOCUMENT_INVALID_ARGUMENT", name, err)
		}
	}
}

func TestKnowledgeUploadTitleOf_strips_extension_and_falls_back(t *testing.T) {
	cases := map[string]string{
		"report.pdf": "report",
		"a.b.c.docx": "a.b.c",
		"README":     "README",
		".hidden":    ".hidden",
	}
	for name, want := range cases {
		if got := knowledgeUploadTitleOf(name); got != want {
			t.Fatalf("title(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestUploadDocument_without_token_fails_closed(t *testing.T) {
	// 用例依赖零值装配即可：身份校验先于任何仓储触达，缺失即 401。
	svc := NewKnowledgeService(biz.NewKnowledgeUsecase(nil, nil, nil, nil, nil, nil, nil), nil)
	req := multipartUploadRequest(t, true, "a.txt", map[string]string{"libraryId": "1"})

	_, err := svc.uploadDocument(req)
	if !isReason(err, sessionv1.ErrorReason_SESSION_INVALID.String()) {
		t.Fatalf("err = %v, want %s", err, sessionv1.ErrorReason_SESSION_INVALID.String())
	}
}

// titleOf 取入参标题（nil 安全）。
func titleOf(in *biz.KnowledgeFileUpload) string {
	if in == nil {
		return "<nil>"
	}
	return in.Title
}

// isReason 断言错误为携带指定 reason 的 kratos 错误。
func isReason(err error, reason string) bool {
	var domain *kratoserrors.Error
	return errors.As(err, &domain) && domain.Reason == reason
}
