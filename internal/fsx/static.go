package fsx

import (
	"bytes"
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

// PrepareDirWithin creates dir, which must be root or lie beneath it, without
// following anything in the way. Walking down from root, a symlink or plain
// file found where a directory is needed is removed rather than written
// through, so a stale output entry can neither block nor redirect a write
// outside root. root itself may be a symlink.
func PrepareDirWithin(root, dir string) error {
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("%s is outside %s", dir, root)
	}
	if rel != "." {
		cur := root
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			cur = filepath.Join(cur, part)
			st, err := os.Lstat(cur)
			if errors.Is(err, fs.ErrNotExist) {
				break
			}
			if err != nil {
				return fmt.Errorf("stat %s: %w", cur, err)
			}
			if st.IsDir() {
				continue
			}
			if err := os.Remove(cur); err != nil {
				return fmt.Errorf("replace %s with a directory: %w", cur, err)
			}
			break
		}
	}
	return EnsureDir(dir)
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
	return copyTree(src, dst, func(src, dst string, mode os.FileMode) (bool, int64, error) {
		n, err := CopyFile(src, dst, mode)
		return true, n, err
	})
}

// CopyTreeIfChanged copies a tree, leaving files whose destination already
// matches the source untouched. Stats count only what was actually written.
func CopyTreeIfChanged(src, dst string) (CopyStats, error) {
	return copyTree(src, dst, CopyFileIfChanged)
}

// copyFunc copies one file and reports whether it wrote the destination.
type copyFunc func(src, dst string, mode os.FileMode) (bool, int64, error)

func copyTree(src, dst string, copyFn copyFunc) (CopyStats, error) {
	var stats CopyStats
	if src == "" {
		return stats, nil
	}
	err := Walk(src, func(f SourceFile) error {
		target := filepath.Join(dst, filepath.FromSlash(f.RelPath))
		if err := PrepareDirWithin(dst, filepath.Dir(target)); err != nil {
			return err
		}
		copied, n, err := copyFn(f.AbsPath, target, f.Info.Mode().Perm())
		if err != nil {
			return err
		}
		if !copied {
			return nil
		}
		stats.Files++
		stats.Bytes += n
		return nil
	})
	return stats, err
}

// CopyFileIfChanged copies src to dst unless dst already has identical bytes,
// in which case nothing is written and false is returned. It reports whether
// the destination was written.
func CopyFileIfChanged(src, dst string, mode os.FileMode) (bool, int64, error) {
	// Compare sizes first: a changed file almost always differs in length, so
	// the common case avoids reading either file.
	srcInfo, err := os.Stat(src)
	if err != nil {
		return false, 0, err
	}
	if mode == 0 {
		mode = srcInfo.Mode().Perm()
	}
	// A permission-only change still needs a copy, or a newly executable
	// asset stays non-executable in the output.
	if dstInfo, err := os.Lstat(dst); err == nil && dstInfo.Mode().IsRegular() &&
		dstInfo.Size() == srcInfo.Size() && dstInfo.Mode().Perm() == mode.Perm() {
		existing, rErr := os.ReadFile(dst)
		source, sErr := os.ReadFile(src)
		if rErr == nil && sErr == nil && bytes.Equal(existing, source) {
			return false, 0, nil
		}
	}
	n, err := CopyFile(src, dst, mode)
	if err != nil {
		return false, 0, err
	}
	return true, n, nil
}

// CopyFile copies one file to dst atomically, applying mode and the source
// modification time. It returns the number of bytes written.
func CopyFile(src, dst string, mode os.FileMode) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close() //nolint:errcheck // read-only handle

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
			_ = tmp.Close()
			_ = os.Remove(tmpName)
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
