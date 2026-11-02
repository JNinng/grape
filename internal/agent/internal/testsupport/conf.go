package testsupport

import (
	"time"

	"grape/internal/conf"

	"google.golang.org/protobuf/types/known/durationpb"
)

// 确认流 / 审计 / 并发 / 授权等骨架级测试共用的身份与会话键要素：
// app 名、租户与用户（userKey = t-1:7）。
const (
	// AppName 是骨架级测试共用的 app 名（会话存储键的组成部分）。
	AppName = "grape-agent"
	// Tenant 是归属者与旁观者共用的租户。
	Tenant = "t-1"
	// OwnerID 是归属者用户 ID；StrangerID 是同租户旁观用户 ID。
	OwnerID    = int64(7)
	StrangerID = int64(8)
	// RunTimeout 是单次运行的守门超时：确认流用例在决定到达前会阻塞
	// 运行，该值只保证阻塞不至挂死，用例节奏由测试自身控制。
	RunTimeout = 30 * time.Second
	// WaitLimit 是轮询等待确认登记/清理的总时限。
	WaitLimit = 10 * time.Second
)

// StackConf 组装骨架级测试的配置：短运行超时只影响挂死兜底，用例本身
// 靠显式时限推进。
func StackConf() *conf.Agent {
	return &conf.Agent{
		AppName: AppName,
		Llm:     &conf.Agent_LLM{Model: "stub"},
		Agui: &conf.Agent_AGUI{
			BasePath:          "/agui",
			Timeout:           durationpb.New(RunTimeout),
			HeartbeatInterval: durationpb.New(100 * time.Millisecond),
		},
	}
}
