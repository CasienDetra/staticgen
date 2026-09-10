// Package serve runs the development server: it watches the source tree,
// rebuilds on change, serves the output directory and pushes reloads to any
// connected browsers.
package serve

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// Watcher watches a set of directory trees.
//
// fsnotify does not watch recursively — adding a directory reports changes to
// its own files but nothing inside its subdirectories. So AddTree walks the tree
// and adds every directory, and AddIfDir is called on Create events so
// directories made after the watch started are picked up.
type Watcher struct {
	fsw  *fsnotify.Watcher
	logf func(string, ...any)

	mu      sync.Mutex
	watched map[string]bool
	// skip reports whether a path should be ignored, e.g. the build output.
	skip func(string) bool
}

// NewWatcher creates a Watcher. logf receives warnings about paths that could
// not be watched; it must not be nil.
func NewWatcher(logf func(string, ...any), skip func(string) bool) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("create file watcher: %w", err)
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Watcher{
		fsw:     fsw,
		logf:    logf,
		watched: map[string]bool{},
		skip:    skip,
	}, nil
}

// Events returns the underlying event channel.
func (w *Watcher) Events() <-chan fsnotify.Event { return w.fsw.Events }

// Errors returns the underlying error channel.
func (w *Watcher) Errors() <-chan error { return w.fsw.Errors }

// Close releases the watcher.
func (w *Watcher) Close() error { return w.fsw.Close() }

// Watched reports every directory currently watched, sorted. Useful for
// diagnosing an inotify limit, which surfaces as missing watches rather than
// an error at Add time on some platforms.
func (w *Watcher) Watched() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]string, 0, len(w.watched))
	for p := range w.watched {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// AddTree watches root and every directory beneath it. A missing root is not an
// error: the static and template directories are optional.
func (w *Watcher) AddTree(root string) error {
	if root == "" {
		return nil
	}
	st, err := os.Stat(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat %s: %w", root, err)
	}
	if !st.IsDir() {
		return w.AddFile(root)
	}

	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// A directory that vanished mid-walk is not worth failing over.
			w.logf("watch: skipping %s: %v", p, err)
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		base := filepath.Base(p)
		if p != root && (strings.HasPrefix(base, ".") || w.skipped(p)) {
			return filepath.SkipDir
		}
		if addErr := w.add(p); addErr != nil {
			w.logf("watch: could not watch %s: %v", p, addErr)
		}
		return nil
	})
}

// AddFile watches a single file, used for the config file.
func (w *Watcher) AddFile(path string) error {
	if path == "" {
		return nil
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	return w.add(path)
}

// AddIfDir watches path when it is a directory. Called on Create events so a
// directory added while the server runs becomes watched.
func (w *Watcher) AddIfDir(path string) {
	st, err := os.Stat(path)
	if err != nil || !st.IsDir() {
		return
	}
	if w.skipped(path) {
		return
	}
	if err := w.add(path); err != nil {
		w.logf("watch: could not watch new directory %s: %v", path, err)
	}
}

func (w *Watcher) skipped(path string) bool {
	return w.skip != nil && w.skip(path)
}

func (w *Watcher) add(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	w.mu.Lock()
	if w.watched[abs] {
		w.mu.Unlock()
		return nil
	}
	w.mu.Unlock()

	if err := w.fsw.Add(abs); err != nil {
		return err
	}

	w.mu.Lock()
	w.watched[abs] = true
	w.mu.Unlock()
	return nil
}
