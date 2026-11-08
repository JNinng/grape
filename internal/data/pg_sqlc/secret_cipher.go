package pg_sqlc

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"grape/internal/biz"
)

// 模型密钥主环境变量：base64 解码后必须 32 字节（AES-256）。未设置时
// 返回可用但拒绝加解密的实现——未配置密钥的部署仍可运行不含密文的模型，
// 一旦使用密文模型即按 ErrModelSecretUnavailable 拒绝。
const (
	ModelSecretKeyEnv     = "GRAPE_MODEL_SECRET_KEY"
	secretCipherPrefix    = "v1:"
	secretNonceLen        = 12
	secretKeyLen          = 32
)

// secretCipher 是 AES-256-GCM 的 biz.SecretCipher 实现。密文格式为
// "v1:" + base64(nonce||ciphertext)，nonce 12 字节随机；每次 Seal 都换
// nonce，同一明文产出不同密文。本期不做密钥轮换，更换主密钥需离线重加密。
type secretCipher struct {
	key []byte // nil 表示主密钥未配置（Seal/Open 拒绝）
}

var _ biz.SecretCipher = (*secretCipher)(nil)

// NewSecretCipher 从环境变量构造密钥设施。未设置时返回拒绝式实现；
// 已设置但不是 base64 或不满足 32 字节时装配期失败（fail-fast，避免
// 带病运行到首次调用才发现密钥不可用）。
func NewSecretCipher() (biz.SecretCipher, func(), error) {
	raw := strings.TrimSpace(os.Getenv(ModelSecretKeyEnv))
	if raw == "" {
		return &secretCipher{}, func() {}, nil
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("pg_sqlc: decode %s: %w", ModelSecretKeyEnv, err)
	}
	if len(key) != secretKeyLen {
		return nil, nil, fmt.Errorf("pg_sqlc: %s must decode to %d bytes, got %d", ModelSecretKeyEnv, secretKeyLen, len(key))
	}
	return &secretCipher{key: key}, func() {}, nil
}

// Seal 加密明文：未配置主密钥时拒绝；输出 "v1:" 前缀的 base64 密文。
func (c *secretCipher) Seal(plaintext string) (string, error) {
	gcm, err := c.gcm()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, secretNonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("pg_sqlc: generate secret nonce: %w", err)
	}
	sealed := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	payload := make([]byte, 0, len(nonce)+len(sealed))
	payload = append(payload, nonce...)
	payload = append(payload, sealed...)
	return secretCipherPrefix + base64.StdEncoding.EncodeToString(payload), nil
}

// Open 解密密文：前缀、base64、长度与 GCM 认证任一不符都映射为
// ErrModelSecretUnavailable（不泄露具体失败点）。
func (c *secretCipher) Open(ciphertext string) (string, error) {
	gcm, err := c.gcm()
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(ciphertext, secretCipherPrefix) {
		return "", fmt.Errorf("%w: unsupported secret ciphertext format", biz.ErrModelSecretUnavailable)
	}
	payload, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(ciphertext, secretCipherPrefix))
	if err != nil {
		return "", fmt.Errorf("%w: decode secret ciphertext: %v", biz.ErrModelSecretUnavailable, err)
	}
	if len(payload) < secretNonceLen+gcm.Overhead() {
		return "", fmt.Errorf("%w: secret ciphertext is too short", biz.ErrModelSecretUnavailable)
	}
	nonce, sealed := payload[:secretNonceLen], payload[secretNonceLen:]
	plaintext, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("%w: decrypt secret ciphertext", biz.ErrModelSecretUnavailable)
	}
	return string(plaintext), nil
}

// gcm 按需构造 AES-GCM；主密钥缺失统一报 ErrModelSecretUnavailable。
func (c *secretCipher) gcm() (cipher.AEAD, error) {
	if len(c.key) == 0 {
		return nil, fmt.Errorf("%w: %s is not configured", biz.ErrModelSecretUnavailable, ModelSecretKeyEnv)
	}
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", biz.ErrModelSecretUnavailable, err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", biz.ErrModelSecretUnavailable, err)
	}
	return gcm, nil
}
