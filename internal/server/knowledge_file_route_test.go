package server

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"grape/internal/biz"
	"grape/internal/conf"
	entrymw "grape/internal/middleware"
	"grape/internal/ops"
	"grape/internal/service"

	kratoshttp "github.com/go-kratos/kratos/v3/transport/http"
	"google.golang.org/protobuf/types/known/durationpb"
)

// 文件上传手写路由的装配回归：multipart 端点经完整 HTTP 链（真实认证 +
// 功能权限 + 路由级超时）摄取文件条目，无令牌 fail-closed 401；识别层
// 拒绝直达客户端（FILE_TYPE_UNSUPPORTED）。

// fileRouteStore 只承接摄取写路径。
type fileRouteStore struct {
	biz.VectorStore
	added int
}

func (s *fileRouteStore) DeleteByFilter(context.Context, *biz.VectorFilter) (int64, error) {
	return 0, nil
}

// GetMetadata 返回空集合：首次摄取没有现存切片，差量对账只走 Add 分支。
func (s *fileRouteStore) GetMetadata(context.Context, *biz.VectorFilter) (map[string]map[string]any, error) {
	return map[string]map[string]any{}, nil
}

func (s *fileRouteStore) Add(context.Context, *biz.VectorDocument) error {
	s.added++
	return nil
}

// fileRouteRepo 承接文件条目的目录读写。
type fileRouteRepo struct {
	biz.KnowledgeRepo
	created  *biz.KnowledgeDocument
	updated  *biz.KnowledgeDocument
	indexed  bool
	fileByID map[int64]*biz.KnowledgeFile
}

func (r *fileRouteRepo) GetLibrary(context.Context, biz.Identity, int64) (*biz.KnowledgeLibrary, error) {
	delimiter := "\n\n"
	return &biz.KnowledgeLibrary{ID: 1, Status: biz.KnowledgeStatusEnabled, ChunkSize: 800, ChunkOverlap: 100, ChunkDelimiter: &delimiter}, nil
}

func (r *fileRouteRepo) CreateDocument(_ context.Context, _ biz.Identity, d *biz.KnowledgeDocument) (*biz.KnowledgeDocument, error) {
	d.ID = 2
	r.created = d
	return d, nil
}

func (r *fileRouteRepo) UpdateDocument(_ context.Context, _ biz.Identity, d *biz.KnowledgeDocument) (*biz.KnowledgeDocument, error) {
	r.updated = d
	return d, nil
}

func (r *fileRouteRepo) MarkDocumentIndexed(context.Context, biz.Identity, int64, string) (*biz.KnowledgeDocument, error) {
	r.indexed = true
	if r.updated != nil {
		return r.updated, nil
	}
	return r.created, nil
}

// fileRouteFiles 是介质仓储替身（行内注册，供重解析取回）。
type fileRouteFiles struct {
	biz.KnowledgeFileRepo
	repo *fileRouteRepo
}

func (f *fileRouteFiles) CreateFile(_ context.Context, _ biz.Identity, file *biz.KnowledgeFile) (*biz.KnowledgeFile, error) {
	file.ID = 9
	file.ObjectKey = "kb-file/9"
	f.repo.fileByID[9] = file
	return file, nil
}

func (f *fileRouteFiles) GetFile(_ context.Context, _ biz.Identity, id int64) (*biz.KnowledgeFile, error) {
	if file, ok := f.repo.fileByID[id]; ok {
		return file, nil
	}
	return nil, biz.ErrDocumentNotFound
}

func (f *fileRouteFiles) SoftDeleteFile(context.Context, biz.Identity, int64) error {
	return nil
}

// fileRouteObjects 是对象存储替身：Put 写入、Get 读回。
type fileRouteObjects struct {
	biz.ObjectStore
	objects map[string][]byte
}

func (o *fileRouteObjects) Put(_ context.Context, key string, body io.Reader, _ string) (string, error) {
	b, err := io.ReadAll(body)
	if err != nil {
		return "", err
	}
	o.objects[key] = b
	return "etag", nil
}

func (o *fileRouteObjects) Get(_ context.Context, key string) (io.ReadCloser, error) {
	b, ok := o.objects[key]
	if !ok {
		return nil, biz.ErrObjectNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (o *fileRouteObjects) Delete(context.Context, string) error { return nil }

// fileRouteParser 在直传档位用例中不应被触达（触达即失败）。
type fileRouteParser struct{}

func (fileRouteParser) Parse(context.Context, *biz.FileParseInput) (string, error) {
	return "", biz.ErrFileTypeUnsupported
}

// fileRoutePermRepo 是权限判定仓储替身：记录受控端点查询的功能权限码
// （锁定上传路由「操作名=路径模板 → sys:knowledge:create」的码表映射），
// granted=false 构造拒绝路径。
type fileRoutePermRepo struct {
	biz.PermissionRepo
	granted  bool
	lastCode string
}

func (r *fileRoutePermRepo) HasPermission(_ context.Context, _ biz.Identity, code string) (bool, error) {
	r.lastCode = code
	return r.granted, nil
}

func (r *fileRoutePermRepo) ListCodes(context.Context, biz.Identity) ([]string, error) {
	return nil, nil
}

// newFileRouteTestServer 装配含知识库上传路由的 HTTP 服务器：真实 trace /
// auth / 权限 / 路由级超时中间件链（权限判定恒放行）。
func newFileRouteTestServer(t *testing.T, store *fileRouteStore) *kratoshttp.Server {
	return newFileRoutePermTestServer(t, store, &fileRoutePermRepo{granted: true})
}

// newFileRoutePermTestServer 与上行同装配，权限仓储由用例注入（拒绝路径
// 用例经此锁定权限判定语义）。
func newFileRoutePermTestServer(t *testing.T, store *fileRouteStore, perms *fileRoutePermRepo) *kratoshttp.Server {
	t.Helper()
	confServer := &conf.Server{Http: &conf.Server_HTTP{RequestTimeout: durationpb.New(time.Minute)}}
	traceMiddleware, err := entrymw.NewTraceMiddleware(confServer)
	if err != nil {
		t.Fatalf("build trace middleware: %v", err)
	}
	ingest, err := biz.NewDocumentUsecase(store, ingestRouteSlowEmbedder{}, ingestRouteResolver{}, nil)
	if err != nil {
		t.Fatalf("build ingest usecase: %v", err)
	}
	repo := &fileRouteRepo{fileByID: map[int64]*biz.KnowledgeFile{}}
	files := &fileRouteFiles{repo: repo}
	objects := &fileRouteObjects{objects: map[string][]byte{}}
	knowledge := service.NewKnowledgeService(
		biz.NewKnowledgeUsecase(repo, nil, store, ingest, files, objects, fileRouteParser{}), nil)
	srv, err := NewHTTPServer(
		confServer,
		nil,
		traceMiddleware,
		NewRouteTimeoutMiddleware(confServer, nil),
		entrymw.NewAuthMiddleware(biz.NewSessionUsecase(&agentFakeSessionRepo{})), nil,
		entrymw.NewPermissionMiddleware(biz.NewPermissionUsecase(perms)),
		ops.NewMetricsHandler(nil, nil),
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		nil, // AgentModelCallService（本文件用例不触达，传 nil）,
		knowledge,
		nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("build http server: %v", err)
	}
	return srv
}

// fileRouteUploadBody 构造 multipart 上传请求体（file 字段 + 表单字段）。
func fileRouteUploadBody(t *testing.T, fields map[string]string, fileName, content string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if fileName != "" {
		part, err := w.CreateFormFile("file", fileName)
		if err != nil {
			t.Fatalf("create form file: %v", err)
		}
		if _, err := part.Write([]byte(content)); err != nil {
			t.Fatalf("write form file: %v", err)
		}
	}
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatalf("write field %s: %v", k, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return &buf, w.FormDataContentType()
}

// fileRouteDo 执行一次 multipart 上传请求并返回响应。
func fileRouteDo(t *testing.T, srv *kratoshttp.Server, token string, fields map[string]string, fileName, content string) *httptest.ResponseRecorder {
	t.Helper()
	body, contentType := fileRouteUploadBody(t, fields, fileName, content)
	req := httptest.NewRequest(http.MethodPost, "/v1/knowledge/documents/upload", body)
	req.Header.Set("Content-Type", contentType)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

const fileRouteText = "这是一段经由手写上传路由摄取的纯文本正文，长度满足最短阈值的要求。"

func TestFileUploadRoute_without_token_fails_closed(t *testing.T) {
	srv := newFileRouteTestServer(t, &fileRouteStore{})

	rec := fileRouteDo(t, srv, "", map[string]string{"libraryId": "1"}, "notes.txt", fileRouteText)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (fail-closed without token)\n%s", rec.Code, rec.Body.String())
	}
}

func TestFileUploadRoute_text_file_ingests_and_renders_document(t *testing.T) {
	store := &fileRouteStore{}
	srv := newFileRouteTestServer(t, store)

	rec := fileRouteDo(t, srv, "any-token", map[string]string{"libraryId": "1"}, "notes.txt", fileRouteText)
	if rec.Code != http.StatusOK || store.added != 1 {
		t.Fatalf("status = %d, chunks = %d; want 200 with one chunk\n%s", rec.Code, store.added, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`"kind":"file"`, `"fileName":"notes.txt"`, `"mimeType":"text/plain`, `"fileSize"`, `"title":"notes"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %s:\n%s", want, body)
		}
	}
}

func TestFileUploadRoute_unsupported_extension_rejected_with_reason(t *testing.T) {
	srv := newFileRouteTestServer(t, &fileRouteStore{})

	rec := fileRouteDo(t, srv, "any-token", map[string]string{"libraryId": "1"}, "payload.exe", "MZ payload")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "FILE_TYPE_UNSUPPORTED") {
		t.Fatalf("status = %d, want 400 FILE_TYPE_UNSUPPORTED\n%s", rec.Code, rec.Body.String())
	}
}

func TestFileUploadRoute_missing_file_part_rejected_as_invalid_argument(t *testing.T) {
	srv := newFileRouteTestServer(t, &fileRouteStore{})

	rec := fileRouteDo(t, srv, "any-token", map[string]string{"libraryId": "1"}, "", fileRouteText)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "DOCUMENT_INVALID_ARGUMENT") {
		t.Fatalf("status = %d, want 400 DOCUMENT_INVALID_ARGUMENT\n%s", rec.Code, rec.Body.String())
	}
}

func TestFileUploadRoute_permission_denied_rejected_as_forbidden(t *testing.T) {
	store := &fileRouteStore{}
	perms := &fileRoutePermRepo{granted: false}
	srv := newFileRoutePermTestServer(t, store, perms)

	rec := fileRouteDo(t, srv, "any-token", map[string]string{"libraryId": "1"}, "notes.txt", fileRouteText)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (permission denied)\n%s", rec.Code, rec.Body.String())
	}
	// 上传路由以路径模板为操作名设码，与「创建条目」同为 sys:knowledge:create。
	if perms.lastCode != biz.PermKnowledgeCreate {
		t.Fatalf("permission code = %q, want %q (path-template operation)", perms.lastCode, biz.PermKnowledgeCreate)
	}
	if store.added != 0 {
		t.Fatalf("chunks ingested despite permission denial: %d", store.added)
	}
}
