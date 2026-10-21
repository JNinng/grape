package pg_sqlc

import "testing"

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
