package pg_sqlc

import (
	"context"
	"fmt"

	"grape/internal/biz"
	"grape/internal/data/pg_sqlc/gen"

	"github.com/jackc/pgx/v5"
)

// knowledgeFileRepo 是 biz.KnowledgeFileRepo 的 PostgreSQL 实现。kb_file 是
// 租户表（RLS ENABLE + FORCE）：跨租户的介质行受 RLS 隔离，无行即拒绝，
// kb_document.file_id 的本租户校验由 GetFile 的单行读承担。读写面只有
// queries/kb_file.sql 的固定查询，不参与动态查询协议。
type knowledgeFileRepo struct {
	data *Data
}

// NewKnowledgeFileRepo 构造文件介质仓储。
func NewKnowledgeFileRepo(data *Data) biz.KnowledgeFileRepo {
	return &knowledgeFileRepo{data: data}
}

func toBizFile(po *gen.KbFile) *biz.KnowledgeFile {
	if po == nil {
		return nil
	}
	return &biz.KnowledgeFile{
		ID:        po.ID,
		ObjectKey: po.ObjectKey,
		Etag:      po.Etag,
		Sha256:    po.Sha256,
		MimeType:  po.MimeType,
		FileSize:  po.FileSize,
		CreatedBy: po.CreatedBy,
		UpdatedBy: po.UpdatedBy,
	}
}

// GetFile 按 id 读取未软删介质行：kb_document.file_id 引用的本租户行校验
// 点，跨租户行受 RLS 隔离，无行即拒绝。
func (r *knowledgeFileRepo) GetFile(ctx context.Context, auth biz.Identity, id int64) (*biz.KnowledgeFile, error) {
	var out *biz.KnowledgeFile
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().GetKbFileByID(ctx, id)
		if err != nil {
			return err
		}
		out = toBizFile(&row)
		return nil
	})
	if err != nil {
		return nil, mapStorageError(err, biz.ErrDocumentNotFound, biz.ErrDocumentInvalidArgument, biz.ErrDocumentInvalidArgument)
	}
	return out, nil
}

// CreateFile 插入介质行：主键与对象键（kb-file/<发号 ID>，URL 安全 ASCII）
// 由应用层发号——上传链路先建行后写字节，键在写入 OSS 前即确定；租户取
// 会话 app.tenant_id。
func (r *knowledgeFileRepo) CreateFile(ctx context.Context, auth biz.Identity, f *biz.KnowledgeFile) (*biz.KnowledgeFile, error) {
	var out *biz.KnowledgeFile
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		id := newID()
		row, err := s.Q().InsertKbFile(ctx, gen.InsertKbFileParams{
			ID:        id,
			ObjectKey: fmt.Sprintf("kb-file/%d", id),
			Etag:      f.Etag,
			Sha256:    f.Sha256,
			MimeType:  f.MimeType,
			FileSize:  f.FileSize,
			CreatedBy: auth.UserID,
			UpdatedBy: auth.UserID,
		})
		if err != nil {
			return err
		}
		out = toBizFile(&row)
		return nil
	})
	if err != nil {
		return nil, mapStorageError(err, biz.ErrDocumentNotFound, biz.ErrDocumentInvalidArgument, biz.ErrDocumentInvalidArgument)
	}
	return out, nil
}

// SoftDeleteFile 软删介质行：先锁目标行（宿主条目删除路径先锁后写），
// 未命中（已删或跨租户不可见）按 NotFound 拒绝。
func (r *knowledgeFileRepo) SoftDeleteFile(ctx context.Context, auth biz.Identity, id int64) error {
	return mapStorageError(r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		if _, err := s.Q().LockKbFileByID(ctx, id); err != nil {
			return err
		}
		rows, err := s.Q().SoftDeleteKbFile(ctx, gen.SoftDeleteKbFileParams{UpdatedBy: auth.UserID, ID: id})
		if err != nil {
			return err
		}
		if rows == 0 {
			return pgx.ErrNoRows
		}
		return nil
	}), biz.ErrDocumentNotFound, biz.ErrDocumentInvalidArgument, biz.ErrDocumentInvalidArgument)
}
