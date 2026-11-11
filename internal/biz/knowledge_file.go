package biz

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	knowledgev1 "grape/api/knowledge/v1"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
)

// 文件摄取（kind=file）用例：上传识别 → 原件落对象存储与介质行 → 目录行
// 落库 → 解析与内容识别处理 → 摄取。失败语义分两段：
//   - 介质成立前（识别 / 校验 / OSS 写入）失败：无任何落库副作用，识别与
//     校验问题按 FILE_TYPE_UNSUPPORTED / DOCUMENT_INVALID_ARGUMENT 拒绝；
//   - 介质成立后（解析 / 内容识别 / 摄取）失败：目录行与介质行保留
//     （content 保持空），管理页可重解析——同 text 摄取失败的留痕口径，
//     对外统一 DOCUMENT_INDEX_FAILED。
//
// 重解析（reindex 对 kind=file 的扩展）从介质行现场取回原件字节重走解析
// 与摄取：解析器升级后重解析即修复历史条目，原件因此保留。

// KnowledgeFile 是文件介质领域模型：kind=file 条目的原件元数据，对象字节
// 在 ObjectStore（object_key 指向），表与 OSS 都不存解析产物。Sha256 是
// 原件内容身份（与 Etag 的 MD5 互为冗余校验）。
type KnowledgeFile struct {
	ID        int64
	ObjectKey string
	Etag      string
	Sha256    string
	MimeType  string
	FileSize  int64
	CreatedBy int64
	UpdatedBy int64
}

// KnowledgeFileRepo 是文件介质仓储接口。介质行是租户表，租户边界由 RLS
// 承担：跨租户的 id 不可见等同不存在。ID 与 ObjectKey（行 ID 派生，形如
// kb-file/<发号 ID>）由实现发号——上传先建行后写字节，键在写入前即确定。
type KnowledgeFileRepo interface {
	// GetFile 按 id 读取未软删介质行；不存在（含跨租户不可见）返回
	// ErrDocumentNotFound。
	GetFile(ctx context.Context, auth Identity, id int64) (*KnowledgeFile, error)
	// CreateFile 插入介质行：MimeType 取嗅探事实，Etag/Sha256/FileSize
	// 由用例按原件字节算好传入。
	CreateFile(ctx context.Context, auth Identity, f *KnowledgeFile) (*KnowledgeFile, error)
	// SoftDeleteFile 软删介质行（宿主条目删除后的清理；OSS 对象由用例
	// 尽力删除）。
	SoftDeleteFile(ctx context.Context, auth Identity, id int64) error
}

// KnowledgeFileUpload 是文件上传的入参：FileName 保留上传原名（落库与
// 风险比对用，识别不信任其声明），Body 是原始文件字节。
type KnowledgeFileUpload struct {
	LibraryID  int64
	Title      string
	SourceName *string
	FileName   string
	Body       []byte
	Status     int32
	ValidFrom  *time.Time
	ValidTo    *time.Time
}

// fileIdentifyError 把识别阶段的拒绝折叠为 FILE_TYPE_UNSUPPORTED（400）：
// 文案携带具体原因（扩展名/嗅探 MIME 不在白名单、外部解析服务未配置、
// zip 容器预检不过），调用方无需翻服务端日志即可处置；完整性问题（空
// 文件、超限）保持 DOCUMENT_INVALID_ARGUMENT 口径。
func fileIdentifyError(err error) error {
	if errors.Is(err, ErrDocumentInvalidArgument) {
		return err
	}
	return kratoserrors.BadRequest(
		knowledgev1.ErrorReason_FILE_TYPE_UNSUPPORTED.String(), err.Error())
}

// UploadDocument 摄取上传文件为 kind=file 条目。识别在介质成立前完成
// （不在白名单的字节不进对象存储），原件先落介质行与 OSS、目录行后落
// （content 保持空），解析与摄取失败留痕可重解析。
func (uc *KnowledgeUsecase) UploadDocument(ctx context.Context, auth Identity, in *KnowledgeFileUpload) (*KnowledgeDocument, error) {
	if in == nil || in.LibraryID <= 0 {
		return nil, ErrDocumentInvalidArgument
	}
	if err := uc.fileFacilities(); err != nil {
		return nil, err
	}
	// 识别先行（三层校验无任何副作用），识别结论即目录行的原件事实。
	identity, err := identifyFile(in.FileName, in.Body, sniffMIME)
	if err != nil {
		return nil, fileIdentifyError(err)
	}
	mimeType := identity.sniffedMIME
	fileSize := int64(len(in.Body))
	doc := &KnowledgeDocument{
		Kind:       DocumentKindFile,
		LibraryID:  in.LibraryID,
		Title:      in.Title,
		SourceName: in.SourceName,
		Status:     in.Status,
		ValidFrom:  in.ValidFrom,
		ValidTo:    in.ValidTo,
		FileName:   &in.FileName,
		MimeType:   &mimeType,
		FileSize:   &fileSize,
	}
	if err := validateDocument(doc, false); err != nil {
		return nil, err
	}
	lib, err := uc.repo.GetLibrary(ctx, auth, in.LibraryID)
	if err != nil {
		return nil, err
	}
	// 介质行先落（发号即得 object_key），对象字节随后写入；写入失败尽力
	// 回收介质行，不留悬挂引用。
	file, err := uc.files.CreateFile(ctx, auth, &KnowledgeFile{
		Etag:     md5Hex(in.Body),
		Sha256:   sha256Hex(in.Body),
		MimeType: identity.sniffedMIME,
		FileSize: fileSize,
	})
	if err != nil {
		return nil, err
	}
	if _, err := uc.objects.Put(ctx, file.ObjectKey, bytes.NewReader(in.Body), identity.sniffedMIME); err != nil {
		uc.cleanupMedia(ctx, auth, file, "upload rejected")
		return nil, fmt.Errorf("biz: put file object %s: %w", file.ObjectKey, err)
	}
	doc.FileID = &file.ID
	// 校验与库判定已在介质成立前完成，目录行经 insertDocumentRow 落库，
	// 不重复校验。
	created, err := uc.insertDocumentRow(ctx, auth, doc)
	if err != nil {
		// 目录行未落成：介质行与对象已成孤儿，尽力回收。
		uc.cleanupMedia(ctx, auth, file, "document row rejected")
		return nil, err
	}
	// 解析与摄取失败留痕：目录行（content 空）与介质行保留，可重解析。
	return uc.reparseDocument(ctx, auth, lib, created)
}

// fileFacilities 校验文件摄取依赖齐备：仓储/对象存储/解析器任一缺失即
// 拒绝（装配错误 fail-closed，不静默半可用）。
func (uc *KnowledgeUsecase) fileFacilities() error {
	switch {
	case uc.files == nil:
		return errors.New("biz: knowledge file repo is not configured")
	case uc.objects == nil:
		return errors.New("biz: object store is not configured")
	case uc.parser == nil:
		return errors.New("biz: file parser is not configured")
	}
	return nil
}

// reparseDocument 对 kind=file 条目重走解析与摄取：从介质行取回原件字节，
// 重新识别（路由可能随白名单演进变化）、解析/内容识别、回写解析产物后
// 摄取。解析产物落目录行（content/content_hash），摄取按路由档位的切分
// kind（direct 档按原文件形态，解析档按 markdown）。失败统一折叠为
// ErrDocumentIndexFailed：目录行保留、管理页可重试。上传与重解析共用本
// 入口（CreateDocument / reindex 的 file 分支同）。
func (uc *KnowledgeUsecase) reparseDocument(ctx context.Context, auth Identity, lib *KnowledgeLibrary, doc *KnowledgeDocument) (*KnowledgeDocument, error) {
	content, ingestKind, err := uc.parseFileContent(ctx, auth, doc)
	if err != nil {
		slog.WarnContext(ctx, "knowledge file document parse failed; directory row retained for re-parse",
			slog.Int64("document_id", doc.ID),
			slog.Int64("library_id", doc.LibraryID),
			slog.Any("error", err))
		return nil, fmt.Errorf("%w: document %d: %v", ErrDocumentIndexFailed, doc.ID, err)
	}
	doc.Content = &content
	doc.ContentHash = hashContent(doc.Content)
	updated, err := uc.repo.UpdateDocument(ctx, auth, doc)
	if err != nil {
		return nil, err
	}
	return uc.ingestDocument(ctx, auth, lib, updated, ingestKind)
}

// parseFileContent 从介质行取回原件并产出解析正文与摄取切分 kind：direct
// 档免解析（内容识别处理直出，切分 kind 取识别结论），native / external
// 档经解析器产 markdown（切分 kind 恒 markdown）。识别在取回后重跑——
// 字节与白名单的现状结论一致才继续，防目录元数据被旁路改写后带病解析。
func (uc *KnowledgeUsecase) parseFileContent(ctx context.Context, auth Identity, doc *KnowledgeDocument) (string, string, error) {
	if doc.FileID == nil {
		return "", "", errors.New("biz: file document has no media reference")
	}
	file, err := uc.files.GetFile(ctx, auth, *doc.FileID)
	if err != nil {
		return "", "", fmt.Errorf("biz: get file media %d: %w", *doc.FileID, err)
	}
	rc, err := uc.objects.Get(ctx, file.ObjectKey)
	if err != nil {
		return "", "", fmt.Errorf("biz: get file object %s: %w", file.ObjectKey, err)
	}
	defer rc.Close()
	body, err := readObjectBody(rc)
	if err != nil {
		return "", "", fmt.Errorf("biz: read file object %s: %w", file.ObjectKey, err)
	}
	identity, err := identifyFile(stringOrEmpty(doc.FileName), body, sniffMIME)
	if err != nil {
		return "", "", fmt.Errorf("biz: identify stored file: %w", err)
	}
	if identity.route == FileRouteDirect {
		content, err := prepareFileContent(body)
		return content, identity.directKind, err
	}
	markdown, err := uc.parser.Parse(ctx, &FileParseInput{MIME: identity.sniffedMIME, FileName: stringOrEmpty(doc.FileName), Body: body})
	if err != nil {
		return "", "", fmt.Errorf("biz: parse file: %w", err)
	}
	content, err := prepareFileContent([]byte(markdown))
	return content, DocumentKindMarkdown, err
}

// readObjectBody 读取对象字节并按单文件上限设防（设施端限流的兜底：
// 端点切换到不限流的真实 OSS 后，历史超限对象不再进入解析）。
func readObjectBody(rc io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(rc, maxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxFileBytes {
		return nil, fmt.Errorf("%w: stored file exceeds the %d byte limit", ErrDocumentInvalidArgument, maxFileBytes)
	}
	return body, nil
}

// cleanupMedia 回收介质行与 OSS 对象（上传链路失败路径的孤儿清理）：
// 对象删除失败只记日志（介质行已软删，悬挂对象由运维清理）。
func (uc *KnowledgeUsecase) cleanupMedia(ctx context.Context, auth Identity, file *KnowledgeFile, cause string) {
	if err := uc.files.SoftDeleteFile(ctx, auth, file.ID); err != nil {
		slog.WarnContext(ctx, "knowledge file media cleanup failed",
			slog.String("cause", cause),
			slog.Int64("file_id", file.ID),
			slog.Any("error", err))
		return
	}
	if err := uc.objects.Delete(ctx, file.ObjectKey); err != nil {
		slog.WarnContext(ctx, "knowledge file object cleanup failed",
			slog.String("cause", cause),
			slog.String("object_key", file.ObjectKey),
			slog.Any("error", err))
	}
}

// md5Hex 计算字节的 MD5 十六进制（S3 ETag 语义，简单上传即内容 MD5）。
func md5Hex(body []byte) string {
	sum := md5.Sum(body)
	return hex.EncodeToString(sum[:])
}

// sha256Hex 计算字节的 sha256 十六进制（介质行的内容身份）。
func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
