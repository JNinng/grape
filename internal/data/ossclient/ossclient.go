// Package ossclient 是 biz.ObjectStore 的 aws-sdk-go-v2 S3 客户端实现：
// 端点事实（provider/endpoint/bucket/AK/SK/region）不在装配期固化，每次
// 调用经 biz.OSSConfigResolver 现场解析 sys_config 的 oss.* 六键（与
// openaicompleter 的「按调用解析 + 客户端缓存」同口径），*s3.Client 按
// 端点与凭据缓存复用。直连构造 s3.Options（BaseEndpoint + 静态凭据 +
// UsePathStyle），不走 LoadDefaultConfig；切 MinIO / 阿里云 OSS / AWS
// 只改 sys_config 配置。测试一律以 httptest 替身服务进行，禁真实网络。
package ossclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"grape/internal/biz"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/google/wire"
)

// ProviderSet 是对象存储 S3 客户端的 Wire 依赖注入集合。
var ProviderSet = wire.NewSet(New)

// noSuchKeyErrorCode 是 S3 对象不存在的协议错误码（gofakes3 与真实
// S3 / MinIO / 阿里云 OSS 兼容层一致）。
const noSuchKeyErrorCode = "NoSuchKey"

// clientKey 是 *s3.Client 的缓存键：端点、区域与凭据共同决定连接与
// 签名行为，任一变化即换新客户端。
type clientKey struct {
	endpoint  string
	region    string
	accessKey string
	secretKey string
}

// Client 是 biz.ObjectStore 的 S3 协议实现。
type Client struct {
	resolver biz.OSSConfigResolver

	mu      sync.Mutex
	clients map[clientKey]*s3.Client
}

var _ biz.ObjectStore = (*Client)(nil)

// New 构造 biz.ObjectStore。无独立设施开关：可用性由配置解析决定
// （oss.* 键不齐时按 biz.ErrObjectStoreDisabled 拒绝，调用方按设施缺失
// 处理）。
func New(resolver biz.OSSConfigResolver) (biz.ObjectStore, func(), error) {
	if resolver == nil {
		return nil, nil, errors.New("ossclient: oss config resolver is required")
	}
	return &Client{resolver: resolver, clients: map[clientKey]*s3.Client{}}, func() {}, nil
}

// Put 覆盖写对象（S3 PutObject 幂等覆盖语义），返回不带引号的 ETag
// （MD5 十六进制）。空白 key 拒绝；body 流式发送，无需预知长度。
func (c *Client) Put(ctx context.Context, key string, body io.Reader, contentType string) (string, error) {
	cfg, client, err := c.clientFor(ctx)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(key) == "" {
		return "", biz.ErrObjectStoreInvalidArgument
	}
	input := &s3.PutObjectInput{
		Bucket: aws.String(cfg.Bucket),
		Key:    aws.String(key),
		Body:   body,
	}
	if contentType != "" {
		input.ContentType = aws.String(contentType)
	}
	out, err := client.PutObject(ctx, input)
	if err != nil {
		return "", fmt.Errorf("ossclient: put object: %w", err)
	}
	return strings.Trim(aws.ToString(out.ETag), `"`), nil
}

// Get 读取对象全量内容；对象不存在映射 biz.ErrObjectNotFound，空白 key
// 拒绝。调用方负责 Close 返回值。
func (c *Client) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	cfg, client, err := c.clientFor(ctx)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(key) == "" {
		return nil, biz.ErrObjectStoreInvalidArgument
	}
	out, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(cfg.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, ossError(err)
	}
	return out.Body, nil
}

// Delete 删除对象：S3 DELETE 语义幂等（不存在的对象不算错误）。
func (c *Client) Delete(ctx context.Context, key string) error {
	cfg, client, err := c.clientFor(ctx)
	if err != nil {
		return err
	}
	if strings.TrimSpace(key) == "" {
		return biz.ErrObjectStoreInvalidArgument
	}
	if _, err := client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(cfg.Bucket),
		Key:    aws.String(key),
	}); err != nil {
		return fmt.Errorf("ossclient: delete object: %w", err)
	}
	return nil
}

// PresignGet 签发下载（GET）预签名 URL：URL 携带 SigV4 查询串签名，
// 持有者可在有效期内无凭据访问；local provider 下指向本服务 /s3/，
// 反向代理须透传 Host 头。
func (c *Client) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	cfg, client, err := c.presignTarget(ctx, key, ttl)
	if err != nil {
		return "", err
	}
	out, err := s3.NewPresignClient(client).PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(cfg.Bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("ossclient: presign get: %w", err)
	}
	return out.URL, nil
}

// PresignPut 签发上传（PUT）预签名 URL，语义同 PresignGet。
func (c *Client) PresignPut(ctx context.Context, key string, ttl time.Duration) (string, error) {
	cfg, client, err := c.presignTarget(ctx, key, ttl)
	if err != nil {
		return "", err
	}
	out, err := s3.NewPresignClient(client).PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(cfg.Bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("ossclient: presign put: %w", err)
	}
	return out.URL, nil
}

// presignTarget 是 Presign 两方法的公共前置：配置解析、入参校验与
// 客户端获取。
func (c *Client) presignTarget(ctx context.Context, key string, ttl time.Duration) (biz.OSSConfig, *s3.Client, error) {
	if strings.TrimSpace(key) == "" {
		return biz.OSSConfig{}, nil, biz.ErrObjectStoreInvalidArgument
	}
	if ttl <= 0 {
		return biz.OSSConfig{}, nil, biz.ErrObjectStoreInvalidArgument
	}
	cfg, client, err := c.clientFor(ctx)
	if err != nil {
		return biz.OSSConfig{}, nil, err
	}
	return cfg, client, nil
}

// clientFor 解析端点配置并取缓存客户端。
func (c *Client) clientFor(ctx context.Context) (biz.OSSConfig, *s3.Client, error) {
	cfg, err := c.resolver.ResolveOSSConfig(ctx)
	if err != nil {
		return biz.OSSConfig{}, nil, err
	}
	key := clientKey{
		endpoint:  cfg.Endpoint,
		region:    cfg.Region,
		accessKey: cfg.AccessKey,
		secretKey: cfg.SecretKey,
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	client, ok := c.clients[key]
	if !ok {
		// 直连构造：端点与凭据全部来自 sys_config，不走
		// LoadDefaultConfig（不读环境/共享配置文件）；path-style 与
		// 内嵌实现的挂载口径一致。
		client = s3.New(s3.Options{
			BaseEndpoint: aws.String(cfg.Endpoint),
			Region:       cfg.Region,
			Credentials:  credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, ""),
			UsePathStyle: true,
		})
		c.clients[key] = client
	}
	return cfg, client, nil
}

// ossError 把 S3 协议错误映射为 biz 语义：对象不存在 →
// biz.ErrObjectNotFound，其余原样上抛（含大小限流的 EntityTooLarge）。
func ossError(err error) error {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) && apiErr.ErrorCode() == noSuchKeyErrorCode {
		return biz.ErrObjectNotFound
	}
	return fmt.Errorf("ossclient: s3 operation: %w", err)
}
