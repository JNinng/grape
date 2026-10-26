package agent

import (
	"context"
	"encoding/json"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestEchoToolReturnsTextVerbatim(t *testing.T) {
	echoTool := newEchoTool()
	if echoTool.Declaration() == nil || echoTool.Declaration().Name != echoToolName {
		t.Fatalf("tool declaration name = %v, want %q", echoTool.Declaration(), echoToolName)
	}
	raw, err := json.Marshal(echoInput{Text: "ping"})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	out, err := echoTool.(tool.CallableTool).Call(context.Background(), raw)
	if err != nil {
		t.Fatalf("call echo tool: want no error, got %v", err)
	}
	echo, ok := out.(echoOutput)
	if !ok || echo.Echo != "ping" {
		t.Fatalf("echo result = %#v, want echoOutput{Echo: \"ping\"}", out)
	}
}

func TestEchoToolRejectsBlankText(t *testing.T) {
	raw, err := json.Marshal(echoInput{Text: "   "})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	_, err = newEchoTool().(tool.CallableTool).Call(context.Background(), raw)
	if err == nil {
		t.Fatalf("blank text: want error, got none")
	}
}
