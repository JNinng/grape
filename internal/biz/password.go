package biz

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/scrypt"
)

// ErrPasswordHashUnsupported 表示库内口令哈希不是可识别的自描述格式：
// 属于数据损坏或未知算法，不是凭据错误（校验失败不是错误，返回 false）。
var ErrPasswordHashUnsupported = kratoserrors.InternalServer("PASSWORD_HASH_UNSUPPORTED", "stored password hash format unsupported")

// 口令哈希纪律：明文只在用例内短暂存在；库内一律是自描述哈希字符串
// （PHC 或 Spring 兼容格式），校验按行内算法标识分发，仓储出入参只见
// 哈希字符串，与令牌哈希纪律同轨。
//
// 当前首选算法是 argon2id；写入一律用它。识别出的其他格式（种子
// scrypt、历史 bcrypt、argon2i）照常校验，成功且非首选时由用例透明
// 重哈希，算法升级无需批量重置。argon2d 无旁路安全的实现，库内出现
// 即视为数据异常（ErrPasswordHashUnsupported）。
const (
	argon2idMemoryKiB = 19456 // 19 MiB，OWASP 推荐下限
	argon2idTime      = 2
	argon2idThreads   = 1
	argon2idSaltLen   = 16
	argon2idKeyLen    = 32
)

// HashPassword 以当前首选算法（argon2id）哈希明文口令，返回 PHC
// 自描述字符串。唯一入口，供创建用户、改密与登录重哈希共用。
func HashPassword(plain string) (string, error) {
	salt := make([]byte, argon2idSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	key := argon2.IDKey([]byte(plain), salt, argon2idTime, argon2idMemoryKiB, argon2idThreads, argon2idKeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argon2idMemoryKiB, argon2idTime, argon2idThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword 按库内哈希的自描述标识分发算法校验明文。ok 为 false
// 表示口令不匹配（不是错误）；rehash 在校验成功且算法非当前首选时为
// true，调用方应立即以 HashPassword 重哈希落库。
func VerifyPassword(storedHash, plain string) (ok bool, rehash bool, err error) {
	switch {
	case strings.HasPrefix(storedHash, "$argon2id$"), strings.HasPrefix(storedHash, "$argon2i$"):
		return verifyPHCArgon2(storedHash, plain)
	case strings.HasPrefix(storedHash, "$2a$"), strings.HasPrefix(storedHash, "$2b$"), strings.HasPrefix(storedHash, "$2y$"):
		return verifyBcrypt(storedHash, plain)
	case strings.HasPrefix(storedHash, "{scrypt}$"):
		return verifyLegacyScrypt(storedHash, plain)
	default:
		return false, false, ErrPasswordHashUnsupported
	}
}

// verifyPHCArgon2 校验 PHC 格式 argon2 哈希：
// $argon2id$v=19$m=19456,t=2,p=1$<salt>$<hash>（base64 无填充）。
// 变体参数与首选不一致时要求重哈希，实现参数升级。
func verifyPHCArgon2(storedHash, plain string) (bool, bool, error) {
	parts := strings.Split(storedHash, "$")
	// parts[0] 为空（前导 $），[1]=变体，[2]=v=19，[3]=m,t,p，[4]=salt，[5]=hash
	if len(parts) != 6 || !strings.HasPrefix(parts[2], "v=") {
		return false, false, ErrPasswordHashUnsupported
	}
	version, err := strconv.Atoi(strings.TrimPrefix(parts[2], "v="))
	if err != nil || version != argon2.Version {
		return false, false, ErrPasswordHashUnsupported
	}
	var memoryKiB, iterations uint32
	var threads uint8
	for _, kv := range strings.Split(parts[3], ",") {
		k, v, found := strings.Cut(kv, "=")
		if !found {
			return false, false, ErrPasswordHashUnsupported
		}
		switch k {
		case "m":
			n, err := strconv.ParseUint(v, 10, 32)
			if err != nil {
				return false, false, ErrPasswordHashUnsupported
			}
			memoryKiB = uint32(n)
		case "t":
			n, err := strconv.ParseUint(v, 10, 32)
			if err != nil {
				return false, false, ErrPasswordHashUnsupported
			}
			iterations = uint32(n)
		case "p":
			n, err := strconv.ParseUint(v, 10, 8)
			if err != nil {
				return false, false, ErrPasswordHashUnsupported
			}
			threads = uint8(n)
		default:
			return false, false, ErrPasswordHashUnsupported
		}
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, false, ErrPasswordHashUnsupported
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, false, ErrPasswordHashUnsupported
	}
	var key []byte
	switch parts[1] {
	case "argon2id":
		key = argon2.IDKey([]byte(plain), salt, iterations, memoryKiB, threads, uint32(len(want)))
	case "argon2i":
		key = argon2.Key([]byte(plain), salt, iterations, memoryKiB, threads, uint32(len(want)))
	default:
		// argon2d 非旁路安全，x/crypto 不提供，库内出现即视为数据异常。
		return false, false, ErrPasswordHashUnsupported
	}
	ok := subtle.ConstantTimeCompare(key, want) == 1
	rehash := ok && (parts[1] != "argon2id" ||
		memoryKiB != argon2idMemoryKiB || iterations != argon2idTime || threads != argon2idThreads)
	return ok, rehash, nil
}

// verifyBcrypt 校验 bcrypt 哈希（$2a$/$2b$/$2y$，PHC 兼容）。bcrypt 非
// 当前首选，校验成功即要求重哈希。
func verifyBcrypt(storedHash, plain string) (bool, bool, error) {
	err := bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(plain))
	if err != nil {
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			return false, false, nil
		}
		return false, false, ErrPasswordHashUnsupported
	}
	return true, true, nil
}

// verifyLegacyScrypt 校验种子格式（Spring Security SCryptPasswordEncoder
// 兼容）：{scrypt}$<十六进制参数>$<salt>$<hash>，标准 base64。参数字
// 为 log2(N)<<16 | r<<8 | p（如 e0801 = N=2^14、r=8、p=1），派生长度
// 取哈希段实际长度。scrypt 非当前首选，校验成功即要求重哈希。
func verifyLegacyScrypt(storedHash, plain string) (bool, bool, error) {
	body := strings.TrimPrefix(storedHash, "{scrypt}$")
	parts := strings.Split(body, "$")
	if len(parts) != 3 {
		return false, false, ErrPasswordHashUnsupported
	}
	params, err := strconv.ParseUint(parts[0], 16, 32)
	if err != nil {
		return false, false, ErrPasswordHashUnsupported
	}
	logN := params >> 16 & 0xffff
	r := params >> 8 & 0xff
	p := params & 0xff
	if logN == 0 || logN > 63 {
		return false, false, ErrPasswordHashUnsupported
	}
	salt, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return false, false, ErrPasswordHashUnsupported
	}
	want, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		return false, false, ErrPasswordHashUnsupported
	}
	key, err := scrypt.Key([]byte(plain), salt, 1<<logN, int(r), int(p), len(want))
	if err != nil {
		return false, false, ErrPasswordHashUnsupported
	}
	ok := subtle.ConstantTimeCompare(key, want) == 1
	return ok, ok, nil
}
