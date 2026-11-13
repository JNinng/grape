package deploy

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// 模板库指纹与克隆等效性测试（真实 PostgreSQL，未设 GRAPE_TEST_ADMIN_DSN
// 时跳过）：指纹改写后 EnsureTestTemplate 自动重建、未改写时幂等跳过，
// 克隆库携带与全量装载一致的对象事实（表、RLS、扩展与种子行）。

const templateTestGrapeUPassword = "grape_u_pwd"
const templateTestTestUPassword = "test_u_pwd"

// probeTable 是模板库内的重建探针：重建（DROP+CREATE）后必然消失，
// 幂等跳过时保留——以此区分两条路径。
const probeTable = "public._template_rebuild_probe"

func TestRewriteDSNDatabase_replaces_database_for_both_dsn_forms(t *testing.T) {
	// URL 形态：仅路径段被替换，其余参数原样保留。
	got, err := RewriteDSNDatabase("postgres://grape_u:pwd@127.0.0.1:5432/grape_pgsqlc_test?sslmode=disable", "grape_t_ab12cd34ef56")
	if err != nil {
		t.Fatalf("rewrite url dsn: %v", err)
	}
	want := "postgres://grape_u:pwd@127.0.0.1:5432/grape_t_ab12cd34ef56?sslmode=disable"
	if got != want {
		t.Fatalf("rewritten url dsn = %q, want %q", got, want)
	}

	// 关键字/值形态：既有 dbname 被替换。
	got, err = RewriteDSNDatabase("host=127.0.0.1 port=5432 dbname=grape_pgsqlc_test user=grape_u", "grape_t_ab12cd34ef56")
	if err != nil {
		t.Fatalf("rewrite keyword dsn: %v", err)
	}
	if want := "host=127.0.0.1 port=5432 dbname=grape_t_ab12cd34ef56 user=grape_u"; got != want {
		t.Fatalf("rewritten keyword dsn = %q, want %q", got, want)
	}

	// 关键字/值形态缺 dbname：追加而非报错。
	got, err = RewriteDSNDatabase("host=127.0.0.1 user=grape_u", "grape_t_ab12cd34ef56")
	if err != nil {
		t.Fatalf("rewrite keyword dsn without dbname: %v", err)
	}
	if !strings.HasSuffix(got, " dbname=grape_t_ab12cd34ef56") {
		t.Fatalf("rewritten dsn must append dbname: %q", got)
	}

	if _, err := RewriteDSNDatabase("  ", "grape_t_ab12cd34ef56"); err == nil {
		t.Fatal("empty dsn must be rejected")
	}
}

func TestEnsureTestTemplate_fingerprint_drives_rebuild_and_clone_matches_full_deploy(t *testing.T) {
	adminDSN := os.Getenv("GRAPE_TEST_ADMIN_DSN")
	if adminDSN == "" {
		t.Skip("GRAPE_TEST_ADMIN_DSN not set; skipping template integration test")
	}
	ctx := context.Background()

	if err := EnsureTestTemplate(ctx, adminDSN, templateTestGrapeUPassword, templateTestTestUPassword); err != nil {
		t.Fatalf("ensure template: %v", err)
	}

	admin := connectTemplateTestAdmin(t, ctx, adminDSN, "postgres")
	defer func() { _ = admin.Close(ctx) }()

	// 指纹已落库且形态正确（前缀 + 64 位十六进制）。
	hash := templateFingerprintComment(t, ctx, admin)
	if !strings.HasPrefix(hash, manifestHashCommentPrefix) {
		t.Fatalf("template comment = %q, want %q prefix", hash, manifestHashCommentPrefix)
	}
	fingerprint := strings.TrimPrefix(hash, manifestHashCommentPrefix)
	if len(fingerprint) != 64 {
		t.Fatalf("fingerprint length = %d, want 64 hex chars", len(fingerprint))
	}

	// 幂等路径：探针表在第二次 Ensure 后必须保留（未重建）。
	_, err := admin.Exec(ctx, "CREATE TABLE IF NOT EXISTS "+probeTable+" (id int)")
	if err != nil {
		t.Fatalf("create probe table: %v", err)
	}
	if err := EnsureTestTemplate(ctx, adminDSN, templateTestGrapeUPassword, templateTestTestUPassword); err != nil {
		t.Fatalf("ensure template (idempotent): %v", err)
	}
	assertProbeTable(t, ctx, admin, true)

	// 重建路径：指纹改写为陈旧值后 Ensure 必须重建——探针表消失、
	// 指纹恢复为当前清单值。
	_, err = admin.Exec(ctx, "DROP TABLE "+probeTable)
	if err != nil {
		t.Fatalf("drop probe table: %v", err)
	}
	_, err = admin.Exec(ctx, "COMMENT ON DATABASE "+TestTemplateDatabase+" IS '"+manifestHashCommentPrefix+"stale'")
	if err != nil {
		t.Fatalf("corrupt template comment: %v", err)
	}
	if err := EnsureTestTemplate(ctx, adminDSN, templateTestGrapeUPassword, templateTestTestUPassword); err != nil {
		t.Fatalf("ensure template (rebuild): %v", err)
	}
	if got := templateFingerprintComment(t, ctx, admin); got != hash {
		t.Fatalf("template comment after rebuild = %q, want restored %q", got, hash)
	}

	// 克隆等效性：克隆库携带与全量装载一致的对象事实，且可独立删除。
	dbName, err := CloneTestDatabase(ctx, adminDSN, templateTestGrapeUPassword, templateTestTestUPassword, t.Name())
	if err != nil {
		t.Fatalf("clone test database: %v", err)
	}
	clone := connectTemplateTestAdmin(t, ctx, adminDSN, dbName)
	for _, object := range []string{
		"public.sys_user", "public.sys_dict_data", "public.agent_tool_call_log",
		"agent.agent_document",
	} {
		var present bool
		if err := clone.QueryRow(ctx,
			`SELECT to_regclass($1) IS NOT NULL`, object).Scan(&present); err != nil {
			t.Fatalf("check object %s: %v", object, err)
		}
		if !present {
			t.Fatalf("cloned database lacks object %s (template must match full deploy)", object)
		}
	}
	var rls bool
	if err := clone.QueryRow(ctx,
		`SELECT relrowsecurity FROM pg_class WHERE oid = to_regclass('public.sys_dict_data')`).Scan(&rls); err != nil {
		t.Fatalf("check rls: %v", err)
	}
	if !rls {
		t.Fatal("cloned database must keep RLS enabled on sys_dict_data")
	}
	var vectorExt int
	if err := clone.QueryRow(ctx,
		`SELECT count(*) FROM pg_extension WHERE extname = 'vector'`).Scan(&vectorExt); err != nil {
		t.Fatalf("check vector extension: %v", err)
	}
	if vectorExt != 1 {
		t.Fatal("cloned database must carry the vector extension")
	}
	var seedDictRows int
	if err := clone.QueryRow(ctx,
		`SELECT count(*) FROM public.sys_dict_data WHERE tenant_id = 't-test-01'`).Scan(&seedDictRows); err != nil {
		t.Fatalf("count seeded dict rows: %v", err)
	}
	if seedDictRows != 14 {
		t.Fatalf("seeded dict rows of t-test-01 = %d, want 14 (test_seed baseline)", seedDictRows)
	}
	if err := clone.Close(ctx); err != nil {
		t.Fatalf("close clone connection: %v", err)
	}

	if err := DropTestDatabase(ctx, adminDSN, dbName); err != nil {
		t.Fatalf("drop test database: %v", err)
	}
	var exists bool
	if err := admin.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, dbName).Scan(&exists); err != nil {
		t.Fatalf("check dropped database: %v", err)
	}
	if exists {
		t.Fatalf("dropped database %s still present", dbName)
	}
}

// templateFingerprintComment 读取模板库注释（失效指纹）。
func templateFingerprintComment(t *testing.T, ctx context.Context, admin *pgx.Conn) string {
	t.Helper()
	var comment *string
	if err := admin.QueryRow(ctx,
		`SELECT shobj_description(oid, 'pg_database') FROM pg_database WHERE datname = $1`,
		TestTemplateDatabase).Scan(&comment); err != nil {
		t.Fatalf("read template comment: %v", err)
	}
	if comment == nil {
		t.Fatal("template comment missing (fingerprint must be recorded)")
	}
	return *comment
}

// assertProbeTable 断言探针表的存在性与预期一致。
func assertProbeTable(t *testing.T, ctx context.Context, admin *pgx.Conn, want bool) {
	t.Helper()
	var present bool
	if err := admin.QueryRow(ctx,
		`SELECT to_regclass($1) IS NOT NULL`, probeTable).Scan(&present); err != nil {
		t.Fatalf("check probe table: %v", err)
	}
	if present != want {
		t.Fatalf("probe table present = %v, want %v (rebuild/idempotent path mismatch)", present, want)
	}
}

// connectTemplateTestAdmin 以管理员连接指定库（模板维护与克隆检查用）。
func connectTemplateTestAdmin(t *testing.T, ctx context.Context, adminDSN, database string) *pgx.Conn {
	t.Helper()
	cfg, err := pgx.ParseConfig(adminDSN)
	if err != nil {
		t.Fatalf("parse admin dsn: %v", err)
	}
	cfg.Database = database
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect admin to %s: %v", database, err)
	}
	return conn
}
