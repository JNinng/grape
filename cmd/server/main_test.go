package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 子命令分发单测：已知子命令正确路由，未知子命令与错误用法拒绝退出，
// help/version 输出自描述信息。serve 路径涉及装配与端口监听，不在单测
// 覆盖范围内，仅验证非法 flag 在参数解析期即拒绝（不触发装配）。

func Test_run_when_unknown_command_reports_and_rejects(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"config-inits"}, &stdout, &stderr); code != 2 {
		t.Fatalf("run(unknown command) = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Errorf("stderr = %q, want it to name the unknown command", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
}

func Test_run_when_help_prints_usage_to_stdout(t *testing.T) {
	for _, arg := range []string{"help", "-h", "--help"} {
		var stdout, stderr bytes.Buffer
		if code := run([]string{arg}, &stdout, &stderr); code != 0 {
			t.Fatalf("run(%s) = %d, want 0", arg, code)
		}
		if !strings.Contains(stdout.String(), "usage:") {
			t.Errorf("run(%s) stdout = %q, want usage text", arg, stdout.String())
		}
		if stderr.Len() != 0 {
			t.Errorf("run(%s) stderr = %q, want empty", arg, stderr.String())
		}
	}
}

func Test_run_when_version_prints_build_info(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(version) = %d, stderr = %q", code, stderr.String())
	}
	// pkg/version.Info() 的单行 key=value 摘要，未注入时为占位默认值。
	for _, field := range []string{"version=", "commit=", "build_time=", "go="} {
		if !strings.Contains(stdout.String(), field) {
			t.Errorf("version output = %q, want it to contain %q", stdout.String(), field)
		}
	}
}

func Test_run_when_version_gets_extra_argument_rejects(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"version", "extra"}, &stdout, &stderr); code != 2 {
		t.Fatalf("run(version extra) = %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
}

func Test_run_routes_configinit(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"configinit", "-dir", dir}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(configinit) = %d, stderr = %q", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "config.yaml")); err != nil {
		t.Fatalf("configinit output should exist: %v", err)
	}
}

func Test_run_routes_secretctl(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"secretctl", "algorithms"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(secretctl algorithms) = %d, stderr = %q", code, stderr.String())
	}
	if stdout.Len() == 0 {
		t.Error("run(secretctl algorithms) wrote nothing, want the kind list")
	}
}

func Test_run_when_flag_first_falls_through_to_serve_arg_parsing(t *testing.T) {
	// 首参为 flag 时回落 serve：非法 flag 在参数解析期即被拒绝并返回 2，
	// 证明回落链路生效且不会误报「unknown command」。
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-definitely-not-a-flag"}, &stdout, &stderr); code != 2 {
		t.Fatalf("run(invalid flag) = %d, want 2", code)
	}
	if strings.Contains(stdout.String(), "unknown command") {
		t.Errorf("stdout = %q, flag-first args must not be treated as a subcommand", stdout.String())
	}
}
