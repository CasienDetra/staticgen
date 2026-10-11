package pipeline

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
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
// reference. Variants are written only when absent, so a rebuild never
// re-encodes an unchanged image.
func processImages(cfg *config.Site, staticDir, outDir string, w *publish.Writer, res *Result) error {
	if !cfg.Images.Resize {
		return nil
	}
	return fsx.Walk(staticDir, func(f fsx.SourceFile) error {
		rel, src := f.RelPath, f.AbsPath
		ext := strings.ToLower(filepath.Ext(src))
		if !rasterExtensions[ext] {
			return nil
		}
		img, err := imaging.Open(src, imaging.AutoOrientation(true))
		if err != nil {
			// An unreadable or unsupported file is left for the browser to
			// report; a bad image must not fail the build.
			res.Warnings = append(res.Warnings, fmt.Sprintf("image %s: %v", rel, err))
			return nil
		}
		bounds := img.Bounds()
		if bounds.Dx() <= cfg.Images.MaxWidth {
			return nil
		}
		scaled := imaging.Resize(img, cfg.Images.MaxWidth, 0, imaging.Lanczos)
		encoded, err := encodeImage(scaled, ext, cfg.Images.Quality)
		if err != nil {
			return fmt.Errorf("encode %s: %w", rel, err)
		}
		out := variantPath(rel, cfg.Images.MaxWidth)
		dest := filepath.Join(outDir, filepath.FromSlash(out))
		if st, err := os.Stat(dest); err == nil && st.Size() > 0 {
			w.Track(out)
			return nil
		}
		changed, err := w.WriteIfChanged(out, encoded)
		if err != nil {
			return err
		}
		if changed {
			res.ImagesResized++
			res.Bytes += int64(len(encoded))
		}
		return nil
	})
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

// variantPath turns images/photo.png into images/photo-100w.png.
func variantPath(rel string, width int) string {
	ext := filepath.Ext(rel)
	return strings.TrimSuffix(rel, ext) + fmt.Sprintf("-%dw%s", width, ext)
}
