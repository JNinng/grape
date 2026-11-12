// Package secret 提供与 internal/ 无关的自描述密文加解密设施：装配层把
// 密钥材料固化进只读 Registry，业务方按基础业务键取写入侧 Sealer、按密文
// 首段自动解密，密钥材料不随密文传播。
//
// 密文格式为「<基础名@版本>:<nonce>:<密文+tag>」，后两段为
// base64.RawURLEncoding；nonce 12 字节 crypto/rand 随机，tag 16 字节由
// AES-GCM 产出。同一明文每次加密产出不同密文。
//
// 轮换在业务键组内按版本进行：登记新版本材料 → 组内 write 指针切到新
// 版本并重启 → 存量重加密 → 确认无旧版本密文残留后下线旧版本条目。旧密文
// 首段带旧版本、仍指向旧版本材料；复用版本号换材料会导致旧密文不可解，
// 必须用新版本号。指针切换后的滚动窗口内新旧实例各写各版本，因密文
// 自描述，双方均可解。
//
// 算法标识使用完整算法名（如 AES-256-GCM），不接受 AES 这类家族名；当前
// 支持集合见 SupportedKinds。加解密失败统一返回 ErrUnavailable，不区分
// 业务键未登记、格式非法与认证失败，避免按错误差异探测密钥与密文结构。
package secret

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"regexp"
	"sort"
)

// Kind 是完整算法标识（如 AES-256-GCM），是登记表 kind 字段与支持集合
// 的唯一取值类型。家族名（AES、RSA）与未知变体不在支持集合内。
type Kind string

const (
	// KindAES256GCM 是 AES-256-GCM：对称认证加密，key 为 base64 编码的
	// 32 字节主密钥。
	KindAES256GCM Kind = "AES-256-GCM"
)

// Material 是单一版本条目的密钥材料。字段取用由 Kind 决定：对称算法只
// 用 Key，非对称算法只用 PublicKey / PrivateKey，另一种组合一律拒绝。
//
// 各字段均为单行 base64，编码对象由 Kind 钉死：对称算法的 Key 编原始密钥
// 字节（当前唯一支持的 AES-256-GCM 为 32 字节）；非对称算法固定长度时编
// 原始字节（如 Ed25519 公私钥各 32 字节），结构化算法（如 RSA / EC）私钥
// 编 DER、公钥编 SPKI DER（等价于 PEM 去头尾）。多行 PEM 不进登记表。
type Material struct {
	Kind       Kind
	Key        string
	PublicKey  string
	PrivateKey string
}

// kindSpec 是算法元数据：决定密钥字段取用与生成口径。
type kindSpec struct {
	// symmetric 为真表示对称算法，取用 Key 且禁止 PublicKey / PrivateKey。
	symmetric bool
	// keyLen 是对称密钥解码后的字节长度。
	keyLen int
}

// kindSpecs 是支持集合单一事实源：登记表校验、CLI 算法清单与随机密钥
// 生成共用。新增算法只在此登记。
var kindSpecs = map[Kind]kindSpec{
	KindAES256GCM: {symmetric: true, keyLen: 32},
}

// SupportedKinds 返回确定性排序的完整算法标识清单，供登记表校验与运维
// 命令共用，与 kindSpecs 同源。
func SupportedKinds() []Kind {
	kinds := make([]Kind, 0, len(kindSpecs))
	for k := range kindSpecs {
		kinds = append(kinds, k)
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })
	return kinds
}

// Generate 用 crypto/rand 生成该算法的密钥材料，产物可通过 Registry 装配
// 校验。当前仅对称算法（AES-256-GCM：Key = base64(32 随机字节)）；未知
// 算法或尚未接入生成口径的非对称算法返回错误。
func Generate(kind Kind) (Material, error) {
	spec, ok := kindSpecs[kind]
	if !ok {
		return Material{}, fmt.Errorf("secret: kind %q is not a supported algorithm", kind)
	}
	if !spec.symmetric {
		// 非对称算法接入时在此按 kind 生成密钥对（单行 base64，编码口径
		// 与登记表一致）；当前没有登记非对称算法，保持显式拒绝。
		return Material{}, fmt.Errorf("secret: kind %q has no key generation implementation", kind)
	}
	key := make([]byte, spec.keyLen)
	if _, err := rand.Read(key); err != nil {
		return Material{}, fmt.Errorf("secret: generate key material: %w", err)
	}
	return Material{Kind: kind, Key: base64.StdEncoding.EncodeToString(key)}, nil
}

var (
	// businessKeyPattern 是基础业务键白名单：小写字母开头，仅含小写字母、
	// 数字与下划线，64 字符内。业务键与版本共同派生环境变量名
	// （KRATOS_<业务键大写>_<版本大写>），首字符限定字母保证变量名合法。
	businessKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	// versionPattern 是版本号白名单：字母、数字与下划线，32 字符内
	// （如 v1、v2）。
	versionPattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,32}$`)
)
