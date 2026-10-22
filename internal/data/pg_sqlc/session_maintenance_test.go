package pg_sqlc

import (
	"context"
	"os"
	"testing"
	"time"

	"grape/internal/biz"

	"github.com/jackc/pgx/v5"
)

// 会话介质维护面集成测试：清理路径验证 SECURITY DEFINER 函数跨租户
// 删除过期超宽限/已撤销行（应用角色直删恒 0 行的 RLS 现实由函数属主
// 兜底）；REINDEX 判定验证阈值之下的跳过理由与达阈值时的属主现实
// （42501 按跳过处理而非报错）。运行环境见 session_test.go 的说明
// （GRAPE_TEST_ADMIN_DSN / GRAPE_TEST_DSN，未设置时跳过）。

// openTestAdminConn 以超级用户连接指定测试库：装载事实与断言残留行的
// 通道（超级用户绕过 RLS），仅供测试内部使用。
func openTestAdminConn(t *testing.T, ctx context.Context, database string) *pgx.Conn {
	t.Helper()
	cfg, err := pgx.ParseConfig(os.Getenv("GRAPE_TEST_ADMIN_DSN"))
	if err != nil {
		t.Fatalf("parse admin dsn: %v", err)
	}
	cfg.Database = database
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(ctx) })
	return conn
}

// insertMaintenanceSession 由超级用户直插一条会话行（FORCE RLS 只约束
// 普通角色）：token_hash 取后缀哈希保证行间唯一。
func insertMaintenanceSession(t *testing.T, ctx context.Context, admin *pgx.Conn,
	id int64, tenant, suffix string, expiresAt time.Time, revoked bool) {
	t.Helper()
	_, err := admin.Exec(ctx, `INSERT INTO public.sys_session
		(id, tenant_id, user_id, token_hash, refresh_token_hash, expires_at,
		 refresh_expires_at, last_activity_at, is_revoked, created_by, updated_by)
		VALUES ($1, $2, 1, $3, $4, $5, $5, $5, $6, 1, 1)`,
		id, tenant, testTokenHash("maint-a-"+suffix), testTokenHash("maint-r-"+suffix),
		expiresAt, revoked)
	if err != nil {
		t.Fatalf("insert session %s: %v", suffix, err)
	}
}

// countSessionsByHash 由超级用户按哈希集合统计残留行数（绕过 RLS）。
func countSessionsByHash(t *testing.T, ctx context.Context, admin *pgx.Conn, suffixes ...string) int64 {
	t.Helper()
	hashes := make([]string, 0, len(suffixes))
	for _, s := range suffixes {
		hashes = append(hashes, testTokenHash("maint-a-"+s))
	}
	var n int64
	if err := admin.QueryRow(ctx,
		"SELECT count(*) FROM public.sys_session WHERE token_hash = ANY($1)", hashes).Scan(&n); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	return n
}

// findReindexAction 从报告里找出 REINDEX 判定动作；缺失时 fatal。
func findReindexAction(t *testing.T, report biz.MaintenanceReport) biz.MaintenanceAction {
	t.Helper()
	for _, a := range report.Actions {
		if a.Kind == "reindex" {
			return a
		}
	}
	t.Fatalf("reindex action missing in %+v", report.Actions)
	return biz.MaintenanceAction{}
}

// TestSessionMaintenanceCleanup 验证清理路径：超级用户直插混合会话行，
// 以 grape_u 跑 Maintain——过期超宽限与已撤销行被删（跨租户亦然），
// 未过期与宽限期内行保留，Rows 与实际删除数一致。
func TestSessionMaintenanceCleanup(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()
	maintainer := NewSessionMaintenance(d)
	admin := openTestAdminConn(t, ctx, testDatabase)

	if got := maintainer.Facility(); got != "postgres" {
		t.Fatalf("facility = %q, want postgres", got)
	}

	now := time.Now()
	// 四类行：keep-fresh 未过期、keep-in-grace 已过期但在宽限内（保留）；
	// del-expired 过期超宽限（跨租户）、del-revoked 已撤销未过期（删除）。
	insertMaintenanceSession(t, ctx, admin, 880000000001, "t-super-01", "keep-fresh", now.Add(time.Hour), false)
	insertMaintenanceSession(t, ctx, admin, 880000000002, "t-super-01", "keep-in-grace", now.Add(-time.Hour), false)
	insertMaintenanceSession(t, ctx, admin, 880000000003, "t-demo-01", "del-expired", now.Add(-25*time.Hour), false)
	insertMaintenanceSession(t, ctx, admin, 880000000004, "t-super-01", "del-revoked", now.Add(time.Hour), true)

	report, err := maintainer.Maintain(ctx)
	if err != nil {
		t.Fatalf("maintain: %v", err)
	}
	if len(report.Actions) == 0 {
		t.Fatal("maintain returned no actions")
	}
	cleanup := report.Actions[0]
	if cleanup.Kind != "delete_expired_sessions" || !cleanup.Performed {
		t.Fatalf("cleanup action = %+v, want delete_expired_sessions performed", cleanup)
	}
	if cleanup.Rows != 2 {
		t.Fatalf("cleanup rows = %d, want 2 (expired beyond grace + revoked)", cleanup.Rows)
	}

	// 残留断言（超级用户视角）：保留行仍在，删除行已消失
	if got := countSessionsByHash(t, ctx, admin, "keep-fresh", "keep-in-grace"); got != 2 {
		t.Fatalf("kept sessions = %d, want 2", got)
	}
	if got := countSessionsByHash(t, ctx, admin, "del-expired", "del-revoked"); got != 0 {
		t.Fatalf("stale sessions = %d, want 0 (expired beyond grace and revoked rows must go, across tenants)", got)
	}

	// 再跑一轮：已无可删行，幂等返回 0
	report, err = maintainer.Maintain(ctx)
	if err != nil {
		t.Fatalf("second maintain: %v", err)
	}
	if report.Actions[0].Rows != 0 {
		t.Fatalf("second cleanup rows = %d, want 0", report.Actions[0].Rows)
	}
}

// waitForDeadTuples 轮询 pg_stat_user_tables 直到 sys_session 的死元组
// 估计值超过 min（统计收集有短暂滞后，超时 fatal）。
func waitForDeadTuples(t *testing.T, ctx context.Context, admin *pgx.Conn, min int64) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var dead int64
		if err := admin.QueryRow(ctx,
			`SELECT n_dead_tup FROM pg_stat_user_tables
			 WHERE schemaname = 'public' AND relname = 'sys_session'`).Scan(&dead); err != nil {
			t.Fatalf("query dead tuples: %v", err)
		}
		if dead > min {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("dead tuples = %d, want > %d within 10s", dead, min)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestSessionMaintenanceReindexDecision 验证 REINDEX 判定路径：小数据量
// 跳过并说明理由；达阈值时以 grape_u 执行不报错——索引属主是部署超级
// 用户时按 42501 跳过留痕（断言与属主无关的稳定部分）。
func TestSessionMaintenanceReindexDecision(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()
	maintainer := NewSessionMaintenance(d)
	admin := openTestAdminConn(t, ctx, testDatabase)

	t.Run("bloat below threshold", func(t *testing.T) {
		report, err := maintainer.Maintain(ctx)
		if err != nil {
			t.Fatalf("maintain: %v", err)
		}
		reindex := findReindexAction(t, report)
		if reindex.Performed || reindex.Note != "bloat below threshold" {
			t.Fatalf("reindex action = %+v, want (performed=false, note=bloat below threshold)", reindex)
		}
	})

	t.Run("threshold reached skips on ownership reality", func(t *testing.T) {
		// 制造 11000 个死元组：插后全删。行都未过期未撤销，本轮清理
		// 动作不会先动它们，死元组全部留给 REINDEX 判定。
		const bulk = 11000
		if _, err := admin.Exec(ctx, `INSERT INTO public.sys_session
			(id, tenant_id, user_id, token_hash, refresh_token_hash, expires_at,
			 refresh_expires_at, last_activity_at, is_revoked, created_by, updated_by)
			SELECT 770000000000 + g, 't-super-01', 1,
			       md5('bloat-a-' || g::text), md5('bloat-r-' || g::text),
			       CURRENT_TIMESTAMP + interval '1 hour', CURRENT_TIMESTAMP + interval '1 hour',
			       CURRENT_TIMESTAMP, false, 1, 1
			FROM generate_series(1, $1) g`, bulk); err != nil {
			t.Fatalf("bulk insert: %v", err)
		}
		if ct, err := admin.Exec(ctx,
			"DELETE FROM public.sys_session WHERE id BETWEEN 770000000001 AND 770000011000"); err != nil {
			t.Fatalf("bulk delete: %v", err)
		} else if ct.RowsAffected() != bulk {
			t.Fatalf("bulk delete rows = %d, want %d", ct.RowsAffected(), bulk)
		}
		// 死元组统计到位后判定必然越阈：11000 > 10000 且占比接近 1
		waitForDeadTuples(t, ctx, admin, 10000)

		report, err := maintainer.Maintain(ctx)
		if err != nil {
			t.Fatalf("maintain at threshold: %v", err)
		}
		reindex := findReindexAction(t, report)
		// 属主现实：索引属主是部署超级用户，应用角色 42501 → 跳过留痕，
		// 不报错。若属主恰为应用角色则成功执行。两种结局都合法。
		if !reindex.Performed && reindex.Note != "insufficient privilege: index owner required" {
			t.Fatalf("reindex skipped without privilege note: %+v", reindex)
		}
	})
}
