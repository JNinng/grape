package service

import (
	"context"
	"time"

	knowledgev1 "grape/api/knowledge/v1"
	"grape/internal/biz"

	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// KnowledgeService 是知识库与条目资源的传输层适配器（LibraryService 与
// DocumentService 共用一个实现）：DTO ↔ DO 转换、动态查询协议转换，委托
// 给 biz 用例，不实现业务规则。
type KnowledgeService struct {
	knowledgev1.UnimplementedLibraryServiceServer
	knowledgev1.UnimplementedDocumentServiceServer
	uc *biz.KnowledgeUsecase
}

// NewKnowledgeService 构造知识库服务。
func NewKnowledgeService(uc *biz.KnowledgeUsecase) *KnowledgeService {
	return &KnowledgeService{uc: uc}
}

// CreateLibrary 创建知识库。
func (s *KnowledgeService) CreateLibrary(ctx context.Context, req *knowledgev1.CreateLibraryRequest) (*knowledgev1.Library, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	in := req.GetLibrary()
	if in == nil {
		return nil, biz.ErrLibraryInvalidArgument
	}
	out, err := s.uc.CreateLibrary(ctx, auth, &biz.KnowledgeLibrary{
		Status:            normalizeKnowledgeStatus(in.Status),
		Name:              in.GetName(),
		Remark:            optionalString(in.Remark),
			EmbeddingModelKey: optionalString(in.EmbeddingModelKey),
		ChunkSize:         in.GetChunkSize(),
		ChunkOverlap:      in.GetChunkOverlap(),
		ValidFrom:         pbToTimePtr(in.GetValidFrom()),
		ValidTo:           pbToTimePtr(in.GetValidTo()),
	})
	if err != nil {
		return nil, err
	}
	return toProtoLibrary(out), nil
}

// GetLibrary 按 id 查询知识库。
func (s *KnowledgeService) GetLibrary(ctx context.Context, req *knowledgev1.GetLibraryRequest) (*knowledgev1.Library, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.uc.GetLibrary(ctx, auth, req.GetId())
	if err != nil {
		return nil, err
	}
	return toProtoLibrary(out), nil
}

// ListLibraries 按动态查询协议返回一页知识库。
func (s *KnowledgeService) ListLibraries(ctx context.Context, req *knowledgev1.ListLibrariesRequest) (*knowledgev1.LibrarySet, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	q, err := convertQuery(req.GetQuery(), nil)
	if err != nil {
		return nil, invalidQueryError(biz.ErrLibraryInvalidArgument, err)
	}
	out, err := s.uc.ListLibraries(ctx, auth, q)
	if err != nil {
		return nil, err
	}
	items := make([]*knowledgev1.Library, 0, len(out.Items))
	for _, l := range out.Items {
		items = append(items, toProtoLibrary(l))
	}
	return &knowledgev1.LibrarySet{
		Items:      items,
		Total:      out.Total,
		HasMore:    out.HasMore,
		NextCursor: out.NextCursor,
		Page:       pageOf(req.GetQuery()),
	}, nil
}

// UpdateLibrary 覆盖知识库可变字段；status 必填。
func (s *KnowledgeService) UpdateLibrary(ctx context.Context, req *knowledgev1.UpdateLibraryRequest) (*knowledgev1.Library, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	in := req.GetLibrary()
	if in == nil || in.Status == nil {
		return nil, biz.ErrLibraryInvalidArgument
	}
	out, err := s.uc.UpdateLibrary(ctx, auth, &biz.KnowledgeLibrary{
		ID:                in.GetId(),
		Status:            *in.Status,
		Name:              in.GetName(),
		Remark:            optionalString(in.Remark),
		EmbeddingModelKey: optionalString(in.EmbeddingModelKey),
		ChunkSize:         in.GetChunkSize(),
		ChunkOverlap:      in.GetChunkOverlap(),
		ValidFrom:         pbToTimePtr(in.GetValidFrom()),
		ValidTo:           pbToTimePtr(in.GetValidTo()),
	})
	if err != nil {
		return nil, err
	}
	return toProtoLibrary(out), nil
}

// DeleteLibrary 软删除知识库。
func (s *KnowledgeService) DeleteLibrary(ctx context.Context, req *knowledgev1.DeleteLibraryRequest) (*emptypb.Empty, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.uc.DeleteLibrary(ctx, auth, req.GetId()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// ListModelOptions 返回知识库可用的模型选项。
func (s *KnowledgeService) ListModelOptions(ctx context.Context, req *knowledgev1.ListModelOptionsRequest) (*knowledgev1.ModelOptionSet, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.uc.ListModelOptions(ctx, auth, req.GetKind())
	if err != nil {
		return nil, err
	}
	items := make([]*knowledgev1.ModelOption, 0, len(out))
	for _, m := range out {
		var dimension int32
		if m.Dimension != nil {
			dimension = *m.Dimension
		}
		items = append(items, &knowledgev1.ModelOption{
			ModelKey:  m.ModelKey,
			Name:      m.Name,
			Kind:      m.Kind,
			Dimension: dimension,
			Platform:  m.Platform,
		})
	}
	return &knowledgev1.ModelOptionSet{Items: items}, nil
}

// CreateDocument 创建条目并摄取。
func (s *KnowledgeService) CreateDocument(ctx context.Context, req *knowledgev1.CreateDocumentRequest) (*knowledgev1.Document, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	in := req.GetDocument()
	if in == nil {
		return nil, biz.ErrDocumentInvalidArgument
	}
	out, err := s.uc.CreateDocument(ctx, auth, &biz.KnowledgeDocument{
		LibraryID:  in.GetLibraryId(),
		Kind:       in.GetKind(),
		Title:      in.GetTitle(),
		Content:    optionalString(in.Content),
		SourceName: optionalString(in.SourceName),
		Status:     normalizeKnowledgeStatus(in.Status),
		ValidFrom:  pbToTimePtr(in.GetValidFrom()),
		ValidTo:    pbToTimePtr(in.GetValidTo()),
	})
	if err != nil {
		return nil, err
	}
	return toProtoDocument(out), nil
}

// GetDocument 按 id 查询条目（含正文）。
func (s *KnowledgeService) GetDocument(ctx context.Context, req *knowledgev1.GetDocumentRequest) (*knowledgev1.Document, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.uc.GetDocument(ctx, auth, req.GetId())
	if err != nil {
		return nil, err
	}
	return toProtoDocument(out), nil
}

// ListDocuments 按动态查询协议返回一页条目（不返回正文）。
func (s *KnowledgeService) ListDocuments(ctx context.Context, req *knowledgev1.ListDocumentsRequest) (*knowledgev1.DocumentSet, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	q, err := convertQuery(req.GetQuery(), nil)
	if err != nil {
		return nil, invalidQueryError(biz.ErrDocumentInvalidArgument, err)
	}
	out, err := s.uc.ListDocuments(ctx, auth, q)
	if err != nil {
		return nil, err
	}
	items := make([]*knowledgev1.Document, 0, len(out.Items))
	for _, d := range out.Items {
		items = append(items, toProtoDocumentLite(d))
	}
	return &knowledgev1.DocumentSet{
		Items:      items,
		Total:      out.Total,
		HasMore:    out.HasMore,
		NextCursor: out.NextCursor,
		Page:       pageOf(req.GetQuery()),
	}, nil
}

// ListLibraryDocuments 固定分页列出库内条目（不返回正文）。
func (s *KnowledgeService) ListLibraryDocuments(ctx context.Context, req *knowledgev1.ListLibraryDocumentsRequest) (*knowledgev1.DocumentSet, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	page, pageSize := normalizePage(req.GetPage(), req.GetPageSize())
	offset := int32((page - 1) * pageSize)
	out, err := s.uc.ListDocumentsByLibrary(ctx, auth, req.GetId(), pageSize, offset)
	if err != nil {
		return nil, err
	}
	items := make([]*knowledgev1.Document, 0, len(out.Items))
	for _, d := range out.Items {
		items = append(items, toProtoDocumentLite(d))
	}
	hasMore := int64(offset+int32(len(items))) < out.Total
	return &knowledgev1.DocumentSet{
		Items:   items,
		Total:   &out.Total,
		HasMore: hasMore,
		Page:    page,
	}, nil
}

// UpdateDocument 覆盖条目可变字段；正文变更时重新摄取。
func (s *KnowledgeService) UpdateDocument(ctx context.Context, req *knowledgev1.UpdateDocumentRequest) (*knowledgev1.Document, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	in := req.GetDocument()
	if in == nil || in.Status == nil {
		return nil, biz.ErrDocumentInvalidArgument
	}
	out, err := s.uc.UpdateDocument(ctx, auth, &biz.KnowledgeDocument{
		ID:         in.GetId(),
		LibraryID:  in.GetLibraryId(),
		Kind:       in.GetKind(),
		Title:      in.GetTitle(),
		Content:    optionalString(in.Content),
		SourceName: optionalString(in.SourceName),
		Status:     *in.Status,
		ValidFrom:  pbToTimePtr(in.GetValidFrom()),
		ValidTo:    pbToTimePtr(in.GetValidTo()),
	})
	if err != nil {
		return nil, err
	}
	return toProtoDocument(out), nil
}

// DeleteDocument 软删除条目并清理 chunk。
func (s *KnowledgeService) DeleteDocument(ctx context.Context, req *knowledgev1.DeleteDocumentRequest) (*emptypb.Empty, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.uc.DeleteDocument(ctx, auth, req.GetId()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// ReindexDocument 重建条目向量。
func (s *KnowledgeService) ReindexDocument(ctx context.Context, req *knowledgev1.ReindexDocumentRequest) (*knowledgev1.Document, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.uc.ReindexDocument(ctx, auth, req.GetId())
	if err != nil {
		return nil, err
	}
	return toProtoDocument(out), nil
}

func toProtoLibrary(l *biz.KnowledgeLibrary) *knowledgev1.Library {
	return &knowledgev1.Library{
		Id:                l.ID,
		Status:            &l.Status,
		Name:              l.Name,
		Remark:            l.Remark,
		EmbeddingModelKey: l.EmbeddingModelKey,
		ChunkSize:         l.ChunkSize,
		ChunkOverlap:      l.ChunkOverlap,
		ValidFrom:         tsPBPtr(l.ValidFrom),
		ValidTo:           tsPBPtr(l.ValidTo),
		CreatedBy:         l.CreatedBy,
		UpdatedBy:         l.UpdatedBy,
		CreatedAt:         timestamppb.New(l.CreatedAt),
		UpdatedAt:         tsPBPtr(l.UpdatedAt),
		DeletedAt:         tsPBPtr(l.DeletedAt),
	}
}

func toProtoDocument(d *biz.KnowledgeDocument) *knowledgev1.Document {
	out := toProtoDocumentLite(d)
	out.Content = d.Content
	return out
}

func toProtoDocumentLite(d *biz.KnowledgeDocument) *knowledgev1.Document {
	return &knowledgev1.Document{
		Id:              d.ID,
		LibraryId:       d.LibraryID,
		Kind:            d.Kind,
		Title:           d.Title,
		SourceName:      d.SourceName,
		Status:          &d.Status,
		ValidFrom:       tsPBPtr(d.ValidFrom),
		ValidTo:         tsPBPtr(d.ValidTo),
		IndexedAt:       tsPBPtr(d.IndexedAt),
		IndexedEmbedder: d.IndexedEmbedder,
		CreatedBy:       d.CreatedBy,
		UpdatedBy:       d.UpdatedBy,
		CreatedAt:       timestamppb.New(d.CreatedAt),
		UpdatedAt:       tsPBPtr(d.UpdatedAt),
		DeletedAt:       tsPBPtr(d.DeletedAt),
	}
}

// normalizeKnowledgeStatus 归一化创建入参状态：optional 缺失映射为未指定
// 哨兵（用例落缺省启用）；显式 0=禁用原样透传。
func normalizeKnowledgeStatus(status *int32) int32 {
	if status == nil {
		return biz.KnowledgeStatusUnspecified
	}
	return *status
}

// pbToTimePtr 转换可空 protobuf 时间戳。
func pbToTimePtr(ts *timestamppb.Timestamp) *time.Time {
	if ts == nil {
		return nil
	}
	t := ts.AsTime()
	return &t
}

// normalizePage 归一化固定分页参数：页码缺省 1，页大小缺省 20、上限 200。
func normalizePage(page, pageSize int32) (int32, int32) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 200 {
		pageSize = 200
	}
	return page, pageSize
}
