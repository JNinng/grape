// Package utils 提供与业务无关的基础工具函数。
package utils

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
)

// 环境变量读取与网卡枚举以包级变量间接，测试中可替换替身，
// 使 ResolveLocalIP 的行为不依赖真实环境与网卡。
var (
	lookupEnv      = os.LookupEnv
	interfaceAddrs = net.InterfaceAddrs
)

// envIPNames 是本机 IP 候选环境变量，按优先级从高到低排列。
var envIPNames = [...]string{"INSTANCE_IP", "POD_IP"}

// ResolveLocalIP 解析本机非回环 IPv4，按 INSTANCE_IP → POD_IP →
// net.InterfaceAddrs 首个非回环 IPv4 依次取用。环境变量已设置但非法
// （解析失败、非 IPv4 或回环）时不采用、回落下一来源；全部来源不可用时
// 返回含各来源拒绝原因的错误，便于部署排查。返回值恒为 4 字节 IPv4。
func ResolveLocalIP() (net.IP, error) {
	var reasons []string
	for _, name := range envIPNames {
		value, ok := lookupEnv(name)
		if !ok {
			reasons = append(reasons, name+" not set")
			continue
		}
		ip := net.ParseIP(value)
		switch {
		case ip == nil:
			reasons = append(reasons, fmt.Sprintf("%s=%q is not a valid IP address", name, value))
		case ip.To4() == nil:
			reasons = append(reasons, fmt.Sprintf("%s=%q is not an IPv4 address", name, value))
		case ip.IsLoopback():
			reasons = append(reasons, fmt.Sprintf("%s=%q is a loopback address", name, value))
		default:
			return ip.To4(), nil
		}
	}

	addrs, err := interfaceAddrs()
	if err != nil {
		reasons = append(reasons, fmt.Sprintf("enumerate network interfaces: %v", err))
		return nil, errors.New("resolve local IPv4 failed: " + strings.Join(reasons, "; "))
	}
	for _, addr := range addrs {
		ipnet, ok := addr.(*net.IPNet)
		if !ok || ipnet.IP == nil || ipnet.IP.To4() == nil || ipnet.IP.IsLoopback() {
			continue
		}
		return ipnet.IP.To4(), nil
	}
	reasons = append(reasons, "no non-loopback IPv4 address on network interfaces")
	return nil, errors.New("resolve local IPv4 failed: " + strings.Join(reasons, "; "))
}
