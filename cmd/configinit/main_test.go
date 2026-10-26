package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"grape/internal/conf"

	"github.com/go-kratos/kratos/v3/config"
	"github.com/go-kratos/kratos/v3/config/env"
	"github.com/go-kratos/kratos/v3/config/file"
)

// writeTemplateToDir 把内嵌模板按原样写到 dir/config.yaml，模拟 configinit 的输出结果。
func writeTemplateToDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, outputName), templateConfig, 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}
}

// loadBootstrapViaServerPath 复用 cmd/server 完全相同的加载路径
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
	if got := bc.GetServer().GetHttp().GetTimeout().AsDuration(); got != time.Second {
		t.Errorf("server.http.timeout = %v, want %v", got, time.Second)
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
}

func Test_existing_target_when_run_without_force_refuses_overwrite(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, outputName)
	sentinel := []byte("sentinel: keep me\n")
	if err := os.WriteFile(target, sentinel, 0o644); err != nil {
		t.Fatalf("prepare existing target: %v", err)
	}

	err := run(dir, false, &bytes.Buffer{})

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

	if err := run(dir, true, &bytes.Buffer{}); err != nil {
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

	if err := run(dir, false, &bytes.Buffer{}); err != nil {
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

	if err := run(dir, false, &bytes.Buffer{}); err != nil {
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
