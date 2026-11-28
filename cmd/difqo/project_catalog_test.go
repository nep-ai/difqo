package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

func TestProjectRegistrationPersistsAndRefreshesCatalog(t *testing.T) {
	dir := t.TempDir()
	checkout := filepath.Join(dir, "fixture checkout")
	if err := os.Mkdir(checkout, 0o700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "--quiet", checkout).CombinedOutput(); err != nil {
		t.Fatalf("initialize fixture: %v: %s", err, out)
	}
	path := filepath.Join(dir, "projects.json")
	catalog := &projectCatalog{path: path}
	if _, err := catalog.add("Fixture", dir, "true"); err == nil {
		t.Fatal("accepted a non-checkout directory")
	}
	project, err := catalog.add("Fixture", checkout, "true")
	if err != nil || project.ID == "" || project.ExecutionBinding == "" {
		t.Fatalf("registration failed: %+v, %v", project, err)
	}
	if registered := catalog.list(); len(registered) != 1 || registered[0] != project {
		t.Fatalf("running catalog did not refresh: %+v", registered)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("catalog was not private: %v", err)
	}
	restarted, err := loadProjects(path)
	if err != nil || !slices.Equal(restarted, catalog.list()) {
		t.Fatalf("restart did not preserve binding: %+v, %v", restarted, err)
	}
	if _, err := catalog.add("Duplicate", checkout, "true"); err == nil {
		t.Fatal("accepted an already registered checkout")
	}
	if err := os.WriteFile(path, []byte("[]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.add("Conflict", checkout, "true"); err == nil {
		t.Fatal("overwrote a manually edited catalog")
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != "[]\n" {
		t.Fatalf("manual edit changed: %v", err)
	}
}
