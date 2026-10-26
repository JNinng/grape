package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"grape/internal/data/pg_sqlc"
)

func TestValidateDeployFiles_real_manifest_passes(t *testing.T) {
	dir, err := pg_sqlc.DeployDir()
	if err != nil {
		t.Fatalf("locate deploy dir: %v", err)
	}
	if err := pg_sqlc.ValidateDeployFiles(dir); err != nil {
		t.Fatalf("real deploy manifest must validate: %v", err)
	}
}

func TestValidateDeployFiles_reports_missing_file(t *testing.T) {
	dir := t.TempDir()
	err := pg_sqlc.ValidateDeployFiles(dir)
	if err == nil {
		t.Fatal("missing manifest files must be reported")
	}
	if !strings.Contains(err.Error(), "schema/10_basic.sql") {
		t.Fatalf("error %q must name the first missing file schema/10_basic.sql", err.Error())
	}
}

func TestValidateDeployFiles_rejects_transaction_breaking_statement(t *testing.T) {
	dir := t.TempDir()
	for _, name := range pg_sqlc.DeployFiles {
		content := "-- ok\nSELECT 1;\n"
		if name == "rls.sql" {
			content = "CREATE INDEX CONCURRENTLY idx ON public.t (col);\n"
		}
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("setup: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	err := pg_sqlc.ValidateDeployFiles(dir)
	if err == nil {
		t.Fatal("CONCURRENTLY in a manifest file must be rejected")
	}
	if !strings.Contains(err.Error(), "rls.sql") {
		t.Fatalf("error %q must name the offending file rls.sql", err.Error())
	}

	dir2 := t.TempDir()
	for _, name := range pg_sqlc.DeployFiles {
		content := "-- ok\nSELECT 1;\n"
		if name == "role.sql" {
			content = "CREATE DATABASE foo;\n"
		}
		path := filepath.Join(dir2, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("setup: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	err = pg_sqlc.ValidateDeployFiles(dir2)
	if err == nil {
		t.Fatal("CREATE DATABASE in a manifest file must be rejected")
	}
	if !strings.Contains(err.Error(), "role.sql") {
		t.Fatalf("error %q must name the offending file role.sql", err.Error())
	}
}

func TestDeployFiles_schema_order_numbers_are_consecutive(t *testing.T) {
	prev := 0
	for _, name := range pg_sqlc.DeployFiles {
		if !strings.HasPrefix(name, "schema/") {
			break
		}
		base := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
		numPart := strings.SplitN(base, "_", 2)[0]
		num, err := strconv.Atoi(numPart)
		if err != nil {
			t.Fatalf("schema file %s has no numeric order prefix", name)
		}
		if num != prev+10 {
			t.Fatalf("schema order broke at %s: got %d, want %d", name, num, prev+10)
		}
		prev = num
	}
	if prev == 0 {
		t.Fatal("manifest must start with schema files")
	}
}

func TestDeployFiles_role_precedes_rls_and_seed_is_last(t *testing.T) {
	index := map[string]int{}
	for i, name := range pg_sqlc.DeployFiles {
		if _, dup := index[name]; dup {
			t.Fatalf("file %s listed twice", name)
		}
		index[name] = i
	}
	wantOrder := []string{"role.sql", "rls.sql", "operate.sql", "expose.sql", "seed.sql"}
	for i, name := range wantOrder {
		pos, ok := index[name]
		if !ok {
			t.Fatalf("manifest is missing %s", name)
		}
		if i > 0 {
			prevName := wantOrder[i-1]
			if pos <= index[prevName] {
				t.Fatalf("%s (pos %d) must come after %s (pos %d)", name, pos, prevName, index[prevName])
			}
		}
	}
	if index["seed.sql"] != len(pg_sqlc.DeployFiles)-1 {
		t.Fatalf("seed.sql must be the last file, got pos %d of %d", index["seed.sql"], len(pg_sqlc.DeployFiles))
	}
}
