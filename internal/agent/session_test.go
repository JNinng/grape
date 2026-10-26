package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"grape/internal/conf"

	"google.golang.org/protobuf/types/known/durationpb"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

// TestNewSessionService_missing_dsn_when_conf_empty_fails_fast 验证装配
// 期 fail-fast：master 槽 DSN 缺失时拒绝构造（不触库即报错），错误指明
// 配置路径，不带病启动回落框架默认连接目标。
func TestNewSessionService_missing_dsn_when_conf_empty_fails_fast(t *testing.T) {
	_, _, err := NewSessionService(&conf.Agent{}, &conf.Data{})
	if err == nil {
		t.Fatal("empty master DSN: want error, got none")
	}
	if !strings.Contains(err.Error(), "data.database.master.source") {
		t.Fatalf("error %q must name the config path", err.Error())
	}
}

// TestTrackEventTTL_zero_when_unset_follows_session_ttl_instead_of_disabling
// 验证 track 事件 TTL 的 0 值语义：未设置/0 不传选项（框架缺省 = 沿用
// session_ttl），正值原样传入。框架对显式 0 的解释是「禁用过期」，二者
// 不可混淆。
func TestTrackEventTTL_zero_when_unset_follows_session_ttl_instead_of_disabling(t *testing.T) {
	if _, ok := trackEventTTLOf(nil); ok {
		t.Fatal("nil session storage: track ttl must be unset")
	}
	if _, ok := trackEventTTLOf(&conf.Agent_SessionStorage{}); ok {
		t.Fatal("zero ttl: track ttl must be unset (framework follows session ttl)")
	}
	if ttl, ok := trackEventTTLOf(&conf.Agent_SessionStorage{
		TrackEventTtl: durationpb.New(5 * time.Minute),
	}); !ok || ttl != 5*time.Minute {
		t.Fatalf("configured ttl = (%v, %v), want (5m, true)", ttl, ok)
	}
}

// TestCompressionOptions_disabled_when_switch_off 验证压缩总开关关闭时
// 不产生任何 LLMAgent 选项：框架在未开启时不改写任何工具结果。
func TestCompressionOptions_disabled_when_switch_off(t *testing.T) {
	if opts := compressionOptions(&conf.Agent{}); len(opts) != 0 {
		t.Fatalf("nil compression: %d options, want 0", len(opts))
	}
	if opts := compressionOptions(&conf.Agent{Compression: &conf.Agent_Compression{}}); len(opts) != 0 {
		t.Fatalf("enabled=false: %d options, want 0", len(opts))
	}
}

// TestCompressionOptions_enabled_when_unset_applies_framework_defaults
// 验证只开开关时按框架缺省接线：Pass 1 阈值 1024、保护窗口 1、Pass 2
// 关闭、无工具名单。
func TestCompressionOptions_enabled_when_unset_applies_framework_defaults(t *testing.T) {
	o := applyAgentOptions(t, &conf.Agent{Compression: &conf.Agent_Compression{Enabled: true}})
	if !o.EnableContextCompaction {
		t.Fatal("EnableContextCompaction = false, want true")
	}
	if o.ContextCompactionToolResultMaxTokens != defaultCompactionMax {
		t.Fatalf("ToolResultMaxTokens = %d, want %d", o.ContextCompactionToolResultMaxTokens, defaultCompactionMax)
	}
	if o.ContextCompactionKeepRecentRequests != defaultKeepRecentReqs {
		t.Fatalf("KeepRecentRequests = %d, want %d", o.ContextCompactionKeepRecentRequests, defaultKeepRecentReqs)
	}
	if o.ContextCompactionOversizedToolResultMaxTokens != 0 {
		t.Fatalf("OversizedToolResultMaxTokens = %d, want 0 (disabled by default)", o.ContextCompactionOversizedToolResultMaxTokens)
	}
	if o.ToolResultCompactionConfig != nil {
		t.Fatalf("ToolResultCompactionConfig = %+v, want nil", o.ToolResultCompactionConfig)
	}
}

// TestCompressionOptions_configured_when_thresholds_and_names_set 验证
// 配置值逐项透传：阈值、保护窗口、Pass 2 开启、keep/force 工具名单。
func TestCompressionOptions_configured_when_thresholds_and_names_set(t *testing.T) {
	c := &conf.Agent{Compression: &conf.Agent_Compression{
		Enabled:                      true,
		ToolResultMaxTokens:          64,
		KeepRecentRequests:           3,
		OversizedToolResultMaxTokens: 8192,
		KeepToolNames:                []string{"create_order", "update_order"},
		ForceCleanToolNames:          []string{"dump_table"},
	}}
	o := applyAgentOptions(t, c)
	if o.ContextCompactionToolResultMaxTokens != 64 {
		t.Fatalf("ToolResultMaxTokens = %d, want 64", o.ContextCompactionToolResultMaxTokens)
	}
	if o.ContextCompactionKeepRecentRequests != 3 {
		t.Fatalf("KeepRecentRequests = %d, want 3", o.ContextCompactionKeepRecentRequests)
	}
	if o.ContextCompactionOversizedToolResultMaxTokens != 8192 {
		t.Fatalf("OversizedToolResultMaxTokens = %d, want 8192", o.ContextCompactionOversizedToolResultMaxTokens)
	}
	cfg := o.ToolResultCompactionConfig
	if cfg == nil {
		t.Fatal("ToolResultCompactionConfig = nil, want set")
	}
	if len(cfg.KeepToolNames) != 2 || cfg.KeepToolNames[0] != "create_order" {
		t.Fatalf("KeepToolNames = %v, want [create_order update_order]", cfg.KeepToolNames)
	}
	if len(cfg.ForceCleanToolNames) != 1 || cfg.ForceCleanToolNames[0] != "dump_table" {
		t.Fatalf("ForceCleanToolNames = %v, want [dump_table]", cfg.ForceCleanToolNames)
	}
}

// TestSessionLoadTool_when_on_demand_enabled_and_service_supports_window
// 验证装配总开 OnDemandSession 后，LLMAgent 对实现 WindowService 的
// 会话服务（session/postgres / inmemory 均实现）暴露 session_load：
// 压缩占位符的 event_id 恢复路径由此可用。
func TestSessionLoadTool_when_on_demand_enabled_and_service_supports_window(t *testing.T) {
	ag := llmagent.New("grape-agent", llmAgentOptions(&conf.Agent{AppName: "grape-agent"}, nil, nil)...)
	inv := &agent.Invocation{
		Session:        session.NewSession("grape-agent", "t-1:7", "sess-1"),
		SessionService: inmemory.NewSessionService(),
	}
	tools, _ := ag.InvocationToolSurface(context.Background(), inv)
	for _, tl := range tools {
		if d := tl.Declaration(); d != nil && d.Name == "session_load" {
			return
		}
	}
	t.Fatalf("session_load tool missing from invocation tool surface, got %d tools", len(tools))
}

// applyAgentOptions 把骨架的 LLMAgent 选项应用到空 Options 上供断言。
func applyAgentOptions(t *testing.T, c *conf.Agent) *llmagent.Options {
	t.Helper()
	o := &llmagent.Options{}
	for _, opt := range llmAgentOptions(c, nil, nil) {
		opt(o)
	}
	return o
}
