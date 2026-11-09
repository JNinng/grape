package pg_sqlc

import (
	"context"

	"grape/internal/biz"
	"grape/internal/data/pg_sqlc/gen"

	"github.com/jackc/pgx/v5"
	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

// knowledgeRepo 是 biz.KnowledgeRepo 的 PostgreSQL 实现。知识库与条目都是
// 租户表（RLS ENABLE + FORCE，表无 @scope），租户边界由会话变量承担；
// 动态 List 消费 dynq 协议，固定分页用于库内条目子列表。
type knowledgeRepo struct {
	data *Data
}

// NewKnowledgeRepo 构造知识库仓储。
func NewKnowledgeRepo(data *Data) biz.KnowledgeRepo {
	return &knowledgeRepo{data: data}
}

func toBizLibrary(po *gen.KbLibrary) *biz.KnowledgeLibrary {
	if po == nil {
		return nil
	}
	delimiter := po.ChunkDelimiter
	return &biz.KnowledgeLibrary{
		ID:                po.ID,
		Status:            int32(po.Status),
		Name:              po.Name,
		Remark:            po.Remark,
		EmbeddingModelKey: po.EmbeddingModelKey,
		ChunkSize:         po.ChunkSize,
		ChunkOverlap:      po.ChunkOverlap,
		ChunkDelimiter:    &delimiter,
		ValidFrom:         tsToTimePtr(po.ValidFrom),
		ValidTo:           tsToTimePtr(po.ValidTo),
		CreatedBy:         po.CreatedBy,
		UpdatedBy:         po.UpdatedBy,
		CreatedAt:         tsToTime(po.CreatedAt),
		UpdatedAt:         tsToTimePtr(po.UpdatedAt),
		DeletedAt:         tsToTimePtr(po.DeletedAt),
	}
}

func toBizDocument(po *gen.KbDocument) *biz.KnowledgeDocument {
	if po == nil {
		return nil
	}
	return &biz.KnowledgeDocument{
		ID:              po.ID,
		LibraryID:       po.LibraryID,
		Kind:            po.Kind,
		Title:           po.Title,
		Content:         po.Content,
		SourceName:      po.SourceName,
		ContentHash:     po.ContentHash,
		Status:          int32(po.Status),
		ValidFrom:       tsToTimePtr(po.ValidFrom),
		ValidTo:         tsToTimePtr(po.ValidTo),
		IndexedAt:       tsToTimePtr(po.IndexedAt),
		IndexedEmbedder: po.IndexedEmbedder,
		CreatedBy:       po.CreatedBy,
		UpdatedBy:       po.UpdatedBy,
		CreatedAt:       tsToTime(po.CreatedAt),
		UpdatedAt:       tsToTimePtr(po.UpdatedAt),
		DeletedAt:       tsToTimePtr(po.DeletedAt),
	}
}

// GetLibrary 按 id 读取未软删知识库。
func (r *knowledgeRepo) GetLibrary(ctx context.Context, auth biz.Identity, id int64) (*biz.KnowledgeLibrary, error) {
	var out *biz.KnowledgeLibrary
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().GetKbLibraryByID(ctx, id)
		if err != nil {
			return err
		}
		out = toBizLibrary(&row)
		return nil
	})
	if err != nil {
		return nil, mapStorageError(err, biz.ErrLibraryNotFound, biz.ErrLibraryInvalidArgument, biz.ErrLibraryAlreadyExists)
	}
	return out, nil
}

// ListLibraries 解析动态查询协议并返回分页信封。
func (r *knowledgeRepo) ListLibraries(ctx context.Context, auth biz.Identity, q *dynq.Query) (*biz.KnowledgeLibraryList, error) {
	var out *biz.KnowledgeLibraryList
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		params, err := gen.ParseKbLibraryQuery(q)
		if err != nil {
			return err
		}
		res, err := gen.ListKbLibrary(ctx, s.Tx(), params)
		if err != nil {
			return err
		}
		list := &biz.KnowledgeLibraryList{
			Items:      make([]*biz.KnowledgeLibrary, 0, len(res.Items)),
			HasMore:    res.HasMore,
			NextCursor: res.NextCursor,
		}
		for i := range res.Items {
			list.Items = append(list.Items, toBizLibrary(&res.Items[i]))
		}
		if dynq.IncludeTotalOf(q.Pagination) {
			countParams := params
			countParams.After = nil
			total, err := gen.CountKbLibrary(ctx, s.Tx(), countParams)
			if err != nil {
				return err
			}
			list.Total = &total
		}
		out = list
		return nil
	})
	if err != nil {
		return nil, mapStorageError(err, biz.ErrLibraryNotFound, biz.ErrLibraryInvalidArgument, biz.ErrLibraryAlreadyExists)
	}
	return out, nil
}

// CreateLibrary 创建知识库；租户取会话，名称冲突映射 AlreadyExists。
func (r *knowledgeRepo) CreateLibrary(ctx context.Context, auth biz.Identity, l *biz.KnowledgeLibrary) (*biz.KnowledgeLibrary, error) {
	var out *biz.KnowledgeLibrary
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().InsertKbLibrary(ctx, gen.InsertKbLibraryParams{
			ID:                newID(),
			Status:            int16(l.Status),
			Name:              l.Name,
			Remark:            l.Remark,
			EmbeddingModelKey: l.EmbeddingModelKey,
			ChunkSize:         l.ChunkSize,
			ChunkOverlap:      l.ChunkOverlap,
			ChunkDelimiter:    stringOrEmpty(l.ChunkDelimiter),
			ValidFrom:         timeToTS(l.ValidFrom),
			ValidTo:           timeToTS(l.ValidTo),
			CreatedBy:         auth.UserID,
			UpdatedBy:         auth.UserID,
		})
		if err != nil {
			return err
		}
		out = toBizLibrary(&row)
		return nil
	})
	if err != nil {
		return nil, mapStorageError(err, biz.ErrLibraryNotFound, biz.ErrLibraryInvalidArgument, biz.ErrLibraryAlreadyExists)
	}
	return out, nil
}

// UpdateLibrary 覆盖可变字段：先锁目标行（未命中 NotFound）再更新。
func (r *knowledgeRepo) UpdateLibrary(ctx context.Context, auth biz.Identity, l *biz.KnowledgeLibrary) (*biz.KnowledgeLibrary, error) {
	var out *biz.KnowledgeLibrary
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		if _, err := s.Q().LockKbLibraryByID(ctx, l.ID); err != nil {
			return err
		}
		row, err := s.Q().UpdateKbLibrary(ctx, gen.UpdateKbLibraryParams{
			Name:              l.Name,
			Status:            int16(l.Status),
			Remark:            l.Remark,
			EmbeddingModelKey: l.EmbeddingModelKey,
			ChunkSize:         l.ChunkSize,
			ChunkOverlap:      l.ChunkOverlap,
			ChunkDelimiter:    stringOrEmpty(l.ChunkDelimiter),
			ValidFrom:         timeToTS(l.ValidFrom),
			ValidTo:           timeToTS(l.ValidTo),
			UpdatedBy:         auth.UserID,
			ID:                l.ID,
		})
		if err != nil {
			return err
		}
		out = toBizLibrary(&row)
		return nil
	})
	if err != nil {
		return nil, mapStorageError(err, biz.ErrLibraryNotFound, biz.ErrLibraryInvalidArgument, biz.ErrLibraryAlreadyExists)
	}
	return out, nil
}

// SoftDeleteLibrary 软删除知识库；条目校验由用例在调用前完成。
func (r *knowledgeRepo) SoftDeleteLibrary(ctx context.Context, auth biz.Identity, id int64) error {
	return mapStorageError(r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		if _, err := s.Q().LockKbLibraryByID(ctx, id); err != nil {
			return err
		}
		rows, err := s.Q().SoftDeleteKbLibrary(ctx, gen.SoftDeleteKbLibraryParams{UpdatedBy: auth.UserID, ID: id})
		if err != nil {
			return err
		}
		if rows == 0 {
			return pgx.ErrNoRows
		}
		return nil
	}), biz.ErrLibraryNotFound, biz.ErrLibraryInvalidArgument, biz.ErrLibraryAlreadyExists)
}

// CountDocumentsByLibrary 统计库下未删除条目数。
func (r *knowledgeRepo) CountDocumentsByLibrary(ctx context.Context, auth biz.Identity, libraryID int64) (int64, error) {
	var total int64
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		var err error
		total, err = s.Q().CountKbDocumentsByLibrary(ctx, libraryID)
		return err
	})
	if err != nil {
		return 0, mapStorageError(err, biz.ErrLibraryNotFound, biz.ErrLibraryInvalidArgument, biz.ErrLibraryAlreadyExists)
	}
	return total, nil
}

// GetDocument 按 id 读取未软删条目。
func (r *knowledgeRepo) GetDocument(ctx context.Context, auth biz.Identity, id int64) (*biz.KnowledgeDocument, error) {
	var out *biz.KnowledgeDocument
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().GetKbDocumentByID(ctx, id)
		if err != nil {
			return err
		}
		out = toBizDocument(&row)
		return nil
	})
	if err != nil {
		return nil, mapStorageError(err, biz.ErrDocumentNotFound, biz.ErrDocumentInvalidArgument, biz.ErrDocumentInvalidArgument)
	}
	return out, nil
}

// ListDocuments 解析动态查询协议并返回分页信封。
func (r *knowledgeRepo) ListDocuments(ctx context.Context, auth biz.Identity, q *dynq.Query) (*biz.KnowledgeDocumentList, error) {
	var out *biz.KnowledgeDocumentList
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		params, err := gen.ParseKbDocumentQuery(q)
		if err != nil {
			return err
		}
		res, err := gen.ListKbDocument(ctx, s.Tx(), params)
		if err != nil {
			return err
		}
		list := &biz.KnowledgeDocumentList{
			Items:      make([]*biz.KnowledgeDocument, 0, len(res.Items)),
			HasMore:    res.HasMore,
			NextCursor: res.NextCursor,
		}
		for i := range res.Items {
			list.Items = append(list.Items, toBizDocument(&res.Items[i]))
		}
		if dynq.IncludeTotalOf(q.Pagination) {
			countParams := params
			countParams.After = nil
			total, err := gen.CountKbDocument(ctx, s.Tx(), countParams)
			if err != nil {
				return err
			}
			list.Total = &total
		}
		out = list
		return nil
	})
	if err != nil {
		return nil, mapStorageError(err, biz.ErrDocumentNotFound, biz.ErrDocumentInvalidArgument, biz.ErrDocumentInvalidArgument)
	}
	return out, nil
}

// ListDocumentsByLibrary 固定 offset 分页返回库内条目与总数。
func (r *knowledgeRepo) ListDocumentsByLibrary(ctx context.Context, auth biz.Identity, libraryID int64, limit, offset int32) (*biz.KnowledgeDocumentPage, error) {
	var out *biz.KnowledgeDocumentPage
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		total, err := s.Q().CountKbDocumentsByLibraryList(ctx, libraryID)
		if err != nil {
			return err
		}
		rows, err := s.Q().ListKbDocumentsByLibrary(ctx, gen.ListKbDocumentsByLibraryParams{
			LibraryID: libraryID,
			Limit:     limit,
			Offset:    offset,
		})
		if err != nil {
			return err
		}
		page := &biz.KnowledgeDocumentPage{Items: make([]*biz.KnowledgeDocument, 0, len(rows)), Total: total}
		for i := range rows {
			page.Items = append(page.Items, toBizDocument(&rows[i]))
		}
		out = page
		return nil
	})
	if err != nil {
		return nil, mapStorageError(err, biz.ErrDocumentNotFound, biz.ErrDocumentInvalidArgument, biz.ErrDocumentInvalidArgument)
	}
	return out, nil
}

// CreateDocument 创建条目；租户取会话。
func (r *knowledgeRepo) CreateDocument(ctx context.Context, auth biz.Identity, d *biz.KnowledgeDocument) (*biz.KnowledgeDocument, error) {
	var out *biz.KnowledgeDocument
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().InsertKbDocument(ctx, gen.InsertKbDocumentParams{
			ID:          newID(),
			LibraryID:   d.LibraryID,
			Kind:        d.Kind,
			Title:       d.Title,
			Content:     d.Content,
			SourceName:  d.SourceName,
			ContentHash: d.ContentHash,
			Status:      int16(d.Status),
			ValidFrom:   timeToTS(d.ValidFrom),
			ValidTo:     timeToTS(d.ValidTo),
			CreatedBy:   auth.UserID,
			UpdatedBy:   auth.UserID,
		})
		if err != nil {
			return err
		}
		out = toBizDocument(&row)
		return nil
	})
	if err != nil {
		return nil, mapStorageError(err, biz.ErrDocumentNotFound, biz.ErrDocumentInvalidArgument, biz.ErrDocumentInvalidArgument)
	}
	return out, nil
}

// UpdateDocument 覆盖可变字段：先锁目标行，沿用其索引事实（indexed_at/
// indexed_embedder 不经本路径变更，由 MarkDocumentIndexed 单独回写）。
func (r *knowledgeRepo) UpdateDocument(ctx context.Context, auth biz.Identity, d *biz.KnowledgeDocument) (*biz.KnowledgeDocument, error) {
	var out *biz.KnowledgeDocument
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		current, err := s.Q().LockKbDocumentByID(ctx, d.ID)
		if err != nil {
			return err
		}
		row, err := s.Q().UpdateKbDocument(ctx, gen.UpdateKbDocumentParams{
			Title:           d.Title,
			Content:         d.Content,
			SourceName:      d.SourceName,
			ContentHash:     d.ContentHash,
			Status:          int16(d.Status),
			ValidFrom:       timeToTS(d.ValidFrom),
			ValidTo:         timeToTS(d.ValidTo),
			IndexedAt:       current.IndexedAt,
			IndexedEmbedder: current.IndexedEmbedder,
			UpdatedBy:       auth.UserID,
			ID:              d.ID,
		})
		if err != nil {
			return err
		}
		out = toBizDocument(&row)
		return nil
	})
	if err != nil {
		return nil, mapStorageError(err, biz.ErrDocumentNotFound, biz.ErrDocumentInvalidArgument, biz.ErrDocumentInvalidArgument)
	}
	return out, nil
}

// MarkDocumentIndexed 回写最近一次成功摄取的时间与空间键。
func (r *knowledgeRepo) MarkDocumentIndexed(ctx context.Context, auth biz.Identity, id int64, embedder string) (*biz.KnowledgeDocument, error) {
	var out *biz.KnowledgeDocument
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().MarkKbDocumentIndexed(ctx, gen.MarkKbDocumentIndexedParams{
			IndexedEmbedder: &embedder,
			UpdatedBy:       auth.UserID,
			ID:              id,
		})
		if err != nil {
			return err
		}
		out = toBizDocument(&row)
		return nil
	})
	if err != nil {
		return nil, mapStorageError(err, biz.ErrDocumentNotFound, biz.ErrDocumentInvalidArgument, biz.ErrDocumentInvalidArgument)
	}
	return out, nil
}

// SoftDeleteDocument 软删除条目；chunk 清理由用例在调用前完成。
func (r *knowledgeRepo) SoftDeleteDocument(ctx context.Context, auth biz.Identity, id int64) error {
	return mapStorageError(r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		if _, err := s.Q().LockKbDocumentByID(ctx, id); err != nil {
			return err
		}
		rows, err := s.Q().SoftDeleteKbDocument(ctx, gen.SoftDeleteKbDocumentParams{UpdatedBy: auth.UserID, ID: id})
		if err != nil {
			return err
		}
		if rows == 0 {
			return pgx.ErrNoRows
		}
		return nil
	}), biz.ErrDocumentNotFound, biz.ErrDocumentInvalidArgument, biz.ErrDocumentInvalidArgument)
}
