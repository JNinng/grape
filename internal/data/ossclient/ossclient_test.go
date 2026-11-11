package ossclient

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"grape/internal/biz"

	"github.com/rclone/gofakes3"
	"github.com/rclone/gofakes3/s3mem"
)

// biz.ObjectStore 的 S3 客户端语义测试：以内嵌 gofakes3（内存 backend，
// SigV4 鉴权开启）作回环设施，验证契约错误映射（对象不存在 / 设施未
// 配置 / 入参非法）、覆盖写与 ETag 口径、预签名 URL 可用性。不触真实
// 网络与数据库。

// testAccessKey / testSecretKey 是回环设施的鉴权对。
const (
	testAccessKey = "unit-ak"
	testSecretKey = "unit-sk"
	testBucket    = "unit-bucket"
	testRegion    = "unit-region"
)

// stubResolver 是 biz.OSSConfigResolver 的测试替身：返回预设配置，可注入
// 设施未配置错误。
type stubResolver struct {
	config biz.OSSConfig
	err    error
}

func (s *stubResolver) ResolveOSSConfig(context.Context) (biz.OSSConfig, error) {
	if s.err != nil {
		return biz.OSSConfig{}, s.err
	}
	return s.config, nil
}

// newLoopback 构造内存 S3 设施与指向它的 Client：返回客户端与设施地址。
func newLoopback(t *testing.T, resolver biz.OSSConfigResolver) biz.ObjectStore {
	t.Helper()
	backend := s3mem.New()
	server := gofakes3.New(backend,
		gofakes3.WithV4Auth(map[string]string{testAccessKey: testSecretKey}),
		gofakes3.WithAutoBucket(true))
	httpServer := httptest.NewServer(server.Server())
	t.Cleanup(httpServer.Close)

	config := biz.OSSConfig{
		Provider:  biz.OSSProviderLocal,
		Endpoint:  httpServer.URL,
		Bucket:    testBucket,
		AccessKey: testAccessKey,
		SecretKey: testSecretKey,
		Region:    testRegion,
	}
	store, cleanup, err := New(&stubResolver{config: config})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(cleanup)
	return store
}

// TestOSSClientPutGetRoundTrip 验证覆盖写与读取往返：body 原样、ETag 去
// 引号（MD5 十六进制）、contentType 透传设施。
func TestOSSClientPutGetRoundTrip(t *testing.T) {
	store := newLoopback(t, nil)
	ctx := context.Background()

	data := []byte("ossclient roundtrip payload")
	etag, err := store.Put(ctx, "docs/roundtrip.txt", bytes.NewReader(data), "text/plain")
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if len(etag) != 32 || strings.ContainsAny(etag, `"`) {
		t.Fatalf("etag = %q, want unquoted 32-char hex", etag)
	}

	body, err := store.Get(ctx, "docs/roundtrip.txt")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer body.Close()
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("roundtrip body = %q, want %q", got, data)
	}
}

// TestOSSClientPutOverwriteReturnsNewETag 验证同键覆盖写返回新 ETag 且
// 读回新内容。
func TestOSSClientPutOverwriteReturnsNewETag(t *testing.T) {
	store := newLoopback(t, nil)
	ctx := context.Background()

	first, err := store.Put(ctx, "overwrite.txt", strings.NewReader("first"), "")
	if err != nil {
		t.Fatalf("put first: %v", err)
	}
	second, err := store.Put(ctx, "overwrite.txt", strings.NewReader("second"), "")
	if err != nil {
		t.Fatalf("put second: %v", err)
	}
	if first == second {
		t.Fatalf("overwrite etag unchanged: %q", first)
	}
	body, err := store.Get(ctx, "overwrite.txt")
	if err != nil {
		t.Fatalf("get after overwrite: %v", err)
	}
	defer body.Close()
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "second" {
		t.Fatalf("content after overwrite = %q, want second", got)
	}
}

// TestOSSClientGetMissingKeyMapsNotFound 验证对象不存在映射
// biz.ErrObjectNotFound（errors.Is 可判）。
func TestOSSClientGetMissingKeyMapsNotFound(t *testing.T) {
	store := newLoopback(t, nil)
	ctx := context.Background()

	if _, err := store.Get(ctx, "ghost.txt"); !errors.Is(err, biz.ErrObjectNotFound) {
		t.Fatalf("get missing = %v, want biz.ErrObjectNotFound", err)
	}
}

// TestOSSClientDeleteIdempotent 验证删除幂等：删除存在的键成功，删除
// 不存在的键同样成功。
func TestOSSClientDeleteIdempotent(t *testing.T) {
	store := newLoopback(t, nil)
	ctx := context.Background()

	if _, err := store.Put(ctx, "gone.txt", strings.NewReader("x"), ""); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := store.Delete(ctx, "gone.txt"); err != nil {
		t.Fatalf("delete existing: %v", err)
	}
	if err := store.Delete(ctx, "gone.txt"); err != nil {
		t.Fatalf("delete missing should be idempotent: %v", err)
	}
	if _, err := store.Get(ctx, "gone.txt"); !errors.Is(err, biz.ErrObjectNotFound) {
		t.Fatalf("get after delete = %v, want biz.ErrObjectNotFound", err)
	}
}

// TestOSSClientPresignGetServesWithoutCredentials 验证 PresignGet 产出
// 的 URL 无凭据可下载（查询串签名自证身份），且 URL 携带 X-Amz-Signature。
func TestOSSClientPresignGetServesWithoutCredentials(t *testing.T) {
	store := newLoopback(t, nil)
	ctx := context.Background()

	data := []byte("presigned body")
	if _, err := store.Put(ctx, "presigned.txt", bytes.NewReader(data), ""); err != nil {
		t.Fatalf("put: %v", err)
	}
	url, err := store.PresignGet(ctx, "presigned.txt", time.Minute)
	if err != nil {
		t.Fatalf("presign get: %v", err)
	}
	if !strings.Contains(url, "X-Amz-Signature=") {
		t.Fatalf("presigned url %q missing signature", url)
	}
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("presigned get: %v", err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read presigned get: %v", err)
	}
	if resp.StatusCode != http.StatusOK || !bytes.Equal(got, data) {
		t.Fatalf("presigned get status=%d body=%q, want 200 %q", resp.StatusCode, got, data)
	}
}

// TestOSSClientPresignPutUploads 验证 PresignPut 产出的 URL 无凭据可上传，
// 上传后对象可读。
func TestOSSClientPresignPutUploads(t *testing.T) {
	store := newLoopback(t, nil)
	ctx := context.Background()

	url, err := store.PresignPut(ctx, "upload.txt", time.Minute)
	if err != nil {
		t.Fatalf("presign put: %v", err)
	}
	req, err := http.NewRequest(http.MethodPut, url, strings.NewReader("uploaded"))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("presigned put: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("presigned put status = %d, want 200", resp.StatusCode)
	}
	body, err := store.Get(ctx, "upload.txt")
	if err != nil {
		t.Fatalf("get uploaded: %v", err)
	}
	defer body.Close()
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read uploaded: %v", err)
	}
	if string(got) != "uploaded" {
		t.Fatalf("uploaded content = %q, want uploaded", got)
	}
}

// TestOSSClientInvalidArguments 验证契约入参校验：空白 key 与非正 TTL
// 拒绝（设施未触达）。
func TestOSSClientInvalidArguments(t *testing.T) {
	store := newLoopback(t, nil)
	ctx := context.Background()

	if _, err := store.Put(ctx, "  ", strings.NewReader("x"), ""); !errors.Is(err, biz.ErrObjectStoreInvalidArgument) {
		t.Fatalf("put blank key = %v, want biz.ErrObjectStoreInvalidArgument", err)
	}
	if _, err := store.Get(ctx, ""); !errors.Is(err, biz.ErrObjectStoreInvalidArgument) {
		t.Fatalf("get blank key = %v, want biz.ErrObjectStoreInvalidArgument", err)
	}
	if err := store.Delete(ctx, ""); !errors.Is(err, biz.ErrObjectStoreInvalidArgument) {
		t.Fatalf("delete blank key = %v, want biz.ErrObjectStoreInvalidArgument", err)
	}
	if _, err := store.PresignGet(ctx, "key.txt", 0); !errors.Is(err, biz.ErrObjectStoreInvalidArgument) {
		t.Fatalf("presign zero ttl = %v, want biz.ErrObjectStoreInvalidArgument", err)
	}
	if _, err := store.PresignPut(ctx, "key.txt", -time.Second); !errors.Is(err, biz.ErrObjectStoreInvalidArgument) {
		t.Fatalf("presign negative ttl = %v, want biz.ErrObjectStoreInvalidArgument", err)
	}
}

// TestOSSClientDisabledFacility 验证配置解析失败按设施缺失透传
// （biz.ErrObjectStoreDisabled），各方法一致。
func TestOSSClientDisabledFacility(t *testing.T) {
	ctx := context.Background()
	disabled, cleanup, err := New(&stubResolver{err: biz.ErrObjectStoreDisabled})
	if err != nil {
		t.Fatalf("New disabled: %v", err)
	}
	defer cleanup()

	if _, err := disabled.Put(ctx, "k", strings.NewReader("x"), ""); !errors.Is(err, biz.ErrObjectStoreDisabled) {
		t.Fatalf("put disabled = %v, want biz.ErrObjectStoreDisabled", err)
	}
	if _, err := disabled.Get(ctx, "k"); !errors.Is(err, biz.ErrObjectStoreDisabled) {
		t.Fatalf("get disabled = %v, want biz.ErrObjectStoreDisabled", err)
	}
	if err := disabled.Delete(ctx, "k"); !errors.Is(err, biz.ErrObjectStoreDisabled) {
		t.Fatalf("delete disabled = %v, want biz.ErrObjectStoreDisabled", err)
	}
	if _, err := disabled.PresignGet(ctx, "k", time.Minute); !errors.Is(err, biz.ErrObjectStoreDisabled) {
		t.Fatalf("presign disabled = %v, want biz.ErrObjectStoreDisabled", err)
	}
}

// TestOSSClientRequiresResolver 验证构造期 nil 解析器拒绝。
func TestOSSClientRequiresResolver(t *testing.T) {
	if _, _, err := New(nil); err == nil {
		t.Fatal("New(nil) = nil error, want resolver required error")
	}
}
