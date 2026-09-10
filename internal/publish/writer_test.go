package publish

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestWriteCreatesParentsAndContent(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(dir)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	if err := w.Write("blog/nested/page/index.html", []byte("<p>hello</p>")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "blog", "nested", "page", "index.html"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "<p>hello</p>" {
		t.Errorf("content = %q", got)
	}

	st, err := os.Stat(filepath.Join(dir, "blog", "nested", "page", "index.html"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// Generated files must be readable but never executable.
	if perm := st.Mode().Perm(); perm != 0o644 {
		t.Errorf("mode = %o, want 644", perm)
	}
}

func TestWriteRejectsPathsThatEscape(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(dir)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	// A page whose frontmatter slug resolved oddly must never be able to write
	// outside the output directory.
	for _, bad := range []string{"../escape.html", "../../escape.html", "/etc/passwd", ".."} {
		if err := w.Write(bad, []byte("x")); err == nil {
			t.Errorf("Write(%q) succeeded, want an error", bad)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape.html")); err == nil {
		t.Error("a file was written outside the output directory")
	}
	if err := w.Write("", []byte("x")); err == nil {
		t.Error("Write(\"\") should fail")
	}
}

func TestWriteLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(dir)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	for i := 0; i < 20; i++ {
		if err := w.Write(filepath.Join("p", string(rune('a'+i%26))+".html"), []byte("x")); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	var temps []string
	err = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasPrefix(d.Name(), ".staticgen-") {
			temps = append(temps, p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(temps) != 0 {
		t.Errorf("temp files left behind: %v", temps)
	}
}

func TestWriteIsConcurrencySafe(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(dir)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	var wg sync.WaitGroup
	const n = 50
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := filepath.Join("c", "page"+strings.Repeat("x", i%5)+".html")
			if err := w.Write(name, []byte(strings.Repeat("y", i))); err != nil {
				t.Errorf("concurrent Write: %v", err)
			}
		}(i)
	}
	wg.Wait()

	count, _ := w.Count()
	if count == 0 {
		t.Error("no files recorded after concurrent writes")
	}
}

func TestCountAndPaths(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(dir)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := w.Write("b.html", []byte("12345")); err != nil {
		t.Fatal(err)
	}
	if err := w.Write("a.html", []byte("abc")); err != nil {
		t.Fatal(err)
	}

	n, total := w.Count()
	if n != 2 || total != 8 {
		t.Errorf("Count = (%d, %d), want (2, 8)", n, total)
	}
	paths := w.Paths()
	if len(paths) != 2 || paths[0] != "a.html" || paths[1] != "b.html" {
		t.Errorf("Paths = %v, want sorted [a.html b.html]", paths)
	}
}

func TestCleanRemovesPreviousBuild(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "public")
	if err := os.MkdirAll(filepath.Join(out, "blog"), 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(out, "blog", "deleted-post", "index.html")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	w, err := NewWriter(out)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := w.Clean(root); err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("a page deleted from the content tree survived the rebuild")
	}
	// The output directory itself must survive so subsequent writes work.
	if st, err := os.Stat(out); err != nil || !st.IsDir() {
		t.Errorf("Clean removed the output directory itself: %v", err)
	}
}

func TestCleanRefusesDangerousTargets(t *testing.T) {
	// Each case builds a real tree so the guard is exercised against actual
	// paths rather than constructed strings.
	t.Run("site root", func(t *testing.T) {
		root := t.TempDir()
		w, err := NewWriter(root)
		if err != nil {
			t.Fatalf("NewWriter: %v", err)
		}
		if err := w.Clean(root); err == nil {
			t.Error("Clean should refuse the site root")
		}
	})

	t.Run("ancestor of site root", func(t *testing.T) {
		parent := t.TempDir()
		root := filepath.Join(parent, "site")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		w, err := NewWriter(parent)
		if err != nil {
			t.Fatalf("NewWriter: %v", err)
		}
		if err := w.Clean(root); err == nil {
			t.Error("Clean should refuse a directory containing the site root")
		}
		if _, err := os.Stat(root); err != nil {
			t.Errorf("the site root was deleted: %v", err)
		}
	})

	t.Run("directory holding markdown sources", func(t *testing.T) {
		root := t.TempDir()
		out := filepath.Join(root, "public")
		if err := os.MkdirAll(out, 0o755); err != nil {
			t.Fatal(err)
		}
		// Pointing the output at a content directory would delete the site.
		if err := os.WriteFile(filepath.Join(out, "post.md"), []byte("# hi"), 0o644); err != nil {
			t.Fatal(err)
		}
		w, err := NewWriter(out)
		if err != nil {
			t.Fatalf("NewWriter: %v", err)
		}
		if err := w.Clean(root); err == nil {
			t.Error("Clean should refuse a directory containing Markdown sources")
		}
		if _, err := os.Stat(filepath.Join(out, "post.md")); err != nil {
			t.Errorf("a Markdown source was deleted: %v", err)
		}
	})

	t.Run("missing directory is not an error", func(t *testing.T) {
		root := t.TempDir()
		out := filepath.Join(root, "does-not-exist")
		// NewWriter creates it, so remove it again to exercise the missing path.
		w, err := NewWriter(out)
		if err != nil {
			t.Fatalf("NewWriter: %v", err)
		}
		if err := os.RemoveAll(out); err != nil {
			t.Fatal(err)
		}
		if err := w.Clean(root); err != nil {
			t.Errorf("Clean on a missing directory: %v", err)
		}
	})
}

func TestNewWriterRejectsEmptyDir(t *testing.T) {
	if _, err := NewWriter(""); err == nil {
		t.Error("NewWriter(\"\") should fail rather than writing into the working directory")
	}
}
