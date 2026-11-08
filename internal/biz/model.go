package biz

import (
	"context"
	"errors"
	"time"
)

// 模型目录与密钥设施的错误语义：
//   - ErrTextModelUnavailable：选中的文本模型不可用（停用、软删、键未知，
//     或既不在 sys_model 也不在 conf）——fail-closed，不静默回落默认模型；
//   - ErrEmbeddingModelUnavailable：选中的嵌入模型不可用，口径同上；
//   - ErrModelSecretUnavailable：模型配置了密钥但主密钥缺失/密文损坏，
//     无法解密——同样 fail-closed。
var (
	ErrTextModelUnavailable      = errors.New("biz: text model unavailable")
	ErrEmbeddingModelUnavailable = errors.New("biz: embedding model unavailable")
	ErrModelSecretUnavailable    = errors.New("biz: model secret unavailable")
)

// 模型目录的配置键（sys_config / sys_tenant_config 的 config_key）与类别
// 枚举。全局默认配置与租户覆盖共用同名键；旧键 vector.embedder 作为
// ai.embedding.model 的别名保留兼容。
const (
	ConfigKeyTextModel      = "ai.text.model"
	ConfigKeyEmbeddingModel = "ai.embedding.model"
	ConfigKeyEmbedderLegacy = "vector.embedder"

	ModelKindText      = "text"
	ModelKindEmbedding = "embedding"

	// ModelEmbeddingDimension 是本期唯一允许的嵌入维度，与
	// agent_vector.sql 的唯一 HNSW 索引对齐。放宽维度必须同时改此处、
	// biz 校验与 DDL 索引。
	ModelEmbeddingDimension = 1024
)

// TextModelSpec 是解析后的文本模型端点事实：provider/base_url/model/api_key
// /timeout 可来自 sys_model 租户行、平台行或 conf.agent.llm；可靠性
// 策略（failover/hedge/logging）不在其中，由 agent 层从全局 conf 复用。
type TextModelSpec struct {
	Key      string
	Provider string
	BaseURL  string
	Model    string
	APIKey   string
	Timeout  time.Duration
}

// TextModelCatalog 解析当前身份的文本模型。实现从认证 ctx 取身份，按
// 「租户配置 > 全局配置 > conf」选键，再按「租户行 > 平台行 > conf」
// 找端点；停用、软删与未知键报错，不静默回落。
type TextModelCatalog interface {
	ResolveTextModel(ctx context.Context) (TextModelSpec, error)
}

// SecretCipher 是模型密钥的加解密窄接口：Seal 产生可落库的密文，
// Open 还原明文。实现必须对同一明文产出不同的密文（随机 nonce），
// 且对损坏/篡改密文返回可识别错误（ErrModelSecretUnavailable）。
type SecretCipher interface {
	Seal(plaintext string) (string, error)
	Open(ciphertext string) (string, error)
}
