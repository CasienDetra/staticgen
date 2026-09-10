package fsx

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// SourceFile is one file discovered by Walk.
type SourceFile struct {
	// RelPath is slash-separated and relative to the walked root.
	RelPath string
	AbsPath string
	Info    fs.FileInfo
}

// Walk visits every regular file under root in lexical order, skipping hidden
// directories and symlinked directories so a build can never escape the source
// tree. A missing root is not an error: it yields no files, which lets the
// static directory be optional.
func Walk(root string, fn func(SourceFile) error) error {
	info, err := os.Stat(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat %s: %w", root, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", root)
	}

	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)

		if d.IsDir() {
			base := filepath.Base(p)
			if rel != "." && strings.HasPrefix(base, ".") {
				return filepath.SkipDir
			}
			return nil
		}

		// Symlinks are skipped: following them could pull in files outside the
		// source tree, and a broken link would otherwise fail the build.
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}

		fi, err := d.Info()
		if err != nil {
			return fmt.Errorf("stat %s: %w", p, err)
		}
		return fn(SourceFile{RelPath: rel, AbsPath: p, Info: fi})
	})
}

// EnsureDir creates dir and its parents.
func EnsureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}
	return nil
}

// CopyStats reports what CopyTree did.
type CopyStats struct {
	Files int
	Bytes int64
}

// CopyTree copies every regular file from src to dst, preserving relative
// layout, file mode and modification time. A missing src yields no copies and
// no error, so the static directory stays optional.
//
// Files are written to a temporary name and renamed into place, so a reader
// (the dev server) never observes a partially written asset.
func CopyTree(src, dst string) (CopyStats, error) {
	var stats CopyStats
	if src == "" {
		return stats, nil
	}
	err := Walk(src, func(f SourceFile) error {
		target := filepath.Join(dst, filepath.FromSlash(f.RelPath))
		n, err := CopyFile(f.AbsPath, target, f.Info.Mode().Perm())
		if err != nil {
			return err
		}
		stats.Files++
		stats.Bytes += n
		return nil
	})
	return stats, err
}

// CopyFile copies one file to dst atomically, applying mode and the source
// modification time. It returns the number of bytes written.
func CopyFile(src, dst string, mode os.FileMode) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close()

	srcInfo, err := in.Stat()
	if err != nil {
		return 0, fmt.Errorf("stat %s: %w", src, err)
	}
	if mode == 0 {
		mode = srcInfo.Mode().Perm()
	}

	if err := EnsureDir(filepath.Dir(dst)); err != nil {
		return 0, err
	}

	tmp, err := os.CreateTemp(filepath.Dir(dst), ".staticgen-*")
	if err != nil {
		return 0, fmt.Errorf("create temp file in %s: %w", filepath.Dir(dst), err)
	}
	tmpName := tmp.Name()
	// Any error path must remove the temp file, or failed builds litter the
	// output directory with dotfiles.
	defer func() {
		if tmpName != "" {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()

	n, err := io.Copy(tmp, in)
	if err != nil {
		return 0, fmt.Errorf("copy %s to %s: %w", src, dst, err)
	}
	if err := tmp.Close(); err != nil {
		return 0, fmt.Errorf("close temp file for %s: %w", dst, err)
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return 0, fmt.Errorf("chmod %s: %w", dst, err)
	}
	// Preserving mtime keeps downstream caches and rsync-style deploys correct.
	if err := os.Chtimes(tmpName, srcInfo.ModTime(), srcInfo.ModTime()); err != nil {
		return 0, fmt.Errorf("set times on %s: %w", dst, err)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return 0, fmt.Errorf("rename %s to %s: %w", tmpName, dst, err)
	}
	tmpName = "" // ownership transferred; disarm cleanup
	return n, nil
}
