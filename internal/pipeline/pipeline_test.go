package pipeline

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/casien/staticgen/internal/config"
)

// fixtureSite writes a minimal site into a temp dir and returns its config.
func fixtureSite(t *testing.T, mutate func(*config.Site)) *config.Site {
	t.Helper()
	cfg := config.Default()
	cfg.Root = t.TempDir()
	cfg.BaseURL = "https://example.com"
	if mutate != nil {
		mutate(cfg)
	}

	write := func(name, text string) {
		t.Helper()
		p := filepath.Join(cfg.Root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("content/_index.md", "---\ntitle: Home\ndescription: The front page.\n---\nWelcome home.")
	write("content/about.md", "---\ntitle: About\ndescription: About this site.\n---\nAbout body.")
	write("content/blog/hello.md", "---\ntitle: Hello\ndescription: First post.\ndate: 2026-01-02\ntags: [intro]\n---\nHello body.")
	write("content/404.md", "---\ntitle: Missing\n---\nNothing here.")
	write("static/robots.txt", "User-agent: *\n")
	return cfg
}

func build(t *testing.T, cfg *config.Site) *Result {
	t.Helper()
	builder, err := New(cfg, Options{Now: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	res, err := builder.Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func mustRead(t *testing.T, root, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

func TestBuildEndToEnd(t *testing.T) {
	cfg := fixtureSite(t, nil)
	res := build(t, cfg)
	out := cfg.OutputPath()

	if len(res.Warnings) != 0 {
		t.Fatalf("warnings: %v", res.Warnings)
	}
	if res.PagesWritten == 0 || res.AssetsWritten == 0 || res.StaticCopied != 1 {
		t.Fatalf("unexpected counts: %+v", res)
	}

	for name, want := range map[string]string{
		"index.html":            "Welcome home",
		"about/index.html":      "About body",
		"blog/hello/index.html": "Hello body",
		"404.html":              "Nothing here",
		"robots.txt":            "User-agent",
		"feed.xml":              "Hello",
		"sitemap.xml":           "https://example.com/blog/hello/",
		"search.json":           "hello",
		"assets/css/style.css":  "",
	} {
		got := mustRead(t, out, filepath.FromSlash(name))
		if want != "" && !strings.Contains(got, want) {
			t.Errorf("%s is missing %q", name, want)
		}
	}
}

func TestBuildFlatURLs(t *testing.T) {
	cfg := fixtureSite(t, func(c *config.Site) { c.Build.PrettyURLs = false })
	build(t, cfg)
	for _, name := range []string{"index.html", "about.html", "blog/hello.html"} {
		mustRead(t, cfg.OutputPath(), filepath.FromSlash(name))
	}
}

func TestBuildRedirectAndAlias(t *testing.T) {
	cfg := fixtureSite(t, nil)
	rewrite := func(name, old, new string) {
		t.Helper()
		p := filepath.Join(cfg.ContentPath(), filepath.FromSlash(name))
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(strings.Replace(string(raw), old, new, 1)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A redirect stub replaces the page: the body is not rendered alongside it.
	rewrite("blog/hello.md", "tags: [intro]", "tags: [intro]\nredirect: https://elsewhere.example/gone")
	// An alias emits a second stub pointing at the page's canonical URL.
	rewrite("about.md", "description: About this site.", "description: About this site.\naliases: [/hi/]")

	res := build(t, cfg)
	if res.Redirects != 2 {
		t.Fatalf("redirects = %d, want 2 (redirect stub + alias stub)", res.Redirects)
	}
	stub := mustRead(t, cfg.OutputPath(), filepath.Join("blog", "hello", "index.html"))
	if !strings.Contains(stub, "https://elsewhere.example/gone") {
		t.Errorf("redirect stub points at the wrong target:\n%s", stub)
	}
	alias := mustRead(t, cfg.OutputPath(), filepath.Join("hi", "index.html"))
	if !strings.Contains(alias, "https://example.com/about/") {
		t.Errorf("alias stub should point at the canonical URL:\n%s", alias)
	}
}

func TestRebuildAfterEdit(t *testing.T) {
	cfg := fixtureSite(t, nil)
	build(t, cfg)

	p := filepath.Join(cfg.ContentPath(), "about.md")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, append(raw, "\nEdited paragraph."...), 0o644); err != nil {
		t.Fatal(err)
	}

	res := build(t, cfg)
	if len(res.Warnings) != 0 {
		t.Fatalf("warnings: %v", res.Warnings)
	}
	if got := mustRead(t, cfg.OutputPath(), filepath.Join("about", "index.html")); !strings.Contains(got, "Edited paragraph.") {
		t.Error("rebuilt output does not contain the edit")
	}
}

// A rebuild with unchanged content must not rewrite the output: the dev
// server rebuilds on every keystroke, so touching every file each time makes
// reloads crawl and defeats any change-detection downstream.
func TestBuildReusesUnchangedOutputs(t *testing.T) {
	cfg := fixtureSite(t, nil)
	builder, err := New(cfg, Options{Now: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	out := cfg.OutputPath()

	first, err := builder.Build(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first.PagesWritten == 0 {
		t.Fatalf("first build wrote no pages: %+v", first)
	}

	second, err := builder.Build(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second.PagesWritten != 0 {
		t.Errorf("unchanged rebuild rewrote %d pages, want 0", second.PagesWritten)
	}
	if second.PagesUnchanged != first.PagesWritten {
		t.Errorf("unchanged rebuild reused %d pages, want %d", second.PagesUnchanged, first.PagesWritten)
	}

	// Editing one post must rewrite that post and the listings that quote its
	// summary, but nothing else: pages unaffected by the edit keep their
	// bytes, so the filesystem does not churn on every keystroke.
	aboutBefore := mustRead(t, out, "about/index.html")
	notFoundBefore := mustRead(t, out, "404.html")
	edit := filepath.Join(cfg.Root, "content/blog/hello.md")
	if err := os.WriteFile(edit, []byte("---\ntitle: Hello\ndescription: First post.\ndate: 2026-01-02\ntags: [intro]\n---\nEdited body."), 0o644); err != nil {
		t.Fatal(err)
	}
	third, err := builder.Build(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if third.PagesWritten == 0 {
		t.Error("edited post was not rebuilt")
	}
	if third.PagesWritten >= first.PagesWritten {
		t.Errorf("one edit rewrote %d of %d pages; most should be reused",
			third.PagesWritten, first.PagesWritten)
	}
	if got := mustRead(t, out, "blog/hello/index.html"); !strings.Contains(got, "Edited body") {
		t.Error("edited post is missing from the output")
	}
	if got := mustRead(t, out, "about/index.html"); got != aboutBefore {
		t.Error("unrelated page changed after editing one post")
	}
	if got := mustRead(t, out, "404.html"); got != notFoundBefore {
		t.Error("unrelated page changed after editing one post")
	}
}

// Deleting content must not leave its page behind: incremental builds skip the
// full output wipe, so stale files have to be pruned explicitly.
func TestBuildPrunesDeletedContent(t *testing.T) {
	cfg := fixtureSite(t, nil)
	builder, err := New(cfg, Options{Now: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if _, err := builder.Build(ctx); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(cfg.Root, "content/about.md")); err != nil {
		t.Fatal(err)
	}

	final, err := builder.Build(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if final.Pruned != 1 {
		t.Errorf("pruned %d files, want 1", final.Pruned)
	}
	if _, err := os.Stat(filepath.Join(cfg.OutputPath(), "about/index.html")); !os.IsNotExist(err) {
		t.Error("deleted content left its page in the output")
	}
	// The surviving pages and copied static files must survive pruning.
	if !strings.Contains(mustRead(t, cfg.OutputPath(), "blog/hello/index.html"), "Hello body") {
		t.Error("pruning removed a live page")
	}
	if mustRead(t, cfg.OutputPath(), "robots.txt") != "User-agent: *\n" {
		t.Error("pruning removed a copied static file")
	}
}

// writePNG writes a solid-colour PNG of the given size, for image fixtures.
func writePNG(t *testing.T, path string, width, height int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 120, A: 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func pngSize(t *testing.T, path string) (int, int) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cfg, err := png.DecodeConfig(f)
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Width, cfg.Height
}

// Oversized images gain a downscaled sibling next to the original, so the
// original URL keeps working while themes can point at the lighter file.
func TestBuildResizesOversizedImages(t *testing.T) {
	cfg := fixtureSite(t, func(c *config.Site) {
		c.Images.Resize = true
		c.Images.MaxWidth = 100
		c.Images.Quality = 80
	})
	writePNG(t, filepath.Join(cfg.StaticPath(), "images", "photo.png"), 240, 120)
	writePNG(t, filepath.Join(cfg.StaticPath(), "images", "small.png"), 60, 30)
	out := cfg.OutputPath()

	if res := build(t, cfg); len(res.Warnings) != 0 {
		t.Fatalf("warnings: %v", res.Warnings)
	}

	if w, h := pngSize(t, filepath.Join(out, "images", "photo.png")); w != 240 || h != 120 {
		t.Errorf("original was modified: %dx%d, want 240x120", w, h)
	}
	if w, h := pngSize(t, filepath.Join(out, "images", "photo-100w.png")); w != 100 || h != 50 {
		t.Errorf("resized image is %dx%d, want 100x50", w, h)
	}
	if _, err := os.Stat(filepath.Join(out, "images", "small-100w.png")); !os.IsNotExist(err) {
		t.Error("an image already within the limit should not gain a variant")
	}

	// The variant must survive a rebuild, and must not be re-encoded.
	variant := filepath.Join(out, "images", "photo-100w.png")
	before, err := os.ReadFile(variant)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(variant, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	build(t, cfg)
	if got, err := os.ReadFile(variant); err != nil {
		t.Fatal(err)
	} else if string(got) == "stale" {
		t.Error("rebuild left a stale variant in place")
	} else if string(got) != string(before) {
		t.Error("rebuild changed the variant; encoding is not deterministic")
	}
}

// Resizing is off by default: images are copied verbatim with no variants.
func TestBuildLeavesImagesAloneWhenResizeDisabled(t *testing.T) {
	cfg := fixtureSite(t, nil)
	writePNG(t, filepath.Join(cfg.StaticPath(), "images", "photo.png"), 240, 120)

	build(t, cfg)

	if _, err := os.Stat(filepath.Join(cfg.OutputPath(), "images", "photo-100w.png")); !os.IsNotExist(err) {
		t.Error("images.resize is false but a variant was written")
	}
}
