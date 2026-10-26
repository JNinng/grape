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
// 守卫 agent_session.sql 与部署清单的一致性：文件在清单内且位于尾部、
// 通过事务破坏语句校验、六张框架表与索引、grape_u 授权齐备。框架表/
// 索引名规则（idx_{schema}_{前缀表}_{后缀}）由框架内部 builder 计算，
// DDL 必须逐字对齐，否则框架升级跑全量校验时即断。
func TestAgentSessionDDL_when_deployed_matches_manifest_and_framework_shape(t *testing.T) {
	if last := DeployFiles[len(DeployFiles)-1]; last != "agent_session.sql" {
		t.Fatalf("agent_session.sql must be the last deploy file, got %q", last)
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

	if !strings.Contains(sql, "CREATE SCHEMA agent;") {
		t.Fatal("agent_session.sql must create schema agent")
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
