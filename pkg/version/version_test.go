package version

import (
	"strings"
	"testing"
)

func TestInfo(t *testing.T) {
	if Version == "" || Commit == "" || BuildTime == "" {
		t.Fatalf("构建信息变量应具有非空默认值: version=%q commit=%q build_time=%q", Version, Commit, BuildTime)
	}

	info := Info()
	if !strings.Contains(info, "version="+Version) {
		t.Fatalf("Info 输出应包含 Version 默认值 %q: %s", Version, info)
	}
	if strings.ContainsAny(info, "\n\r") {
		t.Fatalf("Info 输出应为单行: %q", info)
	}
}
