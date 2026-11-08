package pg_sqlc

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"grape/internal/biz"
)

// 模型密钥加解密单测（纯内存）：round-trip、随机 nonce、错误密钥、篡改
// 密文与主密钥缺失的统一错误语义。

func newTestCipher(t *testing.T, key []byte) biz.SecretCipher {
	t.Helper()
	t.Setenv(ModelSecretKeyEnv, base64.StdEncoding.EncodeToString(key))
	cipher, cleanup, err := NewSecretCipher()
	if err != nil {
		t.Fatalf("NewSecretCipher: %v", err)
	}
	t.Cleanup(cleanup)
	return cipher
}

func TestSecretCipher_round_trip_and_random_nonce(t *testing.T) {
	cipher := newTestCipher(t, make([]byte, secretKeyLen))
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
	if !strings.HasPrefix(first, secretCipherPrefix) {
		t.Fatalf("ciphertext %q lacks %q prefix", first, secretCipherPrefix)
	}
	plain, err := cipher.Open(first)
	if err != nil || plain != "sk-test-secret" {
		t.Fatalf("Open = (%q, %v), want plaintext round-trip", plain, err)
	}
}

func TestSecretCipher_wrong_key_and_tampering_rejected(t *testing.T) {
	cipher := newTestCipher(t, make([]byte, secretKeyLen))
	sealed, err := cipher.Seal("sk-test-secret")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	otherKey := make([]byte, secretKeyLen)
	otherKey[0] = 1
	other := newTestCipher(t, otherKey)
	if _, err := other.Open(sealed); !errors.Is(err, biz.ErrModelSecretUnavailable) {
		t.Fatalf("Open with wrong key = %v, want ErrModelSecretUnavailable", err)
	}

	tampered := sealed[:len(sealed)-2] + "AA"
	if _, err := cipher.Open(tampered); !errors.Is(err, biz.ErrModelSecretUnavailable) {
		t.Fatalf("Open tampered = %v, want ErrModelSecretUnavailable", err)
	}
	if _, err := cipher.Open("v2:not-base64"); !errors.Is(err, biz.ErrModelSecretUnavailable) {
		t.Fatalf("Open unknown format = %v, want ErrModelSecretUnavailable", err)
	}
}

func TestSecretCipher_missing_master_key_rejects_operations(t *testing.T) {
	t.Setenv(ModelSecretKeyEnv, "")
	cipher, cleanup, err := NewSecretCipher()
	if err != nil {
		t.Fatalf("NewSecretCipher(no key): %v", err)
	}
	defer cleanup()
	if _, err := cipher.Seal("x"); !errors.Is(err, biz.ErrModelSecretUnavailable) {
		t.Fatalf("Seal without key = %v, want ErrModelSecretUnavailable", err)
	}
	if _, err := cipher.Open("v1:AAAA"); !errors.Is(err, biz.ErrModelSecretUnavailable) {
		t.Fatalf("Open without key = %v, want ErrModelSecretUnavailable", err)
	}
}

func TestSecretCipher_invalid_master_key_fails_fast(t *testing.T) {
	for name, value := range map[string]string{
		"invalid base64": "not-base64!",
		"wrong length":   base64.StdEncoding.EncodeToString([]byte("short")),
	} {
		t.Setenv(ModelSecretKeyEnv, value)
		if _, _, err := NewSecretCipher(); err == nil {
			t.Fatalf("NewSecretCipher(%s) = nil error, want rejection", name)
		}
	}
}
