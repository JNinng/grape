package biz

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/scrypt"
)

// argon2IDKey / argon2IKey 构造测试夹具的派生键。
func argon2IDKey(t *testing.T, plain string, salt []byte, time, memory uint32, threads uint8) []byte {
	t.Helper()
	return argon2.IDKey([]byte(plain), salt, time, memory, threads, 32)
}

func argon2IKey(t *testing.T, plain string, salt []byte, time, memory uint32, threads uint8) []byte {
	t.Helper()
	return argon2.Key([]byte(plain), salt, time, memory, threads, 32)
}

// legacyScryptHash 构造 Spring Security SCryptPasswordEncoder 兼容的
// 种子格式哈希（测试夹具，与 seed.sql 同格式）。
func legacyScryptHash(t *testing.T, plain string, logN, r, p int) string {
	t.Helper()
	salt := make([]byte, 64)
	if _, err := rand.Read(salt); err != nil {
		t.Fatalf("rand salt: %v", err)
	}
	dk, err := scrypt.Key([]byte(plain), salt, 1<<logN, r, p, 32)
	if err != nil {
		t.Fatalf("scrypt: %v", err)
	}
	return fmt.Sprintf("{scrypt}$%x$%s$%s",
		logN<<16|r<<8|p,
		base64.StdEncoding.EncodeToString(salt),
		base64.StdEncoding.EncodeToString(dk))
}

func TestHashPasswordPrefersArgon2id(t *testing.T) {
	hash, err := HashPassword("s3cret-密码")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=") {
		t.Fatalf("hash = %q, want argon2id PHC", hash)
	}
	ok, rehash, err := VerifyPassword(hash, "s3cret-密码")
	if err != nil || !ok {
		t.Fatalf("verify = (%v, %v), want (true, nil)", ok, err)
	}
	if rehash {
		t.Fatal("preferred algorithm must not request rehash")
	}
	if ok, _, _ := VerifyPassword(hash, "wrong"); ok {
		t.Fatal("wrong password must not verify")
	}

	// 两次哈希盐不同，库内值不重复
	other, _ := HashPassword("s3cret-密码")
	if other == hash {
		t.Fatal("two hashes of the same password must differ")
	}
}

func TestVerifyPasswordDispatchesAlgorithms(t *testing.T) {
	plain := "password-123"

	// 非首选参数的 argon2id：校验通过并要求重哈希（参数升级路径）
	variant := "$argon2id$v=19$m=16384,t=2,p=1$" +
		base64.RawStdEncoding.EncodeToString([]byte("0123456789abcdef")) + "$" +
		base64.RawStdEncoding.EncodeToString(argon2IDKey(t, plain, []byte("0123456789abcdef"), 2, 16384, 1))
	ok, rehash, err := VerifyPassword(variant, plain)
	if err != nil || !ok || !rehash {
		t.Fatalf("variant argon2id = (%v, %v, %v), want (true, true, nil)", ok, rehash, err)
	}

	// argon2i：识别并要求重哈希
	iHash := "$argon2i$v=19$m=16384,t=4,p=1$" +
		base64.RawStdEncoding.EncodeToString([]byte("0123456789abcdef")) + "$" +
		base64.RawStdEncoding.EncodeToString(argon2IKey(t, plain, []byte("0123456789abcdef"), 4, 16384, 1))
	if ok, rehash, err := VerifyPassword(iHash, plain); err != nil || !ok || !rehash {
		t.Fatalf("argon2i = (%v, %v, %v), want (true, true, nil)", ok, rehash, err)
	}

	// bcrypt（历史存量格式）：校验通过即要求重哈希
	bHash, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	if ok, rehash, err := VerifyPassword(string(bHash), plain); err != nil || !ok || !rehash {
		t.Fatalf("bcrypt = (%v, %v, %v), want (true, true, nil)", ok, rehash, err)
	}

	// 种子 scrypt 格式：校验通过即要求重哈希
	lHash := legacyScryptHash(t, plain, 14, 8, 1)
	if ok, rehash, err := VerifyPassword(lHash, plain); err != nil || !ok || !rehash {
		t.Fatalf("legacy scrypt = (%v, %v, %v), want (true, true, nil)", ok, rehash, err)
	}

	// 各种错误口令：一律 false 且不是错误
	for _, h := range []string{variant, string(bHash), lHash} {
		if ok, _, err := VerifyPassword(h, "not-"+plain); ok || err != nil {
			t.Fatalf("wrong password on %q... = (%v, %v), want (false, nil)", h[:12], ok, err)
		}
	}
}

func TestVerifyPasswordRejectsMalformed(t *testing.T) {
	cases := []string{
		"",
		"plain-text",
		"$md5$abc",
		"$argon2d$v=19$m=19456,t=2,p=1$ab$cd",  // argon2d 无安全实现，视为数据异常
		"$argon2id$v=18$m=19456,t=2,p=1$ab$cd", // 版本不支持
		"$argon2id$v=19$m=x,t=2,p=1$ab$cd",     // 参数不可解析
		"{scrypt}$zzzz$ab$cd",                  // 参数不可解析
		"{scrypt}$e0801$!!!$cd",                // 盐不是 base64
	}
	for _, h := range cases {
		if _, _, err := VerifyPassword(h, "x"); !errors.Is(err, ErrPasswordHashUnsupported) {
			t.Fatalf("hash %q: want ErrPasswordHashUnsupported, got %v", h, err)
		}
	}
}

func TestVerifyPasswordLegacyScryptParamVariants(t *testing.T) {
	// 参数字按 Spring 语义解码：log2N<<16 | r<<8 | p；参数不同即哈希不同
	plain := "params-check"
	for _, tc := range []struct{ logN, r, p int }{{14, 8, 1}, {15, 8, 1}, {14, 8, 2}, {14, 16, 1}} {
		h := legacyScryptHash(t, plain, tc.logN, tc.r, tc.p)
		ok, rehash, err := VerifyPassword(h, plain)
		if err != nil || !ok || !rehash {
			t.Fatalf("logN=%d r=%d p=%d = (%v, %v, %v), want (true, true, nil)", tc.logN, tc.r, tc.p, ok, rehash, err)
		}
	}
}
