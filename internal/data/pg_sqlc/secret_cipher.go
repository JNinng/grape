package pg_sqlc

import (
	"fmt"

	"grape/internal/biz"
	"grape/pkg/secret"
)

// ModelSecretBusinessKey 是模型密钥（sys_model.api_key_cipher）加解密
// 消费的基础业务键：写入版本由登记表该组的 write 指针决定，解密按密文
// 首段「基础名@版本」自动选材料。
const ModelSecretBusinessKey = "module_key_encrypt"

// secretCipher 是 biz.SecretCipher 的 Codec 适配：写入侧按 write 指针
// 选版本，读取侧交由 pkg/secret.Registry 单参数解密。业务键未登记或
// 注册表缺失时 available=false，Seal/Open 统一按
// biz.ErrModelSecretUnavailable 拒绝（未配置密钥不阻塞启动，使用密钥时
// 才报错）。
type secretCipher struct {
	registry  *secret.Registry
	sealer    *secret.Sealer
	available bool
}

var _ biz.SecretCipher = (*secretCipher)(nil)

// NewSecretCipher 由装配期密钥注册表构造模型密钥 Codec。注册表为 nil 或
// 该业务键未登记（Sealer 返回错误）时返回「可用但拒绝加解密」的实例，
// 不把装配错误带出——登记表本身的校验在 conf.NewSecretRegistry 已
// fail-fast，这里只决定该业务键能否加解密。
func NewSecretCipher(reg *secret.Registry) biz.SecretCipher {
	if reg == nil {
		return &secretCipher{}
	}
	sealer, err := reg.Sealer(ModelSecretBusinessKey)
	if err != nil {
		return &secretCipher{}
	}
	return &secretCipher{registry: reg, sealer: sealer, available: true}
}

// Seal 加密明文，输出自描述密文「module_key_encrypt@<版本>:<nonce>:<密文+
// tag>」。失败统一映射为 biz.ErrModelSecretUnavailable，不含密钥材料。
func (c *secretCipher) Seal(plaintext string) (string, error) {
	if !c.available {
		return "", fmt.Errorf("%w: model secret business key is not configured", biz.ErrModelSecretUnavailable)
	}
	ciphertext, err := c.sealer.Seal(plaintext)
	if err != nil {
		return "", fmt.Errorf("%w: seal model secret", biz.ErrModelSecretUnavailable)
	}
	return ciphertext, nil
}

// Open 解密密文：按首段业务键与版本自动选材料，任何失败（未登记、格式
// 非法、认证失败）统一映射为 biz.ErrModelSecretUnavailable，不泄露具体
// 失败点。
func (c *secretCipher) Open(ciphertext string) (string, error) {
	if !c.available {
		return "", fmt.Errorf("%w: model secret business key is not configured", biz.ErrModelSecretUnavailable)
	}
	plaintext, err := c.registry.Open(ciphertext)
	if err != nil {
		return "", fmt.Errorf("%w: open model secret", biz.ErrModelSecretUnavailable)
	}
	return plaintext, nil
}
