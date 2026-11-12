package conf

import (
	"errors"
	"strings"
	"testing"

	"grape/pkg/secret"
)

// 启动配置密钥登记表 → pkg/secret 注册表的装配适配测试：结构转换成功、
// write 指针缺失/指向未登记版本拒绝、材料非法错误冒泡（校验语义本身在
// pkg/secret 测）。

// testKey 生成可通过装配校验的对称密钥（base64 的 32 字节）。
func testKey(t *testing.T) string {
	t.Helper()
	m, err := secret.Generate(secret.KindAES256GCM)
	if err != nil {
		t.Fatalf("Generate(AES-256-GCM): %v", err)
	}
	return m.Key
}

// secretGroup 构造单个业务键组。
func secretGroup(write string, versions map[string]*SecretVersion) *SecretGroup {
	return &SecretGroup{Write: write, Versions: versions}
}

// aesVersion 构造合法的对称算法版本条目。
func aesVersion(key string) *SecretVersion {
	return &SecretVersion{Kind: string(secret.KindAES256GCM), Key: key}
}

func TestNewSecretRegistry_valid_conf_builds_usable_registry(t *testing.T) {
	key := testKey(t)
	reg, err := NewSecretRegistry(map[string]*SecretGroup{
		"module_key_encrypt": secretGroup("v1", map[string]*SecretVersion{
			"v1": aesVersion(key),
		}),
	})
	if err != nil {
		t.Fatalf("NewSecretRegistry(valid) = %v, want nil", err)
	}
	sealer, err := reg.Sealer("module_key_encrypt")
	if err != nil {
		t.Fatalf("Sealer(module_key_encrypt): %v", err)
	}
	ct, err := sealer.Seal("payload")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if !strings.HasPrefix(ct, "module_key_encrypt@v1:") {
		t.Fatalf("ciphertext %q does not carry the write version header", ct)
	}
	if got, err := reg.Open(ct); err != nil || got != "payload" {
		t.Fatalf("Open = (%q, %v), want payload", got, err)
	}
}

func TestNewSecretRegistry_rejects_invalid_write_pointer(t *testing.T) {
	key := testKey(t)
	cases := map[string]*SecretGroup{
		"missing write":               secretGroup("", map[string]*SecretVersion{"v1": aesVersion(key)}),
		"write to undeclared version": secretGroup("v2", map[string]*SecretVersion{"v1": aesVersion(key)}),
	}
	for name, group := range cases {
		_, err := NewSecretRegistry(map[string]*SecretGroup{"module_key_encrypt": group})
		if err == nil || !strings.Contains(err.Error(), "conf: secret registry:") {
			t.Fatalf("%s: NewSecretRegistry = %v, want wrapped assembly error", name, err)
		}
	}
}

func TestNewSecretRegistry_bubbles_material_validation(t *testing.T) {
	shortKey := "MDEyMzQ1Njc4OWFiY2RlZg==" // 16 bytes
	cases := map[string]*SecretVersion{
		"invalid base64": {Kind: string(secret.KindAES256GCM), Key: "not base64!!"},
		"short key":      aesVersion(shortKey),
		"kind family":    {Kind: "AES", Key: testKey(t)},
		"mixed fields":   {Kind: string(secret.KindAES256GCM), Key: testKey(t), PublicKey: "cHVibGlj"},
	}
	for name, version := range cases {
		_, err := NewSecretRegistry(map[string]*SecretGroup{
			"module_key_encrypt": secretGroup("v1", map[string]*SecretVersion{"v1": version}),
		})
		if err == nil {
			t.Fatalf("%s: NewSecretRegistry = nil, want material validation error", name)
		}
		if errors.Is(err, secret.ErrUnavailable) {
			t.Fatalf("%s: assembly error matched runtime sentinel, want a distinct assembly error", name)
		}
	}
}

func TestNewSecretRegistry_rejects_empty_entries(t *testing.T) {
	// 每个用例同时断言错误文本能定位到具体的业务键或版本，避免装配失败
	// 时只有一句泛化的「配置非法」而无从排查。
	cases := []struct {
		name string
		cfg  map[string]*SecretGroup
		want string
	}{
		{
			name: "empty table",
			cfg:  nil,
			want: "at least one business key",
		},
		{
			name: "nil group",
			cfg:  map[string]*SecretGroup{"module_key_encrypt": nil},
			want: "secret.module_key_encrypt",
		},
		{
			name: "nil version",
			cfg:  map[string]*SecretGroup{"module_key_encrypt": secretGroup("v1", map[string]*SecretVersion{"v1": nil})},
			want: "versions.v1",
		},
		{
			name: "empty group",
			cfg:  map[string]*SecretGroup{"module_key_encrypt": secretGroup("v1", nil)},
			want: "at least one version",
		},
		{
			name: "empty versions map",
			cfg:  map[string]*SecretGroup{"module_key_encrypt": secretGroup("v1", map[string]*SecretVersion{})},
			want: "at least one version",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewSecretRegistry(tc.cfg)
			if err == nil {
				t.Fatalf("NewSecretRegistry = nil, want error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("NewSecretRegistry error = %q, want it to locate %q", err, tc.want)
			}
		})
	}
}
