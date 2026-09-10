package render

import (
	"html/template"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/casien/staticgen/internal/config"
	"github.com/casien/staticgen/internal/content"
	"github.com/casien/staticgen/internal/site"
)

func testConfig(t *testing.T) *config.Site {
	t.Helper()
	cfg := config.Default()
	cfg.Root = t.TempDir()
	cfg.Title = "Render Test"
	cfg.Description = "A site"
	cfg.Language = "en-us"
	return cfg
}

// testSite assembles an empty site, which synthesizes a home page so the model
// is always complete.
func testSite(t *testing.T, cfg *config.Site) *site.Site {
	t.Helper()
	s, err := site.NewAssembler(cfg).Assemble(nil, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	return s
}

func testPage(title, url, body string) *content.Page {
	return &content.Page{
		URL:        url,
		OutputPath: strings.TrimPrefix(url, "/") + "index.html",
		SourcePath: strings.TrimPrefix(url, "/") + "post.md",
		Kind:       content.KindPage,
		Meta: content.Meta{
			Title:   title,
			Date:    time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
			LastMod: time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC),
			Extra:   map[string]any{},
		},
		BodyHTML:       template.HTML(body),
		PlainText:      strings.TrimSpace(body),
		WordCount:      42,
		ReadingMinutes: 1,
	}
}

func render(t *testing.T, e *Engine, p *content.Page, s *site.Site) string {
	t.Helper()
	out, err := e.RenderPage(p, s)
	if err != nil {
		t.Fatalf("RenderPage(%s): %v", p.URL, err)
	}
	return string(out)
}

func TestRenderDefaultTheme(t *testing.T) {
	cfg := testConfig(t)
	e, err := New(Options{Config: cfg})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	s := testSite(t, cfg)
	p := testPage("Hello World", "/blog/hello/", "<p>The body.</p>")

	got := render(t, e, p, s)

	for _, want := range []string{
		"<!DOCTYPE html>",
		`<html lang="en-us">`,
		"<title>Hello World · Render Test</title>",
		"<h1>Hello World</h1>",
		"<p>The body.</p>",
		`<link rel="stylesheet" href="/assets/css/style.css`,
		`<meta property="og:type" content="article">`,
		`<meta property="og:url" content="/blog/hello/">`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered page missing %q", want)
		}
	}
}

func TestHomeLayoutUsedForHomeKind(t *testing.T) {
	cfg := testConfig(t)
	e, err := New(Options{Config: cfg})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s := testSite(t, cfg)

	got := render(t, e, s.Home, s)

	// home.html renders a hero, which no other layout does.
	if !strings.Contains(got, `class="hero"`) {
		t.Errorf("the home page should use the home layout\n%s", got[:min(400, len(got))])
	}
	if !strings.Contains(got, "<title>Render Test</title>") {
		t.Error("the home page should not get a title suffix")
	}
}

func TestLiveReloadSnippetOnlyInServeMode(t *testing.T) {
	cfg := testConfig(t)
	s := testSite(t, cfg)
	p := testPage("P", "/p/", "body")

	build, err := New(Options{Config: cfg})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if strings.Contains(render(t, build, p, s), "__livereload") {
		t.Error("a production build must not embed the live reload client")
	}

	serveEngine, err := New(Options{Config: cfg, LiveReload: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !strings.Contains(render(t, serveEngine, p, s), "__livereload") {
		t.Error("serve mode should embed the live reload client")
	}
}

func TestUnsafeConfigIsOffByDefault(t *testing.T) {
	// The engine renders pre-built HTML, so this guards the template pipeline
	// rather than Markdown: raw HTML in a page description must be escaped.
	cfg := testConfig(t)
	e, err := New(Options{Config: cfg})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s := testSite(t, cfg)

	p := testPage("P", "/p/", "body")
	p.Meta.Description = `<script>alert("xss")</script>`

	got := render(t, e, p, s)
	if strings.Contains(got, `<script>alert("xss")</script>`) {
		t.Error("a frontmatter description must be escaped, not emitted as markup")
	}
	if !strings.Contains(got, "&lt;script&gt;") {
		t.Error("the description should appear escaped")
	}
}

func TestUserTemplateOverridesEmbedded(t *testing.T) {
	cfg := testConfig(t)
	dir := filepath.Join(cfg.Root, "templates")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Overriding one layout must not require overriding the rest.
	custom := "{{ define \"main\" }}<div id=\"mine\">CUSTOM MARKER</div>{{ end }}\n"
	if err := os.WriteFile(filepath.Join(dir, "page.html"), []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}

	e, err := New(Options{Config: cfg, TemplatesDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s := testSite(t, cfg)
	got := render(t, e, testPage("P", "/p/", "body"), s)

	if !strings.Contains(got, "CUSTOM MARKER") {
		t.Error("the user's page.html should replace the embedded one")
	}
	// The embedded base and partials must still be in play.
	if !strings.Contains(got, "<!DOCTYPE html>") || !strings.Contains(got, `class="site-header"`) {
		t.Error("overriding one layout should leave the shared shell intact")
	}
}

func TestLayoutFromFrontmatter(t *testing.T) {
	cfg := testConfig(t)
	dir := filepath.Join(cfg.Root, "templates")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wide.html"),
		[]byte("{{ define \"main\" }}WIDE LAYOUT{{ end }}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	e, err := New(Options{Config: cfg, TemplatesDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s := testSite(t, cfg)

	p := testPage("P", "/p/", "body")
	p.Meta.Layout = "wide"
	if got := render(t, e, p, s); !strings.Contains(got, "WIDE LAYOUT") {
		t.Error("a page's layout field should select the named template")
	}

	// An unknown layout falls back rather than failing the whole build.
	p.Meta.Layout = "does-not-exist"
	if got := render(t, e, p, s); strings.Contains(got, "WIDE LAYOUT") {
		t.Error("an unknown layout should not resolve to another custom layout")
	}
}

func TestLayoutWithoutMainBlockIsRejected(t *testing.T) {
	cfg := testConfig(t)
	dir := filepath.Join(cfg.Root, "templates")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.html"),
		[]byte("<p>no main block here</p>\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := New(Options{Config: cfg, TemplatesDir: dir}); err == nil {
		t.Fatal("a layout that defines no \"main\" block should be rejected at load time")
	} else if !strings.Contains(err.Error(), "broken.html") {
		t.Errorf("the error should name the offending layout: %v", err)
	}
}

func TestAssetsIncludeEmbeddedAndChromaHook(t *testing.T) {
	cfg := testConfig(t)
	cfg.Markup.Highlight.Enabled = true
	e, err := New(Options{Config: cfg})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	assets := e.Assets()
	for _, name := range []string{"css/style.css", "js/search.js"} {
		if len(assets[name]) == 0 {
			t.Errorf("embedded asset %s is missing or empty", name)
		}
	}
	if strings.Contains(string(assets["css/style.css"]), "{{") {
		t.Error("assets are served verbatim and must not be template-parsed")
	}
}

func TestUserAssetOverridesEmbedded(t *testing.T) {
	cfg := testConfig(t)
	dir := filepath.Join(cfg.Root, "templates")
	if err := os.MkdirAll(filepath.Join(dir, "css"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "css", "style.css"), []byte("/* MINE */"), 0o644); err != nil {
		t.Fatal(err)
	}

	e, err := New(Options{Config: cfg, TemplatesDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := string(e.Assets()["css/style.css"]); got != "/* MINE */" {
		t.Errorf("asset css/style.css = %q, want the user's file", got)
	}
}

func TestAssetVersionCacheBusts(t *testing.T) {
	cfg := testConfig(t)
	e, err := New(Options{Config: cfg, AssetVersion: "abc123"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s := testSite(t, cfg)
	got := render(t, e, testPage("P", "/p/", "body"), s)

	if !strings.Contains(got, "/assets/css/style.css?v=abc123") {
		t.Error("asset URLs should carry the version for cache busting")
	}

	unversioned, err := New(Options{Config: cfg})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := render(t, unversioned, testPage("P", "/p/", "body"), s); strings.Contains(got, "?v=") {
		t.Error("without a version, asset URLs should stay clean")
	}
}

func TestFuncMapHelpers(t *testing.T) {
	cfg := testConfig(t)
	cfg.BaseURL = "https://example.com"
	e, err := New(Options{Config: cfg})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	fn := e.funcMap()

	if got := fn["absURL"].(func(string) string)("https://other.com/x"); got != "https://other.com/x" {
		t.Errorf("absURL must leave an absolute URL alone, got %q", got)
	}
	if got := fn["absURL"].(func(string) string)("/feed.xml"); got != "https://example.com/feed.xml" {
		t.Errorf("absURL(/feed.xml) = %q", got)
	}
	if got := fn["truncate"].(func(int, string) string)(5, "abcdefgh"); got != "abcde…" {
		t.Errorf("truncate = %q", got)
	}
	if got := fn["truncate"].(func(int, string) string)(50, "short"); got != "short" {
		t.Errorf("truncate should not pad, got %q", got)
	}
	if got := fn["readingTime"].(func(int) int)(500); got != 3 {
		t.Errorf("readingTime(500) = %d, want 3 (round up)", got)
	}
	if got := fn["tagURL"].(func(string) string)("Hello World"); got != "/tags/hello-world/" {
		t.Errorf("tagURL = %q", got)
	}
	if got := fn["pluralize"].(func(int, string, string) string)(1, "post", "posts"); got != "post" {
		t.Errorf("pluralize(1) = %q", got)
	}
	if got := fn["pluralize"].(func(int, string, string) string)(2, "post", "posts"); got != "posts" {
		t.Errorf("pluralize(2) = %q", got)
	}
	if got := fn["seq"].(func(int) []int)(3); len(got) != 3 || got[0] != 1 || got[2] != 3 {
		t.Errorf("seq(3) = %v", got)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
