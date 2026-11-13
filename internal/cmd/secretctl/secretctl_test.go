package secretctl

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"grape/pkg/secret"
)

// 密钥运维子命令单测：算法清单与支持集合同源、keygen 输出结构可直接填入
// 登记表、生成材料可通过装配期校验并往返加解密、未知算法拒绝。

func TestAlgorithmsMatchesSupportedKinds(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"algorithms"}, &stdout, &stderr); code != 0 {
		t.Fatalf("Run(algorithms) = %d, stderr = %q", code, stderr.String())
	}
	want := make([]string, 0, len(secret.SupportedKinds()))
	for _, kind := range secret.SupportedKinds() {
		want = append(want, string(kind))
	}
	got := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("algorithms = %v, want %v", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestKeygenSymmetricOutputAndMaterialUsable(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"keygen", string(secret.KindAES256GCM)}, &stdout, &stderr); code != 0 {
		t.Fatalf("Run(keygen) = %d, stderr = %q", code, stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("keygen output lines = %v, want exactly kind + key", lines)
	}
	if lines[0] != "kind: "+string(secret.KindAES256GCM) {
		t.Fatalf("first line = %q, want kind header", lines[0])
	}
	value, ok := strings.CutPrefix(lines[1], "key: ")
	if !ok {
		t.Fatalf("second line = %q, want %q field", lines[1], "key: ")
	}
	key, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		t.Fatalf("key is not valid base64: %v", err)
	}
	if len(key) != 32 {
		t.Fatalf("key decodes to %d bytes, want 32", len(key))
	}

	// 生成的输出可直接填入登记表并通过装配期校验，往返加解密一致。
	reg, err := secret.New(
		map[string]map[string]secret.Material{
			"module_key_encrypt": {
				"v1": {Kind: secret.KindAES256GCM, Key: value},
			},
		},
		map[string]string{"module_key_encrypt": "v1"},
	)
	if err != nil {
		t.Fatalf("secret.New rejects generated material: %v", err)
	}
	sealer, err := reg.Sealer("module_key_encrypt")
	if err != nil {
		t.Fatalf("Sealer: %v", err)
	}
	ciphertext, err := sealer.Seal("sk-round-trip")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	plain, err := reg.Open(ciphertext)
	if err != nil || plain != "sk-round-trip" {
		t.Fatalf("Open = (%q, %v), want round-trip", plain, err)
	}
}

// TestMaterialLinesOrdersFields 固定 keygen 的行序列契约：kind 首行；对称
// 材料一行 key；非对称材料固定先 public_key 后 private_key（顺序与值的
// 对应关系不能互换）。
func TestMaterialLinesOrdersFields(t *testing.T) {
	symmetric := secret.Material{
		Kind: secret.KindAES256GCM,
		Key:  "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
	}
	wantSymmetric := []string{
		"kind: AES-256-GCM",
		"key: MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
	}
	if got := materialLines(symmetric); !equalLines(got, wantSymmetric) {
		t.Fatalf("materialLines(symmetric) = %v, want %v", got, wantSymmetric)
	}

	// 非对称占位材料：当前没有真实非对称算法，行序列契约与算法实现解耦。
	asymmetric := secret.Material{
		Kind:       secret.Kind("Ed25519"),
		PublicKey:  "cHVibGljLWtleQ==",
		PrivateKey: "cHJpdmF0ZS1rZXk=",
	}
	wantAsymmetric := []string{
		"kind: Ed25519",
		"public_key: cHVibGljLWtleQ==",
		"private_key: cHJpdmF0ZS1rZXk=",
	}
	if got := materialLines(asymmetric); !equalLines(got, wantAsymmetric) {
		t.Fatalf("materialLines(asymmetric) = %v, want %v", got, wantAsymmetric)
	}
}

// equalLines 逐行比较行序列（行数与内容）。
func equalLines(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestKeygenUnknownKindRejected(t *testing.T) {
	for _, args := range [][]string{
		{"keygen", "AES"},
		{"keygen", "unknown"},
		{"keygen"},
	} {
		var stdout, stderr bytes.Buffer
		if code := Run(args, &stdout, &stderr); code == 0 {
			t.Fatalf("Run(%v) = 0, want non-zero exit", args)
		}
		if stdout.Len() != 0 {
			t.Fatalf("Run(%v) wrote stdout %q, want no key material", args, stdout.String())
		}
		if stderr.Len() == 0 {
			t.Fatalf("Run(%v) wrote no stderr, want an error message", args)
		}
	}
}
