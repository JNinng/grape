// codec.go 承载自描述密文的注册表与编解码：Registry 在装配期固化材料
// （启动期只读，改配置需重启），Sealer 是写入侧 Codec，Open 是读取侧单
// 参数解密。密文格式与 AAD 构造详见包注释。
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrUnavailable 是加解密失败的唯一哨兵：业务键未登记、密文格式非法、
// base64 非法、长度非法与 GCM 认证失败一律返回它，不区分具体失败点，
// 避免调用方或攻击者按错误差异探测密钥与密文结构。上层按需映射为业务
// 错误（如模型密钥不可用）。
var ErrUnavailable = errors.New("secret: cipher unavailable")

const (
	// nonceLen 是 AES-GCM 的 nonce 长度（字节）。
	nonceLen = 12
	// tagLen 是 AES-GCM 认证标签长度（字节），密文段最短长度即它。
	tagLen = 16
	// segmentSeparator 分隔密文三段。
	segmentSeparator = ":"
	// nameSeparator 分隔基础业务键与版本，构成完整键名「基础名@版本」。
	nameSeparator = "@"
)

// aadSeparator 分隔 AAD 中的完整键名与算法标识。
const aadSeparator = "\x00"

// entry 是完整键名对应的固化材料：已构造的 AEAD 与按键名固定的 AAD。
// AEAD 与 AAD 均为只读，可被并发复用于任意次加解密。
type entry struct {
	aead cipher.AEAD
	aad  []byte
}

// Registry 是「完整键名（基础名@版本）→ 密钥材料」的只读注册表，附各组
// 写入版本指针。装配期由 New 统一校验并固化，运行期无写入口。
type Registry struct {
	entries map[string]*entry
	// writes 是业务键 → 写入版本号。
	writes map[string]string
}

// New 校验登记表并构造只读注册表，任一不满足即返回错误（装配期
// fail-fast）。校验顺序：
//  1. 登记表非空；writers 未引用未登记的业务键；
//  2. 业务键命中白名单，且 write 指针必填、指向本组已登记版本；
//  3. 版本号命中白名单；
//  4. kind 与支持集合精确匹配（不接受家族名或未知变体）；
//  5. 密钥字段按 kind 取用（对称禁 public_key/private_key，非对称禁
//     key），key 为合法 base64 且解码恰为算法要求长度。
//
// versions 的键是基础业务键，值是版本号 → 材料；writers 的键必须与
// versions 的键集合一致（每组都有写入指针，指针组外键视为误配拒绝）。
func New(versions map[string]map[string]Material, writers map[string]string) (*Registry, error) {
	if len(versions) == 0 {
		return nil, errors.New("secret: registry must declare at least one business key")
	}
	for key := range writers {
		if _, ok := versions[key]; !ok {
			return nil, fmt.Errorf("secret: writers references unregistered business key %q", key)
		}
	}
	r := &Registry{
		entries: make(map[string]*entry),
		writes:  make(map[string]string, len(versions)),
	}
	for _, base := range sortedKeys(versions) {
		if !businessKeyPattern.MatchString(base) {
			return nil, fmt.Errorf("secret: business key %q must match %s", base, businessKeyPattern)
		}
		write, ok := writers[base]
		if !ok || write == "" {
			return nil, fmt.Errorf("secret: business key %q requires a write version pointer", base)
		}
		group := versions[base]
		if len(group) == 0 {
			return nil, fmt.Errorf("secret: business key %q must declare at least one version", base)
		}
		if _, ok := group[write]; !ok {
			return nil, fmt.Errorf("secret: business key %q write pointer %q does not reference a declared version", base, write)
		}
		for _, version := range sortedKeys(group) {
			if !versionPattern.MatchString(version) {
				return nil, fmt.Errorf("secret: business key %q version %q must match %s", base, version, versionPattern)
			}
			e, err := buildEntry(base, version, group[version])
			if err != nil {
				return nil, fmt.Errorf("secret: business key %q version %q: %w", base, version, err)
			}
			r.entries[fullName(base, version)] = e
		}
		r.writes[base] = write
	}
	return r, nil
}

// NewRejecting 返回空注册表：任何 Sealer 取用与 Open 都按 ErrUnavailable
// 拒绝。供不涉及密文的兼容装配面使用（未配置密钥时带病运行到首次调用
// 才失败，而非拒绝启动）。
func NewRejecting() *Registry {
	return &Registry{
		entries: make(map[string]*entry),
		writes:  make(map[string]string),
	}
}

// Sealer 按「基础业务键 + 该组 write 指针」构造写入侧 Codec。业务键未
// 登记或 write 指针缺材料返回 ErrUnavailable。业务代码只持有基础名，
// 不感知具体版本。
func (r *Registry) Sealer(businessKey string) (*Sealer, error) {
	version, ok := r.writes[businessKey]
	if !ok {
		return nil, fmt.Errorf("%w: business key %q is not registered", ErrUnavailable, businessKey)
	}
	e, ok := r.entries[fullName(businessKey, version)]
	if !ok {
		return nil, fmt.Errorf("%w: business key %q has no material for its write version", ErrUnavailable, businessKey)
	}
	return &Sealer{base: businessKey, version: version, entry: e}, nil
}

// Sealer 是写入侧 Codec：输出自描述密文，首段固化基础业务键与写入版本。
type Sealer struct {
	base    string
	version string
	entry   *entry
}

// Seal 加密明文，输出「<基础名@版本>:<nonce>:<密文+tag>」，后两段
// base64.RawURLEncoding。每次调用重新生成 12 字节随机 nonce，同一明文
// 产出不同密文。失败统一返回 ErrUnavailable。
func (s *Sealer) Seal(plaintext string) (string, error) {
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return "", ErrUnavailable
	}
	sealed := s.entry.aead.Seal(nil, nonce, []byte(plaintext), s.entry.aad)
	var b strings.Builder
	b.Grow(len(s.base) + 1 + len(s.version) + 2 + 2*base64.RawURLEncoding.EncodedLen(len(nonce)+len(sealed)))
	b.WriteString(s.base)
	b.WriteString(nameSeparator)
	b.WriteString(s.version)
	b.WriteString(segmentSeparator)
	b.WriteString(base64.RawURLEncoding.EncodeToString(nonce))
	b.WriteString(segmentSeparator)
	b.WriteString(base64.RawURLEncoding.EncodeToString(sealed))
	return b.String(), nil
}

// Open 解密密文：按首段「基础名@版本」精确选材料，调用方无需传算法、
// nonce 或密钥。没有版本回退、没有跨组回退；任何失败（未登记、格式
// 非法、base64 非法、长度非法、认证失败）统一返回 ErrUnavailable。
func (r *Registry) Open(ciphertext string) (string, error) {
	parts := strings.Split(ciphertext, segmentSeparator)
	if len(parts) != 3 {
		return "", ErrUnavailable
	}
	name := parts[0]
	at := strings.IndexByte(name, '@')
	if at <= 0 || at == len(name)-1 {
		return "", ErrUnavailable
	}
	e, ok := r.entries[name]
	if !ok {
		return "", ErrUnavailable
	}
	nonce, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(nonce) != nonceLen {
		return "", ErrUnavailable
	}
	sealed, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sealed) < tagLen {
		return "", ErrUnavailable
	}
	plaintext, err := e.aead.Open(nil, nonce, sealed, e.aad)
	if err != nil {
		return "", ErrUnavailable
	}
	return string(plaintext), nil
}

// buildEntry 按 kind 校验材料并构造 AEAD 与 AAD。kind 必须在支持集合内
// （精确匹配）；字段取用与对称性一致；对称密钥须为合法 base64 且长度
// 恰为算法要求。错误信息只含标识与长度，不含密钥材料。
func buildEntry(base, version string, m Material) (*entry, error) {
	spec, ok := kindSpecs[m.Kind]
	if !ok {
		return nil, fmt.Errorf("kind %q is not a supported algorithm", m.Kind)
	}
	if spec.symmetric {
		if m.PublicKey != "" || m.PrivateKey != "" {
			return nil, fmt.Errorf("kind %q is symmetric and must not set public_key or private_key", m.Kind)
		}
		if m.Key == "" {
			return nil, fmt.Errorf("kind %q requires key", m.Kind)
		}
		key, err := base64.StdEncoding.DecodeString(m.Key)
		if err != nil {
			return nil, fmt.Errorf("key must be valid base64")
		}
		if len(key) != spec.keyLen {
			return nil, fmt.Errorf("key must decode to %d bytes, got %d", spec.keyLen, len(key))
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, fmt.Errorf("construct cipher: %w", err)
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, fmt.Errorf("construct AEAD: %w", err)
		}
		return &entry{aead: aead, aad: buildAAD(base, version, m.Kind)}, nil
	}
	// 非对称分支：当前未接入任何非对称算法（kindSpecs 无此类条目），保留
	// 字段取用校验与接入点，保证接入时校验顺序与对称分支同构。
	if m.Key != "" {
		return nil, fmt.Errorf("kind %q is asymmetric and must not set key", m.Kind)
	}
	if m.PublicKey == "" || m.PrivateKey == "" {
		return nil, fmt.Errorf("kind %q requires both public_key and private_key", m.Kind)
	}
	return nil, fmt.Errorf("kind %q is not implemented", m.Kind)
}

// fullName 拼完整键名「基础名@版本」。业务键与版本白名单均不含 @，拼接
// 无歧义。
func fullName(base, version string) string {
	return base + nameSeparator + version
}

// buildAAD 构造 AAD：绑定完整键名（基础名@版本）与算法标识，防止密文
// 跨业务键、跨版本头改写或跨算法挪用。Seal 与 Open 共用本函数，路径
// 单一。
func buildAAD(base, version string, kind Kind) []byte {
	return []byte(fullName(base, version) + aadSeparator + string(kind))
}

// sortedKeys 返回 map 键的确定性排序副本，使校验错误的报告顺序稳定。
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
