package utils

import (
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

// withResolveIP 注入 resolveIP 替身：ip 非 nil 时返回其值，否则返回 ipErr。
func withResolveIP(t *testing.T, ip net.IP, ipErr error) {
	t.Helper()
	orig := resolveIP
	resolveIP = func() (net.IP, error) { return ip, ipErr }
	t.Cleanup(func() { resolveIP = orig })
}

// resetInstanceID 重置实例 ID 缓存，使 InstanceId 以当前 resolveIP 重新计算，
// 各用例互不串味；结束时恢复原缓存。
func resetInstanceID(t *testing.T) {
	t.Helper()
	orig := instanceID
	instanceID = sync.OnceValue(newInstanceID)
	t.Cleanup(func() { instanceID = orig })
}

func TestNewTraceID(t *testing.T) {
	const samples = 1000
	seen := make(map[trace.TraceID]struct{}, samples)
	for i := 0; i < samples; i++ {
		id := NewTraceID()
		if !id.IsValid() {
			t.Fatalf("trace id #%d invalid (all zero): %s", i, id)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("trace id #%d duplicated: %s", i, id)
		}
		seen[id] = struct{}{}
	}
}

func TestNewSpanID(t *testing.T) {
	const samples = 1000
	seen := make(map[trace.SpanID]struct{}, samples)
	for i := 0; i < samples; i++ {
		id := NewSpanID()
		if !id.IsValid() {
			t.Fatalf("span id #%d invalid (all zero): %s", i, id)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("span id #%d duplicated: %s", i, id)
		}
		seen[id] = struct{}{}
	}
}

func TestInstanceId(t *testing.T) {
	pid := strconv.Itoa(os.Getpid())
	tests := []struct {
		name  string
		ip    net.IP
		ipErr error
		want  string
	}{
		{
			name: "ip-pid when IP available",
			ip:   net.ParseIP("10.0.0.5").To4(),
			want: "10.0.0.5-" + pid,
		},
		{
			name:  "bare pid when IP unavailable",
			ipErr: errors.New("boom"),
			want:  pid,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withResolveIP(t, tt.ip, tt.ipErr)
			resetInstanceID(t)
			if got := InstanceId(); got != tt.want {
				t.Fatalf("InstanceId() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestInstanceIdCached 验证缓存契约：实例 ID 只在首次调用时计算，
// 之后即使 IP 解析情况变化（此处为成功转失败）也不重算、不重试。
func TestInstanceIdCached(t *testing.T) {
	withResolveIP(t, net.ParseIP("10.0.0.5").To4(), nil)
	resetInstanceID(t)
	first := InstanceId()

	withResolveIP(t, nil, errors.New("boom"))
	if second := InstanceId(); second != first {
		t.Fatalf("InstanceId() second call = %q, inconsistent with first %q, cache not fixed", second, first)
	}
}

// TestInstanceIdShape 以真实环境校验实例 ID 形态：要么纯 PID（IP 不可用退化），
// 要么以「-PID」结尾（IP-PID 拼装）。
func TestInstanceIdShape(t *testing.T) {
	pid := strconv.Itoa(os.Getpid())
	got := InstanceId()
	if got == pid {
		return
	}
	if !strings.HasSuffix(got, "-"+pid) {
		t.Fatalf("InstanceId() = %q, neither bare pid nor suffixed with -%s", got, pid)
	}
}
