package configinit

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"grape/internal/conf"
	"grape/pkg/secret"

	"github.com/go-kratos/kratos/v3/config"
	"github.com/go-kratos/kratos/v3/config/env"
	"github.com/go-kratos/kratos/v3/config/file"
)

// writeTemplateToDir 把内嵌模板按原样写到 dir/config.yaml，模拟 configinit
// 子命令的输出结果。
func writeTemplateToDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, outputName), templateConfig, 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}
}

// loadBootstrapViaServerPath 复用 serve 完全相同的加载路径
// （file source + env source + 默认 ${ENV:default} 展开 + protojson Scan）
// 把 dir 下的配置解析为 conf.Bootstrap，验证模板与运行解析面一致。
func loadBootstrapViaServerPath(t *testing.T, dir string) *conf.Bootstrap {
	t.Helper()
	c := config.New(
		config.WithSource(
			file.NewSource(dir),
			env.NewSource("KRATOS"),
		),
	)
	t.Cleanup(func() { c.Close() })
	if err := c.Load(); err != nil {
		t.Fatalf("load config: %v", err)
	}
	var bc conf.Bootstrap
	if err := c.Scan(&bc); err != nil {
		t.Fatalf("scan bootstrap: %v", err)
	}
	return &bc
}

func Test_template_when_loaded_by_server_path_yields_required_sections(t *testing.T) {
	dir := t.TempDir()
	writeTemplateToDir(t, dir)

	bc := loadBootstrapViaServerPath(t, dir)

	if got := bc.GetServer().GetHttp().GetAddr(); got != "0.0.0.0:8000" {
		t.Errorf("server.http.addr = %q, want %q", got, "0.0.0.0:8000")
	}
	// 全局传输超时保持 0（SSE 长连接），普通路由限时由 request_timeout 承担。
	if got := bc.GetServer().GetHttp().GetTimeout().AsDuration(); got != 0 {
		t.Errorf("server.http.timeout = %v, want 0s (SSE governance)", got)
	}
	if got := bc.GetServer().GetHttp().GetRequestTimeout().AsDuration(); got != time.Second {
		t.Errorf("server.http.request_timeout = %v, want %v", got, time.Second)
	}
	if got := bc.GetServer().GetGrpc().GetAddr(); got != "0.0.0.0:9000" {
		t.Errorf("server.grpc.addr = %q, want %q", got, "0.0.0.0:9000")
	}
	if got := bc.GetServer().GetGrpc().GetTimeout().AsDuration(); got != time.Second {
		t.Errorf("server.grpc.timeout = %v, want %v", got, time.Second)
	}
	if got := bc.GetData().GetDatabase().GetMaster().GetSource(); !strings.HasPrefix(got, "postgres://") {
		t.Errorf("data.database.master.source = %q, want a postgres:// DSN", got)
	}
	if got := bc.GetData().GetDatabase().GetMaster().GetDebug(); !got {
		t.Errorf("data.database.master.debug = %v, want true", got)
	}
	if got := bc.GetData().GetDatabase().GetSystem().GetSource(); got == "" {
		t.Errorf("data.database.system.source is empty, want a DSN")
	}
	if got := bc.GetAuth().GetDefaultTenantId(); got != "t-super-01" {
		t.Errorf("auth.default_tenant_id = %q, want %q", got, "t-super-01")
	}
	trusted := bc.GetServer().GetTrace().GetTrustedNetworks()
	if len(trusted) != 2 || trusted[0] != "127.0.0.1/32" || trusted[1] != "::1/128" {
		t.Errorf("server.trace.trusted_networks = %v, want [127.0.0.1/32 ::1/128]", trusted)
	}
}

func Test_template_llm_reliability_sections_when_loaded_default_to_off(t *testing.T) {
	// 模型层可靠性段（failover / hedge / logging）在模板中恒默认
	// 关闭：全缺省装配为零包装的裸 provider，密钥项只经环境变量占位。
	dir := t.TempDir()
	writeTemplateToDir(t, dir)

	bc := loadBootstrapViaServerPath(t, dir)

	llm := bc.GetAgent().GetLlm()
	if got := llm.GetFailover().GetEnabled(); got {
		t.Errorf("template llm.failover.enabled = true, want false (zero wrapping default)")
	}
	if got := len(llm.GetFailover().GetCandidates()); got != 0 {
		t.Errorf("template llm.failover.candidates = %d entries, want 0", got)
	}
	if got := llm.GetHedge().GetEnabled(); got {
		t.Errorf("template llm.hedge.enabled = true, want false (zero wrapping default)")
	}
	if got := llm.GetLogging().GetEnabled(); got {
		t.Errorf("template llm.logging.enabled = true, want false (zero wrapping default)")
	}
	if got := llm.GetLogging().GetLogPrompts(); got {
		t.Errorf("template llm.logging.log_prompts = true, want false (no prompt content by default)")
	}
	if got := llm.GetLogging().GetLogCompletions(); got {
		t.Errorf("template llm.logging.log_completions = true, want false (no completion content by default)")
	}
}

func Test_existing_target_when_run_without_force_refuses_overwrite(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, outputName)
	sentinel := []byte("sentinel: keep me\n")
	if err := os.WriteFile(target, sentinel, 0o644); err != nil {
		t.Fatalf("prepare existing target: %v", err)
	}

	err := writeTemplate(dir, false, &bytes.Buffer{})

	if err == nil {
		t.Fatal("run without --force on existing target should fail, got nil error")
	}
	if !strings.Contains(err.Error(), "already exists") || !strings.Contains(err.Error(), "--force") {
		t.Errorf("error = %q, want it to mention the existing file and --force", err.Error())
	}
	got, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatalf("read back target: %v", readErr)
	}
	if !bytes.Equal(got, sentinel) {
		t.Errorf("target content = %q, want it untouched (%q)", got, sentinel)
	}
}

func Test_existing_target_when_run_with_force_overwrites(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, outputName)
	if err := os.WriteFile(target, []byte("stale content\n"), 0o644); err != nil {
		t.Fatalf("prepare existing target: %v", err)
	}

	if err := writeTemplate(dir, true, &bytes.Buffer{}); err != nil {
		t.Fatalf("run with --force: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read back target: %v", err)
	}
	if !bytes.Equal(got, templateConfig) {
		t.Errorf("target content differs from embedded template after overwrite")
	}
}

func Test_empty_dir_when_run_writes_template_byte_for_byte(t *testing.T) {
	dir := t.TempDir()

	if err := writeTemplate(dir, false, &bytes.Buffer{}); err != nil {
		t.Fatalf("run on empty dir: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, outputName))
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if !bytes.Equal(got, templateConfig) {
		t.Errorf("output differs from embedded template byte-for-byte (length got=%d want=%d)", len(got), len(templateConfig))
	}
}

func Test_missing_dir_when_run_creates_it(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "parent", "child")

	if err := writeTemplate(dir, false, &bytes.Buffer{}); err != nil {
		t.Fatalf("run on missing dir: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, outputName))
	if err != nil {
		t.Fatalf("output should exist after run: %v", err)
	}
	if info.IsDir() {
		t.Errorf("output path %s is a directory, want a file", filepath.Join(dir, outputName))
	}
}

func Test_dir_path_colliding_with_existing_file_when_run_fails(t *testing.T) {
	dir := t.TempDir()
	collision := filepath.Join(dir, "occupied")
	if err := os.WriteFile(collision, []byte("not a directory\n"), 0o644); err != nil {
		t.Fatalf("prepare collision file: %v", err)
	}

	err := writeTemplate(collision, false, &bytes.Buffer{})

	if err == nil {
		t.Fatal("run with -dir pointing at a file should fail, got nil error")
	}
	if !strings.Contains(err.Error(), "create output directory") {
		t.Errorf("error = %q, want it to mention directory creation failure", err.Error())
	}
}

func Test_run_when_extra_argument_rejected(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"unexpected"}, &stdout, &stderr); code != 2 {
		t.Fatalf("Run(unexpected arg) = %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "unexpected argument") {
		t.Errorf("stderr = %q, want it to name the unexpected argument", stderr.String())
	}
}

func Test_template_when_configs_body_changes_fails_loudly(t *testing.T) {
	// 模板正文必须与 configs/config.yaml 逐字节一致（模板仅多出头部说明注释），
	// conf 变更时同批更新两份文件。
	content, err := os.ReadFile(filepath.Join("..", "..", "..", "configs", "config.yaml"))
	if err != nil {
		t.Fatalf("read configs/config.yaml: %v", err)
	}
	if !bytes.HasSuffix(templateConfig, content) {
		t.Errorf("template/config.yaml diverged from configs/config.yaml body; update both in the same commit")
	}
}

// defaultModelKey 与模板占位默认值一致（32 字节本地开发密钥），用于验证
// 环境变量覆盖确实生效。
const defaultModelKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="

// overrideModelKey 是另一份 32 字节 base64 密钥，经 KRATOS_ 前缀注入。
const overrideModelKey = "QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVowMTIzNDU="

func Test_template_secret_registry_when_loaded_passes_assembly_validation(t *testing.T) {
	// 模板经 serve 相同加载路径解析出密钥登记表，且默认材料通过装配期
	// 校验并可用于加解密。
	dir := t.TempDir()
	writeTemplateToDir(t, dir)

	bc := loadBootstrapViaServerPath(t, dir)

	group := bc.GetSecret()["module_key_encrypt"]
	if group == nil {
		t.Fatal("secret.module_key_encrypt group is missing from the template")
	}
	if got := group.GetWrite(); got != "v1" {
		t.Errorf("secret.module_key_encrypt.write = %q, want %q", got, "v1")
	}
	version := group.GetVersions()["v1"]
	if version == nil {
		t.Fatal("secret.module_key_encrypt.versions.v1 is missing from the template")
	}
	if got := version.GetKind(); got != "AES-256-GCM" {
		t.Errorf("secret.module_key_encrypt.versions.v1.kind = %q, want %q", got, "AES-256-GCM")
	}
	if got := version.GetKey(); got == "" {
		t.Error("secret.module_key_encrypt.versions.v1.key is empty, want the placeholder default")
	}

	registry, err := conf.NewSecretRegistry(bc.GetSecret())
	if err != nil {
		t.Fatalf("NewSecretRegistry(template) = %v, want nil", err)
	}
	sealer, err := registry.Sealer("module_key_encrypt")
	if err != nil {
		t.Fatalf("Sealer(module_key_encrypt): %v", err)
	}
	ct, err := sealer.Seal("payload")
	if err != nil {
		t.Fatalf("Seal with template registry: %v", err)
	}
	if got, err := registry.Open(ct); err != nil || got != "payload" {
		t.Fatalf("Open with template registry = (%q, %v), want payload", got, err)
	}
}

func Test_template_secret_registry_when_env_override_applied(t *testing.T) {
	// env source 仅识别 KRATOS_ 前缀：KRATOS_MODULE_KEY_ENCRYPT_V1 覆盖
	// 占位默认值，注册表实际使用覆盖后的材料。
	t.Setenv("KRATOS_MODULE_KEY_ENCRYPT_V1", overrideModelKey)
	dir := t.TempDir()
	writeTemplateToDir(t, dir)

	bc := loadBootstrapViaServerPath(t, dir)

	group := bc.GetSecret()["module_key_encrypt"]
	if group == nil {
		t.Fatal("secret.module_key_encrypt group is missing from the template")
	}
	if got := group.GetVersions()["v1"].GetKey(); got != overrideModelKey {
		t.Fatalf("secret.module_key_encrypt.versions.v1.key = %q, want the overridden value", got)
	}

	registry, err := conf.NewSecretRegistry(bc.GetSecret())
	if err != nil {
		t.Fatalf("NewSecretRegistry(overridden template) = %v, want nil", err)
	}
	sealer, err := registry.Sealer("module_key_encrypt")
	if err != nil {
		t.Fatalf("Sealer(module_key_encrypt): %v", err)
	}
	ct, err := sealer.Seal("payload")
	if err != nil {
		t.Fatalf("Seal with overridden registry: %v", err)
	}
	if got, err := registry.Open(ct); err != nil || got != "payload" {
		t.Fatalf("Open with overridden registry = (%q, %v), want payload", got, err)
	}

	// 仓库内置默认材料构造的注册表不得解开覆盖后的密文：证明覆盖真正
	// 进入了注册表，而非仅停留在配置字段。
	defaultRegistry, err := conf.NewSecretRegistry(map[string]*conf.SecretGroup{
		"module_key_encrypt": {
			Write: "v1",
			Versions: map[string]*conf.SecretVersion{
				"v1": {Kind: "AES-256-GCM", Key: defaultModelKey},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewSecretRegistry(default material) = %v, want nil", err)
	}
	if _, err := defaultRegistry.Open(ct); !errors.Is(err, secret.ErrUnavailable) {
		t.Fatalf("Open(overridden ciphertext) with default material = %v, want ErrUnavailable", err)
	}
}
