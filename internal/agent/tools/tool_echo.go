// Package tools 承载业务工具与向量访问适配器：echo 链路验证工具、用户
// 工具、检索工具与 biz 向量契约 → 框架向量存储的形状适配器。工具在本包
// 声明、内部调 biz 用例完成数据访问（RLS/@scope 由存储层会话门面强制，
// 工具不直连 SQL、不感知租户过滤）；执行身份取认证 ctx（identity 包）。
// 构造函数由根包骨架（stack.go）装配进 LLMAgent 与 ack 网关，包外不得
// 自行拼装工具清单。
package tools

import (
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// EchoToolName 是骨架工具名：模型可见、ack 分级与恢复路径解析都
// 以它为键。
const EchoToolName = "echo"

// echoInput / echoOutput 是 echo 工具的入参与出参。只读、无副作用，
// 用于打通模型 → 工具 → SSE 事件流的端到端链路；业务工具
// 经 biz 用例接入，不再在本层堆积。
type echoInput struct {
	Text string `json:"text" jsonschema:"description=Text to echo back,required"`
}

type echoOutput struct {
	Echo string `json:"echo" jsonschema:"description=The echoed text"`
}

// NewEchoTool 构造最小只读工具，走框架 function 工具注册：入参 schema
// 由框架按 Go 类型推导，模型可见的声明含参数描述。
func NewEchoTool() tool.Tool {
	return function.NewFunctionTool(
		func(_ context.Context, in echoInput) (echoOutput, error) {
			if strings.TrimSpace(in.Text) == "" {
				return echoOutput{}, fmt.Errorf("text is required")
			}
			return echoOutput{Echo: in.Text}, nil
		},
		function.WithName(EchoToolName),
		function.WithDescription("Echo back the given text verbatim. "+
			"Use it to verify the tool-calling pipeline."),
	)
}
