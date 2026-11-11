package biz

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	knowledgev1 "grape/api/knowledge/v1"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
)

// 文件摄取用例的单测边界：上传链路的副作用次序与失败留痕语义（识别拒绝
// 无副作用、解析失败留痕可重解析）、重解析的取回-解析-摄取链路、删除的
// 介质清理。识别层用真实嗅探（mimetype），解析器与存储以替身注入。

// fakeFileRepo 是介质仓储替身：记录创建与软删，GetFile 按预置表返回。
type fakeFileRepo struct {
	KnowledgeFileRepo
	created *KnowledgeFile
	deleted []int64
	byID    map[int64]*KnowledgeFile
}

func (r *fakeFileRepo) CreateFile(_ context.Context, auth Identity, f *KnowledgeFile) (*KnowledgeFile, error) {
	f.ID = 9
	f.ObjectKey = "kb-file/9"
	f.CreatedBy = auth.UserID
	r.created = f
	if r.byID == nil {
		r.byID = map[int64]*KnowledgeFile{}
	}
	r.byID[f.ID] = f
	return f, nil
}

func (r *fakeFileRepo) GetFile(_ context.Context, _ Identity, id int64) (*KnowledgeFile, error) {
	if f, ok := r.byID[id]; ok {
		return f, nil
	}
	return nil, ErrDocumentNotFound
}

func (r *fakeFileRepo) SoftDeleteFile(_ context.Context, _ Identity, id int64) error {
	r.deleted = append(r.deleted, id)
	return nil
}

// fakeObjectStore 是对象存储替身：Put 写入、Get 读回（键值一致，行为
// 对齐真实对象存储），记录关键事实供断言。
type fakeObjectStore struct {
	ObjectStore
	putKey      string
	putBody     []byte
	putType     string
	getKey      string
	objects     map[string][]byte
	deletedKeys []string
}

func (f *fakeObjectStore) Put(_ context.Context, key string, body io.Reader, contentType string) (string, error) {
	b, err := io.ReadAll(body)
	if err != nil {
		return "", err
	}
	if f.objects == nil {
		f.objects = map[string][]byte{}
	}
	f.objects[key] = b
	f.putKey, f.putBody, f.putType = key, b, contentType
	return "etag-value", nil
}

func (f *fakeObjectStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	f.getKey = key
	b, ok := f.objects[key]
	if !ok {
		return nil, ErrObjectNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (f *fakeObjectStore) Delete(_ context.Context, key string) error {
	f.deletedKeys = append(f.deletedKeys, key)
	delete(f.objects, key)
	return nil
}

// fakeParser 是解析器替身：预置 markdown 产物或注入故障，记录调用次数
// 与最近一次输入的 MIME（验证解析路由依据）。
type fakeParser struct {
	FileParser
	markdown string
	err      error
	calls    int
	lastMIME string
}

func (p *fakeParser) Parse(_ context.Context, in *FileParseInput) (string, error) {
	p.calls++
	p.lastMIME = in.MIME
	if p.err != nil {
		return "", p.err
	}
	return p.markdown, nil
}

// fileFakeRepo 在库用例替身上补 UpdateDocument 与 SoftDeleteDocument 记录
// （重解析的产物回写与删除清理路径）。
type fileFakeRepo struct {
	libraryFakeRepo
	updated    []*KnowledgeDocument
	deletedDoc []int64
}

func (r *fileFakeRepo) UpdateDocument(_ context.Context, _ Identity, d *KnowledgeDocument) (*KnowledgeDocument, error) {
	r.updated = append(r.updated, d)
	return d, nil
}

func (r *fileFakeRepo) SoftDeleteDocument(_ context.Context, _ Identity, id int64) error {
	r.deletedDoc = append(r.deletedDoc, id)
	return nil
}

// newFileUsecaseForTest 构造带文件摄取依赖的知识库用例。
func newFileUsecaseForTest(t *testing.T, repo KnowledgeRepo, files KnowledgeFileRepo, objects ObjectStore, parser FileParser, store VectorStore) *KnowledgeUsecase {
	t.Helper()
	ingest, err := NewDocumentUsecase(store, &stubEmbedder{dims: 4}, &stubResolver{space: testEmbedderSpace, dim: 4}, nil)
	if err != nil {
		t.Fatalf("NewDocumentUsecase: %v", err)
	}
	return NewKnowledgeUsecase(repo, nil, store, ingest, files, objects, parser)
}

// uploadFixture 是上传用例的预置装配：库与全部替身。
type uploadFixture struct {
	repo    *fileFakeRepo
	files   *fakeFileRepo
	objects *fakeObjectStore
	parser  *fakeParser
	store   *mockVectorStore
	uc      *KnowledgeUsecase
}

func newUploadFixture(t *testing.T) *uploadFixture {
	t.Helper()
	f := &uploadFixture{
		repo:    &fileFakeRepo{libraryFakeRepo: libraryFakeRepo{existing: &KnowledgeLibrary{ID: 1, Status: KnowledgeStatusEnabled, ChunkSize: 800, ChunkOverlap: 100}}},
		files:   &fakeFileRepo{byID: map[int64]*KnowledgeFile{}},
		objects: &fakeObjectStore{},
		parser:  &fakeParser{},
		store:   &mockVectorStore{},
	}
	f.uc = newFileUsecaseForTest(t, f.repo, f.files, f.objects, f.parser, f.store)
	return f
}

// uploadText 是满足最短阈值的纯文本字节（嗅探为 text/plain）。
const uploadText = "这是一段用于文件摄取测试的纯文本正文，长度满足最短阈值的要求，且不构成标题形态。"

func TestUploadDocument_text_file_ingests_direct_route_with_sniffed_facts(t *testing.T) {
	f := newUploadFixture(t)

	out, err := f.uc.UploadDocument(ingestCtx(), libraryAuth(), &KnowledgeFileUpload{
		LibraryID: 1,
		Title:     "notes",
		FileName:  "notes.txt",
		Body:      []byte(uploadText),
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if out.Kind != DocumentKindFile || out.FileID == nil || out.FileName == nil || *out.FileName != "notes.txt" {
		t.Fatalf("document facts wrong: kind=%s fileID=%v fileName=%v", out.Kind, out.FileID, out.FileName)
	}
	if out.MimeType == nil || !strings.HasPrefix(*out.MimeType, "text/plain") {
		t.Fatalf("mime = %v, want sniffed text/plain", out.MimeType)
	}
	if out.FileSize == nil || *out.FileSize != int64(len(uploadText)) {
		t.Fatalf("fileSize = %v, want %d", out.FileSize, len(uploadText))
	}
	// 介质行：嗅探 MIME 落库、内容身份与对象键齐备。
	created := f.files.created
	if created == nil || created.MimeType != *out.MimeType || created.ObjectKey != "kb-file/9" {
		t.Fatalf("media row wrong: %+v", created)
	}
	if created.FileSize != int64(len(uploadText)) || len(created.Sha256) != 64 || len(created.Etag) != 32 {
		t.Fatalf("media digest facts wrong: %+v", created)
	}
	// 对象字节落存储：键、内容、声明 MIME 一致。
	if f.objects.putKey != "kb-file/9" || string(f.objects.putBody) != uploadText || f.objects.putType != *out.MimeType {
		t.Fatalf("object put facts wrong: key=%s type=%s", f.objects.putKey, f.objects.putType)
	}
	// 解析器不参与纯文本档位；正文经内容识别处理后落目录行。
	if f.parser.calls != 0 {
		t.Fatalf("parser calls = %d, want 0 for direct route", f.parser.calls)
	}
	if len(f.repo.updated) != 1 || f.repo.updated[0].Content == nil || !strings.Contains(*f.repo.updated[0].Content, "纯文本正文") {
		t.Fatalf("parse product not written: %+v", f.repo.updated)
	}
	// 摄取按 text 切分（heading_path 不入 metadata）。
	if len(f.store.added) == 0 || f.store.added[0].Metadata["heading_path"] != nil {
		t.Fatalf("chunks not ingested as text kind: %+v", f.store.added)
	}
}

func TestUploadDocument_unsupported_extension_rejected_without_side_effects(t *testing.T) {
	f := newUploadFixture(t)

	_, err := f.uc.UploadDocument(ingestCtx(), libraryAuth(), &KnowledgeFileUpload{
		LibraryID: 1,
		FileName:  "malware.exe",
		Body:      []byte("MZ\x90\x00 payload"),
	})
	var domain *kratoserrors.Error
	if !errors.As(err, &domain) || domain.Reason != knowledgev1.ErrorReason_FILE_TYPE_UNSUPPORTED.String() {
		t.Fatalf("err = %v, want FILE_TYPE_UNSUPPORTED", err)
	}
	if f.files.created != nil || f.objects.putKey != "" || f.repo.createdDoc != nil {
		t.Fatalf("side effects present on rejection: created=%v put=%s doc=%v",
			f.files.created, f.objects.putKey, f.repo.createdDoc)
	}
}

func TestUploadDocument_external_parse_unavailable_keeps_row_for_retry(t *testing.T) {
	f := newUploadFixture(t)
	f.parser.err = fileParseUnavailableError()

	out, err := f.uc.UploadDocument(ingestCtx(), libraryAuth(), &KnowledgeFileUpload{
		LibraryID: 1,
		Title:     "paper",
		FileName:  "paper.pdf",
		Body:      []byte("%PDF-1.7\n%%EOF\n\n" + strings.Repeat("payload ", 20)),
	})
	if !errors.Is(err, ErrDocumentIndexFailed) {
		t.Fatalf("err = %v, want ErrDocumentIndexFailed", err)
	}
	if out != nil {
		t.Fatalf("out = %+v, want nil on failure", out)
	}
	// 目录行与介质行保留（可重解析），解析产物未回写。
	if f.repo.createdDoc == nil || len(f.repo.updated) != 0 {
		t.Fatalf("directory row not retained: created=%v updated=%d", f.repo.createdDoc, len(f.repo.updated))
	}
	if f.files.created == nil || len(f.files.deleted) != 0 {
		t.Fatalf("media row not retained: created=%v deleted=%v", f.files.created, f.files.deleted)
	}
}

func TestUploadDocument_garbled_text_keeps_row_and_fails_indexing(t *testing.T) {
	f := newUploadFixture(t)
	// 乱码字节：UTF-16LE BOM + 未配对代理项（内容识别判定乱码）。
	garbled := []byte{0xFF, 0xFE, 0x00, 0xD8, 0x00, 0xD8, 0x00, 0xD8}

	_, err := f.uc.UploadDocument(ingestCtx(), libraryAuth(), &KnowledgeFileUpload{
		LibraryID: 1,
		Title:     "broken",
		FileName:  "broken.txt",
		Body:      garbled,
	})
	if !errors.Is(err, ErrDocumentIndexFailed) {
		t.Fatalf("err = %v, want ErrDocumentIndexFailed", err)
	}
	if f.repo.createdDoc == nil || len(f.repo.updated) != 0 {
		t.Fatalf("failure not retained as directory row: created=%v updated=%d", f.repo.createdDoc, len(f.repo.updated))
	}
}

func TestReindexDocument_file_rereparses_from_media_as_markdown(t *testing.T) {
	f := newUploadFixture(t)
	html := "<html><body><h1>Parsed Title</h1><p>" + strings.Repeat("段落文本，满足阈值。", 6) + "</p></body></html>"
	f.objects.objects = map[string][]byte{"kb-file/9": []byte(html)}
	f.parser.markdown = "# Parsed Title\n\n" + strings.Repeat("解析产物段落，满足阈值。", 8)
	f.files.byID[9] = &KnowledgeFile{ID: 9, ObjectKey: "kb-file/9", MimeType: "text/html"}
	mimeType := "text/html"
	fileName := "page.html"
	fileID := int64(9)
	size := int64(len(html))
	f.repo.doc = &KnowledgeDocument{
		ID: 2, LibraryID: 1, Kind: DocumentKindFile, Title: "page",
		FileID: &fileID, FileName: &fileName, MimeType: &mimeType, FileSize: &size,
	}

	out, err := f.uc.ReindexDocument(ingestCtx(), libraryAuth(), 2)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if f.parser.calls != 1 || !strings.Contains(f.parser.markdownInput(), "text/html") {
		t.Fatalf("parser not called with sniffed mime: calls=%d", f.parser.calls)
	}
	// 解析产物回写目录行并以 markdown 切分（heading_path 进 metadata）。
	if len(f.repo.updated) != 1 || f.repo.updated[0].Content == nil || !strings.Contains(*f.repo.updated[0].Content, "Parsed Title") {
		t.Fatalf("reparse product not written: %+v", f.repo.updated)
	}
	if len(f.store.added) == 0 {
		t.Fatalf("no chunks ingested")
	}
	// markdown 切分的 heading_path 是文件解析链路的产出点（标题推断后
	// 由切分器写入 chunk metadata）。
	if _, hasHeading := f.store.added[0].Metadata["heading_path"]; !hasHeading {
		t.Fatalf("markdown chunks lack heading_path: %+v", f.store.added[0].Metadata)
	}
	_ = out
}

func TestReindexDocument_file_without_media_reference_fails(t *testing.T) {
	f := newUploadFixture(t)
	f.repo.doc = &KnowledgeDocument{ID: 2, LibraryID: 1, Kind: DocumentKindFile, Title: "broken"}

	if _, err := f.uc.ReindexDocument(ingestCtx(), libraryAuth(), 2); !errors.Is(err, ErrDocumentIndexFailed) {
		t.Fatalf("err = %v, want ErrDocumentIndexFailed", err)
	}
}

func TestCreateDocument_file_kind_without_file_facts_rejected(t *testing.T) {
	f := newUploadFixture(t)

	_, err := f.uc.CreateDocument(ingestCtx(), libraryAuth(), &KnowledgeDocument{
		LibraryID: 1, Kind: DocumentKindFile, Title: "direct", Status: KnowledgeStatusEnabled,
	})
	if !isInvalidArgument(err) {
		t.Fatalf("err = %v, want invalid argument (file facts missing)", err)
	}
	if f.repo.createdDoc != nil {
		t.Fatalf("row created despite missing file facts")
	}
}

func TestDeleteDocument_file_cleans_media_row_and_object(t *testing.T) {
	f := newUploadFixture(t)
	fileID := int64(9)
	f.files.byID[9] = &KnowledgeFile{ID: 9, ObjectKey: "kb-file/9"}
	f.repo.doc = &KnowledgeDocument{ID: 2, LibraryID: 1, Kind: DocumentKindFile, Title: "page", FileID: &fileID}

	if err := f.uc.DeleteDocument(ingestCtx(), libraryAuth(), 2); err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(f.files.deleted) != 1 || f.files.deleted[0] != 9 {
		t.Fatalf("media soft delete calls = %v, want [9]", f.files.deleted)
	}
	if len(f.objects.deletedKeys) != 1 || f.objects.deletedKeys[0] != "kb-file/9" {
		t.Fatalf("object delete calls = %v, want [kb-file/9]", f.objects.deletedKeys)
	}
}

func TestDeleteDocument_text_document_skips_media_cleanup(t *testing.T) {
	f := newUploadFixture(t)
	f.repo.doc = &KnowledgeDocument{ID: 3, LibraryID: 1, Kind: DocumentKindText, Title: "plain"}

	if err := f.uc.DeleteDocument(ingestCtx(), libraryAuth(), 3); err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(f.files.deleted) != 0 || len(f.objects.deletedKeys) != 0 {
		t.Fatalf("media cleanup ran for text document")
	}
}

// fileParseUnavailableError 构造「外部档类型但解析服务未配置」的组合错误
// （与 docparse 实现的错误形态一致）。
func fileParseUnavailableError() error {
	return fmt.Errorf("%w: %v", ErrFileTypeUnsupported, ErrFileParseUnavailable)
}

// markdownInput 取解析器最近一次输入的 MIME（替身记录）。
func (p *fakeParser) markdownInput() string { return p.lastMIME }
