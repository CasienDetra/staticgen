package fsx

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, p, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
}

// A newly copied empty file is a copy, not a skip.
func TestCopyTreeIfChangedCountsEmptyFiles(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(src, ".keep"), "", 0o644)

	stats, err := CopyTreeIfChanged(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Files != 1 {
		t.Errorf("Files = %d, want 1", stats.Files)
	}
	stats, err = CopyTreeIfChanged(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Files != 0 {
		t.Errorf("unchanged rebuild Files = %d, want 0", stats.Files)
	}
}

// A permission-only change must reach the output.
func TestCopyFileIfChangedCopiesModeChanges(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "run.sh"), filepath.Join(dir, "out", "run.sh")
	writeFile(t, src, "#!/bin/sh\n", 0o644)
	if _, _, err := CopyFileIfChanged(src, dst, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(src, 0o755); err != nil {
		t.Fatal(err)
	}
	copied, _, err := CopyFileIfChanged(src, dst, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if !copied {
		t.Error("mode change was not copied")
	}
	st, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v, want 0755", st.Mode().Perm())
	}
}

// A symlinked directory inside the output must not redirect a copy outside it.
func TestCopyTreeDoesNotWriteThroughSymlinks(t *testing.T) {
	src, dst, outside := t.TempDir(), t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(src, "css", "site.css"), "body{}", 0o644)
	if err := os.Symlink(outside, filepath.Join(dst, "css")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := CopyTreeIfChanged(src, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outside, "site.css")); !os.IsNotExist(err) {
		t.Error("copy followed a symlink out of the output directory")
	}
	if st, err := os.Lstat(filepath.Join(dst, "css")); err != nil || !st.IsDir() {
		t.Errorf("css should now be a real directory: %v", err)
	}
}

func TestPrepareDirWithinRejectsOutsidePaths(t *testing.T) {
	root := t.TempDir()
	if err := PrepareDirWithin(root, filepath.Dir(root)); err == nil {
		t.Error("expected an error for a directory outside root")
	}
}
