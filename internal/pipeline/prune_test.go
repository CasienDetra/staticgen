package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Pruning must never follow a symlink out of the output directory and delete
// files belonging to the source tree or anywhere else on disk.
func TestPruneDoesNotFollowSymlinks(t *testing.T) {
	cfg := fixtureSite(t, nil)
	out := cfg.OutputPath()
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}

	// A directory of valuable files outside the output tree.
	outside := t.TempDir()
	precious := filepath.Join(outside, "precious.txt")
	if err := os.WriteFile(precious, []byte("do not delete"), 0o644); err != nil {
		t.Fatal(err)
	}

	// An attacker-controlled or mistaken symlink inside the output.
	link := filepath.Join(out, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	builder, err := New(cfg, Options{Now: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := builder.Build(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := builder.Build(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(precious); err != nil {
		t.Fatalf("pruning deleted a file outside the output directory: %v", err)
	}
}

// A symlink inside the output that is not produced by this build may be
// removed, but only the link, never its target.
func TestPruneRemovesSymlinkNotTarget(t *testing.T) {
	cfg := fixtureSite(t, nil)
	out := cfg.OutputPath()
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target.txt")
	if err := os.WriteFile(target, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(out, "stale-link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	removed := pruneStale(out, []string{"index.html"})
	t.Logf("pruned %d", removed)
	if _, err := os.Stat(target); err != nil {
		t.Errorf("target of a symlink was deleted: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(out, "stale-link")); !os.IsNotExist(err) {
		t.Error("stale symlink itself should have been pruned")
	}
}

// An output directory that is itself a symlink (e.g. to a web root) is
// pruned through, not deleted as a stale file.
func TestPruneThroughSymlinkedOutputRoot(t *testing.T) {
	real := t.TempDir()
	cfg := fixtureSite(t, nil)
	if err := os.Symlink(real, cfg.OutputPath()); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	builder, err := New(cfg, Options{Now: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := builder.Build(ctx); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(real, "stale.html")
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := builder.Build(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if st, err := os.Lstat(cfg.OutputPath()); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("output symlink was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(real, "index.html")); err != nil {
		t.Errorf("build output missing behind the symlink: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("stale file behind the symlinked output was not pruned")
	}
	if res.Pruned == 0 {
		t.Error("Pruned = 0, want the stale file counted")
	}
}
