package deploy

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestTestDeployFiles_appends_test_seed_after_production_manifest(t *testing.T) {
	want := make([]string, 0, len(DeployFiles)+len(TestSeedFiles))
	want = append(want, DeployFiles...)
	want = append(want, TestSeedFiles...)

	if got := TestDeployFiles(); !reflect.DeepEqual(got, want) {
		t.Fatalf("TestDeployFiles() = %v, want production manifest followed by test seed %v", got, TestSeedFiles)
	}
}

func TestTestDeployFiles_returns_independent_slice(t *testing.T) {
	files := TestDeployFiles()
	if len(files) == 0 {
		t.Fatal("TestDeployFiles() returned an empty manifest")
	}
	files[0] = "mutated.sql"

	if DeployFiles[0] == "mutated.sql" {
		t.Fatal("mutating TestDeployFiles() result changed DeployFiles")
	}
	if got := TestDeployFiles()[0]; got == "mutated.sql" {
		t.Fatalf("TestDeployFiles() returned a cached slice, first entry = %q", got)
	}
}

func TestTestSeedFiles_exist_in_deploy_dir(t *testing.T) {
	dir, err := DeployDir()
	if err != nil {
		t.Fatalf("locate deploy dir: %v", err)
	}
	for _, name := range TestSeedFiles {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("test seed file %s missing: %v", name, err)
		}
		if info.IsDir() {
			t.Fatalf("test seed path %s is a directory", name)
		}
	}
}
