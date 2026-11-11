// Package oss 是内嵌对象存储设施（gofakes3，数据落 PostgreSQL
// oss_object 表）：gofakes3.Backend 的 PG 实现与 /s3/ 挂载面。业务侧只认
// aws-sdk-go-v2 S3 客户端（端点事实在 sys_config 的 oss.* 配置键，经
// biz.OSSConfigResolver 解析），后续切 MinIO / 阿里云 OSS / AWS 只改配置。
//
// 鉴权与挂载口径：SigV4 签名自证身份（AK/SK 取自 sys_config），不走
// JWT 中间件链；本包在 /s3/ 前缀外层校验签名（签名覆盖含前缀的原始
// 路径），校验通过后剥前缀交给内层 gofakes3 路由（path-style，host 不
// 参与 bucket 路由）。oss_object 是设施面（无 RLS，bucket 即命名空间），
// Backend 以池直连读写，不经事务门面。
//
// 本期只开简单上传：PutObject 按 Content-Length 限流 ≤ 20MB（超限
// EntityTooLarge 拒绝）；不实现 MultipartBackend 时 multipart 由
// gofakes3 内存 uploader 组装、complete 时整体走 PutObject，同样受限流
// 约束。gofakes3 不支持版本控制、细粒度 ACL、lifecycle，业务不依赖。
package oss

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"grape/internal/data/pg_sqlc/gen"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rclone/gofakes3"
)

// maxObjectSize 是单对象字节上限：PutObject 按 Content-Length（未知时
// 读到上限 +1 判定）限流，超限 EntityTooLarge 拒绝。与文件解析摄取的
// 完整性限流同值，防止 bytea 大对象撑爆 TOAST 与内存。
const maxObjectSize = 20 << 20

// contentTypeMetadataKey 是 gofakes3 元数据映射里的 Content-Type 键
// （gofakes3 把 Content-Type 与 X-Amz-Meta-* 一并放进 meta）。
const contentTypeMetadataKey = "Content-Type"

// Backend 是 gofakes3.Backend 的 PostgreSQL 实现。查询走 sqlc 固定命名
// 查询（queries/oss_object.sql）；构造一次、并发安全（pgxpool）。
type Backend struct {
	q *gen.Queries
}

var _ gofakes3.Backend = (*Backend)(nil)

// NewBackend 以池直连构造 Backend：oss_object 无 RLS、S3 请求经 SigV4
// 自证身份，不携带租户/身份语义。
func NewBackend(pool *pgxpool.Pool) *Backend {
	return &Backend{q: gen.New(pool)}
}

// ListBuckets 列举桶：桶不单独登记，按对象行聚合；空桶（零对象）不
// 出现在清单里，即「桶存在性以桶内对象行存在为准」。
func (b *Backend) ListBuckets(ctx context.Context) ([]gofakes3.BucketInfo, error) {
	rows, err := b.q.ListOssBucketHeads(ctx)
	if err != nil {
		return nil, err
	}
	buckets := make([]gofakes3.BucketInfo, 0, len(rows))
	for _, row := range rows {
		buckets = append(buckets, gofakes3.BucketInfo{
			Name:         row.Bucket,
			CreationDate: gofakes3.NewContentTime(row.CreatedAt.Time),
		})
	}
	return buckets, nil
}

// ListBucket 列举桶内条目：SQL 按 LIKE 前缀收窄 + marker 字典序起点取
// 行，delimiter 的公共前缀归并走 gofakes3.Prefix.Match（与 s3mem 同一
// 算法）。空桶按 ErrNoSuchBucket 返回（与 s3mem 一致；空桶等价不存在的
// 口径见包注释）。
func (b *Backend) ListBucket(ctx context.Context, name string, prefix *gofakes3.Prefix, page gofakes3.ListBucketPage) (*gofakes3.ObjectList, error) {
	exists, err := b.BucketExists(ctx, name)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, gofakes3.BucketNotFound(name)
	}
	if prefix == nil {
		prefix = &gofakes3.Prefix{}
	}
	var marker *string
	if page.HasMarker {
		m := page.Marker
		marker = &m
	}
	rows, err := b.q.ListOssObjectEntries(ctx, gen.ListOssObjectEntriesParams{
		Bucket:  name,
		Pattern: likePrefixPattern(prefix.Prefix),
		Marker:  marker,
	})
	if err != nil {
		return nil, err
	}
	response := gofakes3.NewObjectList()
	var match gofakes3.PrefixMatch
	var count int64
	var lastMatchedPart string
	for i, row := range rows {
		if !prefix.Match(row.ObjectKey, &match) {
			continue
		} else if match.CommonPrefix {
			// 同一公共前缀的后续命中行不计入 MaxKeys（AddPrefix 的输出
			// 去重管不到计数，须与 s3mem 的 lastMatchedPart 同算法，否则
			// 分页边界落在公共前缀组内部时会提前截断并逐页空转）。
			if match.MatchedPart == lastMatchedPart {
				continue
			}
			response.AddPrefix(match.MatchedPart)
			lastMatchedPart = match.MatchedPart
		} else {
			response.Add(&gofakes3.Content{
				Key:          row.ObjectKey,
				LastModified: gofakes3.NewContentTime(row.UpdatedAt.Time),
				ETag:         quotedETag(row.Etag),
				Size:         row.Size,
			})
		}
		count++
		if page.MaxKeys > 0 && count >= page.MaxKeys {
			response.NextMarker = row.ObjectKey
			response.IsTruncated = i < len(rows)-1
			break
		}
	}
	return response, nil
}

// CreateBucket 建桶：桶不单独登记，桶内已有对象（桶已在）按 S3 语义
// 返回 BucketAlreadyExists；空桶为幂等成功（WithAutoBucket 的首写路径
// 依赖本方法成功返回）。
func (b *Backend) CreateBucket(ctx context.Context, name string) error {
	count, err := b.q.CountOssBucketObjects(ctx, name)
	if err != nil {
		return err
	}
	if count > 0 {
		return gofakes3.ResourceError(gofakes3.ErrBucketAlreadyExists, name)
	}
	return nil
}

// BucketExists 报告桶是否存在：以桶内对象行存在为准。
func (b *Backend) BucketExists(ctx context.Context, name string) (bool, error) {
	count, err := b.q.CountOssBucketObjects(ctx, name)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// DeleteBucket 删桶：桶内仍有对象按 ErrBucketNotEmpty 拒绝；空桶（按
// 本实现口径即不存在）为幂等成功、无可删行。
func (b *Backend) DeleteBucket(ctx context.Context, name string) error {
	count, err := b.q.CountOssBucketObjects(ctx, name)
	if err != nil {
		return err
	}
	if count > 0 {
		return gofakes3.ResourceError(gofakes3.ErrBucketNotEmpty, name)
	}
	return nil
}

// GetObject 读取对象：rangeRequest 非空时按 Range 换算字节区间、经
// substr 只取切片（整对象不出库），回填 Object.Range（Object.Size 恒为
// 全长，服务器据此发 206）；不存在返回 ErrNoSuchKey。
func (b *Backend) GetObject(ctx context.Context, bucketName, objectName string, rangeRequest *gofakes3.ObjectRangeRequest) (*gofakes3.Object, error) {
	if rangeRequest == nil {
		row, err := b.q.GetOssObject(ctx, gen.GetOssObjectParams{Bucket: bucketName, ObjectKey: objectName})
		if err != nil {
			return nil, ossQueryError(err, objectName)
		}
		hash, err := etagHash(row.Etag)
		if err != nil {
			return nil, err
		}
		return &gofakes3.Object{
			Name:     objectName,
			Size:     row.Size,
			Metadata: objectMetadata(row.Metadata, row.ContentType),
			Contents: io.NopCloser(bytes.NewReader(row.Data)),
			Hash:     hash,
		}, nil
	}

	meta, err := b.q.GetOssObjectMeta(ctx, gen.GetOssObjectMetaParams{Bucket: bucketName, ObjectKey: objectName})
	if err != nil {
		return nil, ossQueryError(err, objectName)
	}
	hash, err := etagHash(meta.Etag)
	if err != nil {
		return nil, err
	}
	rnge, err := rangeRequest.Range(meta.Size)
	if err != nil {
		return nil, err
	}
	obj := &gofakes3.Object{
		Name:     objectName,
		Size:     meta.Size,
		Metadata: objectMetadata(meta.Metadata, meta.ContentType),
		Contents: io.NopCloser(bytes.NewReader(nil)),
		Hash:     hash,
	}
	if rnge != nil {
		slice, err := b.q.GetOssObjectRange(ctx, gen.GetOssObjectRangeParams{
			Bucket:      bucketName,
			ObjectKey:   objectName,
			StartOffset: int32(rnge.Start + 1), // substr 按 1 基字节偏移
			Length:      int32(rnge.Length),
		})
		if err != nil {
			return nil, ossQueryError(err, objectName)
		}
		obj.Contents = io.NopCloser(bytes.NewReader(slice.DataSlice))
		obj.Range = rnge
	}
	return obj, nil
}

// HeadObject 读取对象元数据（不出字节）：Contents 为空读端（gofakes3
// 契约要求非 nil 的 no-op ReadCloser）。
func (b *Backend) HeadObject(ctx context.Context, bucketName, objectName string) (*gofakes3.Object, error) {
	row, err := b.q.GetOssObjectMeta(ctx, gen.GetOssObjectMetaParams{Bucket: bucketName, ObjectKey: objectName})
	if err != nil {
		return nil, ossQueryError(err, objectName)
	}
	hash, err := etagHash(row.Etag)
	if err != nil {
		return nil, err
	}
	return &gofakes3.Object{
		Name:     objectName,
		Size:     row.Size,
		Metadata: objectMetadata(row.Metadata, row.ContentType),
		Contents: io.NopCloser(bytes.NewReader(nil)),
		Hash:     hash,
	}, nil
}

// DeleteObject 删除单个对象：S3 语义幂等（不存在的键不算错误，0 行
// 即成功）。
func (b *Backend) DeleteObject(ctx context.Context, bucketName, objectName string) (gofakes3.ObjectDeleteResult, error) {
	if _, err := b.q.DeleteOssObject(ctx, gen.DeleteOssObjectParams{Bucket: bucketName, ObjectKey: objectName}); err != nil {
		return gofakes3.ObjectDeleteResult{}, err
	}
	return gofakes3.ObjectDeleteResult{}, nil
}

// DeleteMulti 批量删除：S3 语义下不存在的键同样按已删除报告（幂等）。
func (b *Backend) DeleteMulti(ctx context.Context, bucketName string, objects ...string) (gofakes3.MultiDeleteResult, error) {
	result := gofakes3.MultiDeleteResult{Deleted: make([]gofakes3.ObjectID, 0, len(objects))}
	if len(objects) == 0 {
		return result, nil
	}
	if _, err := b.q.DeleteOssObjects(ctx, gen.DeleteOssObjectsParams{Bucket: bucketName, ObjectKeys: objects}); err != nil {
		return gofakes3.MultiDeleteResult{}, err
	}
	for _, key := range objects {
		result.Deleted = append(result.Deleted, gofakes3.ObjectID{Key: key})
	}
	return result, nil
}

// PutObject 覆盖写对象：先按 Content-Length 限流（未知大小读到上限 +1
// 判定），etag 为内容 MD5 十六进制（S3 ETag 语义，存储不带引号）；
// meta 是服务器从请求头解析的完整元数据（Content-Type / Last-Modified /
// X-Amz-Meta-* 等），原样落 jsonb，content_type 单列冗余。同一键重复
// 写为幂等覆盖，created_at 保持首次写入。
func (b *Backend) PutObject(ctx context.Context, bucketName, key string, meta map[string]string, input io.Reader, size int64) (gofakes3.PutObjectResult, error) {
	data, err := readLimited(input, size)
	if err != nil {
		return gofakes3.PutObjectResult{}, err
	}
	sum := md5.Sum(data)
	etag := hex.EncodeToString(sum[:])
	row, err := b.q.UpsertOssObject(ctx, gen.UpsertOssObjectParams{
		Bucket:      bucketName,
		ObjectKey:   key,
		Data:        data,
		Size:        int64(len(data)),
		ContentType: metaContentType(meta),
		Etag:        etag,
		Metadata:    metadataJSON(meta),
	})
	if err != nil {
		return gofakes3.PutObjectResult{}, err
	}
	return gofakes3.PutObjectResult{ETag: quotedETag(row.Etag)}, nil
}

// CopyObject 复制对象：单语句完成读源 + 写目标（etag 与 data 随源复制，
// content_type/metadata 取调用方给定的完整元数据——COPY 指令为源元数据、
// REPLACE 为请求元数据，由服务器解析）；源不存在返回 ErrNoSuchKey。
func (b *Backend) CopyObject(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string, meta map[string]string) (gofakes3.CopyObjectResult, error) {
	row, err := b.q.CopyOssObject(ctx, gen.CopyOssObjectParams{
		DstBucket:   dstBucket,
		DstKey:      dstKey,
		ContentType: metaContentType(meta),
		Metadata:    metadataJSON(meta),
		SrcBucket:   srcBucket,
		SrcKey:      srcKey,
	})
	if err != nil {
		return gofakes3.CopyObjectResult{}, ossQueryError(err, srcKey)
	}
	return gofakes3.CopyObjectResult{
		ETag:         quotedETag(row.Etag),
		LastModified: gofakes3.NewContentTime(row.UpdatedAt.Time),
	}, nil
}

// readLimited 按限流口径读取输入：size 已知（≥0）时经 gofakes3.ReadAll
// 读满预期长度（不足按协议错误上抛），未知（-1，chunked 形态）时读到
// 上限 +1 判定超限。超限以 EntityTooLarge 拒绝。
func readLimited(input io.Reader, size int64) ([]byte, error) {
	if size >= 0 {
		if size > maxObjectSize {
			return nil, gofakes3.ResourceError(gofakes3.ErrEntityTooLarge, "object")
		}
		data, err := gofakes3.ReadAll(input, size)
		if err != nil {
			return nil, err
		}
		return data, nil
	}
	data, err := io.ReadAll(io.LimitReader(input, maxObjectSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxObjectSize {
		return nil, gofakes3.ResourceError(gofakes3.ErrEntityTooLarge, "object")
	}
	return data, nil
}

// ossQueryError 把存储错误映射为 gofakes3 语义：无行即对象不存在
// （ErrNoSuchKey），其余原样上抛。
func ossQueryError(err error, key string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return gofakes3.KeyNotFound(key)
	}
	return err
}

// objectMetadata 还原对象元数据映射：jsonb 原样解码；Content-Type 缺失
// 时以单列值补齐（复制等路径的兜底）。
func objectMetadata(raw []byte, contentType string) map[string]string {
	meta := decodeMetadata(raw)
	if _, ok := meta[contentTypeMetadataKey]; !ok && contentType != "" {
		meta[contentTypeMetadataKey] = contentType
	}
	return meta
}

// metadataJSON 编码元数据为 jsonb：空映射落 NULL。
func metadataJSON(meta map[string]string) []byte {
	if len(meta) == 0 {
		return nil
	}
	encoded, err := json.Marshal(meta)
	if err != nil {
		// map[string]string 不可序列化仅在键含非法 UTF-8 时发生；退化
		// 为 NULL（元数据丢失优于写入失败）。
		return nil
	}
	return encoded
}

// decodeMetadata 解码 jsonb 元数据；NULL 或损坏返回空映射（元数据是
// 附属事实，不因解码失败拒绝读取）。
func decodeMetadata(raw []byte) map[string]string {
	meta := map[string]string{}
	if len(raw) == 0 {
		return meta
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return map[string]string{}
	}
	return meta
}

// metaContentType 取元数据里的 Content-Type；未声明返回空串（列存空串，
// 不推断）。
func metaContentType(meta map[string]string) string {
	if meta == nil {
		return ""
	}
	return meta[contentTypeMetadataKey]
}

// quotedETag 按 S3 响应口径给 etag 加引号（存储为不带引号的十六进制）。
func quotedETag(etag string) string {
	return `"` + etag + `"`
}

// etagHash 还原 ETag 的原始 MD5 字节（gofakes3 以 Object.Hash 计算响应
// ETag）。损坏的 etag 视为内部错误：写入路径只会写入合法十六进制。
func etagHash(etag string) ([]byte, error) {
	hash, err := hex.DecodeString(etag)
	if err != nil {
		return nil, fmt.Errorf("oss: stored etag %q is not valid hex: %w", etag, err)
	}
	return hash, nil
}

// likePrefixPattern 把字面前缀转义为 LIKE 模式（通配符语义由 Go 侧的
// gofakes3.Prefix.Match 承担，SQL 只做前缀收窄）。PostgreSQL LIKE 的
// 默认转义符为反斜杠。
func likePrefixPattern(prefix string) string {
	var b strings.Builder
	b.Grow(len(prefix) + 1)
	for _, r := range prefix {
		if r == '\\' || r == '%' || r == '_' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('%')
	return b.String()
}
