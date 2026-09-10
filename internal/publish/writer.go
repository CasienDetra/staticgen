// Package publish writes build output to disk.
package publish

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/casien/staticgen/internal/fsx"
)

// Writer emits files into the output directory.
//
// Every write goes to a temporary file in the destination directory and is
// renamed into place, so the dev server can never serve a half-written page.
// Writes are safe to issue from multiple goroutines.
type Writer struct {
	dir string

	mu    sync.Mutex
	files map[string]int64
}

// NewWriter returns a Writer targeting dir, creating it if needed.
func NewWriter(dir string) (*Writer, error) {
	if dir == "" {
		return nil, errors.New("publish: output directory is required")
	}
	if err := fsx.EnsureDir(dir); err != nil {
		return nil, err
	}
	return &Writer{dir: dir, files: map[string]int64{}}, nil
}

// Dir returns the output directory.
func (w *Writer) Dir() string { return w.dir }

// Write writes data to relPath beneath the output directory.
func (w *Writer) Write(relPath string, data []byte) error {
	if relPath == "" {
		return errors.New("publish: empty output path")
	}
	dest, err := w.resolve(relPath)
	if err != nil {
		return err
	}
	if err := fsx.EnsureDir(filepath.Dir(dest)); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(dest), ".staticgen-*")
	if err != nil {
		return fmt.Errorf("publish %s: %w", relPath, err)
	}
	tmpName := tmp.Name()
	defer func() {
		if tmpName != "" {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("publish %s: %w", relPath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("publish %s: %w", relPath, err)
	}
	// Generated files are world-readable; no execute bit.
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("publish %s: %w", relPath, err)
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return fmt.Errorf("publish %s: %w", relPath, err)
	}
	tmpName = ""

	w.mu.Lock()
	w.files[relPath] = int64(len(data))
	w.mu.Unlock()
	return nil
}

// resolve maps a site-relative output path to an absolute path, refusing
// anything that would escape the output directory.
func (w *Writer) resolve(relPath string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(relPath))
	if filepath.IsAbs(clean) {
		return "", fmt.Errorf("publish: refusing absolute output path %q", relPath)
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("publish: refusing to write outside output directory: %q", relPath)
	}
	return filepath.Join(w.dir, clean), nil
}

// Count returns how many files were written and their total size.
func (w *Writer) Count() (int, int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	var total int64
	for _, n := range w.files {
		total += n
	}
	return len(w.files), total
}

// Paths returns the written output paths, sorted.
func (w *Writer) Paths() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]string, 0, len(w.files))
	for p := range w.files {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Clean removes every entry in the output directory so a rebuild cannot leave
// behind pages that were deleted or renamed.
//
// root is the site root; Clean refuses to run when the output directory is not
// safely nested inside it, which prevents a misconfigured output path from
// deleting a source tree.
func (w *Writer) Clean(root string) error {
	if err := checkCleanTarget(w.dir, root); err != nil {
		return err
	}
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("clean %s: %w", w.dir, err)
	}
	for _, e := range entries {
		p := filepath.Join(w.dir, e.Name())
		if err := os.RemoveAll(p); err != nil {
			return fmt.Errorf("clean %s: %w", p, err)
		}
	}
	return nil
}

// checkCleanTarget guards against destructive misconfiguration.
//
// The output directory does not have to live inside the site root — building to
// an artefact path elsewhere is legitimate — so instead of requiring nesting this
// refuses the specific targets where cleaning would destroy something: the root
// itself, any ancestor of the root, the filesystem root, the home directory, and
// any directory that already holds Markdown sources.
func checkCleanTarget(dir, root string) error {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}

	if absDir == string(filepath.Separator) {
		return errors.New("refusing to clean the filesystem root")
	}
	if absDir == absRoot {
		return fmt.Errorf("refusing to clean %s: it is the site root", absDir)
	}
	// If the site root sits inside the output directory, cleaning would delete
	// the sources along with the build.
	if rel, relErr := filepath.Rel(absDir, absRoot); relErr == nil &&
		rel != "." && !strings.HasPrefix(rel, "..") {
		return fmt.Errorf("refusing to clean %s: it contains the site root", absDir)
	}
	if home, homeErr := os.UserHomeDir(); homeErr == nil && absDir == home {
		return fmt.Errorf("refusing to clean the home directory %s", absDir)
	}

	entries, err := os.ReadDir(absDir)
	if err != nil {
		// Nothing to clean, or unreadable; the build will report it later.
		return nil
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if fsx.IsContent(e.Name()) {
			return fmt.Errorf("refusing to clean %s: it contains Markdown source files", absDir)
		}
	}
	return nil
}
