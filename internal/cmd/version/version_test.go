package version

import (
	"bytes"
	"strings"
	"testing"
)

// version 子命令单测：输出 pkg/version 的全部构建字段，多余参数被拒。

func Test_run_prints_all_build_fields(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run([]string{}, &stdout, &stderr); code != 0 {
		t.Fatalf("Run() = %d, stderr = %q", code, stderr.String())
	}
	// pkg/version.Info() 的单行 key=value 摘要，未注入时为占位默认值。
	for _, field := range []string{"version=", "commit=", "build_time=", "go="} {
		if !strings.Contains(stdout.String(), field) {
			t.Errorf("version output = %q, want it to contain %q", stdout.String(), field)
		}
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func Test_run_when_extra_argument_rejected(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"extra"}, &stdout, &stderr); code != 2 {
		t.Fatalf("Run(extra) = %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "unexpected argument") {
		t.Errorf("stderr = %q, want it to name the unexpected argument", stderr.String())
	}
}
