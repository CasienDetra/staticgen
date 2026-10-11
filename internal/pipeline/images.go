package pipeline

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/gif"
	"image/png"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/disintegration/imaging"

	"github.com/casien/staticgen/internal/config"
	"github.com/casien/staticgen/internal/fsx"
	"github.com/casien/staticgen/internal/publish"
)

// rasterExtensions are the formats imaging can decode and re-encode.
var rasterExtensions = map[string]bool{
	".jpg":  true,
	".jpeg": true,
	".png":  true,
	".gif":  true,
	".tif":  true,
	".tiff": true,
}

// processImages writes a downscaled sibling for every static image wider than
// the configured maximum.
//
// The original is left untouched, so URLs that already point at it keep
// working; the variant sits next to it as <name>-<width>w<ext> for themes to
// reference.
//
// prev maps each variant written by the previous build to the fingerprint of
// the source and settings it was made from; the returned map is the same for
// this build. The variant filename encodes only the source name and the
// width, so without it a rebuild would keep serving a variant made from an
// older source or different settings. The check happens before decoding, so a
// rebuild that changed no images does no image work at all. The fingerprints
// are kept in memory rather than in the output, so nothing internal is
// published or collides with a static asset; a fresh process (a new
// `staticgen build`) re-encodes once and rewrites only variants that differ.
func processImages(cfg *config.Site, staticDir, outDir string, w *publish.Writer, res *Result, prev map[string]string) (map[string]string, error) {
	if !cfg.Images.Resize {
		return nil, nil
	}

	// Built only from variants this build produced or reused, so a removed
	// or shrunk image drops out.
	used := map[string]string{}

	err := fsx.Walk(staticDir, func(f fsx.SourceFile) error {
		rel, src := f.RelPath, f.AbsPath
		ext := strings.ToLower(filepath.Ext(src))
		if !rasterExtensions[ext] {
			return nil
		}

		// An animated GIF carries frames and timing that a single-image
		// decode cannot carry, so a variant would silently lose the
		// animation. Skipping keeps the original usable.
		if ext == ".gif" {
			if animated, err := isAnimatedGIF(src); err == nil && animated {
				res.Warnings = append(res.Warnings,
					fmt.Sprintf("image %s: animated GIF skipped, no downscaled variant written", rel))
				return nil
			}
		}

		out := variantPath(rel, cfg.Images.MaxWidth)

		// Fingerprint the source and every setting that affects the encoded
		// result, and compare before decoding.
		sum, err := fingerprint(src, cfg)
		if err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("image %s: %v", rel, err))
			return nil
		}
		if prev[out] == sum {
			if _, statErr := os.Stat(filepath.Join(outDir, filepath.FromSlash(out))); statErr == nil {
				w.Track(out)
				used[out] = sum
				return nil
			}
		}

		img, err := imaging.Open(src, imaging.AutoOrientation(true))
		if err != nil {
			// An unreadable or unsupported file is left for the browser to
			// report; a bad image must not fail the build.
			res.Warnings = append(res.Warnings, fmt.Sprintf("image %s: %v", rel, err))
			return nil
		}
		if img.Bounds().Dx() <= cfg.Images.MaxWidth {
			return nil
		}
		scaled := imaging.Resize(img, cfg.Images.MaxWidth, 0, imaging.Lanczos)
		encoded, err := encodeImage(scaled, ext, cfg.Images.Quality)
		if err != nil {
			return fmt.Errorf("encode %s: %w", rel, err)
		}

		changed, err := w.WriteIfChanged(out, encoded)
		if err != nil {
			return err
		}
		if changed {
			res.ImagesResized++
			res.Bytes += int64(len(encoded))
		}
		used[out] = sum
		return nil
	})
	if err != nil {
		return nil, err
	}
	return used, nil
}

// fingerprint hashes the source bytes with the settings that change the
// encoded result, so a changed image or a changed quality invalidates it.
func fingerprint(src string, cfg *config.Site) (string, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write(data)
	_, _ = fmt.Fprintf(h, "|max_width=%d|quality=%d", cfg.Images.MaxWidth, cfg.Images.Quality)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// encodeImage re-encodes a scaled image in its original format. Quality
// applies to lossy formats; PNG and GIF take a compression level instead.
func encodeImage(img image.Image, ext string, quality int) ([]byte, error) {
	format := imaging.PNG
	var opts []imaging.EncodeOption

	switch ext {
	case ".jpg", ".jpeg":
		format, opts = imaging.JPEG, []imaging.EncodeOption{imaging.JPEGQuality(quality)}
	case ".gif":
		format = imaging.GIF
	case ".tif", ".tiff":
		format = imaging.TIFF
	default:
		opts = []imaging.EncodeOption{imaging.PNGCompressionLevel(pngCompression(quality))}
	}

	var buf bytes.Buffer
	if err := imaging.Encode(&buf, img, format, opts...); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// pngCompression maps a 0-100 quality onto PNG's 0-9 speed scale, where lower
// means better compression.
func pngCompression(quality int) png.CompressionLevel {
	c := 9 - quality/12
	if c < 0 {
		return png.CompressionLevel(0)
	}
	if c > 9 {
		return png.CompressionLevel(9)
	}
	return png.CompressionLevel(c)
}

// isAnimatedGIF reports whether the file holds more than one frame.
func isAnimatedGIF(p string) (bool, error) {
	f, err := os.Open(p)
	if err != nil {
		return false, err
	}
	defer f.Close() //nolint:errcheck // read-only handle
	g, err := gif.DecodeAll(f)
	if err != nil {
		return false, err
	}
	return len(g.Image) > 1, nil
}

// variantPath turns images/photo.png into images/photo-100w.png.
func variantPath(rel string, width int) string {
	ext := path.Ext(rel)
	return strings.TrimSuffix(rel, ext) + fmt.Sprintf("-%dw%s", width, ext)
}
