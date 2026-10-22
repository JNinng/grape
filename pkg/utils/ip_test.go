package utils

import (
	"errors"
	"net"
	"strings"
	"testing"
)

// withEnv 注入 lookupEnv 替身：env 中存在的键返回其值，其余视为未设置。
func withEnv(t *testing.T, env map[string]string) {
	t.Helper()
	orig := lookupEnv
	lookupEnv = func(name string) (string, bool) {
		value, ok := env[name]
		return value, ok
	}
	t.Cleanup(func() { lookupEnv = orig })
}

// withAddrs 注入 interfaceAddrs 替身：ifaceErr 非 nil 时模拟枚举失败。
func withAddrs(t *testing.T, addrs []net.Addr, ifaceErr error) {
	t.Helper()
	orig := interfaceAddrs
	interfaceAddrs = func() ([]net.Addr, error) { return addrs, ifaceErr }
	t.Cleanup(func() { interfaceAddrs = orig })
}

// fakeIPNet 便捷构造 *net.IPNet 形式的网卡地址。
func fakeIPNet(ip net.IP) net.Addr {
	return &net.IPNet{IP: ip, Mask: net.CIDRMask(24, 32)}
}

func TestResolveLocalIP(t *testing.T) {
	loopbackV4 := net.ParseIP("127.0.0.1")
	siteIP := net.ParseIP("10.0.0.5")

	tests := []struct {
		name       string
		env        map[string]string
		addrs      []net.Addr
		ifaceErr   error
		wantIP     net.IP // 期望返回的 IPv4；nil 表示期望失败
		wantErrSub string // 期望错误信息包含的片段；空表示期望成功
	}{
		{
			name:   "INSTANCE_IP合法时直接采用",
			env:    map[string]string{"INSTANCE_IP": "192.168.1.10"},
			addrs:  []net.Addr{fakeIPNet(siteIP)}, // 枚举被注入但不应被触达
			wantIP: net.ParseIP("192.168.1.10"),
		},
		{
			name:   "INSTANCE_IP非法时回落POD_IP",
			env:    map[string]string{"INSTANCE_IP": "not-an-ip", "POD_IP": "10.0.0.5"},
			addrs:  []net.Addr{fakeIPNet(siteIP)},
			wantIP: siteIP,
		},
		{
			name:   "两个env均非法时回落网卡枚举",
			env:    map[string]string{"INSTANCE_IP": "", "POD_IP": "300.1.2.3"},
			addrs:  []net.Addr{fakeIPNet(net.ParseIP("fe80::1")), fakeIPNet(loopbackV4), fakeIPNet(siteIP)},
			wantIP: siteIP,
		},
		{
			name:   "回环env视为非法并回落网卡",
			env:    map[string]string{"INSTANCE_IP": "127.0.0.1"},
			addrs:  []net.Addr{fakeIPNet(siteIP)},
			wantIP: siteIP,
		},
		{
			name:   "IPv6env视为非法并回落网卡",
			env:    map[string]string{"POD_IP": "::1"},
			addrs:  []net.Addr{fakeIPNet(siteIP)},
			wantIP: siteIP,
		},
		{
			name:       "无env且网卡为空时报错",
			env:        map[string]string{},
			addrs:      []net.Addr{fakeIPNet(loopbackV4), fakeIPNet(net.ParseIP("::1"))},
			wantErrSub: "网卡中无非回环 IPv4",
		},
		{
			name:       "网卡枚举失败时报错",
			env:        map[string]string{},
			ifaceErr:   errors.New("boom"),
			wantErrSub: "枚举网卡失败",
		},
		{
			name:       "错误信息附带各env被拒原因",
			env:        map[string]string{"INSTANCE_IP": "bad", "POD_IP": "127.0.0.1"},
			addrs:      nil,
			wantErrSub: "回环",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withEnv(t, tt.env)
			withAddrs(t, tt.addrs, tt.ifaceErr)

			got, err := ResolveLocalIP()
			if tt.wantErrSub == "" {
				if err != nil {
					t.Fatalf("ResolveLocalIP() 意外出错: %v", err)
				}
				if got.To4() == nil || !got.Equal(tt.wantIP) {
					t.Fatalf("ResolveLocalIP() = %v, 期望 %v", got, tt.wantIP)
				}
				return
			}
			if err == nil {
				t.Fatalf("ResolveLocalIP() 意外成功, 期望错误包含 %q", tt.wantErrSub)
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("错误信息 %q 未包含 %q", err.Error(), tt.wantErrSub)
			}
			if got != nil {
				t.Fatalf("失败时返回值应为 nil, 实际为 %v", got)
			}
		})
	}
}
