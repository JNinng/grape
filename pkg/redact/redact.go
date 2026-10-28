// Package redact 提供按类别登记的确定性脱敏器注册表，是日志、审计表与
// 复现视图共用的脱敏单一代码点。本包不依赖
// internal/ 与任何第三方库；消费方按类别登记与取用，不感知具体算法。
//
// 确定性契约：同一 Redactor 对同一输入恒产生同一输出——固定掩码与截断
// 天然满足，哈希形态以注入的盐做 keyed 哈希（同盐同输出，跨进程可复算），
// 键名过滤只做结构改写不改值语义。禁止在本包引入随机、时间等不确定源。
//
// 消费面（只留接口，不在此实现）：业务审计表的参数/结果摘要落库前按
// tool_param / retrieval 类别脱敏（biz 审计用例）；检索留痕的查询文本与
// 过滤摘要按 retrieval 类别脱敏（data 层留痕装饰器）；复现视图回放同一
// 摘要，不二次脱敏。标准注册表另供 internal/data/pg_sqlc 的 debug SQL
// tracer 脱敏 token/password。
package redact

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"unicode/utf8"
)

// 注册表的标准类别。类别是登记键而非类型：消费方只认字符串常量，
// 实现可整体替换（如 token 从掩码换 keyed 哈希）而不动调用点。
const (
	// CategoryPassword 口令与口令派生物（如 bcrypt/argon2 哈希）。
	CategoryPassword = "password"
	// CategoryToken 令牌 / bearer 凭据及其哈希形态。
	CategoryToken = "token"
	// CategoryToolParam 工具调用参数中的敏感键（键名清单由装配方配置）。
	CategoryToolParam = "tool_param"
	// CategoryRetrieval 检索内容（截断 + 可配置保留长度）。
	CategoryRetrieval = "retrieval"
)

// Redactor 是脱敏器的统一形态：输入原值，输出脱敏值。实现必须确定、
// 纯函数（不读全局态），且对任意输入（含空串、超长、非法 UTF-8 之外的
// 任意字节序列）安全返回，不得 panic。
type Redactor interface {
	Redact(value string) string
}

// NewMask 返回固定掩码脱敏器：任何非空输入一律替换为 replacement。
// replacement 可为空串（值抹除）；口令类推荐不可逆掩码，无需保形。
func NewMask(replacement string) Redactor {
	return maskRedactor{replacement: replacement}
}

type maskRedactor struct {
	replacement string
}

func (m maskRedactor) Redact(value string) string {
	if value == "" {
		return ""
	}
	return m.replacement
}

// NewHash 返回 keyed 哈希脱敏器：输出 "sha256:" + sha256(salt + 0x00 +
// value) 的前 16 位十六进制。确定性跨进程成立（同盐同输出），用于需要
// 关联同一敏感值多次出现的场景（如同一令牌的多条日志可对上）。空串
// 输入返回空串——空值无信息量，不产出无意义常量。盐为空即无盐哈希，
// 仅用于低敏感场景，口令/令牌请注入非空盐。
func NewHash(salt string) Redactor {
	return hashRedactor{salt: salt}
}

type hashRedactor struct {
	salt string
}

// hashPrefix 是输出前缀，标明算法与截断长度，避免与真实值混淆。
const hashPrefix = "sha256:"

// hashHexLen 是保留的十六进制位数：16 位（64 bit）足以关联去重，
// 不给原值留暴力还原空间。
const hashHexLen = 16

func (h hashRedactor) Redact(value string) string {
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(h.salt + "\x00" + value))
	return hashPrefix + hex.EncodeToString(sum[:])[:hashHexLen]
}

// NewTruncate 返回截断脱敏器：按 rune 保留前 keep 个字符，超出部分以
// suffix 替代，输出恒为 前缀+suffix。keep 为 0 表示整段以 suffix 替代，
// 负值按 0 处理；suffix 建议携带长度信息（由调用方自行拼接）以便审计
// 侧还原原文规模。检索内容（CategoryRetrieval）的默认形态。
func NewTruncate(keep int, suffix string) Redactor {
	if keep < 0 {
		keep = 0
	}
	return truncateRedactor{keep: keep, suffix: suffix}
}

type truncateRedactor struct {
	keep   int
	suffix string
}

func (t truncateRedactor) Redact(value string) string {
	if value == "" {
		return ""
	}
	// keep 语义按 rune 计：先数总量，再按 rune 边界切前缀，避免多字节
	// 字符被劈开。runes <= keep 时原文直返。
	if utf8.RuneCountInString(value) <= t.keep {
		return value
	}
	if t.keep == 0 {
		return t.suffix
	}
	cut := value
	n := 0
	for i := range value {
		if n == t.keep {
			cut = value[:i]
			break
		}
		n++
	}
	return cut + t.suffix
}

// NewKeyFilter 返回按键名匹配的 JSON 值脱敏器：输入为 JSON object/array
// 文本时深序遍历，命中键名（大小写不敏感）的值以 inner.Redact(原值 JSON
// 文本) 替换；命中对象/数组时整段子树作为一个值脱敏。非 JSON 文本与标量
// JSON 原样返回——没有键结构就无从按键匹配，不做整体猜测；非法 JSON
// 同样原样返回（展示路径 fail-open，绝不因脱敏丢内容或 panic）。
//
// 形态说明：命中值统一落为 JSON 字符串（数字/布尔原是标量），键顺序经
// 重编码后为字典序——输出保持确定性，但与输入不逐字节保形。
// 工具调用参数（CategoryToolParam）的默认形态。
func NewKeyFilter(keys []string, inner Redactor) (Redactor, error) {
	if len(keys) == 0 {
		return nil, errors.New("redact: key list is empty")
	}
	if inner == nil {
		return nil, errors.New("redact: inner redactor is nil")
	}
	set := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		set[strings.ToLower(k)] = struct{}{}
	}
	return keyFilter{keys: set, inner: inner}, nil
}

// maxKeyFilterDepth 是递归下钻的层数上限：防御恶意深嵌套导致的栈放大，
// 超限子树原样保留。
const maxKeyFilterDepth = 24

type keyFilter struct {
	keys  map[string]struct{}
	inner Redactor
}

func (f keyFilter) Redact(value string) string {
	if value == "" {
		return ""
	}
	var root any
	if err := json.Unmarshal([]byte(value), &root); err != nil {
		return value
	}
	switch root.(type) {
	case map[string]any, []any:
	default:
		return value
	}
	redacted := f.walk(root, 0)
	if redacted == nil {
		return value
	}
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(redacted); err != nil {
		return value
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// walk 深序遍历解码后的 JSON 值，返回脱敏后的新值；map/array 原地构造
// 副本，标量原样。matched 为 true 表示本节点被键名命中，由调用方整体
// 替换，不再下钻。
func (f keyFilter) walk(node any, depth int) any {
	if depth > maxKeyFilterDepth {
		return node
	}
	switch v := node.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, child := range v {
			if _, hit := f.keys[strings.ToLower(k)]; hit {
				out[k] = f.inner.Redact(marshalCompact(child))
				continue
			}
			out[k] = f.walk(child, depth+1)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			out[i] = f.walk(child, depth+1)
		}
		return out
	default:
		return node
	}
}

// marshalCompact 把任意已解码 JSON 值编码为紧凑文本，作为整段值的脱敏
// 输入；编码失败时以 Go 默认格式兜底（fmt 语义），保证不 panic。
func marshalCompact(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// Registry 是按类别登记的并发安全脱敏器注册表：写少读多，Register 走
// 写锁、Redact/Lookup 走读锁，注册完成后可无界并发读。类别集合固定为
// 上方常量所列，亦接受调用方自定义键（登记表不设枚举校验——消费方按
// 常量取用，多余键无害）。
type Registry struct {
	mu         sync.RWMutex
	byCategory map[string]Redactor
}

// NewRegistry 构造空注册表。
func NewRegistry() *Registry {
	return &Registry{byCategory: make(map[string]Redactor)}
}

// Register 登记（或整体替换）一个类别的脱敏器。类别为空或脱敏器为 nil
// 属装配错误，返回 error 拒绝；重复登记按替换处理（后登记者生效）。
func (r *Registry) Register(category string, red Redactor) error {
	if category == "" {
		return errors.New("redact: category is empty")
	}
	if red == nil {
		return errors.New("redact: redactor is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byCategory[category] = red
	return nil
}

// Lookup 取某类别的脱敏器；未登记返回 false。
func (r *Registry) Lookup(category string) (Redactor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	red, ok := r.byCategory[category]
	return red, ok
}

// Redact 是取用即脱敏的便捷入口：类别已登记返回脱敏值与 true，未登记
// 原样返回与 false——未登记类别意味着该面尚未声明脱敏语义，调用方据
// false 决定放行或拒绝，绝不静默假脱敏。
func (r *Registry) Redact(category, value string) (string, bool) {
	red, ok := r.Lookup(category)
	if !ok {
		return value, false
	}
	return red.Redact(value), true
}

// 标准注册表的骨架默认值：tool_param 键名清单是起步集合（可 Register
// 整体替换），retrieval 保留 512 rune。
const defaultRetrievalKeep = 512

// defaultToolParamKeys 是工具参数敏感键的起步清单：覆盖常见口令/令牌/
// 密钥类参数名。业务工具面扩张时由装配方按需替换（Register 覆盖）。
var defaultToolParamKeys = []string{
	"password", "passwd", "secret", "token", "access_token",
	"refresh_token", "api_key", "apikey", "authorization",
}

// NewStandardRegistry 构造含四类骨架默认值的注册表：password/token 为
// 固定掩码，tool_param 为起步键名清单 + 掩码，retrieval 为 512 rune
// 截断（截断尾部带英文标记以区别于天然短文本）。默认值面向「不出错」
// 而非「最合适」——各消费面应在装配期按需 Register 覆盖（如 token 换
// keyed 哈希以支持关联）。
func NewStandardRegistry() *Registry {
	r := NewRegistry()
	mask := NewMask("[redacted]")
	filter, err := NewKeyFilter(defaultToolParamKeys, mask)
	if err != nil {
		// 常量清单与掩码构造恒成功；防御性分支保持构造函数无错误返回。
		panic("redact: standard registry defaults are invalid: " + err.Error())
	}
	for _, pair := range []struct {
		category string
		red      Redactor
	}{
		{CategoryPassword, mask},
		{CategoryToken, mask},
		{CategoryToolParam, filter},
		{CategoryRetrieval, NewTruncate(defaultRetrievalKeep, "...(truncated)")},
	} {
		if err := r.Register(pair.category, pair.red); err != nil {
			panic("redact: standard registry defaults are invalid: " + err.Error())
		}
	}
	return r
}
