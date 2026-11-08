// Package conf 承载启动配置的生成结构与手写辅助。vector_spaces.go 解析
// 并校验向量嵌入空间（conf.proto 的 Vector 段之上）：键派生、键→维度
// 注册表与全局默认键校验。openaiembedder 与 pg_sqlc 向量仓储共用本文件，
// 保证键派生与维度纪律只有一处解析源。
package conf

import (
	"fmt"
	"regexp"
)

const (
	// VectorMaxDimension 是 pgvector 单列维度的存储上限（类型系统硬上限）。
	// HNSW 索引另有 2000 维的 vector ops 上限，更高维度需 halfvec 索引
	// 变体（见 sqlc/agent_vector.sql 注释），本常量只约束存储面。
	VectorMaxDimension = 16000
)

// vectorSpaceKeyPattern 是空间键白名单：字母或数字开头，仅含字母数字与
// . _ @ -，128 字符内（与 agent_document.embedder 列宽一致）。键始终走
// 占位符参数、不拼 SQL 文本，白名单的目的是把配置手误挡在装配期。
var vectorSpaceKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@-]{0,127}$`)

// EmbedderSpaceKey 派生嵌入空间键：version 留空取 model，非空拼
// model@version。模型与版本是身份的组成部分——同一模型换维度或换端点
// 必须换版本号得到新键，禁止在原键下变更语义（存量向量不可跨语义复用）。
func EmbedderSpaceKey(model, version string) string {
	if version == "" {
		return model
	}
	return model + "@" + version
}

// VectorSpaces 解析并校验向量空间注册表，返回键→维度映射与全局默认键。
// 校验：embedders 至少一条且 model 非空、键命中白名单且唯一、
// dimension ∈ (0, VectorMaxDimension]、default_embedder 引用已声明键。
// 失败即返回错误（装配期 fail-fast），调用方不得带病启动。
func VectorSpaces(v *Vector) (map[string]int32, string, error) {
	if len(v.GetEmbedders()) == 0 {
		return nil, "", fmt.Errorf("conf: vector.embedders must declare at least one embedding space")
	}
	spaces := make(map[string]int32, len(v.GetEmbedders()))
	for i, e := range v.GetEmbedders() {
		if e.GetModel() == "" {
			return nil, "", fmt.Errorf("conf: vector.embedders[%d].model is required", i)
		}
		key := EmbedderSpaceKey(e.GetModel(), e.GetVersion())
		if !vectorSpaceKeyPattern.MatchString(key) {
			return nil, "", fmt.Errorf("conf: vector.embedders[%d] derives key %q which must match %s", i, key, vectorSpaceKeyPattern)
		}
		if e.GetDimension() <= 0 || e.GetDimension() > VectorMaxDimension {
			return nil, "", fmt.Errorf("conf: vector.embedders[%d] (%s) dimension must be in (0, %d], got %d",
				i, key, VectorMaxDimension, e.GetDimension())
		}
		if _, dup := spaces[key]; dup {
			return nil, "", fmt.Errorf("conf: vector.embedders[%d] derives duplicate key %s", i, key)
		}
		spaces[key] = e.GetDimension()
	}
	def := v.GetDefaultEmbedder()
	if def == "" {
		return nil, "", fmt.Errorf("conf: vector.default_embedder is required and must reference a declared embedder key")
	}
	if _, ok := spaces[def]; !ok {
		return nil, "", fmt.Errorf("conf: vector.default_embedder %q does not reference a declared embedder key", def)
	}
	return spaces, def, nil
}
