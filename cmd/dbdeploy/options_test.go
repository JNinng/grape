package main

import (
	"strings"
	"testing"

	"grape/internal/data/pg_sqlc"
)

const (
	testAdminDSN      = "postgres://postgresql:root@127.0.0.1:5432/postgres"
	envKeyAdminDSN    = "GRAPE_DEPLOY_ADMIN_DSN"
	envKeyGrapeUPwd   = "GRAPE_DEPLOY_GRAPE_U_PASSWORD"
	envKeyTestUPwd    = "GRAPE_DEPLOY_TEST_U_PASSWORD"
	envKeyDatabase    = "GRAPE_DEPLOY_DATABASE"
	envFullPasswordSet = "env set"
)

func envGetter(env map[string]string) func(string) string {
	return func(key string) string { return env[key] }
}

func fullEnv() map[string]string {
	return map[string]string{
		envKeyAdminDSN:  testAdminDSN,
		envKeyGrapeUPwd: "env-grape-pwd",
		envKeyTestUPwd:  "env-test-pwd",
	}
}

func TestParseOptions_flags_take_precedence_over_env(t *testing.T) {
	opts, err := ParseOptions([]string{
		"--admin-dsn", "postgres://flag-user:flag-pwd@flag-host:5432/postgres",
		"--database", "flag_db",
		"--grape-u-password", "flag-grape-pwd",
		"--test-u-password", "flag-test-pwd",
	}, envGetter(fullEnv()))
	if err != nil {
		t.Fatalf("ParseOptions returned error: %v", err)
	}
	if opts.AdminDSN != "postgres://flag-user:flag-pwd@flag-host:5432/postgres" {
		t.Fatalf("AdminDSN = %q, want flag value", opts.AdminDSN)
	}
	if opts.Database != "flag_db" {
		t.Fatalf("Database = %q, want flag_db", opts.Database)
	}
	if opts.GrapeUPassword != "flag-grape-pwd" || opts.TestUPassword != "flag-test-pwd" {
		t.Fatalf("passwords = %q/%q, want flag values", opts.GrapeUPassword, opts.TestUPassword)
	}
}

func TestParseOptions_env_fills_unset_flags(t *testing.T) {
	env := fullEnv()
	env[envKeyDatabase] = "env_db"
	opts, err := ParseOptions(nil, envGetter(env))
	if err != nil {
		t.Fatalf("ParseOptions returned error: %v", err)
	}
	if opts.AdminDSN != testAdminDSN {
		t.Fatalf("AdminDSN = %q, want env value", opts.AdminDSN)
	}
	if opts.Database != "env_db" {
		t.Fatalf("Database = %q, want env_db", opts.Database)
	}
	if opts.GrapeUPassword != "env-grape-pwd" || opts.TestUPassword != "env-test-pwd" {
		t.Fatalf("passwords = %q/%q, want env values", opts.GrapeUPassword, opts.TestUPassword)
	}
}

func TestParseOptions_defaults_database_to_grape(t *testing.T) {
	opts, err := ParseOptions(nil, envGetter(fullEnv()))
	if err != nil {
		t.Fatalf("ParseOptions returned error: %v", err)
	}
	if opts.Database != "grape" {
		t.Fatalf("Database = %q, want default grape", opts.Database)
	}
}

func TestParseOptions_missing_admin_dsn_reports_clear_error(t *testing.T) {
	_, err := ParseOptions(nil, envGetter(map[string]string{}))
	if err == nil {
		t.Fatal("missing admin dsn must be rejected")
	}
	if !strings.Contains(err.Error(), "--admin-dsn") {
		t.Fatalf("error %q must mention flag --admin-dsn", err.Error())
	}
	if !strings.Contains(err.Error(), envKeyAdminDSN) {
		t.Fatalf("error %q must mention env var %s", err.Error(), envKeyAdminDSN)
	}
}

func TestParseOptions_missing_passwords_report_clear_errors(t *testing.T) {
	env := map[string]string{envKeyAdminDSN: testAdminDSN}
	_, err := ParseOptions(nil, envGetter(env))
	if err == nil {
		t.Fatal("missing grape_u password must be rejected")
	}
	if !strings.Contains(err.Error(), envKeyGrapeUPwd) {
		t.Fatalf("error %q must mention env var %s", err.Error(), envKeyGrapeUPwd)
	}

	env[envKeyGrapeUPwd] = envFullPasswordSet
	_, err = ParseOptions(nil, envGetter(env))
	if err == nil {
		t.Fatal("missing test_u password must be rejected")
	}
	if !strings.Contains(err.Error(), envKeyTestUPwd) {
		t.Fatalf("error %q must mention env var %s", err.Error(), envKeyTestUPwd)
	}
}

func TestParseOptions_invalid_database_name_rejected(t *testing.T) {
	for _, name := range []string{"bad name", "1leading", "semi;colon", "dash-inside"} {
		_, err := ParseOptions([]string{"--database", name}, envGetter(fullEnv()))
		if err == nil {
			t.Fatalf("database name %q must be rejected", name)
		}
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("error %q must mention the offending name %q", err.Error(), name)
		}
	}
}

func TestResolveDeployPasswords_substitutes_both_placeholders(t *testing.T) {
	sql := "CREATE USER grape_u WITH PASSWORD '" + pg_sqlc.GrapeUPasswordPlaceholder + "';\n" +
		"CREATE USER test_u WITH PASSWORD '" + pg_sqlc.TestUPasswordPlaceholder + "';"
	got := pg_sqlc.ResolveDeployPasswords(sql, "gp", "tp")
	want := "CREATE USER grape_u WITH PASSWORD 'gp';\nCREATE USER test_u WITH PASSWORD 'tp';"
	if got != want {
		t.Fatalf("resolved = %q, want %q", got, want)
	}
}

func TestResolveDeployPasswords_escapes_single_quotes_in_password(t *testing.T) {
	sql := "PASSWORD '" + pg_sqlc.GrapeUPasswordPlaceholder + "'"
	got := pg_sqlc.ResolveDeployPasswords(sql, "it's", "x")
	want := "PASSWORD 'it''s'"
	if got != want {
		t.Fatalf("resolved = %q, want escaped %q", got, want)
	}
}

func TestHasResidualPlaceholder_detects_unreplaced_marker(t *testing.T) {
	if !pg_sqlc.HasResidualPlaceholder("PASSWORD '" + pg_sqlc.GrapeUPasswordPlaceholder + "'") {
		t.Fatal("unreplaced placeholder must be detected")
	}
	if pg_sqlc.HasResidualPlaceholder("PASSWORD 'clean'") {
		t.Fatal("clean sql must not be flagged")
	}
}
