package content

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/casien/staticgen/internal/markdown"
)

// now is a fixed clock so draft, future and expiry filtering are deterministic.
var now = time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

func testRenderer() *markdown.Renderer {
	return markdown.New(markdown.Options{Typographer: true})
}

func writeFile(t *testing.T, root, rel, body string) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	return p
}

// load runs the loader with the site defaults: a fixed clock and pretty URLs.
// Tests that need flat URLs build a Loader directly.
func load(t *testing.T, root string, opts LoadOptions) []*Page {
	t.Helper()
	if opts.Now.IsZero() {
		opts.Now = now
	}
	opts.PrettyURLs = true
	pages, err := NewLoader(root, testRenderer(), opts).Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return pages
}

func TestLoaderFlatURLs(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "about.md", "---\ntitle: About\n---\nx\n")
	writeFile(t, root, "blog/post.md", "---\ntitle: Post\n---\ny\n")
	writeFile(t, root, "blog/_index.md", "---\ntitle: Blog\n---\nz\n")
	writeFile(t, root, "404.md", "---\ntitle: Missing\n---\nq\n")

	pages, err := NewLoader(root, testRenderer(), LoadOptions{Now: now, PrettyURLs: false}).Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := map[string]string{ // URL -> output path
		"/about.html":     "about.html",
		"/blog/post.html": "blog/post.html",
		"/blog/":          "blog/index.html", // a section stays a directory
		"/404.html":       "404.html",
	}
	if len(pages) != len(want) {
		t.Fatalf("loaded %v, want %d pages", titles(pages), len(want))
	}
	for _, p := range pages {
		out, ok := want[p.URL]
		if !ok {
			t.Errorf("unexpected URL %q", p.URL)
			continue
		}
		if filepath.ToSlash(p.OutputPath) != out {
			t.Errorf("%s: OutputPath = %q, want %q", p.URL, p.OutputPath, out)
		}
	}
}

func byURL(pages []*Page) map[string]*Page {
	out := make(map[string]*Page, len(pages))
	for _, p := range pages {
		out[p.URL] = p
	}
	return out
}

func TestLoaderURLsAndKinds(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "_index.md", "---\ntitle: Home\n---\nWelcome.\n")
	writeFile(t, root, "about.md", "---\ntitle: About\n---\nAbout text.\n")
	writeFile(t, root, "404.md", "---\ntitle: Missing\n---\nNot here.\n")
	writeFile(t, root, "blog/_index.md", "---\ntitle: Blog\n---\nPosts.\n")
	writeFile(t, root, "blog/2026-01-15-hello.md", "---\ntitle: Hello\n---\nBody.\n")

	pages := byURL(load(t, root, LoadOptions{PrettyURLs: true}))

	want := map[string]struct {
		kind    Kind
		out     string
		noIndex bool
	}{
		"/":            {KindHome, "index.html", false},
		"/about/":      {KindPage, "about/index.html", false},
		"/404.html":    {KindPage, "404.html", true},
		"/blog/":       {KindSection, "blog/index.html", false},
		"/blog/hello/": {KindPage, "blog/hello/index.html", false},
	}

	if len(pages) != len(want) {
		t.Fatalf("loaded %d pages (%v), want %d", len(pages), keys(pages), len(want))
	}
	for url, w := range want {
		p, ok := pages[url]
		if !ok {
			t.Errorf("no page at %s; got %v", url, keys(pages))
			continue
		}
		if p.Kind != w.kind {
			t.Errorf("%s: Kind = %q, want %q", url, p.Kind, w.kind)
		}
		if filepath.ToSlash(p.OutputPath) != w.out {
			t.Errorf("%s: OutputPath = %q, want %q", url, p.OutputPath, w.out)
		}
		if p.NoIndex != w.noIndex {
			t.Errorf("%s: NoIndex = %v, want %v", url, p.NoIndex, w.noIndex)
		}
	}
}

func TestLoaderSections(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "blog/a.md", "---\ntitle: A\n---\nx\n")
	writeFile(t, root, "docs/b.md", "---\ntitle: B\n---\ny\n")
	writeFile(t, root, "root.md", "---\ntitle: R\n---\nz\n")

	pages := byURL(load(t, root, LoadOptions{}))
	if got := pages["/blog/a/"].Section; got != "blog" {
		t.Errorf("Section = %q, want blog", got)
	}
	if got := pages["/docs/b/"].Section; got != "docs" {
		t.Errorf("Section = %q, want docs", got)
	}
	if got := pages["/root/"].Section; got != "" {
		t.Errorf("root page Section = %q, want empty", got)
	}
}

func TestLoaderDraftFiltering(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.md", "---\ntitle: A\ndraft: true\n---\nx\n")
	writeFile(t, root, "b.md", "---\ntitle: B\n---\ny\n")

	if pages := load(t, root, LoadOptions{}); len(pages) != 1 {
		t.Errorf("without IncludeDrafts loaded %d pages, want 1 (the draft excluded)", len(pages))
	} else if pages[0].Meta.Title != "B" {
		t.Errorf("loaded %q, want the published page B", pages[0].Meta.Title)
	}

	if pages := load(t, root, LoadOptions{IncludeDrafts: true}); len(pages) != 2 {
		t.Errorf("with IncludeDrafts loaded %d pages, want 2", len(pages))
	}
}

func TestLoaderFutureFiltering(t *testing.T) {
	root := t.TempDir()
	future := now.Add(48 * time.Hour).Format(time.RFC3339)
	past := now.Add(-48 * time.Hour).Format(time.RFC3339)
	writeFile(t, root, "future.md", "---\ntitle: Future\ndate: "+future+"\n---\nx\n")
	writeFile(t, root, "past.md", "---\ntitle: Past\ndate: "+past+"\n---\ny\n")

	pages := load(t, root, LoadOptions{})
	if len(pages) != 1 || pages[0].Meta.Title != "Past" {
		t.Errorf("loaded %v, want only the past page", titles(pages))
	}

	if pages := load(t, root, LoadOptions{IncludeFuture: true}); len(pages) != 2 {
		t.Errorf("with IncludeFuture loaded %v, want both pages", titles(pages))
	}
}

// A date inferred from mtime is not a publication schedule. Filtering on it
// would silently drop pages whenever a checkout, restore or clock skew produced
// a timestamp ahead of the build clock.
func TestLoaderMtimeInFutureIsNotFiltered(t *testing.T) {
	root := t.TempDir()
	p := writeFile(t, root, "no-date.md", "---\ntitle: NoDate\n---\nx\n")
	future := now.Add(72 * time.Hour)
	if err := os.Chtimes(p, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	pages := load(t, root, LoadOptions{}) // IncludeFuture deliberately false
	if len(pages) != 1 {
		t.Errorf("loaded %v, want the page even though its mtime is in the future", titles(pages))
	}
}

func TestLoaderExpiry(t *testing.T) {
	root := t.TempDir()
	expired := now.Add(-24 * time.Hour).Format(time.RFC3339)
	live := now.Add(24 * time.Hour).Format(time.RFC3339)
	writeFile(t, root, "gone.md", "---\ntitle: Gone\nexpires: "+expired+"\n---\nx\n")
	writeFile(t, root, "here.md", "---\ntitle: Here\nexpires: "+live+"\n---\ny\n")

	pages := load(t, root, LoadOptions{})
	if len(pages) != 1 || pages[0].Meta.Title != "Here" {
		t.Errorf("loaded %v, want only the unexpired page", titles(pages))
	}
}

func TestLoaderDatePrecedence(t *testing.T) {
	root := t.TempDir()

	// 1. Explicit frontmatter date wins over everything.
	explicit := "2025-12-25T08:00:00Z"
	writeFile(t, root, "2026-01-15-explicit.md", "---\ntitle: E\ndate: "+explicit+"\n---\nx\n")

	// 2. A filename date prefix is used when frontmatter has none.
	writeFile(t, root, "2026-02-20-fromname.md", "---\ntitle: N\n---\ny\n")

	// 3. Otherwise the file's modification time is used.
	mtime := time.Date(2026, 3, 9, 6, 0, 0, 0, time.UTC)
	p := writeFile(t, root, "frommtime.md", "---\ntitle: M\n---\nz\n")
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	// A filename whose date fields are not a real date must fall back to mtime
	// rather than silently producing a normalised date in another year.
	bogus := writeFile(t, root, "2026-13-45-bogus.md", "---\ntitle: B\n---\nw\n")
	bogusMtime := time.Date(2026, 4, 4, 4, 0, 0, 0, time.UTC)
	if err := os.Chtimes(bogus, bogusMtime, bogusMtime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	pages := byURL(load(t, root, LoadOptions{}))

	if got := pages["/explicit/"].Date(); got.Year() != 2025 || got.Month() != time.December || got.Day() != 25 {
		t.Errorf("explicit date = %v, want 2025-12-25", got)
	}
	if got := pages["/fromname/"].Date(); got.Year() != 2026 || got.Month() != time.February || got.Day() != 20 {
		t.Errorf("filename date = %v, want 2026-02-20", got)
	}
	if got := pages["/frommtime/"].Date(); !got.Equal(mtime) {
		t.Errorf("mtime date = %v, want %v", got, mtime)
	}
	if got := pages["/bogus/"].Date(); got.Year() != 2026 || got.Month() != time.April {
		t.Errorf("bogus filename date = %v, want the 2026-04-04 mtime", got)
	}
}

func TestLoaderTitleDerivation(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "no-frontmatter.md", "# Heading One Title\n\nBody.\n")
	writeFile(t, root, "no-title-no-heading.md", "Just body text, no heading at all.\n")
	writeFile(t, root, "explicit.md", "---\ntitle: Explicit Wins\n---\n# Ignored Heading\n")

	pages := byURL(load(t, root, LoadOptions{}))

	if got := pages["/no-frontmatter/"].Meta.Title; got != "Heading One Title" {
		t.Errorf("title from H1 = %q, want %q", got, "Heading One Title")
	}
	if got := pages["/no-title-no-heading/"].Meta.Title; got != "No Title No Heading" {
		t.Errorf("title from slug = %q, want %q", got, "No Title No Heading")
	}
	if got := pages["/explicit/"].Meta.Title; got != "Explicit Wins" {
		t.Errorf("explicit title = %q, want it to win over the H1", got)
	}
}

func TestLoaderSummarySources(t *testing.T) {
	root := t.TempDir()

	writeFile(t, root, "explicit.md", "---\ntitle: E\nsummary: A hand written summary.\n---\nBody body body.\n")
	writeFile(t, root, "marker.md", "---\ntitle: M\n---\nBefore the marker.\n\n<!--more-->\n\nAfter the marker.\n")
	long := "word " + strings.Repeat("filler ", 100)
	writeFile(t, root, "auto.md", "---\ntitle: A\n---\n"+long+"\n")

	pages := byURL(load(t, root, LoadOptions{SummaryWords: 20}))

	if got := string(pages["/explicit/"].SummaryHTML); !strings.Contains(got, "A hand written summary.") {
		t.Errorf("explicit summary = %q", got)
	}
	// An explicit summary is plain text, so markup in it must not be emitted raw.
	writeFile(t, root, "escaped.md", "---\ntitle: X\nsummary: \"<script>alert(1)</script>\"\n---\nBody.\n")
	pages = byURL(load(t, root, LoadOptions{SummaryWords: 20}))
	if got := string(pages["/escaped/"].SummaryHTML); strings.Contains(got, "<script>") {
		t.Errorf("explicit summary was not escaped: %q", got)
	}

	marker := pages["/marker/"]
	if got := string(marker.SummaryHTML); !strings.Contains(got, "Before the marker.") || strings.Contains(got, "After the marker.") {
		t.Errorf("marker summary = %q, want only the text before the marker", got)
	}
	// The marker itself must not survive into the body, and the text on both
	// sides of it must.
	if body := string(marker.BodyHTML); strings.Contains(body, "more-->") || !strings.Contains(body, "After the marker.") {
		t.Errorf("body after marker split = %q", body)
	}

	auto := string(pages["/auto/"].SummaryHTML)
	if words := strings.Fields(strings.Trim(auto, "<p>/")); len(words) > 25 {
		t.Errorf("auto summary has %d words, want it truncated near 20", len(words))
	}
	if !strings.HasSuffix(strings.TrimSpace(auto), "</p>") {
		t.Errorf("auto summary should be wrapped in a paragraph: %q", auto)
	}
}

func TestLoaderSkipsPartialsAndNonMarkdown(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "_drafts/wip.md", "---\ntitle: WIP\n---\nx\n")
	writeFile(t, root, "_partial.md", "not a page\n")
	writeFile(t, root, ".hidden/x.md", "not a page\n")
	writeFile(t, root, "notes.txt", "not markdown\n")
	writeFile(t, root, "image.png", "not markdown\n")
	writeFile(t, root, "real.md", "---\ntitle: Real\n---\nx\n")

	pages := load(t, root, LoadOptions{})
	if len(pages) != 1 {
		t.Fatalf("loaded %v, want only the real page", titles(pages))
	}
	if pages[0].Meta.Title != "Real" {
		t.Errorf("loaded %q, want Real", pages[0].Meta.Title)
	}
}

func TestLoaderReportsBrokenPageWithoutLosingTheRest(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "good.md", "---\ntitle: Good\n---\nx\n")
	writeFile(t, root, "bad.md", "---\ntitle: [unclosed\n---\ny\n")

	pages, err := NewLoader(root, testRenderer(), LoadOptions{Now: now}).Load()
	if err == nil {
		t.Fatal("expected an error for the page with invalid frontmatter")
	}
	if !strings.Contains(err.Error(), "bad.md") {
		t.Errorf("error should name the offending file, got: %v", err)
	}
	// The healthy page must still be returned, so a build degrades rather than
	// producing nothing.
	if len(pages) != 1 || pages[0].Meta.Title != "Good" {
		t.Errorf("loaded %v alongside the error, want the good page", titles(pages))
	}
}

func TestLoaderPermalinkAndWordCount(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "post.md", "---\ntitle: P\n---\nOne two three four five.\n")

	pages := load(t, root, LoadOptions{BaseURL: "https://example.com"})
	p := pages[0]
	if p.Permalink != "https://example.com/post/" {
		t.Errorf("Permalink = %q", p.Permalink)
	}
	if p.WordCount != 5 {
		t.Errorf("WordCount = %d, want 5", p.WordCount)
	}
	if p.ReadingMinutes != 1 {
		t.Errorf("ReadingMinutes = %d, want 1", p.ReadingMinutes)
	}

	// With no base_url the permalink stays relative rather than becoming a
	// broken absolute URL.
	pages = load(t, root, LoadOptions{})
	if pages[0].Permalink != "/post/" {
		t.Errorf("Permalink without base_url = %q, want /post/", pages[0].Permalink)
	}
}

func TestLoaderRedirectIsNoIndex(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "moved.md", "---\ntitle: Moved\nredirect: /new-home/\n---\nx\n")

	pages := load(t, root, LoadOptions{})
	if len(pages) != 1 {
		t.Fatalf("loaded %d pages, want 1", len(pages))
	}
	if !pages[0].NoIndex {
		t.Error("a redirect stub should be NoIndex so it stays out of feeds and listings")
	}
}

func TestLoaderAliasesArePreserved(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "post.md", "---\ntitle: P\naliases:\n  - /old-url/\n  - /older/\n---\nx\n")

	pages := load(t, root, LoadOptions{})
	if len(pages[0].Meta.Aliases) != 2 {
		t.Errorf("Aliases = %v, want both entries", pages[0].Meta.Aliases)
	}
}

func TestLoaderMissingContentDirIsEmptyNotFatal(t *testing.T) {
	pages, err := NewLoader(t.TempDir()+"/nope", testRenderer(), LoadOptions{Now: now}).Load()
	if err != nil {
		t.Fatalf("Load on a missing content dir: %v", err)
	}
	if len(pages) != 0 {
		t.Errorf("loaded %d pages from a missing directory", len(pages))
	}
}

func titles(pages []*Page) []string {
	out := make([]string, 0, len(pages))
	for _, p := range pages {
		out = append(out, p.Meta.Title)
	}
	return out
}

func keys(pages map[string]*Page) []string {
	out := make([]string, 0, len(pages))
	for k := range pages {
		out = append(out, k)
	}
	return out
}
