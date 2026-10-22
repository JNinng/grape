package version

import (
	"strings"
	"testing"
)

func TestInfo(t *testing.T) {
	if Version == "" || Commit == "" || BuildTime == "" {
		t.Fatalf("build info vars should have non-empty defaults: version=%q commit=%q build_time=%q", Version, Commit, BuildTime)
	}

	info := Info()
	if !strings.Contains(info, "version="+Version) {
		t.Fatalf("Info output should contain Version default %q: %s", Version, info)
	}
	if strings.ContainsAny(info, "\n\r") {
		t.Fatalf("Info output should be single-line: %q", info)
	}
}
