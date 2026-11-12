package secret_test

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"grape/pkg/secret"
)

// 自描述密文 Codec 的契约测试（纯内存，无存储依赖）：格式、随机 nonce、
// 按业务键/版本选材料、AAD 绑定、统一哨兵错误、材料校验矩阵与并发安全。

// randomKey 生成一把可通过装配校验的对称密钥（base64 的 32 字节）。
func randomKey(t *testing.T) string {
	t.Helper()
	m, err := secret.Generate(secret.KindAES256GCM)
	if err != nil {
		t.Fatalf("Generate(AES-256-GCM): %v", err)
	}
	return m.Key
}

// aesMaterial 构造对称算法材料。
func aesMaterial(key string) secret.Material {
	return secret.Material{Kind: secret.KindAES256GCM, Key: key}
}

// mustRegistry 构造注册表，构造失败即终止用例。
func mustRegistry(t *testing.T, versions map[string]map[string]secret.Material, writers map[string]string) *secret.Registry {
	t.Helper()
	reg, err := secret.New(versions, writers)
	if err != nil {
		t.Fatalf("New(valid registry): %v", err)
	}
	return reg
}

// mustSealer 取写入侧 Codec，失败即终止用例。
func mustSealer(t *testing.T, reg *secret.Registry, businessKey string) *secret.Sealer {
	t.Helper()
	sealer, err := reg.Sealer(businessKey)
	if err != nil {
		t.Fatalf("Sealer(%q): %v", businessKey, err)
	}
	return sealer
}

func TestSeal_outputs_three_self_describing_segments(t *testing.T) {
	reg := mustRegistry(t,
		map[string]map[string]secret.Material{"module_key_encrypt": {"v1": aesMaterial(randomKey(t))}},
		map[string]string{"module_key_encrypt": "v1"})
	sealer := mustSealer(t, reg, "module_key_encrypt")

	const plaintext = "sk-model-secret"
	ct, err := sealer.Seal(plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	parts := strings.Split(ct, ":")
	if len(parts) != 3 {
		t.Fatalf("ciphertext %q splits into %d segments, want 3", ct, len(parts))
	}
	if parts[0] != "module_key_encrypt@v1" {
		t.Fatalf("first segment = %q, want %q", parts[0], "module_key_encrypt@v1")
	}
	nonce, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("nonce segment is not raw-url base64: %v", err)
	}
	if len(nonce) != 12 {
		t.Fatalf("nonce length = %d, want 12", len(nonce))
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("body segment is not raw-url base64: %v", err)
	}
	if want := len(plaintext) + 16; len(body) != want {
		t.Fatalf("body length = %d, want plaintext+tag = %d", len(body), want)
	}
}

func TestSeal_same_plaintext_produces_distinct_ciphertexts(t *testing.T) {
	reg := mustRegistry(t,
		map[string]map[string]secret.Material{"k": {"v1": aesMaterial(randomKey(t))}},
		map[string]string{"k": "v1"})
	sealer := mustSealer(t, reg, "k")

	first, err := sealer.Seal("same plaintext")
	if err != nil {
		t.Fatalf("first Seal: %v", err)
	}
	second, err := sealer.Seal("same plaintext")
	if err != nil {
		t.Fatalf("second Seal: %v", err)
	}
	if first == second {
		t.Fatalf("two seals of the same plaintext are identical (%q), want random nonce to differ", first)
	}
	for _, ct := range []string{first, second} {
		got, err := reg.Open(ct)
		if err != nil || got != "same plaintext" {
			t.Fatalf("Open(%q) = (%q, %v), want the original plaintext", ct, got, err)
		}
	}
}

func TestOpen_selects_material_by_header_version(t *testing.T) {
	keyV1, keyV2 := randomKey(t), randomKey(t)
	versions := map[string]map[string]secret.Material{
		"module_key_encrypt": {
			"v1": aesMaterial(keyV1),
			"v2": aesMaterial(keyV2),
		},
	}
	// 写入指针在 v1 的实例写 v1 密文；切到 v2 的实例写 v2 密文。
	writerV1 := mustRegistry(t, versions, map[string]string{"module_key_encrypt": "v1"})
	writerV2 := mustRegistry(t, versions, map[string]string{"module_key_encrypt": "v2"})

	ctV1, err := mustSealer(t, writerV1, "module_key_encrypt").Seal("one")
	if err != nil {
		t.Fatalf("seal v1: %v", err)
	}
	ctV2, err := mustSealer(t, writerV2, "module_key_encrypt").Seal("two")
	if err != nil {
		t.Fatalf("seal v2: %v", err)
	}
	if !strings.HasPrefix(ctV1, "module_key_encrypt@v1:") {
		t.Fatalf("v1 writer produced %q, want header module_key_encrypt@v1", ctV1)
	}
	if !strings.HasPrefix(ctV2, "module_key_encrypt@v2:") {
		t.Fatalf("v2 writer produced %q, want header module_key_encrypt@v2", ctV2)
	}
	// 读取侧只接密文字符串：两个版本各自选到自己的材料。
	for _, tc := range []struct {
		ct   string
		want string
	}{{ctV1, "one"}, {ctV2, "two"}} {
		got, err := writerV1.Open(tc.ct)
		if err != nil || got != tc.want {
			t.Fatalf("Open(%q) = (%q, %v), want %q", tc.ct, got, err, tc.want)
		}
	}
}

func TestOpen_rejects_ciphertext_from_other_business_key(t *testing.T) {
	// 同一份密钥材料同时登记在 group_a@v1 与 group_b@v1，且同一注册表都能
	// 选到条目：改写首段业务键后若仍可解，只可能来自 AAD 业务键绑定失效。
	sharedKey := randomKey(t)
	reg := mustRegistry(t,
		map[string]map[string]secret.Material{
			"group_a": {"v1": aesMaterial(sharedKey)},
			"group_b": {"v1": aesMaterial(sharedKey)},
		},
		map[string]string{"group_a": "v1", "group_b": "v1"})

	ctA, err := mustSealer(t, reg, "group_a").Seal("a-only")
	if err != nil {
		t.Fatalf("seal in group_a: %v", err)
	}
	if !strings.HasPrefix(ctA, "group_a@v1:") {
		t.Fatalf("group_a writer produced %q, want header group_a@v1", ctA)
	}
	if got, err := reg.Open(ctA); err != nil || got != "a-only" {
		t.Fatalf("Open within group_a = (%q, %v), want a-only", got, err)
	}
	// 把首段的业务键改写为 group_b（密文其余部分不变）：group_b@v1 条目使
	// 用同一材料、同一版本、同一算法，必须由 AAD 认证失败挡住。
	rewrittenKey := strings.Replace(ctA, "group_a@v1:", "group_b@v1:", 1)
	if rewrittenKey == ctA {
		t.Fatalf("ciphertext %q does not carry the group_a@v1 header", ctA)
	}
	if _, err := reg.Open(rewrittenKey); !errors.Is(err, secret.ErrUnavailable) {
		t.Fatalf("Open(group_b header with group_a ciphertext) = %v, want ErrUnavailable (AAD binds business key)", err)
	}
	// 头改写同样被 AAD 挡住：同一密钥材料登记在两个版本时，把首段的
	// 版本号从 v1 改成 v2 也必须认证失败（AAD 绑定完整键名）。
	dual := map[string]map[string]secret.Material{
		"module_key_encrypt": {"v1": aesMaterial(sharedKey), "v2": aesMaterial(sharedKey)},
	}
	regDual := mustRegistry(t, dual, map[string]string{"module_key_encrypt": "v1"})
	ctV1, err := mustSealer(t, regDual, "module_key_encrypt").Seal("header-bound")
	if err != nil {
		t.Fatalf("seal with dual registry: %v", err)
	}
	rewritten := strings.Replace(ctV1, "module_key_encrypt@v1:", "module_key_encrypt@v2:", 1)
	if _, err := regDual.Open(rewritten); !errors.Is(err, secret.ErrUnavailable) {
		t.Fatalf("Open(header-rewritten ciphertext) = %v, want ErrUnavailable (AAD binds version)", err)
	}
}

func TestOpen_failure_returns_single_opaque_sentinel(t *testing.T) {
	reg := mustRegistry(t,
		map[string]map[string]secret.Material{"k": {"v1": aesMaterial(randomKey(t))}},
		map[string]string{"k": "v1"})
	ct, err := mustSealer(t, reg, "k").Seal("payload")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	parts := strings.Split(ct, ":")
	flip := func(segment string) string {
		raw, err := base64.RawURLEncoding.DecodeString(segment)
		if err != nil {
			t.Fatalf("decode segment %q: %v", segment, err)
		}
		raw[0] ^= 0xff
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	failures := map[string]string{
		"empty":                "",
		"not a ciphertext":     "plain-text-payload",
		"unknown business key": "other@v1:" + parts[1] + ":" + parts[2],
		"unknown version":      "k@v9:" + parts[1] + ":" + parts[2],
		"invalid nonce base64": "k@v1:!!not-base64!!:" + parts[2],
		"tampered nonce":       "k@v1:" + flip(parts[1]) + ":" + parts[2],
		"tampered body":        "k@v1:" + parts[1] + ":" + flip(parts[2]),
	}
	for name, malformed := range failures {
		_, err := reg.Open(malformed)
		// 哨兵必须原样返回：既不包装也不附加定位信息，文本对所有失败点一致。
		if err != secret.ErrUnavailable {
			t.Fatalf("%s: Open = %v, want the ErrUnavailable sentinel itself", name, err)
		}
		if err.Error() != secret.ErrUnavailable.Error() {
			t.Fatalf("%s: error text = %q, want the fixed sentinel text", name, err)
		}
		// 错误文本不得携带密文片段（nonce 或密文+tag）。
		for _, segment := range parts[1:] {
			if strings.Contains(err.Error(), segment) {
				t.Fatalf("%s: error %q leaks ciphertext segment", name, err)
			}
		}
	}
}

func TestOpen_rejects_tampered_ciphertext(t *testing.T) {
	reg := mustRegistry(t,
		map[string]map[string]secret.Material{"k": {"v1": aesMaterial(randomKey(t))}},
		map[string]string{"k": "v1"})
	ct, err := mustSealer(t, reg, "k").Seal("payload")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	parts := strings.Split(ct, ":")
	if len(parts) != 3 {
		t.Fatalf("ciphertext %q splits into %d segments, want 3", ct, len(parts))
	}
	flip := func(segment string) string {
		raw, err := base64.RawURLEncoding.DecodeString(segment)
		if err != nil {
			t.Fatalf("decode segment %q: %v", segment, err)
		}
		raw[0] ^= 0xff
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	cases := map[string]string{
		"nonce":              parts[0] + ":" + flip(parts[1]) + ":" + parts[2],
		"ciphertext first":   parts[0] + ":" + parts[1] + ":" + flip(parts[2]),
		"ciphertext last":    parts[0] + ":" + parts[1] + ":" + flipBodyLast(t, parts[2]),
		"truncated body":     parts[0] + ":" + parts[1] + ":" + truncateBody(t, parts[2]),
		"appended extra tag": parts[0] + ":" + parts[1] + ":" + parts[2] + "A",
	}
	for name, tampered := range cases {
		if _, err := reg.Open(tampered); !errors.Is(err, secret.ErrUnavailable) {
			t.Fatalf("%s: Open(tampered) = %v, want ErrUnavailable", name, err)
		}
	}
}

// flipBodyLast 翻转密文段的最后一个字节（tag 内）。
func flipBodyLast(t *testing.T, segment string) string {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		t.Fatalf("decode body: %v", err)
	}
	raw[len(raw)-1] ^= 0xff
	return base64.RawURLEncoding.EncodeToString(raw)
}

// truncateBody 去掉密文段一个字节（截断 tag）。
func truncateBody(t *testing.T, segment string) string {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:len(raw)-1])
}

func TestOpen_rejects_malformed_or_unknown_ciphertext(t *testing.T) {
	reg := mustRegistry(t,
		map[string]map[string]secret.Material{"k": {"v1": aesMaterial(randomKey(t))}},
		map[string]string{"k": "v1"})
	ct, err := mustSealer(t, reg, "k").Seal("payload")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	parts := strings.Split(ct, ":")
	nonce, body := parts[1], parts[2]

	cases := map[string]string{
		"empty":                "",
		"no separators":        "module_key_encrypt@v1",
		"one separator":        "module_key_encrypt@v1:" + nonce,
		"empty nonce and body": "module_key_encrypt@v1::",
		"empty body":           "module_key_encrypt@v1:" + nonce + ":",
		"extra segment":        ct + ":extra",
		"empty business key":   "@v1:" + nonce + ":" + body,
		"empty version":        "module_key_encrypt@:" + nonce + ":" + body,
		"unknown business key": "other@v1:" + nonce + ":" + body,
		"unknown version":      "module_key_encrypt@v9:" + nonce + ":" + body,
		"invalid nonce base64": "module_key_encrypt@v1:!!not-base64!!:" + body,
		"short nonce":          "module_key_encrypt@v1:" + base64.RawURLEncoding.EncodeToString([]byte("short")) + ":" + body,
		"short body":           "module_key_encrypt@v1:" + nonce + ":" + base64.RawURLEncoding.EncodeToString([]byte("short")),
		"padded nonce":         "module_key_encrypt@v1:" + nonce + "==:" + body,
	}
	for name, malformed := range cases {
		if _, err := reg.Open(malformed); !errors.Is(err, secret.ErrUnavailable) {
			t.Fatalf("%s: Open(%q) = %v, want ErrUnavailable", name, malformed, err)
		}
	}
}

func TestNewRejecting_rejects_everything(t *testing.T) {
	reg := secret.NewRejecting()
	if _, err := reg.Sealer("module_key_encrypt"); !errors.Is(err, secret.ErrUnavailable) {
		t.Fatalf("Sealer on rejecting registry = %v, want ErrUnavailable", err)
	}
	// 用真实注册表产出的合法密文也不得被拒绝式实例解开。
	realReg := mustRegistry(t,
		map[string]map[string]secret.Material{"module_key_encrypt": {"v1": aesMaterial(randomKey(t))}},
		map[string]string{"module_key_encrypt": "v1"})
	ct, err := mustSealer(t, realReg, "module_key_encrypt").Seal("payload")
	if err != nil {
		t.Fatalf("Seal on real registry: %v", err)
	}
	if _, err := reg.Open(ct); !errors.Is(err, secret.ErrUnavailable) {
		t.Fatalf("Open on rejecting registry = %v, want ErrUnavailable", err)
	}
	if _, err := reg.Sealer(""); !errors.Is(err, secret.ErrUnavailable) {
		t.Fatalf("Sealer(\"\") on rejecting registry = %v, want ErrUnavailable", err)
	}
}

func TestSealer_rejects_unregistered_business_key(t *testing.T) {
	reg := mustRegistry(t,
		map[string]map[string]secret.Material{"k": {"v1": aesMaterial(randomKey(t))}},
		map[string]string{"k": "v1"})
	if _, err := reg.Sealer("unknown"); !errors.Is(err, secret.ErrUnavailable) {
		t.Fatalf("Sealer(unknown) = %v, want ErrUnavailable", err)
	}
}

func TestNew_validation_matrix(t *testing.T) {
	validKey := randomKey(t)
	shortKey := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef")) // 16 bytes
	validMaterial := aesMaterial(validKey)

	cases := []struct {
		name     string
		versions map[string]map[string]secret.Material
		writers  map[string]string
		want     string
	}{
		{
			name:     "valid baseline",
			versions: map[string]map[string]secret.Material{"k": {"v1": validMaterial}},
			writers:  map[string]string{"k": "v1"},
		},
		{
			name:     "empty registry",
			versions: nil,
			writers:  nil,
			want:     "at least one business key",
		},
		{
			name:     "writers references unknown business key",
			versions: map[string]map[string]secret.Material{"k": {"v1": validMaterial}},
			writers:  map[string]string{"k": "v1", "other": "v1"},
			want:     "unregistered business key",
		},
		{
			name:     "missing write pointer",
			versions: map[string]map[string]secret.Material{"k": {"v1": validMaterial}},
			writers:  map[string]string{},
			want:     "requires a write version pointer",
		},
		{
			name:     "empty write pointer",
			versions: map[string]map[string]secret.Material{"k": {"v1": validMaterial}},
			writers:  map[string]string{"k": ""},
			want:     "requires a write version pointer",
		},
		{
			name:     "write pointer to undeclared version",
			versions: map[string]map[string]secret.Material{"k": {"v1": validMaterial}},
			writers:  map[string]string{"k": "v2"},
			want:     "does not reference a declared version",
		},
		{
			name:     "empty version group",
			versions: map[string]map[string]secret.Material{"k": {}},
			writers:  map[string]string{"k": "v1"},
			want:     "at least one version",
		},
		{
			name:     "uppercase business key",
			versions: map[string]map[string]secret.Material{"Module_Key": {"v1": validMaterial}},
			writers:  map[string]string{"Module_Key": "v1"},
			want:     "must match",
		},
		{
			name:     "empty business key",
			versions: map[string]map[string]secret.Material{"": {"v1": validMaterial}},
			writers:  map[string]string{"": "v1"},
			want:     "must match",
		},
		{
			name:     "empty version alongside declared version",
			versions: map[string]map[string]secret.Material{"k": {"": validMaterial, "v1": validMaterial}},
			writers:  map[string]string{"k": "v1"},
			want:     "must match",
		},
		{
			name:     "business key starting with digit",
			versions: map[string]map[string]secret.Material{"1module": {"v1": validMaterial}},
			writers:  map[string]string{"1module": "v1"},
			want:     "must match",
		},
		{
			name:     "business key with hyphen",
			versions: map[string]map[string]secret.Material{"module-key": {"v1": validMaterial}},
			writers:  map[string]string{"module-key": "v1"},
			want:     "must match",
		},
		{
			name:     "business key too long",
			versions: map[string]map[string]secret.Material{strings.Repeat("a", 65): {"v1": validMaterial}},
			writers:  map[string]string{strings.Repeat("a", 65): "v1"},
			want:     "must match",
		},
		{
			name:     "version with hyphen",
			versions: map[string]map[string]secret.Material{"k": {"v-1": validMaterial}},
			writers:  map[string]string{"k": "v-1"},
			want:     "must match",
		},
		{
			name:     "version too long",
			versions: map[string]map[string]secret.Material{"k": {strings.Repeat("v", 33): validMaterial}},
			writers:  map[string]string{"k": strings.Repeat("v", 33)},
			want:     "must match",
		},
		{
			name:     "kind family name",
			versions: map[string]map[string]secret.Material{"k": {"v1": {Kind: "AES", Key: validKey}}},
			writers:  map[string]string{"k": "v1"},
			want:     "not a supported algorithm",
		},
		{
			name:     "kind unknown variant",
			versions: map[string]map[string]secret.Material{"k": {"v1": {Kind: "AES-256-CBC", Key: validKey}}},
			writers:  map[string]string{"k": "v1"},
			want:     "not a supported algorithm",
		},
		{
			name:     "kind empty",
			versions: map[string]map[string]secret.Material{"k": {"v1": {Key: validKey}}},
			writers:  map[string]string{"k": "v1"},
			want:     "not a supported algorithm",
		},
		{
			name:     "symmetric with public key",
			versions: map[string]map[string]secret.Material{"k": {"v1": {Kind: secret.KindAES256GCM, Key: validKey, PublicKey: "cHVibGlj"}}},
			writers:  map[string]string{"k": "v1"},
			want:     "must not set public_key or private_key",
		},
		{
			name:     "symmetric with private key",
			versions: map[string]map[string]secret.Material{"k": {"v1": {Kind: secret.KindAES256GCM, Key: validKey, PrivateKey: "cHJpdmF0ZQ=="}}},
			writers:  map[string]string{"k": "v1"},
			want:     "must not set public_key or private_key",
		},
		{
			name:     "symmetric without key",
			versions: map[string]map[string]secret.Material{"k": {"v1": {Kind: secret.KindAES256GCM}}},
			writers:  map[string]string{"k": "v1"},
			want:     "requires key",
		},
		{
			name:     "key invalid base64",
			versions: map[string]map[string]secret.Material{"k": {"v1": aesMaterial("not base64!!")}},
			writers:  map[string]string{"k": "v1"},
			want:     "must be valid base64",
		},
		{
			name:     "key wrong length",
			versions: map[string]map[string]secret.Material{"k": {"v1": aesMaterial(shortKey)}},
			writers:  map[string]string{"k": "v1"},
			want:     "must decode to 32 bytes",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := secret.New(tc.versions, tc.writers)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("New(valid) = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("New = %v, want error containing %q", err, tc.want)
			}
			// 错误信息不得包含密钥材料。
			if strings.Contains(err.Error(), validKey) || strings.Contains(err.Error(), shortKey) {
				t.Fatalf("error %q leaks key material", err)
			}
		})
	}
}

func TestSupportedKinds_matches_validation_set(t *testing.T) {
	got := secret.SupportedKinds()
	want := []secret.Kind{secret.KindAES256GCM}
	if len(got) != len(want) {
		t.Fatalf("SupportedKinds() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("SupportedKinds() = %v, want %v (deterministic order)", got, want)
		}
	}
	// 清单即校验集合：清单内算法可通过 New，清单外（家族名/变体）拒绝。
	for _, kind := range got {
		m, err := secret.Generate(kind)
		if err != nil {
			t.Fatalf("Generate(%q): %v", kind, err)
		}
		if _, err := secret.New(
			map[string]map[string]secret.Material{"k": {"v1": m}},
			map[string]string{"k": "v1"},
		); err != nil {
			t.Fatalf("New with material generated for %q: %v", kind, err)
		}
	}
}

func TestGenerate_material_passes_assembly_validation(t *testing.T) {
	for _, kind := range secret.SupportedKinds() {
		m, err := secret.Generate(kind)
		if err != nil {
			t.Fatalf("Generate(%q): %v", kind, err)
		}
		if m.Kind != kind {
			t.Fatalf("Generate(%q).Kind = %q, want %q", kind, m.Kind, kind)
		}
		if m.PublicKey != "" || m.PrivateKey != "" {
			t.Fatalf("Generate(%q) set asymmetric fields: %+v", kind, m)
		}
		raw, err := base64.StdEncoding.DecodeString(m.Key)
		if err != nil || len(raw) != 32 {
			t.Fatalf("Generate(%q).Key decodes to (%d bytes, %v), want 32 bytes of base64", kind, len(raw), err)
		}
		reg := mustRegistry(t,
			map[string]map[string]secret.Material{"k": {"v1": m}},
			map[string]string{"k": "v1"})
		sealer := mustSealer(t, reg, "k")
		ct, err := sealer.Seal("payload")
		if err != nil {
			t.Fatalf("Seal with generated %q material: %v", kind, err)
		}
		if got, err := reg.Open(ct); err != nil || got != "payload" {
			t.Fatalf("Open with generated %q material = (%q, %v), want payload", kind, got, err)
		}
	}
	for _, kind := range []secret.Kind{"", "AES", "AES-256-CBC", "RSA-2048"} {
		if _, err := secret.Generate(kind); err == nil {
			t.Fatalf("Generate(%q) = nil error, want unsupported kind rejection", kind)
		}
	}
}

func TestConcurrent_seal_and_open(t *testing.T) {
	reg := mustRegistry(t,
		map[string]map[string]secret.Material{"module_key_encrypt": {"v1": aesMaterial(randomKey(t))}},
		map[string]string{"module_key_encrypt": "v1"})
	sealer := mustSealer(t, reg, "module_key_encrypt")

	const (
		workers = 8
		rounds  = 25
	)
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(w int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				plaintext := fmt.Sprintf("payload-%d-%d", w, i)
				ct, err := sealer.Seal(plaintext)
				if err != nil {
					t.Errorf("worker %d Seal: %v", w, err)
					return
				}
				got, err := reg.Open(ct)
				if err != nil {
					t.Errorf("worker %d Open: %v", w, err)
					return
				}
				if got != plaintext {
					t.Errorf("worker %d Open = %q, want %q", w, got, plaintext)
					return
				}
			}
		}(w)
	}
	wg.Wait()
}
