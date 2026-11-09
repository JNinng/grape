package biz

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	knowledgev1 "grape/api/knowledge/v1"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

var (
	// ErrLibraryNotFound 表示目标知识库不存在（含跨租户不可见）。
	ErrLibraryNotFound = kratoserrors.NotFound(knowledgev1.ErrorReason_LIBRARY_NOT_FOUND.String(), "knowledge library not found")
	// ErrLibraryInvalidArgument 表示知识库请求参数非法。
	ErrLibraryInvalidArgument = kratoserrors.BadRequest(knowledgev1.ErrorReason_LIBRARY_INVALID_ARGUMENT.String(), "invalid knowledge library argument")
	// ErrLibraryAlreadyExists 表示租户内未删除库间名称唯一冲突。
	ErrLibraryAlreadyExists = kratoserrors.Conflict(knowledgev1.ErrorReason_LIBRARY_ALREADY_EXISTS.String(), "knowledge library already exists")
	// ErrLibraryHasDocuments 表示库内仍有未删除条目，禁止删除。
	ErrLibraryHasDocuments = kratoserrors.Conflict(knowledgev1.ErrorReason_LIBRARY_HAS_DOCUMENTS.String(), "knowledge library has documents")
	// ErrDocumentNotFound 表示目标条目不存在（含跨租户不可见）。
	ErrDocumentNotFound = kratoserrors.NotFound(knowledgev1.ErrorReason_DOCUMENT_NOT_FOUND.String(), "knowledge document not found")
	// ErrDocumentInvalidArgument 表示条目请求参数非法。
	ErrDocumentInvalidArgument = kratoserrors.BadRequest(knowledgev1.ErrorReason_DOCUMENT_INVALID_ARGUMENT.String(), "invalid knowledge document argument")
	// ErrDocumentKindUnsupported 表示本期只支持文本条目。
	ErrDocumentKindUnsupported = kratoserrors.BadRequest(knowledgev1.ErrorReason_DOCUMENT_KIND_UNSUPPORTED.String(), "only text documents are supported")
	// ErrDocumentIndexFailed 表示摄取/重建索引失败。
	ErrDocumentIndexFailed = kratoserrors.InternalServer(knowledgev1.ErrorReason_DOCUMENT_INDEX_FAILED.String(), "knowledge document indexing failed")
	// ErrDocumentChunksUnavailable 表示分片查询能力缺失（向量设施未启用）。
	ErrDocumentChunksUnavailable = kratoserrors.ServiceUnavailable(knowledgev1.ErrorReason_DOCUMENT_CHUNKS_UNAVAILABLE.String(), "knowledge document chunks unavailable")
	// ErrDocumentChunksFailed 表示分片查询执行失败（细节只进服务端日志）。
	ErrDocumentChunksFailed = kratoserrors.InternalServer(knowledgev1.ErrorReason_DOCUMENT_CHUNKS_FAILED.String(), "knowledge document chunks failed")
)

// 知识库与条目状态取值（与存储层 status 对齐）。
const (
	KnowledgeStatusDisabled    int32 = 0
	KnowledgeStatusEnabled     int32 = 1
	KnowledgeStatusUnspecified int32 = -1
)

// 有效期缺省边界：无限期失效时间写 253402300799（9999-12-31T23:59:59Z），
// 与 §3.4 的 chunk metadata 契约一致。
const unixInfinite = int64(253402300799)

// KnowledgeLibrary 是知识库领域模型。ChunkDelimiter 是切分区分串：写入
// 侧 nil 表示未设置（创建落缺省区分串、更新保持原值），空串表示关闭
// 区分串切分；读出恒非 nil。
type KnowledgeLibrary struct {
	ID                int64
	Status            int32
	Name              string
	Remark            *string
	EmbeddingModelKey *string
	ChunkSize         int32
	ChunkOverlap      int32
	ChunkDelimiter    *string
	ValidFrom         *time.Time
	ValidTo           *time.Time
	CreatedBy         int64
	UpdatedBy         int64
	CreatedAt         time.Time
	UpdatedAt         *time.Time
	DeletedAt         *time.Time
}

// KnowledgeLibraryList 是知识库动态列表信封。
type KnowledgeLibraryList struct {
	Items      []*KnowledgeLibrary
	Total      *int64
	HasMore    bool
	NextCursor string
}

// KnowledgeDocument 是知识条目领域模型。Content 在列表响应中可以为空
// （data 层按调用方形态回填），ContentHash 不对外暴露。
type KnowledgeDocument struct {
	ID              int64
	LibraryID       int64
	Kind            string
	Title           string
	Content         *string
	SourceName      *string
	ContentHash     *string
	Status          int32
	ValidFrom       *time.Time
	ValidTo         *time.Time
	IndexedAt       *time.Time
	IndexedEmbedder *string
	CreatedBy       int64
	UpdatedBy       int64
	CreatedAt       time.Time
	UpdatedAt       *time.Time
	DeletedAt       *time.Time
}

// KnowledgeDocumentList 是条目动态列表信封。
type KnowledgeDocumentList struct {
	Items      []*KnowledgeDocument
	Total      *int64
	HasMore    bool
	NextCursor string
}

// KnowledgeDocumentPage 是库内条目固定分页结果。
type KnowledgeDocumentPage struct {
	Items []*KnowledgeDocument
	Total int64
}

// KnowledgeRepo 是知识库与条目的仓储接口。两者都是租户表，租户边界由
// RLS 承担；动态 List 消费 dynq 协议。更新路径先锁定目标行再写入。
type KnowledgeRepo interface {
	GetLibrary(ctx context.Context, auth Identity, id int64) (*KnowledgeLibrary, error)
	ListLibraries(ctx context.Context, auth Identity, q *dynq.Query) (*KnowledgeLibraryList, error)
	CreateLibrary(ctx context.Context, auth Identity, l *KnowledgeLibrary) (*KnowledgeLibrary, error)
	UpdateLibrary(ctx context.Context, auth Identity, l *KnowledgeLibrary) (*KnowledgeLibrary, error)
	SoftDeleteLibrary(ctx context.Context, auth Identity, id int64) error
	CountDocumentsByLibrary(ctx context.Context, auth Identity, libraryID int64) (int64, error)

	GetDocument(ctx context.Context, auth Identity, id int64) (*KnowledgeDocument, error)
	ListDocuments(ctx context.Context, auth Identity, q *dynq.Query) (*KnowledgeDocumentList, error)
	ListDocumentsByLibrary(ctx context.Context, auth Identity, libraryID int64, limit, offset int32) (*KnowledgeDocumentPage, error)
	CreateDocument(ctx context.Context, auth Identity, d *KnowledgeDocument) (*KnowledgeDocument, error)
	UpdateDocument(ctx context.Context, auth Identity, d *KnowledgeDocument) (*KnowledgeDocument, error)
	MarkDocumentIndexed(ctx context.Context, auth Identity, id int64, embedder string) (*KnowledgeDocument, error)
	SoftDeleteDocument(ctx context.Context, auth Identity, id int64) error
}

// KnowledgeUsecase 是知识库与条目用例：目录校验、切分/向量化编排、
// 状态与有效期同步进 chunk metadata、删除清理。
type KnowledgeUsecase struct {
	repo   KnowledgeRepo
	models ModelRepo
	store  VectorStore
	ingest *DocumentUsecase
}

// NewKnowledgeUsecase 构造知识库用例。
func NewKnowledgeUsecase(repo KnowledgeRepo, models ModelRepo, store VectorStore, ingest *DocumentUsecase) *KnowledgeUsecase {
	return &KnowledgeUsecase{repo: repo, models: models, store: store, ingest: ingest}
}

// GetLibrary 按 id 查询知识库。
func (uc *KnowledgeUsecase) GetLibrary(ctx context.Context, auth Identity, id int64) (*KnowledgeLibrary, error) {
	if id <= 0 {
		return nil, ErrLibraryInvalidArgument
	}
	return uc.repo.GetLibrary(ctx, auth, id)
}

// ListLibraries 动态列出租户内知识库。
func (uc *KnowledgeUsecase) ListLibraries(ctx context.Context, auth Identity, q *dynq.Query) (*KnowledgeLibraryList, error) {
	return uc.repo.ListLibraries(ctx, auth, q)
}

// ListModelOptions 返回知识库可用的模型选项（权限码由入口层把守）。
func (uc *KnowledgeUsecase) ListModelOptions(ctx context.Context, auth Identity, kind string) ([]*ModelCfg, error) {
	kind = strings.TrimSpace(kind)
	if kind != "" && kind != ModelKindText && kind != ModelKindEmbedding {
		return nil, kratoserrors.BadRequest(knowledgev1.ErrorReason_MODEL_OPTION_INVALID_ARGUMENT.String(), "invalid model option kind")
	}
	return uc.models.ListModelOptions(ctx, auth, kind)
}

// CreateLibrary 创建知识库：校验名称/切分参数/有效期/嵌入模型键。
func (uc *KnowledgeUsecase) CreateLibrary(ctx context.Context, auth Identity, l *KnowledgeLibrary) (*KnowledgeLibrary, error) {
	if err := uc.validateLibrary(ctx, auth, l, false); err != nil {
		return nil, err
	}
	if l.Status == KnowledgeStatusUnspecified {
		l.Status = KnowledgeStatusEnabled
	}
	normalizeChunking(l)
	return uc.repo.CreateLibrary(ctx, auth, l)
}

// UpdateLibrary 覆盖知识库可变字段，并把新状态/有效期同步到库内全部
// 条目的 chunk metadata（不重新嵌入）。同步失败使本次更新失败。未携带
// 区分串时保持原值（不隐式改变存量库的切分行为）。
func (uc *KnowledgeUsecase) UpdateLibrary(ctx context.Context, auth Identity, l *KnowledgeLibrary) (*KnowledgeLibrary, error) {
	if l == nil || l.ID <= 0 {
		return nil, ErrLibraryInvalidArgument
	}
	if err := uc.validateLibrary(ctx, auth, l, true); err != nil {
		return nil, err
	}
	if l.ChunkDelimiter == nil {
		existing, err := uc.repo.GetLibrary(ctx, auth, l.ID)
		if err != nil {
			return nil, err
		}
		l.ChunkDelimiter = existing.ChunkDelimiter
	}
	normalizeChunking(l)
	if err := uc.syncLibraryChunks(ctx, auth, l); err != nil {
		return nil, err
	}
	return uc.repo.UpdateLibrary(ctx, auth, l)
}

// DeleteLibrary 软删除知识库：存在未删除条目时拒绝；无条目时清理库内
// 全部 chunk（含残留）后软删。
func (uc *KnowledgeUsecase) DeleteLibrary(ctx context.Context, auth Identity, id int64) error {
	if id <= 0 {
		return ErrLibraryInvalidArgument
	}
	count, err := uc.repo.CountDocumentsByLibrary(ctx, auth, id)
	if err != nil {
		return err
	}
	if count > 0 {
		return ErrLibraryHasDocuments
	}
	if _, err := uc.store.DeleteByFilter(ctx, &VectorFilter{Conditions: []MetadataCondition{
		{Key: "kb_id", Op: VectorMetadataEq, Values: []any{idString(id)}},
	}}); err != nil {
		return fmt.Errorf("biz: clear chunks of library %d: %w", id, err)
	}
	return uc.repo.SoftDeleteLibrary(ctx, auth, id)
}

// KnowledgeChunk 是条目在向量存储中的一个实际分片：Index 为原文顺序，
// Anchor 与 Content 取自切片元数据与正文，Embedder 是落库时的嵌入空间。
type KnowledgeChunk struct {
	ID       string
	Index    int
	Anchor   string
	Content  string
	Embedder string
}

// ListDocumentChunks 返回条目的实际分片（按分片序），供管理页核对切分
// 结果。条目不存在按 NotFound 拒绝；分片只经 VectorStore 枚举——切分实现
// 与存量数据可能不一致（改过区分串/切分参数而未重建），回放切分算法得到
// 的不是「实际」分片。设施未启用的错误原样透传，不降级为空列表。
func (uc *KnowledgeUsecase) ListDocumentChunks(ctx context.Context, auth Identity, id int64) ([]*KnowledgeChunk, error) {
	if id <= 0 {
		return nil, ErrDocumentInvalidArgument
	}
	if _, err := uc.repo.GetDocument(ctx, auth, id); err != nil {
		return nil, err
	}
	docs, err := uc.store.List(ctx, &VectorFilter{Conditions: []MetadataCondition{
		{Key: "document_id", Op: VectorMetadataEq, Values: []any{idString(id)}},
	}})
	if err != nil {
		return nil, fmt.Errorf("biz: list chunks of document %d: %w", id, err)
	}
	chunks := make([]*KnowledgeChunk, 0, len(docs))
	for _, doc := range docs {
		if doc == nil {
			continue
		}
		chunks = append(chunks, &KnowledgeChunk{
			ID:       doc.ID,
			Index:    chunkIndexOf(doc.ID, doc.Metadata),
			Anchor:   metadataText(doc.Metadata, "anchor"),
			Content:  doc.Content,
			Embedder: doc.Embedder,
		})
	}
	sort.SliceStable(chunks, func(i, j int) bool {
		if chunks[i].Index != chunks[j].Index {
			return chunks[i].Index < chunks[j].Index
		}
		return chunks[i].ID < chunks[j].ID
	})
	return chunks, nil
}

// chunkIndexOf 取分片序号：切片 ID 的 "#<index>" 后缀是摄取侧确定性派生
// 的结果，优先采用；形状不符时回落 metadata 的 chunk_index。
func chunkIndexOf(id string, metadata map[string]any) int {
	if at := strings.LastIndex(id, "#"); at >= 0 {
		if index, err := strconv.Atoi(id[at+1:]); err == nil {
			return index
		}
	}
	switch v := metadata["chunk_index"].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		if index, err := strconv.Atoi(v); err == nil {
			return index
		}
	}
	return 0
}

// GetDocument 按 id 查询条目。
func (uc *KnowledgeUsecase) GetDocument(ctx context.Context, auth Identity, id int64) (*KnowledgeDocument, error) {
	if id <= 0 {
		return nil, ErrDocumentInvalidArgument
	}
	return uc.repo.GetDocument(ctx, auth, id)
}

// ListDocuments 动态列出租户内条目（列表形态不返回正文）。
func (uc *KnowledgeUsecase) ListDocuments(ctx context.Context, auth Identity, q *dynq.Query) (*KnowledgeDocumentList, error) {
	return uc.repo.ListDocuments(ctx, auth, q)
}

// ListDocumentsByLibrary 固定分页列出库内条目。
func (uc *KnowledgeUsecase) ListDocumentsByLibrary(ctx context.Context, auth Identity, libraryID int64, limit, offset int32) (*KnowledgeDocumentPage, error) {
	if libraryID <= 0 {
		return nil, ErrDocumentInvalidArgument
	}
	if _, err := uc.repo.GetLibrary(ctx, auth, libraryID); err != nil {
		return nil, err
	}
	return uc.repo.ListDocumentsByLibrary(ctx, auth, libraryID, limit, offset)
}

// CreateDocument 创建文本条目并摄取：目录行先落库（获得 id），摄取完成后
// 回写 indexed_at/indexed_embedder。摄取失败时目录行保留，管理页可重建索引。
func (uc *KnowledgeUsecase) CreateDocument(ctx context.Context, auth Identity, d *KnowledgeDocument) (*KnowledgeDocument, error) {
	if d == nil {
		return nil, ErrDocumentInvalidArgument
	}
	if d.Kind != "" && d.Kind != "text" {
		return nil, ErrDocumentKindUnsupported
	}
	d.Kind = "text"
	if d.Status == KnowledgeStatusUnspecified {
		d.Status = KnowledgeStatusEnabled
	}
	if err := validateDocument(d, false); err != nil {
		return nil, err
	}
	lib, err := uc.repo.GetLibrary(ctx, auth, d.LibraryID)
	if err != nil {
		return nil, err
	}
	d.ContentHash = hashContent(d.Content)
	created, err := uc.repo.CreateDocument(ctx, auth, d)
	if err != nil {
		return nil, err
	}
	indexed, err := uc.reindex(ctx, auth, lib, created)
	if err != nil {
		return nil, err
	}
	return indexed, nil
}

// UpdateDocument 覆盖条目可变字段：正文变更时重新摄取；仅状态/有效期
// 变更时同步 chunk metadata（不重嵌）。library_id 与 kind 不可变。
func (uc *KnowledgeUsecase) UpdateDocument(ctx context.Context, auth Identity, d *KnowledgeDocument) (*KnowledgeDocument, error) {
	if d == nil || d.ID <= 0 {
		return nil, ErrDocumentInvalidArgument
	}
	existing, err := uc.repo.GetDocument(ctx, auth, d.ID)
	if err != nil {
		return nil, err
	}
	if d.LibraryID == 0 {
		d.LibraryID = existing.LibraryID
	}
	if d.LibraryID != existing.LibraryID {
		return nil, ErrDocumentInvalidArgument
	}
	if d.Kind != "" && d.Kind != existing.Kind {
		return nil, ErrDocumentInvalidArgument
	}
	d.Kind = existing.Kind
	// 未携带正文/标题时按不变处理（状态/有效期变更不要求重传正文）。
	if d.Content == nil {
		d.Content = existing.Content
	}
	if strings.TrimSpace(d.Title) == "" {
		d.Title = existing.Title
	}
	if err := validateDocument(d, true); err != nil {
		return nil, err
	}
	lib, err := uc.repo.GetLibrary(ctx, auth, d.LibraryID)
	if err != nil {
		return nil, err
	}
	newHash := hashContent(d.Content)
	contentChanged := contentChanged(existing.ContentHash, newHash)
	d.ContentHash = newHash

	if contentChanged {
		updated, err := uc.repo.UpdateDocument(ctx, auth, d)
		if err != nil {
			return nil, err
		}
		return uc.reindex(ctx, auth, lib, updated)
	}

	// 仅状态/有效期变更：先把新值同步进 chunk metadata，再写目录行；
	// 同步失败使本次更新失败，避免「目录停用但检索仍命中」。
	patch := effectiveChunkMetadata(lib, d)
	if _, err := uc.store.UpdateMetadata(ctx, &VectorFilter{Conditions: []MetadataCondition{
		{Key: "document_id", Op: VectorMetadataEq, Values: []any{idString(d.ID)}},
	}}, patch); err != nil {
		return nil, fmt.Errorf("biz: sync chunk metadata of document %d: %w", d.ID, err)
	}
	return uc.repo.UpdateDocument(ctx, auth, d)
}

// DeleteDocument 软删除条目并清理其 chunk（chunk 先删，避免残留可检索行）。
func (uc *KnowledgeUsecase) DeleteDocument(ctx context.Context, auth Identity, id int64) error {
	if id <= 0 {
		return ErrDocumentInvalidArgument
	}
	if _, err := uc.repo.GetDocument(ctx, auth, id); err != nil {
		return err
	}
	if _, err := uc.store.DeleteByFilter(ctx, &VectorFilter{Conditions: []MetadataCondition{
		{Key: "document_id", Op: VectorMetadataEq, Values: []any{idString(id)}},
	}}); err != nil {
		return fmt.Errorf("biz: clear chunks of document %d: %w", id, err)
	}
	return uc.repo.SoftDeleteDocument(ctx, auth, id)
}

// ReindexDocument 重建条目向量：按当前正文与库配置重新摄取。
func (uc *KnowledgeUsecase) ReindexDocument(ctx context.Context, auth Identity, id int64) (*KnowledgeDocument, error) {
	if id <= 0 {
		return nil, ErrDocumentInvalidArgument
	}
	doc, err := uc.repo.GetDocument(ctx, auth, id)
	if err != nil {
		return nil, err
	}
	lib, err := uc.repo.GetLibrary(ctx, auth, doc.LibraryID)
	if err != nil {
		return nil, err
	}
	return uc.reindex(ctx, auth, lib, doc)
}

// reindex 执行摄取并回写索引事实。库级 embedding_model_key 与切分参数在
// 本次摄取现场生效。
func (uc *KnowledgeUsecase) reindex(ctx context.Context, auth Identity, lib *KnowledgeLibrary, doc *KnowledgeDocument) (*KnowledgeDocument, error) {
	if doc.Content == nil || strings.TrimSpace(*doc.Content) == "" {
		return nil, ErrDocumentInvalidArgument
	}
	metadata := effectiveChunkMetadata(lib, doc)
	sourceName := doc.Title
	if doc.SourceName != nil && strings.TrimSpace(*doc.SourceName) != "" {
		sourceName = *doc.SourceName
	}
	result, err := uc.ingest.Ingest(ctx, &Document{
		ID:             idString(doc.ID),
		Name:           sourceName,
		Content:        *doc.Content,
		Metadata:       metadata,
		Embedder:       stringOrEmpty(lib.EmbeddingModelKey),
		ChunkSize:      int(lib.ChunkSize),
		ChunkOverlap:   int(lib.ChunkOverlap),
		ChunkDelimiter: lib.ChunkDelimiter,
	})
	if err != nil {
		// 客户端只收到统一的 DOCUMENT_INDEX_FAILED；底层原因（嵌入端点不可用、
		// 维度漂移、向量设施未启用等）落服务日志，供管理页排障。
		slog.WarnContext(ctx, "knowledge document indexing failed",
			slog.Int64("document_id", doc.ID),
			slog.Int64("library_id", doc.LibraryID),
			slog.String("embedder", stringOrEmpty(lib.EmbeddingModelKey)),
			slog.Any("error", err))
		return nil, fmt.Errorf("%w: document %d: %v", ErrDocumentIndexFailed, doc.ID, err)
	}
	indexed, err := uc.repo.MarkDocumentIndexed(ctx, auth, doc.ID, result.Embedder)
	if err != nil {
		return nil, err
	}
	return indexed, nil
}

// syncLibraryChunks 把库的新状态/有效期同步进库内全部条目的 chunk
// metadata：逐条按「库 AND 条目」计算生效状态与有效期交集。
func (uc *KnowledgeUsecase) syncLibraryChunks(ctx context.Context, auth Identity, lib *KnowledgeLibrary) error {
	const pageSize int32 = 200
	var offset int32
	for {
		page, err := uc.repo.ListDocumentsByLibrary(ctx, auth, lib.ID, pageSize, offset)
		if err != nil {
			return err
		}
		for _, doc := range page.Items {
			patch := effectiveChunkMetadata(lib, doc)
			if _, err := uc.store.UpdateMetadata(ctx, &VectorFilter{Conditions: []MetadataCondition{
				{Key: "document_id", Op: VectorMetadataEq, Values: []any{idString(doc.ID)}},
			}}, patch); err != nil {
				return fmt.Errorf("biz: sync chunk metadata of document %d: %w", doc.ID, err)
			}
		}
		if int32(len(page.Items)) < pageSize {
			return nil
		}
		offset += pageSize
	}
}

// validateLibrary 校验知识库可变字段并校验嵌入模型键可见（非空时）。
func (uc *KnowledgeUsecase) validateLibrary(ctx context.Context, auth Identity, l *KnowledgeLibrary, update bool) error {
	if l == nil {
		return ErrLibraryInvalidArgument
	}
	if strings.TrimSpace(l.Name) == "" || utf8.RuneCountInString(strings.TrimSpace(l.Name)) > 128 {
		return ErrLibraryInvalidArgument
	}
	if l.Remark != nil && utf8.RuneCountInString(*l.Remark) > 512 {
		return ErrLibraryInvalidArgument
	}
	if l.ChunkDelimiter != nil && utf8.RuneCountInString(*l.ChunkDelimiter) > maxChunkDelimiterRunes {
		return ErrLibraryInvalidArgument
	}
	if !update {
		if l.ChunkSize < 0 || l.ChunkOverlap < 0 {
			return ErrLibraryInvalidArgument
		}
	} else {
		if l.ChunkSize <= 0 {
			return ErrLibraryInvalidArgument
		}
	}
	if l.ValidFrom != nil && l.ValidTo != nil && !l.ValidTo.After(*l.ValidFrom) {
		return ErrLibraryInvalidArgument
	}
	switch {
	case update:
		if l.Status != KnowledgeStatusDisabled && l.Status != KnowledgeStatusEnabled {
			return ErrLibraryInvalidArgument
		}
	case l.Status != KnowledgeStatusUnspecified && l.Status != KnowledgeStatusDisabled && l.Status != KnowledgeStatusEnabled:
		return ErrLibraryInvalidArgument
	}
	key := strings.TrimSpace(stringOrEmpty(l.EmbeddingModelKey))
	if key != "" {
		if err := uc.validateEmbeddingKey(ctx, auth, key); err != nil {
			return err
		}
		l.EmbeddingModelKey = &key
	}
	return nil
}

// validateEmbeddingKey 校验知识库选中的嵌入模型键在可见且启用的模型选项里。
func (uc *KnowledgeUsecase) validateEmbeddingKey(ctx context.Context, auth Identity, key string) error {
	options, err := uc.models.ListModelOptions(ctx, auth, ModelKindEmbedding)
	if err != nil {
		return err
	}
	for _, option := range options {
		if option.ModelKey == key {
			return nil
		}
	}
	return ErrLibraryInvalidArgument
}

// normalizeChunking 归一化切分参数：创建时两者皆 0 取缺省 800/100；
// 仅 chunk_size 为 0 取 800；重叠不小于块大小时视为非法（由调用方判定）；
// 区分串未设置取缺省区分串（显式空串保留，表示关闭区分串切分）。
func normalizeChunking(l *KnowledgeLibrary) {
	if l.ChunkSize == 0 {
		l.ChunkSize = 800
		if l.ChunkOverlap == 0 {
			l.ChunkOverlap = 100
		}
	}
	if l.ChunkDelimiter == nil {
		delimiter := defaultChunkDelimiter
		l.ChunkDelimiter = &delimiter
	}
}

// validateDocument 校验条目可变字段：标题/正文、有效期、状态。
func validateDocument(d *KnowledgeDocument, update bool) error {
	if strings.TrimSpace(d.Title) == "" || utf8.RuneCountInString(strings.TrimSpace(d.Title)) > 256 {
		return ErrDocumentInvalidArgument
	}
	if d.SourceName != nil && utf8.RuneCountInString(*d.SourceName) > 512 {
		return ErrDocumentInvalidArgument
	}
	if d.Content == nil || strings.TrimSpace(*d.Content) == "" {
		return ErrDocumentInvalidArgument
	}
	if d.ValidFrom != nil && d.ValidTo != nil && !d.ValidTo.After(*d.ValidFrom) {
		return ErrDocumentInvalidArgument
	}
	switch {
	case update:
		if d.Status != KnowledgeStatusDisabled && d.Status != KnowledgeStatusEnabled {
			return ErrDocumentInvalidArgument
		}
	case d.Status != KnowledgeStatusUnspecified && d.Status != KnowledgeStatusDisabled && d.Status != KnowledgeStatusEnabled:
		return ErrDocumentInvalidArgument
	}
	return nil
}

// effectiveChunkMetadata 计算 chunk 的检索过滤事实：库与条目状态取与，
// 有效期取交集（NULL 视为无界）。
func effectiveChunkMetadata(lib *KnowledgeLibrary, doc *KnowledgeDocument) map[string]any {
	status := 0
	if lib.Status == KnowledgeStatusEnabled && doc.Status == KnowledgeStatusEnabled {
		status = 1
	}
	from := int64(0)
	if lib.ValidFrom != nil {
		from = lib.ValidFrom.Unix()
	}
	if doc.ValidFrom != nil && doc.ValidFrom.Unix() > from {
		from = doc.ValidFrom.Unix()
	}
	to := unixInfinite
	if lib.ValidTo != nil {
		to = lib.ValidTo.Unix()
	}
	if doc.ValidTo != nil && doc.ValidTo.Unix() < to {
		to = doc.ValidTo.Unix()
	}
	return map[string]any{
		"kb_id":           idString(lib.ID),
		"document_id":     idString(doc.ID),
		"doc_status":      status,
		"valid_from_unix": from,
		"valid_to_unix":   to,
	}
}

// hashContent 计算正文摘要（hex sha256）；nil 返回 nil。
func hashContent(content *string) *string {
	if content == nil {
		return nil
	}
	sum := sha256.Sum256([]byte(*content))
	hash := hex.EncodeToString(sum[:])
	return &hash
}

// contentChanged 判断正文摘要是否变化；两边都空视为未变化。
func contentChanged(oldHash, newHash *string) bool {
	if oldHash == nil || newHash == nil {
		return oldHash != nil || newHash != nil
	}
	return *oldHash != *newHash
}

// idString 把 int64 主键转为十进制字符串（chunk metadata 契约）。
func idString(id int64) string {
	return fmt.Sprintf("%d", id)
}

func stringOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
