package oss

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"grape/internal/conf"
	pgsqlc "grape/internal/data/pg_sqlc"
	"grape/internal/data/pg_sqlc/deploy"
	"grape/internal/data/pg_sqlc/gen"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rclone/gofakes3"
)

// 内嵌 OSS 集成测试（真实 PostgreSQL，未设 GRAPE_OSS_TEST_ADMIN_DSN /
// GRAPE_OSS_TEST_DSN 时跳过）：覆盖 gofakes3 Backend 的存储语义（覆盖写、
// Range、列举、桶生命周期、限流、复制）与 /s3/ 挂载面的 SigV4 鉴权 +
// aws-sdk-go-v2 回环（path-style、预签名、篡改与匿名拒绝）。测试从模板库
// 克隆全新独立库；与根包关系面套件共用同一批测试角色口令。

const (
	// testGrapeUPassword / testTestUPassword 与 role.sql 占位符替换值：
	// 与根包、vector 包套件同一既定测试口令。
	testGrapeUPassword = "grape_u_pwd"
	testTestUPassword  = "test_u_pwd"

	// 测试 AK/SK 与业务桶名（桶名与种子 oss.bucket 一致）。
	testAccessKey = "test-access-key"
	testSecretKey = "test-secret-key"
	testBucket    = "grape"

	// testRegion 是 SDK 客户端的签名 region（内嵌实现按请求 scope 接受
	// 任意取值，取值本身不参与断言）。
	testRegion = "test-region"
)

// setupOSSTestDB 从模板库克隆全新独立测试库并以 grape_u 连接返回
// 连接池；返回的 adminDSN/testDSN 均已改写指向本次克隆的库——adminDSN 供
// 用例维护 sys_config 的 oss.* 配置状态（grape_u 对 sys_config 无写权限），
// testDSN 供门面装配。测试结束经 t.Cleanup 删库，测试可并行互不干扰。
func setupOSSTestDB(t *testing.T) (*pgxpool.Pool, string, string) {
	t.Helper()
	adminDSN := os.Getenv("GRAPE_OSS_TEST_ADMIN_DSN")
	testDSN := os.Getenv("GRAPE_OSS_TEST_DSN")
	if adminDSN == "" || testDSN == "" {
		t.Skip("GRAPE_OSS_TEST_ADMIN_DSN / GRAPE_OSS_TEST_DSN not set; skipping embedded oss integration tests")
	}
	ctx := context.Background()

	dbName, err := deploy.CloneTestDatabase(ctx, adminDSN, testGrapeUPassword, testTestUPassword, t.Name())
	if err != nil {
		t.Fatalf("clone test database: %v", err)
	}
	t.Cleanup(func() {
		if err := deploy.DropTestDatabase(ctx, adminDSN, dbName); err != nil {
			t.Logf("drop test database %s: %v", dbName, err)
		}
	})

	// 环境变量 DSN 指向固定库名，管理连接与测试池经改写指向本次克隆的库
	adminDSNOfClone, err := deploy.RewriteDSNDatabase(adminDSN, dbName)
	if err != nil {
		t.Fatalf("rewrite admin dsn: %v", err)
	}
	poolDSN, err := deploy.RewriteDSNDatabase(testDSN, dbName)
	if err != nil {
		t.Fatalf("rewrite test dsn: %v", err)
	}
	pool, err := pgxpool.New(ctx, poolDSN)
	if err != nil {
		t.Fatalf("connect grape_u: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, adminDSNOfClone, poolDSN
}

// seedOSSCredentials 以管理员补插测试 AK/SK 配置（种子不含明文凭据，
// 内嵌服务启用前必须由运维经管理端配置；测试复刻该事实）。
func seedOSSCredentials(t *testing.T, adminDSN string) {
	t.Helper()
	setOSSConfigs(t, adminDSN, map[string]string{
		"oss.access_key": `"` + testAccessKey + `"`,
		"oss.secret_key": `"` + testSecretKey + `"`,
	})
}

// setOSSConfigs 以管理员重写 sys_config 的 oss.* 键值（先删后插，幂等；
// id 取 99 段高位，避开种子 1–13）。adminDSN 由 setup 返回、已指向克隆库。
func setOSSConfigs(t *testing.T, adminDSN string, values map[string]string) {
	t.Helper()
	ctx := context.Background()
	cfg, err := pgx.ParseConfig(adminDSN)
	if err != nil {
		t.Fatalf("parse admin dsn: %v", err)
	}
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	defer conn.Close(ctx)
	for key, value := range values {
		if _, err := conn.Exec(ctx,
			"DELETE FROM public.sys_config WHERE config_key = $1", key); err != nil {
			t.Fatalf("delete config %s: %v", key, err)
		}
		if _, err := conn.Exec(ctx,
			`INSERT INTO public.sys_config (id, status, "name", config_key, value, value_type, remark,
			                                created_by, created_at, updated_by, updated_at)
			 VALUES ($1, 1, 'test config', $2, $3, 'json', 'test',
			         1, CURRENT_TIMESTAMP, 1, CURRENT_TIMESTAMP)`, ossConfigTestID(key), key, value); err != nil {
			t.Fatalf("insert config %s: %v", key, err)
		}
	}
}

// ossConfigTestID 给测试配置键分配固定 id（键级幂等：先删后插不与他键
// 撞主键；99 段高位避开种子 1–13）。
func ossConfigTestID(key string) int64 {
	base := int64(990000)
	for i, c := range []byte(key) {
		base += int64(int(c)) << (i % 8)
	}
	return base
}

// deleteOSSConfigs 以管理员删除 oss.* 键（模拟配置缺失的 fail-closed 面）。
// adminDSN 由 setup 返回、已指向克隆库。
func deleteOSSConfigs(t *testing.T, adminDSN string, keys ...string) {
	t.Helper()
	ctx := context.Background()
	cfg, err := pgx.ParseConfig(adminDSN)
	if err != nil {
		t.Fatalf("parse admin dsn: %v", err)
	}
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	defer conn.Close(ctx)
	for _, key := range keys {
		if _, err := conn.Exec(ctx, "DELETE FROM public.sys_config WHERE config_key = $1", key); err != nil {
			t.Fatalf("delete config %s: %v", key, err)
		}
	}
}

// ossTestConf 构造与部署一致的设施配置。
func ossTestConf() *conf.OSS {
	return &conf.OSS{Enabled: true}
}

// mustNewBackend 以测试池构造 Backend。
func mustNewBackend(t *testing.T, pool *pgxpool.Pool) *Backend {
	t.Helper()
	return NewBackend(pool)
}

// putObject 是 Backend.PutObject 的测试包装（Content-Length 已知形态）。
func putObject(t *testing.T, b *Backend, key string, data []byte, meta map[string]string) gofakes3.PutObjectResult {
	t.Helper()
	result, err := b.PutObject(context.Background(), testBucket, key, meta, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("put object %s: %v", key, err)
	}
	return result
}

// md5Hex 计算内容的 MD5 十六进制（与 Backend 的 etag 口径一致）。
func md5Hex(data []byte) string {
	sum := md5.Sum(data)
	return hex.EncodeToString(sum[:])
}

// TestOSSBackendPutGetRoundTrip 验证覆盖写与全量读的口径一致：data/
// size/etag/元数据（Content-Type 与 X-Amz-Meta-*）原样往返。
func TestOSSBackendPutGetRoundTrip(t *testing.T) {
	t.Parallel()
	pool, _, _ := setupOSSTestDB(t)
	b := mustNewBackend(t, pool)
	ctx := context.Background()

	data := []byte("hello oss object payload")
	meta := map[string]string{
		"Content-Type":    "text/markdown; charset=utf-8",
		"X-Amz-Meta-From": "unit-test",
	}
	result := putObject(t, b, "docs/roundtrip.md", data, meta)
	if want := `"` + md5Hex(data) + `"`; result.ETag != want {
		t.Fatalf("put etag = %q, want %q", result.ETag, want)
	}

	obj, err := b.GetObject(ctx, testBucket, "docs/roundtrip.md", nil)
	if err != nil {
		t.Fatalf("get object: %v", err)
	}
	defer obj.Contents.Close()
	got, err := io.ReadAll(obj.Contents)
	if err != nil {
		t.Fatalf("read contents: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("roundtrip body mismatch: got %q want %q", got, data)
	}
	if obj.Size != int64(len(data)) {
		t.Fatalf("object size = %d, want %d", obj.Size, len(data))
	}
	if gotHash := hex.EncodeToString(obj.Hash); gotHash != md5Hex(data) {
		t.Fatalf("object hash = %s, want %s", gotHash, md5Hex(data))
	}
	if got := obj.Metadata["Content-Type"]; got != meta["Content-Type"] {
		t.Fatalf("content type = %q, want %q", got, meta["Content-Type"])
	}
	if got := obj.Metadata["X-Amz-Meta-From"]; got != meta["X-Amz-Meta-From"] {
		t.Fatalf("user metadata = %q, want %q", got, meta["X-Amz-Meta-From"])
	}
}

// TestOSSBackendPutObjectOverwriteKeepsCreatedAt 验证同键覆盖写：内容与
// etag 更新、created_at 保持首次写入、updated_at 前移。
func TestOSSBackendPutObjectOverwriteKeepsCreatedAt(t *testing.T) {
	t.Parallel()
	pool, _, _ := setupOSSTestDB(t)
	b := mustNewBackend(t, pool)
	ctx := context.Background()

	first := putObject(t, b, "overwrite.txt", []byte("first"), nil)
	rowOne, err := b.q.GetOssObjectMeta(ctx, gen.GetOssObjectMetaParams{Bucket: testBucket, ObjectKey: "overwrite.txt"})
	if err != nil {
		t.Fatalf("get meta: %v", err)
	}
	time.Sleep(1100 * time.Millisecond) // timestamp(0) 秒级精度，跨秒观察 updated_at 前移
	second := putObject(t, b, "overwrite.txt", []byte("second"), nil)
	rowTwo, err := b.q.GetOssObjectMeta(ctx, gen.GetOssObjectMetaParams{Bucket: testBucket, ObjectKey: "overwrite.txt"})
	if err != nil {
		t.Fatalf("get meta after overwrite: %v", err)
	}

	if first.ETag == second.ETag {
		t.Fatalf("overwrite etag %q unchanged", first.ETag)
	}
	if want := `"` + md5Hex([]byte("second")) + `"`; second.ETag != want {
		t.Fatalf("overwrite etag = %q, want %q", second.ETag, want)
	}
	if !rowOne.CreatedAt.Time.Equal(rowTwo.CreatedAt.Time) {
		t.Fatalf("created_at moved on overwrite: %v -> %v", rowOne.CreatedAt.Time, rowTwo.CreatedAt.Time)
	}
	if !rowTwo.UpdatedAt.Time.After(rowOne.UpdatedAt.Time) {
		t.Fatalf("updated_at did not advance: %v -> %v", rowOne.UpdatedAt.Time, rowTwo.UpdatedAt.Time)
	}
}

// TestOSSBackendGetObjectRange 验证 Range 读取只返回区间字节且 Size 恒
// 为全长（服务器据此发 206 与 Content-Range）。
func TestOSSBackendGetObjectRange(t *testing.T) {
	t.Parallel()
	pool, _, _ := setupOSSTestDB(t)
	b := mustNewBackend(t, pool)
	ctx := context.Background()

	data := bytes.Repeat([]byte("0123456789"), 10) // 100 字节
	putObject(t, b, "range.bin", data, nil)

	rnge, err := (&gofakes3.ObjectRangeRequest{Start: 10, End: 19}).Range(int64(len(data)))
	if err != nil {
		t.Fatalf("compute range: %v", err)
	}
	obj, err := b.GetObject(ctx, testBucket, "range.bin",
		&gofakes3.ObjectRangeRequest{Start: 10, End: 19})
	if err != nil {
		t.Fatalf("get range: %v", err)
	}
	defer obj.Contents.Close()
	got, err := io.ReadAll(obj.Contents)
	if err != nil {
		t.Fatalf("read range: %v", err)
	}
	if !bytes.Equal(got, data[10:20]) {
		t.Fatalf("range body = %q, want %q", got, data[10:20])
	}
	if obj.Size != int64(len(data)) {
		t.Fatalf("range object size = %d, want full %d", obj.Size, len(data))
	}
	if obj.Range == nil || obj.Range.Start != rnge.Start || obj.Range.Length != rnge.Length {
		t.Fatalf("object range = %+v, want start=%d length=%d", obj.Range, rnge.Start, rnge.Length)
	}
}

// TestOSSBackendGetHeadMissingKey 验证对象不存在时 Get/Head 统一返回
// ErrNoSuchKey（gofakes3 资源错误）。
func TestOSSBackendGetHeadMissingKey(t *testing.T) {
	t.Parallel()
	pool, _, _ := setupOSSTestDB(t)
	b := mustNewBackend(t, pool)
	ctx := context.Background()

	if _, err := b.GetObject(ctx, testBucket, "missing.txt", nil); !gofakes3.HasErrorCode(err, gofakes3.ErrNoSuchKey) {
		t.Fatalf("get missing = %v, want ErrNoSuchKey", err)
	}
	if _, err := b.HeadObject(ctx, testBucket, "missing.txt"); !gofakes3.HasErrorCode(err, gofakes3.ErrNoSuchKey) {
		t.Fatalf("head missing = %v, want ErrNoSuchKey", err)
	}
}

// TestOSSBackendDeleteObjectIdempotent 验证删除幂等：存在的键删除成功，
// 不存在的键同样不报错（S3 DELETE 语义）。
func TestOSSBackendDeleteObjectIdempotent(t *testing.T) {
	t.Parallel()
	pool, _, _ := setupOSSTestDB(t)
	b := mustNewBackend(t, pool)
	ctx := context.Background()

	putObject(t, b, "gone.txt", []byte("x"), nil)
	if _, err := b.DeleteObject(ctx, testBucket, "gone.txt"); err != nil {
		t.Fatalf("delete existing: %v", err)
	}
	if _, err := b.DeleteObject(ctx, testBucket, "gone.txt"); err != nil {
		t.Fatalf("delete missing should be idempotent: %v", err)
	}
	if _, err := b.GetObject(ctx, testBucket, "gone.txt", nil); !gofakes3.HasErrorCode(err, gofakes3.ErrNoSuchKey) {
		t.Fatalf("get after delete = %v, want ErrNoSuchKey", err)
	}
}

// TestOSSBackendDeleteMulti 验证批量删除：不存在的键同样按已删除报告，
// 其余对象不受影响。
func TestOSSBackendDeleteMulti(t *testing.T) {
	t.Parallel()
	pool, _, _ := setupOSSTestDB(t)
	b := mustNewBackend(t, pool)
	ctx := context.Background()

	putObject(t, b, "multi/a.txt", []byte("a"), nil)
	putObject(t, b, "multi/b.txt", []byte("b"), nil)

	result, err := b.DeleteMulti(ctx, testBucket, "multi/a.txt", "multi/ghost.txt")
	if err != nil {
		t.Fatalf("delete multi: %v", err)
	}
	if len(result.Deleted) != 2 {
		t.Fatalf("deleted count = %d, want 2", len(result.Deleted))
	}
	if _, err := b.GetObject(ctx, testBucket, "multi/a.txt", nil); !gofakes3.HasErrorCode(err, gofakes3.ErrNoSuchKey) {
		t.Fatalf("a.txt = %v, want ErrNoSuchKey", err)
	}
	if _, err := b.GetObject(ctx, testBucket, "multi/b.txt", nil); err != nil {
		t.Fatalf("b.txt should survive: %v", err)
	}
}

// TestOSSBackendListBucketPrefixDelimiterMarker 验证列举的前缀收窄、
// delimiter 公共前缀归并与 marker 字典序起点；空桶按 NoSuchBucket。
func TestOSSBackendListBucketPrefixDelimiterMarker(t *testing.T) {
	t.Parallel()
	pool, _, _ := setupOSSTestDB(t)
	b := mustNewBackend(t, pool)
	ctx := context.Background()

	if _, err := b.ListBucket(ctx, testBucket, nil, gofakes3.ListBucketPage{}); !gofakes3.HasErrorCode(err, gofakes3.ErrNoSuchBucket) {
		t.Fatalf("list empty bucket = %v, want ErrNoSuchBucket", err)
	}

	for _, key := range []string{"a.txt", "m.txt", "dir/x.txt", "dir/y.txt", "dir/sub/z.txt"} {
		putObject(t, b, key, []byte(key), nil)
	}

	list := func(prefix *gofakes3.Prefix, page gofakes3.ListBucketPage) *gofakes3.ObjectList {
		t.Helper()
		out, err := b.ListBucket(ctx, testBucket, prefix, page)
		if err != nil {
			t.Fatalf("list bucket: %v", err)
		}
		return out
	}

	root := list(&gofakes3.Prefix{HasDelimiter: true, Delimiter: "/"}, gofakes3.ListBucketPage{})
	if len(root.Contents) != 2 || root.Contents[0].Key != "a.txt" || root.Contents[1].Key != "m.txt" {
		t.Fatalf("root contents = %+v, want [a.txt m.txt]", root.Contents)
	}
	if len(root.CommonPrefixes) != 1 || root.CommonPrefixes[0].Prefix != "dir/" {
		t.Fatalf("root common prefixes = %+v, want [dir/]", root.CommonPrefixes)
	}

	dir := list(&gofakes3.Prefix{HasPrefix: true, Prefix: "dir/", HasDelimiter: true, Delimiter: "/"},
		gofakes3.ListBucketPage{})
	if len(dir.Contents) != 2 || dir.Contents[0].Key != "dir/x.txt" || dir.Contents[1].Key != "dir/y.txt" {
		t.Fatalf("dir contents = %+v, want [dir/x.txt dir/y.txt]", dir.Contents)
	}
	if len(dir.CommonPrefixes) != 1 || dir.CommonPrefixes[0].Prefix != "dir/sub/" {
		t.Fatalf("dir common prefixes = %+v, want [dir/sub/]", dir.CommonPrefixes)
	}

	paged := list(nil, gofakes3.ListBucketPage{MaxKeys: 2})
	if len(paged.Contents) != 2 || !paged.IsTruncated ||
		paged.Contents[0].Key != "a.txt" || paged.Contents[1].Key != "dir/sub/z.txt" ||
		paged.NextMarker != "dir/sub/z.txt" {
		t.Fatalf("paged = %+v truncated=%v marker=%q, want [a.txt dir/sub/z.txt] + truncation",
			paged.Contents, paged.IsTruncated, paged.NextMarker)
	}

	after := list(&gofakes3.Prefix{HasDelimiter: true, Delimiter: "/"}, gofakes3.ListBucketPage{HasMarker: true, Marker: "a.txt"})
	if len(after.Contents) != 1 || after.Contents[0].Key != "m.txt" {
		t.Fatalf("after marker a.txt = %+v, want [m.txt]", after.Contents)
	}
	if len(after.CommonPrefixes) != 1 || after.CommonPrefixes[0].Prefix != "dir/" {
		t.Fatalf("after marker common prefixes = %+v, want [dir/]", after.CommonPrefixes)
	}
}

// TestOSSBackendListBucketDelimiterPaginationNoDuplicateCommonPrefix 验证
// delimiter 列举分页跨公共前缀组：同一公共前缀的重复命中行不计入
// MaxKeys（与 s3mem 同算法），分页边界不落在组内部空转，下一页在组内
// marker 起点继续时补齐剩余条目且不再虚报截断。
func TestOSSBackendListBucketDelimiterPaginationNoDuplicateCommonPrefix(t *testing.T) {
	t.Parallel()
	pool, _, _ := setupOSSTestDB(t)
	b := mustNewBackend(t, pool)
	ctx := context.Background()

	for _, key := range []string{"a.txt", "dir/p1.txt", "dir/p2.txt", "dir/p3.txt", "z.txt"} {
		putObject(t, b, key, []byte(key), nil)
	}

	list := func(page gofakes3.ListBucketPage) *gofakes3.ObjectList {
		t.Helper()
		out, err := b.ListBucket(ctx, testBucket,
			&gofakes3.Prefix{HasDelimiter: true, Delimiter: "/"}, page)
		if err != nil {
			t.Fatalf("list bucket: %v", err)
		}
		return out
	}

	page1 := list(gofakes3.ListBucketPage{MaxKeys: 2})
	if len(page1.Contents) != 1 || page1.Contents[0].Key != "a.txt" {
		t.Fatalf("page1 contents = %+v, want [a.txt]", page1.Contents)
	}
	if len(page1.CommonPrefixes) != 1 || page1.CommonPrefixes[0].Prefix != "dir/" {
		t.Fatalf("page1 common prefixes = %+v, want [dir/]", page1.CommonPrefixes)
	}
	if !page1.IsTruncated || page1.NextMarker != "dir/p1.txt" {
		t.Fatalf("page1 truncated=%v marker=%q, want truncated with marker dir/p1.txt",
			page1.IsTruncated, page1.NextMarker)
	}

	page2 := list(gofakes3.ListBucketPage{MaxKeys: 2, HasMarker: true, Marker: page1.NextMarker})
	if len(page2.Contents) != 1 || page2.Contents[0].Key != "z.txt" {
		t.Fatalf("page2 contents = %+v, want [z.txt] (duplicate dir/ rows must not consume MaxKeys)",
			page2.Contents)
	}
	if len(page2.CommonPrefixes) != 1 || page2.CommonPrefixes[0].Prefix != "dir/" {
		t.Fatalf("page2 common prefixes = %+v, want [dir/]", page2.CommonPrefixes)
	}
	if page2.IsTruncated {
		t.Fatalf("page2 truncated = true, want false (listing exhausted after z.txt)")
	}
}

// TestOSSBackendBucketLifecycle 验证桶口径：存在性以对象行为准，创建
// 幂等（已有对象即 BucketAlreadyExists）、非空拒绝删除、清空后可删。
func TestOSSBackendBucketLifecycle(t *testing.T) {
	t.Parallel()
	pool, _, _ := setupOSSTestDB(t)
	b := mustNewBackend(t, pool)
	ctx := context.Background()

	exists, err := b.BucketExists(ctx, testBucket)
	if err != nil || exists {
		t.Fatalf("bucket exists before first object = %v (%v), want false", exists, err)
	}
	// 首个对象写入（WithAutoBucket 路径的 CreateBucket 空桶幂等成功）。
	if err := b.CreateBucket(ctx, testBucket); err != nil {
		t.Fatalf("create empty bucket: %v", err)
	}
	putObject(t, b, "bucket/obj.txt", []byte("x"), nil)
	exists, err = b.BucketExists(ctx, testBucket)
	if err != nil || !exists {
		t.Fatalf("bucket exists after put = %v (%v), want true", exists, err)
	}
	if err := b.CreateBucket(ctx, testBucket); !gofakes3.HasErrorCode(err, gofakes3.ErrBucketAlreadyExists) {
		t.Fatalf("create existing = %v, want ErrBucketAlreadyExists", err)
	}
	if err := b.DeleteBucket(ctx, testBucket); !gofakes3.HasErrorCode(err, gofakes3.ErrBucketNotEmpty) {
		t.Fatalf("delete non-empty = %v, want ErrBucketNotEmpty", err)
	}
	if _, err := b.DeleteObject(ctx, testBucket, "bucket/obj.txt"); err != nil {
		t.Fatalf("cleanup object: %v", err)
	}
	if err := b.DeleteBucket(ctx, testBucket); err != nil {
		t.Fatalf("delete emptied bucket: %v", err)
	}
}

// TestOSSBackendPutObjectRejectsOversize 验证 20MB 限流：已知大小直接
// 拒绝；未知大小读到上限 +1 判定；上限内未知大小正常写入。
func TestOSSBackendPutObjectRejectsOversize(t *testing.T) {
	t.Parallel()
	pool, _, _ := setupOSSTestDB(t)
	b := mustNewBackend(t, pool)
	ctx := context.Background()

	oversize := make([]byte, maxObjectSize+1)
	if _, err := b.PutObject(ctx, testBucket, "big-known.bin", nil, bytes.NewReader(oversize), int64(len(oversize))); !gofakes3.HasErrorCode(err, gofakes3.ErrEntityTooLarge) {
		t.Fatalf("known oversize = %v, want ErrEntityTooLarge", err)
	}
	if _, err := b.PutObject(ctx, testBucket, "big-unknown.bin", nil, bytes.NewReader(oversize), -1); !gofakes3.HasErrorCode(err, gofakes3.ErrEntityTooLarge) {
		t.Fatalf("unknown oversize = %v, want ErrEntityTooLarge", err)
	}
	okData := make([]byte, 1<<20)
	if _, err := b.PutObject(ctx, testBucket, "ok-unknown.bin", nil, bytes.NewReader(okData), -1); err != nil {
		t.Fatalf("unknown size within limit: %v", err)
	}
}

// TestOSSBackendPutObjectAcceptsExactMaxSize 验证恰好 20MB 的已知大小
// 写入成功（限流只在严格大于上限时拒绝），落库 size 与 etag 一致。
func TestOSSBackendPutObjectAcceptsExactMaxSize(t *testing.T) {
	t.Parallel()
	pool, _, _ := setupOSSTestDB(t)
	b := mustNewBackend(t, pool)
	ctx := context.Background()

	data := make([]byte, maxObjectSize)
	result, err := b.PutObject(ctx, testBucket, "exact-max.bin", nil, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("put exact max size: %v", err)
	}
	if want := `"` + md5Hex(data) + `"`; result.ETag != want {
		t.Fatalf("etag = %q, want %q", result.ETag, want)
	}
	row, err := b.q.GetOssObjectMeta(ctx, gen.GetOssObjectMetaParams{Bucket: testBucket, ObjectKey: "exact-max.bin"})
	if err != nil {
		t.Fatalf("get meta after put: %v", err)
	}
	if row.Size != int64(maxObjectSize) {
		t.Fatalf("stored size = %d, want %d", row.Size, maxObjectSize)
	}
}

// TestOSSBackendCopyObject 验证复制：data/etag 随源，meta 取调用方给定的
// 完整元数据；源不存在返回 ErrNoSuchKey。
func TestOSSBackendCopyObject(t *testing.T) {
	t.Parallel()
	pool, _, _ := setupOSSTestDB(t)
	b := mustNewBackend(t, pool)
	ctx := context.Background()

	srcData := []byte("copy source payload")
	src := putObject(t, b, "copy/src.txt", srcData, map[string]string{"Content-Type": "text/plain"})

	dst, err := b.CopyObject(ctx, testBucket, "copy/src.txt", testBucket, "copy/dst.md",
		map[string]string{"Content-Type": "application/json"})
	if err != nil {
		t.Fatalf("copy object: %v", err)
	}
	if dst.ETag != src.ETag {
		t.Fatalf("copy etag = %q, want source %q", dst.ETag, src.ETag)
	}
	copied, err := b.GetObject(ctx, testBucket, "copy/dst.md", nil)
	if err != nil {
		t.Fatalf("get copy: %v", err)
	}
	defer copied.Contents.Close()
	got, err := io.ReadAll(copied.Contents)
	if err != nil {
		t.Fatalf("read copy: %v", err)
	}
	if !bytes.Equal(got, srcData) {
		t.Fatalf("copy body mismatch: %q", got)
	}
	if got := copied.Metadata["Content-Type"]; got != "application/json" {
		t.Fatalf("copy content type = %q, want application/json", got)
	}
	if _, err := b.CopyObject(ctx, testBucket, "copy/ghost.txt", testBucket, "copy/other.txt", nil); !gofakes3.HasErrorCode(err, gofakes3.ErrNoSuchKey) {
		t.Fatalf("copy missing source = %v, want ErrNoSuchKey", err)
	}
}

// TestOSSBackendMetadataDriftTolerated 验证 metadata 形态漂移（非对象的
// 合法 JSON）不阻塞对象读取：元数据退化空映射，content_type 单列兜底。
func TestOSSBackendMetadataDriftTolerated(t *testing.T) {
	t.Parallel()
	pool, _, _ := setupOSSTestDB(t)
	b := mustNewBackend(t, pool)
	ctx := context.Background()

	putObject(t, b, "drift/meta.txt", []byte("payload"), nil)
	// PG 强制 jsonb 合法性，「损坏 jsonb」不存在于库层；退化场景是形态
	// 漂移（非对象 JSON，如数组）——解码退化空映射，读取不受阻。
	if _, err := pool.Exec(ctx, "UPDATE public.oss_object SET metadata = $1::jsonb WHERE object_key = $2", "[1,2,3]", "drift/meta.txt"); err != nil {
		t.Fatalf("write drifted metadata: %v", err)
	}

	obj, err := b.HeadObject(ctx, testBucket, "drift/meta.txt")
	if err != nil {
		t.Fatalf("head with drifted metadata: %v", err)
	}
	if len(obj.Metadata) != 0 {
		t.Fatalf("metadata = %+v, want empty", obj.Metadata)
	}
}

// newOSSLoopback 构造启用状态的内嵌 S3 服务并挂上 httptest：返回服务
// 地址（BaseEndpoint 形态，含 /s3 前缀）与 Backend（供直接断言落库事实）。
func newOSSLoopback(t *testing.T, pool *pgxpool.Pool, testDSN string) (string, *Backend) {
	t.Helper()
	handler, cleanup, err := NewHandler(ossTestConf(), pgsqlcTestData(t, testDSN))
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	if handler == nil {
		t.Fatal("NewHandler = nil, want mounted handler for enabled local facility")
	}
	t.Cleanup(cleanup)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server.URL + basePath, NewBackend(pool)
}

// pgsqlcTestData 以 testDSN 构造存储门面（与 vector 套件同法）。
func pgsqlcTestData(t *testing.T, testDSN string) *pgsqlc.Data {
	t.Helper()
	d, cleanup, err := pgsqlc.NewData(&conf.Data{Database: &conf.Data_Database{
		Master: &conf.Data_Database_Slot{Source: testDSN},
		System: &conf.Data_Database_Slot{Source: testDSN},
	}})
	if err != nil {
		t.Fatalf("connect data facade: %v", err)
	}
	t.Cleanup(cleanup)
	return d
}

// newS3Client 以 path-style + 静态凭据构造回环 SDK 客户端（生产形态，
// 不走 LoadDefaultConfig）。
func newS3Client(t *testing.T, endpoint string) *awss3.Client {
	t.Helper()
	return awss3.New(awss3.Options{
		BaseEndpoint: aws.String(endpoint),
		Region:       testRegion,
		Credentials:  credentials.NewStaticCredentialsProvider(testAccessKey, testSecretKey, ""),
		UsePathStyle: true,
	})
}

// TestOSSHandlerSDKRoundTrip 验证 /s3/ 回环：SDK Put / Get(Range 206) /
// Head / Delete 全链路落在 oss_object。
func TestOSSHandlerSDKRoundTrip(t *testing.T) {
	t.Parallel()
	pool, adminDSN, testDSN := setupOSSTestDB(t)
	seedOSSCredentials(t, adminDSN)
	endpoint, b := newOSSLoopback(t, pool, testDSN)
	ctx := context.Background()
	client := newS3Client(t, endpoint)

	data := []byte("s3 sdk roundtrip payload")
	putOut, err := client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket:      aws.String(testBucket),
		Key:         aws.String("sdk/roundtrip.txt"),
		Body:        bytes.NewReader(data),
		ContentType: aws.String("text/plain"),
	})
	if err != nil {
		t.Fatalf("sdk put: %v", err)
	}
	if want := `"` + md5Hex(data) + `"`; aws.ToString(putOut.ETag) != want {
		t.Fatalf("sdk put etag = %q, want %q", aws.ToString(putOut.ETag), want)
	}

	head, err := client.HeadObject(ctx, &awss3.HeadObjectInput{
		Bucket: aws.String(testBucket), Key: aws.String("sdk/roundtrip.txt")})
	if err != nil {
		t.Fatalf("sdk head: %v", err)
	}
	if aws.ToInt64(head.ContentLength) != int64(len(data)) {
		t.Fatalf("head content length = %d, want %d", aws.ToInt64(head.ContentLength), len(data))
	}
	if aws.ToString(head.ContentType) != "text/plain" {
		t.Fatalf("head content type = %q, want text/plain", aws.ToString(head.ContentType))
	}

	getOut, err := client.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(testBucket), Key: aws.String("sdk/roundtrip.txt"),
		Range: aws.String("bytes=4-9"),
	})
	if err != nil {
		t.Fatalf("sdk get range: %v", err)
	}
	ranged, err := io.ReadAll(getOut.Body)
	getOut.Body.Close()
	if err != nil {
		t.Fatalf("read range body: %v", err)
	}
	if !bytes.Equal(ranged, data[4:10]) {
		t.Fatalf("range body = %q, want %q", ranged, data[4:10])
	}

	if _, err := client.DeleteObject(ctx, &awss3.DeleteObjectInput{
		Bucket: aws.String(testBucket), Key: aws.String("sdk/roundtrip.txt")}); err != nil {
		t.Fatalf("sdk delete: %v", err)
	}
	if _, err := b.GetObject(ctx, testBucket, "sdk/roundtrip.txt", nil); !gofakes3.HasErrorCode(err, gofakes3.ErrNoSuchKey) {
		t.Fatalf("object after delete = %v, want ErrNoSuchKey", err)
	}
}

// TestOSSHandlerPresignedGetAndPut 验证预签名 URL 直传直下载：无凭据的
// 裸 HTTP 请求按签名放行，PresignPut 写入的对象可在落库中读到。
func TestOSSHandlerPresignedGetAndPut(t *testing.T) {
	t.Parallel()
	pool, adminDSN, testDSN := setupOSSTestDB(t)
	seedOSSCredentials(t, adminDSN)
	endpoint, b := newOSSLoopback(t, pool, testDSN)
	ctx := context.Background()
	client := newS3Client(t, endpoint)

	data := []byte("presigned payload")
	putURL, err := awss3.NewPresignClient(client).PresignPutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String(testBucket), Key: aws.String("presigned/up.bin"),
	}, awss3.WithPresignExpires(5*time.Minute))
	if err != nil {
		t.Fatalf("presign put: %v", err)
	}
	putReq, err := http.NewRequest(http.MethodPut, putURL.URL, bytes.NewReader(data))
	if err != nil {
		t.Fatalf("build presigned put: %v", err)
	}
	putReq.Header.Set("Content-Type", "application/octet-stream")
	resp, err := http.DefaultClient.Do(putReq)
	if err != nil {
		t.Fatalf("presigned put request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("presigned put status = %d, want 200", resp.StatusCode)
	}

	stored, err := b.GetObject(ctx, testBucket, "presigned/up.bin", nil)
	if err != nil {
		t.Fatalf("presigned put should land in storage: %v", err)
	}
	stored.Contents.Close()

	getURL, err := awss3.NewPresignClient(client).PresignGetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(testBucket), Key: aws.String("presigned/up.bin"),
	}, awss3.WithPresignExpires(5*time.Minute))
	if err != nil {
		t.Fatalf("presign get: %v", err)
	}
	getResp, err := http.Get(getURL.URL)
	if err != nil {
		t.Fatalf("presigned get request: %v", err)
	}
	defer getResp.Body.Close()
	got, err := io.ReadAll(getResp.Body)
	if err != nil {
		t.Fatalf("read presigned get: %v", err)
	}
	if getResp.StatusCode != http.StatusOK || !bytes.Equal(got, data) {
		t.Fatalf("presigned get status=%d body=%q, want 200 %q", getResp.StatusCode, got, data)
	}
}

// TestOSSHandlerRejectsUnsignedAndTampered 验证 SigV4 是唯一鉴权点：
// 匿名 403、篡改签名 SignatureDoesNotMatch、未知 AK InvalidAccessKeyId、
// 过期预签名 403；失败应答为 XML 错误体。
func TestOSSHandlerRejectsUnsignedAndTampered(t *testing.T) {
	t.Parallel()
	pool, adminDSN, testDSN := setupOSSTestDB(t)
	seedOSSCredentials(t, adminDSN)
	endpoint, b := newOSSLoopback(t, pool, testDSN)
	ctx := context.Background()
	client := newS3Client(t, endpoint)

	putObject(t, b, "guarded.txt", []byte("secret"), nil)

	// 匿名（无签名）请求。
	anon, err := http.Get(endpoint + "/" + testBucket + "/guarded.txt")
	if err != nil {
		t.Fatalf("anonymous request: %v", err)
	}
	anon.Body.Close()
	if anon.StatusCode != http.StatusForbidden {
		t.Fatalf("anonymous status = %d, want 403", anon.StatusCode)
	}

	// 预签名后篡改签名段。
	getURL, err := awss3.NewPresignClient(client).PresignGetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(testBucket), Key: aws.String("guarded.txt"),
	}, awss3.WithPresignExpires(5*time.Minute))
	if err != nil {
		t.Fatalf("presign: %v", err)
	}
	tampered := strings.Replace(getURL.URL, "X-Amz-Signature=", "X-Amz-Signature=00", 1)
	if tampered == getURL.URL {
		t.Fatalf("failed to tamper signature in %q", getURL.URL)
	}
	tamperResp, err := http.Get(tampered)
	if err != nil {
		t.Fatalf("tampered request: %v", err)
	}
	tamperBody, _ := io.ReadAll(tamperResp.Body)
	tamperResp.Body.Close()
	if tamperResp.StatusCode != http.StatusForbidden || !strings.Contains(string(tamperBody), "SignatureDoesNotMatch") {
		t.Fatalf("tampered status=%d body=%q, want 403 SignatureDoesNotMatch", tamperResp.StatusCode, tamperBody)
	}

	// 未知 AK。
	stranger := awss3.New(awss3.Options{
		BaseEndpoint: aws.String(endpoint), Region: testRegion,
		Credentials:  credentials.NewStaticCredentialsProvider("unknown-ak", testSecretKey, ""),
		UsePathStyle: true,
	})
	if _, err := stranger.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(testBucket), Key: aws.String("guarded.txt")}); err == nil ||
		!strings.Contains(err.Error(), "InvalidAccessKeyId") {
		t.Fatalf("unknown access key = %v, want InvalidAccessKeyId", err)
	}

	// 过期预签名（1s TTL + 跨过有效期）。
	expiredURL, err := awss3.NewPresignClient(client).PresignGetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(testBucket), Key: aws.String("guarded.txt"),
	}, awss3.WithPresignExpires(1*time.Second))
	if err != nil {
		t.Fatalf("presign short: %v", err)
	}
	time.Sleep(2200 * time.Millisecond)
	expResp, err := http.Get(expiredURL.URL)
	if err != nil {
		t.Fatalf("expired request: %v", err)
	}
	expBody, _ := io.ReadAll(expResp.Body)
	expResp.Body.Close()
	if expResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expired status = %d body=%q, want 403", expResp.StatusCode, expBody)
	}
}

// TestOSSHandlerMountDecisionMatrix 验证挂载决策：enabled=false 不读库
// 直接跳过；provider 非 local 跳过；provider 缺失或 AK/SK 缺失在启用态
// fail-closed 拒绝启动。
func TestOSSHandlerMountDecisionMatrix(t *testing.T) {
	t.Parallel()
	_, adminDSN, testDSN := setupOSSTestDB(t)

	// enabled=false：不读配置即跳过（oss.* 键全部缺失也无副作用）。
	handler, cleanup, err := NewHandler(&conf.OSS{Enabled: false}, pgsqlcTestData(t, testDSN))
	if err != nil || handler != nil {
		t.Fatalf("disabled facility = (%v, %v), want (nil, nil)", handler, err)
	}
	cleanup()

	// provider=minio：本服务不出 S3 API，跳过挂载。
	seedOSSCredentials(t, adminDSN)
	setOSSConfigs(t, adminDSN, map[string]string{"oss.provider": `"minio"`})
	handler, cleanup, err = NewHandler(ossTestConf(), pgsqlcTestData(t, testDSN))
	if err != nil || handler != nil {
		t.Fatalf("minio provider = (%v, %v), want (nil, nil)", handler, err)
	}
	cleanup()

	// provider 缺失：启用态 fail-closed。
	deleteOSSConfigs(t, adminDSN, "oss.provider")
	if _, _, err := NewHandler(ossTestConf(), pgsqlcTestData(t, testDSN)); err == nil ||
		!strings.Contains(err.Error(), "oss.provider") {
		t.Fatalf("missing provider = %v, want error naming oss.provider", err)
	}

	// provider=local 而 AK/SK 缺失：启用态 fail-closed。
	setOSSConfigs(t, adminDSN, map[string]string{"oss.provider": `"local"`})
	deleteOSSConfigs(t, adminDSN, "oss.access_key")
	if _, _, err := NewHandler(ossTestConf(), pgsqlcTestData(t, testDSN)); err == nil ||
		!strings.Contains(err.Error(), "oss.access_key") {
		t.Fatalf("missing access key = %v, want error naming oss.access_key", err)
	}

	// 配置齐全：挂载成功。
	seedOSSCredentials(t, adminDSN)
	handler, cleanup, err = NewHandler(ossTestConf(), pgsqlcTestData(t, testDSN))
	if err != nil || handler == nil {
		t.Fatalf("complete config = (%v, %v), want handler", handler, err)
	}
	cleanup()
}

// TestOSSBackendGrapeUAuthorization 验证设施授权口径：grape_u 连接
// （测试池）可完成 DML（读写本表），符合 schema/93_oss.sql 的显式授权。
func TestOSSBackendGrapeUAuthorization(t *testing.T) {
	t.Parallel()
	pool, _, _ := setupOSSTestDB(t)
	b := mustNewBackend(t, pool)
	ctx := context.Background()

	putObject(t, b, "authz/probe.txt", []byte("grape-u"), nil)
	if _, err := b.HeadObject(ctx, testBucket, "authz/probe.txt"); err != nil {
		t.Fatalf("grape_u head after put: %v", err)
	}
	if _, err := b.DeleteObject(ctx, testBucket, "authz/probe.txt"); err != nil {
		t.Fatalf("grape_u delete: %v", err)
	}
}
