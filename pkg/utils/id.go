package utils

import (
	"encoding/binary"
	"math/rand"
	"os"
	"strconv"
	"sync"

	"go.opentelemetry.io/otel/trace"
)

// resolveIP 以包级变量间接引用 IP 解析，测试中可替换替身，
// 使 InstanceId 的「IP-PID」与「纯 PID」两条路径不依赖真实环境。
var resolveIP = ResolveLocalIP

// newInstanceID 计算实例 ID：本机 IPv4 与进程 PID 以「-」相连，如 10.0.0.5-12345；
// IP 解析失败时退化为纯 PID，如 12345。两种形态以是否含「-」区分。
func newInstanceID() string {
	pid := strconv.Itoa(os.Getpid())
	ip, err := resolveIP()
	if err != nil {
		return pid
	}
	return ip.String() + "-" + pid
}

// instanceID 固化实例 ID：首次调用即完成计算（含失败退化），此后恒返回同一值。
// 不做失败重试、不随环境变化重算——实例 ID 的契约是进程内稳定，
// IP 事后恢复可用而换一个身份，比持续缺 IP 危害更大。
var instanceID = sync.OnceValue(newInstanceID)

// InstanceId 返回当前进程的实例 ID，拼装格式见 newInstanceID。
// 结果在进程生命周期内恒定，即使 IP 不可用而退化为纯 PID 亦不重试。
func InstanceId() string {
	return instanceID()
}

// NewTraceID 生成合法的 otel TraceID：两次 rand.Uint64()（Go 1.20+ 运行时自动播种）
// 经 binary.NativeEndian 填满 16 字节，字节序不影响结果（消费方只按字节串与十六进制解读）；
// 全零对 otel 非法（概率 2^-128），循环重生成直至 IsValid 兜底。
func NewTraceID() trace.TraceID {
	var tid trace.TraceID
	for {
		binary.NativeEndian.PutUint64(tid[:8], rand.Uint64())
		binary.NativeEndian.PutUint64(tid[8:], rand.Uint64())
		if tid.IsValid() {
			return tid
		}
	}
}
