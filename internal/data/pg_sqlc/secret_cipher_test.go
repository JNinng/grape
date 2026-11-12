package pg_sqlc

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"grape/internal/biz"
	"grape/pkg/secret"
)

// 模型密钥 Codec 适配单测（纯内存，不依赖数据库）：自描述密文首段、
// 随机 nonce、业务键/注册表缺失与篡改的拒绝语义、write 指针切换后的
// 双读窗口。

// testMaterial 构造确定内容的 AES-256-GCM 材料，seed 用于区分不同版本。
func testMaterial(seed byte) secret.Material {
	key := make([]byte, 32)
	for i := range key {
		key[i] = seed + byte(i)
	}
	return secret.Material{Kind: secret.KindAES256GCM, Key: base64.StdEncoding.EncodeToString(key)}
}

// testRegistry 用给定版本与 write 指针构造注册表，构造失败即终止测试。
func testRegistry(t *testing.T, versions map[string]map[string]secret.Material, writers map[string]string) *secret.Registry {
	t.Helper()
	reg, err := secret.New(versions, writers)
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}
	return reg
}

// testCipherV1 构造 write 指针指向 v1 的模型密钥 Codec。
func testCipherV1(t *testing.T) biz.SecretCipher {
	t.Helper()
	reg := testRegistry(t,
		map[string]map[string]secret.Material{
			ModelSecretBusinessKey: {"v1": testMaterial(1)},
		},
		map[string]string{ModelSecretBusinessKey: "v1"},
	)
	return NewSecretCipher(reg)
}

func TestSecretCipher_round_trip_and_descriptive_prefix(t *testing.T) {
	cipher := testCipherV1(t)
	first, err := cipher.Seal("sk-test-secret")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	second, err := cipher.Seal("sk-test-secret")
	if err != nil {
		t.Fatalf("Seal again: %v", err)
	}
	if first == second {
		t.Fatal("Seal produced identical ciphertexts; nonce must be random")
	}
	if !strings.HasPrefix(first, ModelSecretBusinessKey+"@v1:") {
		t.Fatalf("ciphertext %q does not start with %q", first, ModelSecretBusinessKey+"@v1:")
	}
	plain, err := cipher.Open(first)
	if err != nil || plain != "sk-test-secret" {
		t.Fatalf("Open = (%q, %v), want plaintext round-trip", plain, err)
	}
}

func TestSecretCipher_unavailable_rejects_operations(t *testing.T) {
	cases := map[string]biz.SecretCipher{
		"nil registry": NewSecretCipher(nil),
		"unrelated business key": NewSecretCipher(testRegistry(t,
			map[string]map[string]secret.Material{
				"other_key": {"v1": testMaterial(1)},
			},
			map[string]string{"other_key": "v1"},
		)),
	}
	for name, cipher := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := cipher.Seal("x"); !errors.Is(err, biz.ErrModelSecretUnavailable) {
				t.Fatalf("Seal = %v, want ErrModelSecretUnavailable", err)
			}
			if _, err := cipher.Open(ModelSecretBusinessKey + "@v1:AAAA:BBBB"); !errors.Is(err, biz.ErrModelSecretUnavailable) {
				t.Fatalf("Open = %v, want ErrModelSecretUnavailable", err)
			}
		})
	}
}

func TestSecretCipher_tampered_and_malformed_ciphertext_rejected(t *testing.T) {
	cipher := testCipherV1(t)
	sealed, err := cipher.Seal("sk-test-secret")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	for name, ciphertext := range map[string]string{
		"tampered body": sealed[:len(sealed)-2] + "AA",
		"legacy format": "v1:AAAA",
		"garbage":       "not-a-ciphertext",
	} {
		if _, err := cipher.Open(ciphertext); !errors.Is(err, biz.ErrModelSecretUnavailable) {
			t.Fatalf("Open(%s) = %v, want ErrModelSecretUnavailable", name, err)
		}
	}
}

func TestSecretCipher_write_pointer_switch_keeps_old_ciphertexts(t *testing.T) {
	// 轮换前实例：只登记 v1，write 指针 v1。
	oldCipher := testCipherV1(t)
	oldCiphertext, err := oldCipher.Seal("sk-old-secret")
	if err != nil {
		t.Fatalf("Seal with v1: %v", err)
	}

	// 轮换后实例：v1/v2 材料并存，write 指针切到 v2。
	rotated := testRegistry(t,
		map[string]map[string]secret.Material{
			ModelSecretBusinessKey: {
				"v1": testMaterial(1),
				"v2": testMaterial(2),
			},
		},
		map[string]string{ModelSecretBusinessKey: "v2"},
	)
	cipher := NewSecretCipher(rotated)

	newCiphertext, err := cipher.Seal("sk-new-secret")
	if err != nil {
		t.Fatalf("Seal with v2: %v", err)
	}
	if !strings.HasPrefix(newCiphertext, ModelSecretBusinessKey+"@v2:") {
		t.Fatalf("new ciphertext %q does not start with v2", newCiphertext)
	}
	if plain, err := cipher.Open(newCiphertext); err != nil || plain != "sk-new-secret" {
		t.Fatalf("Open new ciphertext = (%q, %v), want round-trip", plain, err)
	}
	// 滚动窗口双读：v1 存量密文仍按首段选 v1 材料解开。
	if plain, err := cipher.Open(oldCiphertext); err != nil || plain != "sk-old-secret" {
		t.Fatalf("Open old ciphertext = (%q, %v), want round-trip", plain, err)
	}
}
