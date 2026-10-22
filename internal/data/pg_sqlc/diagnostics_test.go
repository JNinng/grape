package pg_sqlc

import (
	"context"
	"testing"

	"grape/internal/biz"
)

// TestDiagnosticsPressure 以真实 PostgreSQL 验证诊断面：池统计、活动
// 会话与表级统计均能产出样本。断言基于部署后的稳定事实（表已建、采集
// 自身占用一条池连接），不依赖种子行数。运行环境见 session_test.go
// 的说明（GRAPE_TEST_ADMIN_DSN / GRAPE_TEST_DSN，未设置时跳过）。
func TestDiagnosticsPressure(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()
	diag := NewDiagnostics(d)

	if got := diag.Facility(); got != "postgres" {
		t.Fatalf("facility = %q, want postgres", got)
	}

	samples, err := diag.Pressure(ctx)
	if err != nil {
		t.Fatalf("pressure: %v", err)
	}
	byName := make(map[string][]biz.MetricSample)
	for _, s := range samples {
		byName[s.Name] = append(byName[s.Name], s)
	}

	t.Run("pool stats present and busy", func(t *testing.T) {
		// 采集自身从池取了一条连接：total >= 1 是硬事实。
		total := findSample(t, byName, "grape_pg_pool_connections", map[string]string{"state": "total"})
		if total.Value < 1 {
			t.Fatalf("pool total = %v, want >= 1 (collection holds one connection)", total.Value)
		}
		for _, state := range []string{"idle", "acquired"} {
			findSample(t, byName, "grape_pg_pool_connections", map[string]string{"state": state})
		}
		findSample(t, byName, "grape_pg_pool_connections_max", nil)
	})

	t.Run("activity samples present", func(t *testing.T) {
		// 执行采集查询的后端自身对 pg_stat_activity 可见且 state=active。
		findSample(t, byName, "grape_pg_activity_sessions", map[string]string{"state": "active"})
		findSample(t, byName, "grape_pg_transaction_longest_seconds", nil)
		findSample(t, byName, "grape_pg_queries_over_1s", nil)
	})

	t.Run("sys_session table stats present", func(t *testing.T) {
		for _, name := range []string{
			"grape_pg_table_live_tuples",
			"grape_pg_table_dead_tuples",
			"grape_pg_table_size_bytes",
			"grape_pg_table_index_size_bytes",
		} {
			findSample(t, byName, name, map[string]string{"table": "sys_session"})
		}
	})

	t.Run("index stats present", func(t *testing.T) {
		findSample(t, byName, "grape_pg_index_size_bytes", map[string]string{"table": "sys_session", "index": "sys_session_pkey"})
	})
}

// findSample 按指标名与标签集定位样本，缺失即失败。
func findSample(t *testing.T, byName map[string][]biz.MetricSample, name string, labels map[string]string) biz.MetricSample {
	t.Helper()
	for _, s := range byName[name] {
		matched := true
		for k, v := range labels {
			if s.Labels[k] != v {
				matched = false
				break
			}
		}
		if matched {
			return s
		}
	}
	t.Fatalf("sample %s with labels %v not found in %d samples of that name", name, labels, len(byName[name]))
	return biz.MetricSample{}
}
