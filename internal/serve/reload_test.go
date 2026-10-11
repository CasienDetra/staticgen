package serve

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/casien/staticgen/internal/config"
	"github.com/casien/staticgen/internal/pipeline"
)

// newReloadServer builds a Server wired to its own temp dirs so the reload
// predicates can be exercised against real paths.
func newReloadServer(t *testing.T) (*Server, string, string) {
	t.Helper()
	root := t.TempDir()
	out := filepath.Join(root, "public")
	tmpl := filepath.Join(root, "templates")
	content := filepath.Join(root, "content")
	cfgFile := filepath.Join(root, "site.yaml")
	for _, dir := range []string{out, tmpl, content} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(cfgFile, []byte("title: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Root = root
	srv, err := New(Options{
		Config:     cfg,
		OutputDir:  out,
		ConfigPath: cfgFile,
		Deps: Deps{
			Build:  func(context.Context) (*pipeline.Result, error) { return &pipeline.Result{}, nil },
			Reload: func() error { return nil },
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return srv, out, tmpl
}

// Editing a config or template file changes the pipeline stages themselves, so
// it needs a full builder reload rather than a plain rebuild.
func TestNeedsReloadForConfigAndTemplates(t *testing.T) {
	srv, _, tmpl := newReloadServer(t)

	if !srv.needsReload(filepath.Join(srv.configPath)) {
		t.Error("a config edit must trigger a reload")
	}
	if !srv.needsReload(filepath.Join(tmpl, "page.html")) {
		t.Error("a template edit must trigger a reload")
	}
	if !srv.needsReload(filepath.Join(tmpl, "partials", "head.html")) {
		t.Error("an edit to a template partial must trigger a reload")
	}
	if srv.needsReload(filepath.Join(srv.configPath + ".bak")) {
		t.Error("a file that merely shares a prefix must not trigger a reload")
	}
	if srv.needsReload(filepath.Join(filepath.Dir(tmpl), "content", "post.md")) {
		t.Error("a content edit is a plain rebuild, not a reload")
	}
}

// A rebuild writes into the output directory; reacting to those writes would
// rebuild forever.
func TestIsIgnoredCoversOutputTree(t *testing.T) {
	srv, out, _ := newReloadServer(t)

	for _, p := range []string{
		out,
		filepath.Join(out, "blog", "post-1", "index.html"),
		filepath.Join(out, "feed.xml"),
	} {
		if !srv.isIgnored(p) {
			t.Errorf("%s is inside the output and must be ignored", p)
		}
	}
	if srv.isIgnored(filepath.Join(srv.configPath)) {
		t.Error("the config file is not in the output")
	}
}

func TestIsTempFile(t *testing.T) {
	for _, name := range []string{
		".#post.md", "post.md~", "#post.md#", "post.md.swp", "post.md.tmp", ".git",
	} {
		if !isTempFile(filepath.Join("/tmp", name)) {
			t.Errorf("%s is editor or VCS noise and must be ignored", name)
		}
	}
	for _, name := range []string{"post.md", "index.html", "site.yaml", "a.tmp.md"} {
		if isTempFile(filepath.Join("/tmp", name)) {
			t.Errorf("%s is a real file and must not be ignored", name)
		}
	}
}

// The watcher must cover a whole subtree but skip hidden and opted-out
// directories, which is what keeps an editor's swap files from triggering
// rebuilds.
func TestWatcherAddTree(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"content", "content/blog", ".git", "skipme", filepath.Join("content", "nested")} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	w, err := NewWatcher(t.Logf, func(p string) bool { return filepath.Base(p) == "skipme" })
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if err := w.AddTree(root); err != nil {
		t.Fatal(err)
	}
	watched := w.Watched()

	for _, want := range []string{"content", filepath.Join("content", "blog"), filepath.Join("content", "nested")} {
		found := false
		for _, p := range watched {
			if p == filepath.Join(root, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s should be watched; watched %v", want, watched)
		}
	}
	for _, unwanted := range []string{".git", "skipme"} {
		for _, p := range watched {
			if p == filepath.Join(root, unwanted) {
				t.Errorf("%s must not be watched", unwanted)
			}
		}
	}

	// Optional directories may not exist yet; that is not an error.
	if err := w.AddTree(filepath.Join(root, "does-not-exist")); err != nil {
		t.Errorf("a missing tree should be ignored, got %v", err)
	}
	if err := w.AddTree(""); err != nil {
		t.Errorf("an empty root should be ignored, got %v", err)
	}
	if err := w.AddFile(""); err != nil {
		t.Errorf("an empty file path should be ignored, got %v", err)
	}
	w.AddIfDir(filepath.Join(root, "content"))
}

// A file created after the watch started must still produce an event.
func TestWatcherReportsNewFile(t *testing.T) {
	root := t.TempDir()
	w, err := NewWatcher(t.Logf, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if err := w.AddTree(root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "new.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-w.Events():
			if filepath.Base(ev.Name) == "new.md" {
				return
			}
		case err := <-w.Errors():
			t.Fatalf("watcher error: %v", err)
		case <-deadline:
			t.Fatal("no event for a file created in a watched directory")
		}
	}
}

func TestPlural(t *testing.T) {
	for _, tc := range []struct {
		n         int
		one, many string
	}{
		{0, "page", "pages"}, {1, "page", "pages"}, {2, "page", "pages"},
	} {
		want := tc.many
		if tc.n == 1 {
			want = tc.one
		}
		if got := plural(tc.n, tc.one, tc.many); got != want {
			t.Errorf("plural(%d) = %q, want %q", tc.n, got, want)
		}
	}
}
