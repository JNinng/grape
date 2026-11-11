package oss

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	nethttp "net/http"
	"strings"

	"grape/internal/biz"
	"grape/internal/conf"
	pgsqlc "grape/internal/data/pg_sqlc"
	"grape/internal/data/pg_sqlc/gen"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rclone/gofakes3"
	"github.com/rclone/gofakes3/signature"
)

// basePath 是 S3 API 的挂载前缀（固定，不入 conf）：/s3/ 前缀经
// srv.HandlePrefix 链外直挂，保留前缀由 server.ReservedAPIPrefixes 声明。
const basePath = "/s3"

// bootstrapKeys 是装配期需要读取的 sys_config oss.* 键：provider 决定
// 挂载，其余五键在 provider=local 时一并 fail-fast（enabled=true 而
// 配置不齐拒绝启动，见 conf.proto OSS 注释）。
var bootstrapKeys = []string{
	biz.ConfigKeyOSSProvider,
	biz.ConfigKeyOSSEndpoint,
	biz.ConfigKeyOSSBucket,
	biz.ConfigKeyOSSAccessKey,
	biz.ConfigKeyOSSSecretKey,
	biz.ConfigKeyOSSRegion,
}

// S3Handler 是内嵌 S3 服务原始 Handler 的注入类型。ops 的 metrics 出口
// 以裸 nethttp.Handler 注入 NewHTTPServer，本命名类型避免 wire 按参数
// 类型装配时二义（与 server.WebHandler 同一手法，类型定义在供给侧）。
type S3Handler nethttp.Handler

// NewHandler 构造内嵌 S3 服务的挂载 Handler。装配期系统面以池直连读取
// sys_config（无认证身份；sys_config 无 RLS，Pool() 的既定用途）：
//   - conf.oss.enabled=false → 返回 nil（不挂路由、不读配置）；
//   - provider 缺失 → 错误（enabled=true 即要求显式 provider）；
//   - provider 非 local → 返回 nil（对象存储走外部端点，本服务不出 S3 API）；
//   - provider=local 而 oss.* 配置不齐 → 错误（fail-closed）。
//
// 返回的 Handler 在 /s3/ 前缀上先做 SigV4 校验（签名覆盖含前缀的原始
// 路径），通过后剥前缀交给内层 gofakes3 路由（path-style）。内层服务器
// 不再注册鉴权键（gofakes3 的鉴权中间件以路径原文参与签名，前缀剥离后
// 会误判），外层校验是唯一鉴权点。
func NewHandler(c *conf.OSS, d *pgsqlc.Data) (S3Handler, func(), error) {
	if !c.GetEnabled() {
		return nil, func() {}, nil
	}
	pool := d.Pool()
	if pool == nil {
		return nil, nil, errors.New("oss: facility enabled but the data pool is unavailable")
	}
	boot, err := loadBootstrap(context.Background(), pool)
	if err != nil {
		return nil, nil, err
	}
	if boot[biz.ConfigKeyOSSProvider] != biz.OSSProviderLocal {
		// provider 非 local：对象存储走外部端点，本服务不挂 /s3/。
		return nil, func() {}, nil
	}
	for _, key := range bootstrapKeys {
		if boot[key] == "" {
			return nil, nil, fmt.Errorf("oss: facility enabled with local provider but sys_config key %s is missing, disabled or empty", key)
		}
	}
	authPair := map[string]string{
		boot[biz.ConfigKeyOSSAccessKey]: boot[biz.ConfigKeyOSSSecretKey],
	}
	// Server() 返回包好中间件链（时序偏移、请求 ID、CORS）的 http.Handler；
	// 不传 WithV4Auth：内层鉴权以路径原文参与签名，前缀剥离后会误判，
	// 外层 signedPrefixHandler 是唯一鉴权点。
	inner := gofakes3.New(NewBackend(pool), gofakes3.WithAutoBucket(true)).Server()
	return newSignedPrefixHandler(basePath, authPair, inner), func() {}, nil
}

// loadBootstrap 池直连读取 sys_config 的 oss.* 键（启用值；value_type=json
// 的 JSON 字符串字面量）。provider 键缺失按错误返回；其余键缺失记空串，
// 由调用方按挂载决策判定。
func loadBootstrap(ctx context.Context, pool *pgxpool.Pool) (map[string]string, error) {
	queries := gen.New(pool)
	values := make(map[string]string, len(bootstrapKeys))
	for _, key := range bootstrapKeys {
		v, err := queries.GetSysConfigValueByKey(ctx, key)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("oss: read sys_config key %s: %w", key, err)
		}
		if v == nil {
			continue
		}
		decoded, err := decodeJSONString(*v)
		if err != nil {
			return nil, fmt.Errorf("oss: sys_config key %s has an invalid json value: %w", key, err)
		}
		values[key] = strings.TrimSpace(decoded)
	}
	if values[biz.ConfigKeyOSSProvider] == "" {
		return nil, errors.New("oss: facility enabled but sys_config key oss.provider is missing, disabled or empty")
	}
	return values, nil
}

// signedPrefixHandler 是 /s3/ 前缀的鉴权与路由适配：先以含前缀的原始
// 路径做 SigV4 校验（与 SDK/预签名 URL 的签名口径一致），通过后剥前缀
// 交给内层 gofakes3（其路由按 /bucket/key 解析）。HEAD 等方法照常参与
// 签名；匿名、错键、篡改签名与时钟偏移超限一律 403。
type signedPrefixHandler struct {
	prefix string
	keys   map[string]string
	inner  nethttp.Handler
}

// newSignedPrefixHandler 构造前缀鉴权适配 Handler。
func newSignedPrefixHandler(prefix string, keys map[string]string, inner nethttp.Handler) S3Handler {
	return S3Handler(&signedPrefixHandler{prefix: prefix, keys: keys, inner: inner})
}

// ServeHTTP 实现前缀鉴权与路由：错误应答形态与 gofakes3 内建鉴权中间件
// 一致（XML 错误体 + x-amz-request-id）。
func (h *signedPrefixHandler) ServeHTTP(w nethttp.ResponseWriter, r *nethttp.Request) {
	if code := signature.V4SignVerifyWithLookup(r, h.lookup); code != signature.ErrNone {
		resp := signature.GetAPIError(code)
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(resp.HTTPStatusCode)
		_, _ = w.Write(signature.EncodeAPIErrorToResponseWithRequestID(resp, w.Header().Get("x-amz-request-id")))
		return
	}
	// 剥挂载前缀：Path 为解码形态、RawPath 为转义形态（仅含转义字符时
	// 非空），前缀本身不含转义字符，两侧同步剥离后 RawPath 仍与 Path
	// 一致或为空。
	r.URL.Path = strings.TrimPrefix(r.URL.Path, h.prefix)
	if r.URL.RawPath != "" {
		r.URL.RawPath = strings.TrimPrefix(r.URL.RawPath, h.prefix)
	}
	h.inner.ServeHTTP(w, r)
}

// lookup 以 AK 找 SK（内层服务器不再持有鉴权键，这里是唯一鉴权点）。
func (h *signedPrefixHandler) lookup(accessKey string) (string, bool) {
	secret, ok := h.keys[accessKey]
	return secret, ok
}

// decodeJSONString 解码 value_type=json 的 JSON 字符串字面量。
func decodeJSONString(raw string) (string, error) {
	var decoded string
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return "", err
	}
	return decoded, nil
}
