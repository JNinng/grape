// secret_registry.go 是启动配置密钥登记表（conf.proto 的 secret 段）与
// pkg/secret 注册表之间的装配适配层：只做结构转换，不重复实现算法校验。
// 白名单、算法标识、字段取用与密钥长度校验的单一事实源在 pkg/secret，
// 本文件保证错误在装配期冒泡（fail-fast）。
package conf

import (
	"fmt"

	"grape/pkg/secret"
)

// NewSecretRegistry 把 Bootstrap.secret 登记表转换为 pkg/secret 的只读
// 注册表。任一组或版本为空、write 指针缺失或无效、业务键/版本号/算法
// 标识/密钥材料不合法都返回错误，调用方不得带病启动。
func NewSecretRegistry(cfg map[string]*SecretGroup) (*secret.Registry, error) {
	versions := make(map[string]map[string]secret.Material, len(cfg))
	writers := make(map[string]string, len(cfg))
	for key, group := range cfg {
		if group == nil {
			return nil, fmt.Errorf("conf: secret.%s group is empty", key)
		}
		materials := make(map[string]secret.Material, len(group.GetVersions()))
		for version, v := range group.GetVersions() {
			if v == nil {
				return nil, fmt.Errorf("conf: secret.%s.versions.%s is empty", key, version)
			}
			materials[version] = secret.Material{
				Kind:       secret.Kind(v.GetKind()),
				Key:        v.GetKey(),
				PublicKey:  v.GetPublicKey(),
				PrivateKey: v.GetPrivateKey(),
			}
		}
		versions[key] = materials
		writers[key] = group.GetWrite()
	}
	registry, err := secret.New(versions, writers)
	if err != nil {
		return nil, fmt.Errorf("conf: secret registry: %w", err)
	}
	return registry, nil
}
