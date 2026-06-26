package retention

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCleanupDryRunDoesNotDelete(t *testing.T) {
	root := t.TempDir()
	oldPath := filepath.Join(root, "old-run")
	if err := os.MkdirAll(oldPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldPath, "worker.log"), []byte("log"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	result := Cleanup(CleanupOptions{Roots: []string{root}, MaxAge: 24 * time.Hour, DryRun: true, Now: time.Now()})
	if result.Deleted != 1 {
		t.Fatalf("deleted = %d, want 1", result.Deleted)
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("dry run removed path: %v", err)
	}
}

func TestCleanupDeletesOldEntries(t *testing.T) {
	root := t.TempDir()
	oldPath := filepath.Join(root, "old-run")
	newPath := filepath.Join(root, "new-run")
	for _, path := range []string{oldPath, newPath} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "worker.log"), []byte("log"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	oldTime := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	result := Cleanup(CleanupOptions{Roots: []string{root}, MaxAge: 24 * time.Hour, DryRun: false, Now: time.Now()})
	if result.Deleted != 1 {
		t.Fatalf("deleted = %d, want 1", result.Deleted)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old path still exists or unexpected error: %v", err)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("new path missing: %v", err)
	}
}
