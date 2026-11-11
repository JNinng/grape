package biz

import (
	"context"
	"errors"
	"io"
	"time"
)

// 对象存储设施的错误语义：
//   - ErrObjectStoreDisabled：设施未配置（sys_config 的 oss.provider /
//     oss.endpoint / oss.bucket 任一缺失或停用）——按能力缺失拒绝，调用
//     方按设施缺失处理而非系统故障（与 ErrEmbedderDisabled 同语义）；
//   - ErrObjectNotFound：目标对象不存在（含已删除），不区分「从未写入」
//     与「写入后删除」；
//   - ErrObjectInvalidArgument：入参非法（空白对象键、非正有效期等）。
var (
	ErrObjectStoreDisabled        = errors.New("biz: object store facility disabled")
	ErrObjectNotFound             = errors.New("biz: object not found")
	ErrObjectStoreInvalidArgument = errors.New("biz: object store invalid argument")
)

// 对象存储设施的配置键（sys_config 的 config_key，value_type=json，值为
// JSON 字面量）。全局事实，无租户维度；写入经受控写函数（平台根租户）。
// access_key / secret_key 明文落库，与 jwt.secret 同口径（设施面文档化
// 例外，见 permission.md；操作审计快照按键形态脱敏）。
const (
	ConfigKeyOSSProvider  = "oss.provider"
	ConfigKeyOSSEndpoint  = "oss.endpoint"
	ConfigKeyOSSBucket    = "oss.bucket"
	ConfigKeyOSSAccessKey = "oss.access_key"
	ConfigKeyOSSSecretKey = "oss.secret_key"
	ConfigKeyOSSRegion    = "oss.region"
)

// provider 取值词表（oss.provider 配置键）：local 为服务内嵌实现
// （gofakes3 挂载 /s3/），其余取值走外部 S3 兼容端点。业务侧只认 S3
// 协议，切换只改配置不改代码。
const (
	OSSProviderLocal  = "local"
	OSSProviderMinIO  = "minio"
	OSSProviderS3     = "s3"
	OSSProviderAliyun = "aliyun"
)

// OSSConfig 是解析后的对象存储端点事实（sys_config oss.* 六键）。Endpoint
// 是 S3 兼容端点根地址（业务客户端的 BaseEndpoint，不含尾斜杠；local 指
// 本服务 /s3/）；Region 为 SigV4 签名区域（内嵌实现按请求 scope 接受任意
// 取值）；AccessKey/SecretKey 为已解密明文。
type OSSConfig struct {
	Provider  string
	Endpoint  string
	Bucket    string
	AccessKey string
	SecretKey string
	Region    string
}

// OSSConfigResolver 解析对象存储端点事实。实现在 data 层：每次调用现场
// 读取 sys_config（无缓存行值，配置变更下一次调用即生效），身份缺失
// fail-closed（从认证 ctx 提取）；provider/endpoint/bucket 缺失或停用
// 返回 ErrObjectStoreDisabled，不静默回落默认端点。
type OSSConfigResolver interface {
	ResolveOSSConfig(ctx context.Context) (OSSConfig, error)
}

// ObjectStore 是对象存储设施的框架无关契约：标准 S3 语义（键值存取 +
// 预签名 URL），实现由 data 层按依赖反转提供（aws-sdk-go-v2 S3 客户端，
// 端点经 OSSConfigResolver 按配置解析），biz 不感知 SDK 与端点配置。
//
// 对象大小上限（20MB）由设施端强制（内嵌实现按 Content-Length 限流），
// 超限以设施错误上抛；本期只开简单上传，multipart 由前端直传大文件时
// 经 PresignPut 走设施端内存组装（同样受限流约束）。
type ObjectStore interface {
	// Put 覆盖写对象：同键幂等覆盖（S3 PutObject 语义），返回新内容
	// 的 ETag（MD5 十六进制，不带引号）。contentType 为声明给设施的
	// Content-Type（空串表示未声明，设施端不推断）；空白 key 拒绝。
	// body 由实现读取至 EOF，调用方不需预知长度。
	Put(ctx context.Context, key string, body io.Reader, contentType string) (etag string, err error)
	// Get 读取对象全量内容；调用方负责 Close 返回值。对象不存在返回
	// ErrObjectNotFound，空白 key 拒绝。
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	// Delete 删除对象：幂等（删除不存在的对象不算错误，S3 DELETE 语义）。
	Delete(ctx context.Context, key string) error
	// PresignGet 签发下载（GET）预签名 URL：持有 URL 即可在有效期内
	// 无凭据访问该对象，URL 形态由设施端点决定（local 为本服务 /s3/），
	// 反向代理须透传 Host 头。ttl 非正拒绝。
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
	// PresignPut 签发上传（PUT）预签名 URL，语义同 PresignGet。
	PresignPut(ctx context.Context, key string, ttl time.Duration) (string, error)
}
