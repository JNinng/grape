package agent

import (
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// echoToolName 是 B1 骨架工具名：模型可见、ack 分级与恢复路径解析都
// 以它为键。
const echoToolName = "echo"

// echoInput / echoOutput 是 echo 工具的入参与出参。只读、无副作用，
// 用于 B1 打通模型 → 工具 → SSE 事件流的端到端链路；C1 起业务工具
// 经 biz 用例接入，不再在本层堆积。
type echoInput struct {
	Text string `json:"text"`
}

type echoOutput struct {
	Echo string `json:"echo"`
}

// newEchoTool 构造最小只读工具，走框架 function 工具注册：入参 schema
// 由框架按 Go 类型推导，模型可见的声明含参数描述。
func newEchoTool() tool.Tool {
	return function.NewFunctionTool(
		func(_ context.Context, in echoInput) (echoOutput, error) {
			if strings.TrimSpace(in.Text) == "" {
				return echoOutput{}, fmt.Errorf("text is required")
			}
			return echoOutput{Echo: in.Text}, nil
		},
		function.WithName(echoToolName),
		function.WithDescription("Echo back the given text verbatim. "+
			"Use it to verify the tool-calling pipeline; the text field is required."),
	)
}
