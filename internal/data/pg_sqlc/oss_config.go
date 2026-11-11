package pg_sqlc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"grape/internal/biz"

	"github.com/jackc/pgx/v5"
)

// biz.OSSConfigResolver 的 PostgreSQL 实现：按 sys_config 的 oss.* 六键
// 解析对象存储端点事实。全局事实无租户维度，但读取仍走 IdentityTx（业务
// 读写必须经事务门面，见 data/README.md）；每次调用现场读取、无行值缓存，
// 配置变更下一次调用即生效。任一键缺失或停用按 biz.ErrObjectStoreDisabled
// 拒绝（fail-closed，不静默回落默认端点）；值非合法 JSON 字符串视为配置
// 损坏，原样上抛。AK/SK 明文口径见 permission.md。

// ossConfigKeys 是 ObjectStore 可用所需的全部配置键（provider /
// endpoint / bucket / access_key / secret_key / region）：SigV4 客户端
// 与内嵌服务端都依赖完整六键，缺一即按设施未配置拒绝。
var ossConfigKeys = []string{
	biz.ConfigKeyOSSProvider,
	biz.ConfigKeyOSSEndpoint,
	biz.ConfigKeyOSSBucket,
	biz.ConfigKeyOSSAccessKey,
	biz.ConfigKeyOSSSecretKey,
	biz.ConfigKeyOSSRegion,
}

// ossConfigResolver 是 biz.OSSConfigResolver 的实现。
type ossConfigResolver struct {
	data *Data
}

var _ biz.OSSConfigResolver = (*ossConfigResolver)(nil)

// NewOSSConfigResolver 构造对象存储端点解析器。
func NewOSSConfigResolver(d *Data) (*ossConfigResolver, func(), error) {
	if d == nil {
		return nil, nil, errors.New("pg_sqlc: oss config resolver requires data")
	}
	return &ossConfigResolver{data: d}, func() {}, nil
}

// ResolveOSSConfig 解析 sys_config 的 oss.* 六键。身份缺失 fail-closed
// （resolverIdentity 与模型目录解析共用）；键缺失/停用/值为空统一按
// biz.ErrObjectStoreDisabled 拒绝。
func (r *ossConfigResolver) ResolveOSSConfig(ctx context.Context) (biz.OSSConfig, error) {
	auth, err := resolverIdentity(ctx)
	if err != nil {
		return biz.OSSConfig{}, err
	}
	values := make(map[string]string, len(ossConfigKeys))
	err = r.data.IdentityTx(ctx, auth, func(s *IdentityTx) error {
		for _, key := range ossConfigKeys {
			v, err := s.Q().GetSysConfigValueByKey(ctx, key)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if v == nil {
				continue
			}
			decoded, err := ossConfigJSONValue(*v)
			if err != nil {
				return fmt.Errorf("pg_sqlc: resolve oss config %s: %w", key, err)
			}
			if decoded != "" {
				values[key] = decoded
			}
		}
		return nil
	})
	if err != nil {
		return biz.OSSConfig{}, fmt.Errorf("pg_sqlc: resolve oss config: %w", err)
	}
	for _, key := range ossConfigKeys {
		if values[key] == "" {
			return biz.OSSConfig{}, fmt.Errorf("%w: sys_config key %s is missing, disabled or empty", biz.ErrObjectStoreDisabled, key)
		}
	}
	return biz.OSSConfig{
		Provider:  values[biz.ConfigKeyOSSProvider],
		Endpoint:  strings.TrimRight(values[biz.ConfigKeyOSSEndpoint], "/"),
		Bucket:    values[biz.ConfigKeyOSSBucket],
		AccessKey: values[biz.ConfigKeyOSSAccessKey],
		SecretKey: values[biz.ConfigKeyOSSSecretKey],
		Region:    values[biz.ConfigKeyOSSRegion],
	}, nil
}

// ossConfigJSONValue 解码 value_type=json 的配置值：约定值为 JSON 字符串
// 字面量（如 `"local"`），解码后去首尾空白；空串按未配置处理。
func ossConfigJSONValue(raw string) (string, error) {
	var decoded string
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return "", fmt.Errorf("value is not a JSON string: %w", err)
	}
	return strings.TrimSpace(decoded), nil
}
