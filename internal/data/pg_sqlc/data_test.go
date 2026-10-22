package pg_sqlc

import (
	"strings"
	"testing"

	"grape/internal/conf"
)

// TestRedactArgs 校验 debug 日志的参数脱敏：app.session_* 注入的令牌
// 哈希不落日志，租户注入与非 set_config 语句原样透传。
func TestRedactArgs(t *testing.T) {
	// 令牌哈希注入：名字/值成对出现在前两个参数，值必须脱敏
	args := redactArgs("SELECT set_config($1, $2, true)", []any{"app.session_token_hash", "deadbeef"})
	if args[1] != "[redacted]" {
		t.Fatalf("session token hash leaked to log: %v", args[1])
	}
	if args[0] != "app.session_token_hash" {
		t.Fatalf("guc name must stay visible: %v", args[0])
	}

	args = redactArgs("SELECT set_config($1, $2, true)", []any{"app.session_refresh_token_hash", "cafe"})
	if args[1] != "[redacted]" {
		t.Fatalf("refresh token hash leaked to log: %v", args[1])
	}

	// 租户注入：名字内嵌在 SQL 文本，租户 ID 非凭据，保留
	args = redactArgs("SELECT set_config('app.tenant_id', $1, true)", []any{"t-super-01"})
	if args[0] != "t-super-01" {
		t.Fatalf("tenant id must stay visible: %v", args[0])
	}

	// 非 set_config 语句原样返回（同一底层数组，不拷贝）
	original := []any{"hash-xyz", 42}
	if got := redactArgs("SELECT * FROM t WHERE h = $1", original); len(got) != 2 || got[0] != "hash-xyz" {
		t.Fatalf("non set_config args must pass through: %v", got)
	}

	// set_config 但名字不是 app.session_*：不脱敏
	args = redactArgs("SELECT set_config($1, $2, true)", []any{"app.tenant_id", "t-1"})
	if args[1] != "t-1" {
		t.Fatalf("non-session guc value must stay visible: %v", args[1])
	}
}

// slotData 构造双槽 conf.Data：nil 槽位即配置缺失。
func slotData(master, system *conf.Data_Database_Slot) *conf.Data {
	return &conf.Data{
		Database: &conf.Data_Database{
			Master: master,
			System: system,
		},
	}
}

func validSlot() *conf.Data_Database_Slot {
	return &conf.Data_Database_Slot{Source: "postgres://grape_u:grape_u_pwd@127.0.0.1:5432/grape"}
}

// TestValidateSlots_missingMaster_returnsError 校验 master 槽缺失时装配期报错。
func TestValidateSlots_missingMaster_returnsError(t *testing.T) {
	err := validateSlots(slotData(nil, validSlot()))
	if err == nil {
		t.Fatal("missing master slot must fail validation")
	}
}

// TestValidateSlots_missingSystem_returnsError 校验 system 槽缺失时装配期报错
// （系统库句柄暂无消费者，配置错误仍须在启动期暴露）。
func TestValidateSlots_missingSystem_returnsError(t *testing.T) {
	err := validateSlots(slotData(validSlot(), nil))
	if err == nil {
		t.Fatal("missing system slot must fail validation")
	}
}

// TestValidateSlots_brokenSlot_namesSlotInError 校验报错指名缺失槽位。
func TestValidateSlots_brokenSlot_namesSlotInError(t *testing.T) {
	err := validateSlots(slotData(validSlot(), nil))
	if !strings.Contains(err.Error(), "system") {
		t.Fatalf("error must name the broken slot: %v", err)
	}
}

// TestParseSlot_emptySource_returnsError 校验空 source 显式拒绝：
// pgx 对空串不报错而是回落默认连接目标，必须由本函数拦截。
func TestParseSlot_emptySource_returnsError(t *testing.T) {
	_, err := parseSlot(&conf.Data_Database_Slot{})
	if err == nil {
		t.Fatal("empty source must be rejected")
	}
}

// TestParseSlot_invalidDSN_returnsError 校验非法 DSN 报错。
func TestParseSlot_invalidDSN_returnsError(t *testing.T) {
	_, err := parseSlot(&conf.Data_Database_Slot{Source: "://not-a-dsn"})
	if err == nil {
		t.Fatal("invalid DSN must fail parsing")
	}
}

// TestParseSlot_validDSN_parsesConfig 校验合法 DSN 解析成功，debug 开启
// 时挂上 queryTracer。不触库、不建池。
func TestParseSlot_validDSN_parsesConfig(t *testing.T) {
	cfg, err := parseSlot(&conf.Data_Database_Slot{
		Source: "postgres://grape_u:grape_u_pwd@127.0.0.1:5432/grape",
		Debug:  true,
	})
	if err != nil {
		t.Fatalf("valid DSN must parse: %v", err)
	}
	if _, ok := cfg.ConnConfig.Tracer.(*queryTracer); !ok {
		t.Fatalf("debug slot must carry queryTracer, got %T", cfg.ConnConfig.Tracer)
	}
}
