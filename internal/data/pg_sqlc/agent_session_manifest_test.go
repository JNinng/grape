package pg_sqlc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// agentSessionDDLTables 是框架 session/postgres 服务启动校验要求的六张表
// （trpc-agent-go internal/session/sqldb 表名 × WithSchema("agent") +
// WithTablePrefix("agent_")），缺一框架 NewService 即失败。
var agentSessionDDLTables = []string{
	"agent.agent_session_states",
	"agent.agent_session_events",
	"agent.agent_session_track_events",
	"agent.agent_session_summaries",
	"agent.agent_app_states",
	"agent.agent_user_states",
}

// TestAgentSessionDDL_when_deployed_matches_manifest_and_framework_shape
// 守卫 agent_session.sql 与部署清单的一致性：文件在清单内且位于
// seed.sql 之后、通过事务破坏语句校验、六张框架表与索引齐备、关键
// 列型与框架期望一致。WithSkipDBInit(true) 下框架启动期零校验，本
// 静态守卫是表结构漂移的唯一防线：框架表/索引名规则
// （idx_{schema}_{前缀表}_{suffix}）由框架内部 builder 计算，列型/
// 可空性与框架 verifySchema 的 expectedSchema 对应，DDL 偏离任何一项，
// 框架升级跑全量校验或运行期读写即断。
func TestAgentSessionDDL_when_deployed_matches_manifest_and_framework_shape(t *testing.T) {
	// 相对序约束：agent_session.sql 在 seed.sql 之后即可，不锁绝对
	// 最后——并行流（如向量域）追加的清单项可落在其后，避免尾部互斥。
	seedPos, agentPos := -1, -1
	for i, name := range DeployFiles {
		switch name {
		case "seed.sql":
			seedPos = i
		case "agent_session.sql":
			agentPos = i
		}
	}
	if seedPos < 0 || agentPos < 0 {
		t.Fatalf("manifest must contain seed.sql and agent_session.sql, got %v", DeployFiles)
	}
	if agentPos <= seedPos {
		t.Fatalf("agent_session.sql (pos %d) must come after seed.sql (pos %d)", agentPos, seedPos)
	}
	dir, err := DeployDir()
	if err != nil {
		t.Fatalf("locate deploy dir: %v", err)
	}
	if err := ValidateDeployFiles(dir); err != nil {
		t.Fatalf("validate deploy files: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(dir, "agent_session.sql"))
	if err != nil {
		t.Fatalf("read agent_session.sql: %v", err)
	}
	sql := string(content)

	if !strings.Contains(sql, "CREATE SCHEMA IF NOT EXISTS agent;") {
		t.Fatal("agent_session.sql must create schema agent idempotently (IF NOT EXISTS)")
	}
	for _, table := range agentSessionDDLTables {
		if !strings.Contains(sql, "CREATE TABLE IF NOT EXISTS "+table+" ") {
			t.Errorf("agent_session.sql missing framework table %s", table)
		}
	}
	// 每张表的 unique_active / lookup / expires 索引按框架 builder 命名
	// 齐备（unique_active 4 张、lookup 2 张、expires 6 张，共 12 个）。
	wantIndexSuffixes := map[string]int{"unique_active": 4, "lookup": 2, "expires": 6}
	for suffix, want := range wantIndexSuffixes {
		if n := countAgentIndexes(sql, suffix); n != want {
			t.Errorf("index suffix %s count = %d, want %d", suffix, n, want)
		}
	}
	for _, grant := range []string{
		"GRANT USAGE ON SCHEMA agent TO grape_u;",
		"GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA agent TO grape_u;",
		"GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA agent TO grape_u;",
	} {
		if !strings.Contains(sql, grant) {
			t.Errorf("agent_session.sql missing grant %q", grant)
		}
	}
	sessionAssertKeyColumns(t, sql)
}

// sessionAssertKeyColumns 断言关键列的型与可空性同框架 expectedSchema 一致：
// 只挑运行路径最敏感的几处做静态锚点，全量逐列校验仍以框架升级 diff
// 为准。
func sessionAssertKeyColumns(t *testing.T, sql string) {
	t.Helper()
	for _, a := range []struct {
		name string
		ddl  string
	}{
		{"events.event must be jsonb NOT NULL", "event      JSONB NOT NULL,"},
		{"track_events.track must be varchar NOT NULL", "track      VARCHAR(255) NOT NULL,"},
		{"states.state must be nullable jsonb", "state      JSONB DEFAULT NULL,"},
	} {
		if !strings.Contains(sql, a.ddl) {
			t.Errorf("column anchor %q missing: want %q", a.name, a.ddl)
		}
	}
	// 框架 summaries 表以 updated_at 记时、无 created_at 列：提取该表
	// DDL 段断言其中不出现 created_at。
	seg := ddlSegment(t, sql, "agent.agent_session_summaries")
	if strings.Contains(seg, "created_at") {
		t.Error("agent_session_summaries must not define created_at (framework expects updated_at only)")
	}
}

// ddlSegment 提取某张表的 CREATE TABLE 语句段（起于建表语句，止于分号）。
func ddlSegment(t *testing.T, sql, table string) string {
	t.Helper()
	start := strings.Index(sql, "CREATE TABLE IF NOT EXISTS "+table+" ")
	if start < 0 {
		t.Fatalf("table %s not found in DDL", table)
	}
	end := strings.Index(sql[start:], ";")
	if end < 0 {
		t.Fatalf("table %s DDL is not terminated", table)
	}
	return sql[start : start+end]
}

// countAgentIndexes 统计 DDL 中带指定后缀的 agent 框架索引数。
func countAgentIndexes(sql, suffix string) int {
	return strings.Count(sql, "idx_agent_agent_session_states_"+suffix) +
		strings.Count(sql, "idx_agent_agent_session_events_"+suffix) +
		strings.Count(sql, "idx_agent_agent_session_track_events_"+suffix) +
		strings.Count(sql, "idx_agent_agent_session_summaries_"+suffix) +
		strings.Count(sql, "idx_agent_agent_app_states_"+suffix) +
		strings.Count(sql, "idx_agent_agent_user_states_"+suffix)
}
