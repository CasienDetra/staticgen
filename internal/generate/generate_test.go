package generate

import (
	"encoding/json"
	"encoding/xml"
	"html/template"
	"strings"
	"testing"
	"time"

	"github.com/casien/staticgen/internal/config"
	"github.com/casien/staticgen/internal/content"
	"github.com/casien/staticgen/internal/site"
)

var builtAt = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

func buildSite(t *testing.T, baseURL string, cfgMutate func(*config.Site), pages ...*content.Page) *site.Site {
	t.Helper()
	cfg := config.Default()
	cfg.Root = "/site"
	cfg.Title = "Feed Test"
	cfg.Description = "A site used to test feeds"
	cfg.Language = "en-us"
	cfg.BaseURL = baseURL
	cfg.Author = config.Author{Name: "Author Name", Email: "a@example.com"}
	if cfgMutate != nil {
		cfgMutate(cfg)
	}
	s, err := site.NewAssembler(cfg).Assemble(pages, builtAt)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	return s
}

func post(url, title, html, plain string, date time.Time, tags []string) *content.Page {
	return &content.Page{
		URL:        url,
		OutputPath: strings.TrimPrefix(url, "/") + "index.html",
		SourcePath: strings.TrimPrefix(url, "/") + "post.md",
		Section:    "blog",
		Kind:       content.KindPage,
		Meta: content.Meta{
			Title:   title,
			Date:    date,
			LastMod: date,
			Tags:    tags,
			Extra:   map[string]any{},
		},
		BodyHTML:  template.HTML(html),
		PlainText: plain,
		WordCount: len(strings.Fields(plain)),
	}
}

func threePosts() []*content.Page {
	return []*content.Page{
		post("/blog/old/", "Old Post", "<p>old body</p>", "old body text", time.Date(2026, 1, 10, 9, 0, 0, 0, time.UTC), []string{"go"}),
		post("/blog/mid/", "Mid Post", "<p>mid body</p>", "mid body text", time.Date(2026, 2, 10, 9, 0, 0, 0, time.UTC), []string{"go", "testing"}),
		post("/blog/new/", "New Post", "<p>new body</p>", "new body text", time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC), []string{"rust"}),
	}
}

func TestRSS(t *testing.T) {
	s := buildSite(t, "https://example.com", nil, threePosts()...)

	raw, err := RSS(s)
	if err != nil {
		t.Fatalf("RSS: %v", err)
	}
	if !strings.HasPrefix(string(raw), "<?xml") {
		t.Errorf("feed should start with an XML declaration, got %q", raw[:40])
	}

	var doc rssDoc
	if err := xml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("feed is not valid RSS: %v\n%s", err, raw)
	}

	if doc.Version != "2.0" {
		t.Errorf("version = %q, want 2.0", doc.Version)
	}

	// Namespaced elements and the channel <link> are asserted against the raw
	// document. Go's decoder resolves "atom:link" to its namespace URI while the
	// struct tag carries the literal prefix, and an empty <atom:link> then
	// matches and blanks the <link> field — a round-trip artefact, not a defect
	// in the feed. What readers receive is the serialised form.
	xmlText := string(raw)
	for _, want := range []string{
		`<rss version="2.0"`,
		`xmlns:atom="http://www.w3.org/2005/Atom"`,
		`xmlns:content="http://purl.org/rss/1.0/modules/content/"`,
		`<link>https://example.com/</link>`,
		`<atom:link href="https://example.com/feed.xml" rel="self" type="application/rss+xml"`,
		`<content:encoded>`,
	} {
		if !strings.Contains(xmlText, want) {
			t.Errorf("feed is missing %q", want)
		}
	}

	ch := doc.Channel
	if ch.Title != "Feed Test" {
		t.Errorf("channel title = %q", ch.Title)
	}
	if ch.Description == "" {
		t.Error("channel description is empty")
	}
	if ch.Language != "en-us" {
		t.Errorf("language = %q", ch.Language)
	}
	if ch.Generator != "staticgen" {
		t.Errorf("generator = %q", ch.Generator)
	}
	if _, err := time.Parse(time.RFC1123Z, ch.LastBuild); err != nil {
		t.Errorf("lastBuildDate %q is not RFC822: %v", ch.LastBuild, err)
	}
	// lastBuildDate should reflect the newest post, not the build clock.
	if want := "Tue, 10 Mar 2026"; !strings.HasPrefix(ch.LastBuild, want) {
		t.Errorf("lastBuildDate = %q, want it to start %q", ch.LastBuild, want)
	}

	if len(ch.Items) != 3 {
		t.Fatalf("items = %d, want 3", len(ch.Items))
	}
	// Newest first, matching how readers display a feed.
	if ch.Items[0].Title != "New Post" {
		t.Errorf("first item = %q, want the newest post", ch.Items[0].Title)
	}
	item := ch.Items[0]
	if item.Link != "https://example.com/blog/new/" {
		t.Errorf("item link = %q, want an absolute permalink", item.Link)
	}
	if item.GUID.Value != item.Link || item.GUID.IsPermaLink != "true" {
		t.Errorf("guid = %+v, want a permalink guid matching the link", item.GUID)
	}
	// The body is carried in content:encoded, checked against the raw document
	// for the same namespacing reason as above.
	if !strings.Contains(xmlText, "&lt;p&gt;new body&lt;/p&gt;") {
		t.Error("content:encoded does not carry the escaped post body")
	}
	if item.Description == "" {
		t.Error("item description is empty")
	}
	if _, err := time.Parse(time.RFC1123Z, item.PubDate); err != nil {
		t.Errorf("pubDate %q is not RFC822: %v", item.PubDate, err)
	}
	// RSS 2.0 author is "email (name)".
	if item.Author != "a@example.com (Author Name)" {
		t.Errorf("author = %q", item.Author)
	}
	if len(item.Categories) != 1 || item.Categories[0] != "rust" {
		t.Errorf("categories = %v, want [rust]", item.Categories)
	}
}

func TestRSSRespectsLimit(t *testing.T) {
	s := buildSite(t, "https://example.com", func(c *config.Site) { c.Feeds.RSSLimit = 2 }, threePosts()...)

	raw, err := RSS(s)
	if err != nil {
		t.Fatalf("RSS: %v", err)
	}
	var doc rssDoc
	if err := xml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(doc.Channel.Items) != 2 {
		t.Errorf("items = %d, want the configured limit of 2", len(doc.Channel.Items))
	}
	// The limit keeps the newest entries, not an arbitrary slice.
	if doc.Channel.Items[0].Title != "New Post" {
		t.Errorf("first item = %q, want the newest", doc.Channel.Items[0].Title)
	}
}

func TestRSSExcludesNoIndexPages(t *testing.T) {
	pages := threePosts()
	notFound := post("/404.html", "Page not found", "<p>nope</p>", "nope", builtAt, nil)
	notFound.NoIndex = true
	notFound.OutputPath = "404.html"
	pages = append(pages, notFound)

	s := buildSite(t, "https://example.com", nil, pages...)
	raw, err := RSS(s)
	if err != nil {
		t.Fatalf("RSS: %v", err)
	}
	var doc rssDoc
	if err := xml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(doc.Channel.Items) != 3 {
		t.Errorf("items = %d, want 3 (the 404 page is not content)", len(doc.Channel.Items))
	}
	for _, it := range doc.Channel.Items {
		if strings.Contains(it.Link, "404") {
			t.Errorf("the 404 page leaked into the feed: %s", it.Link)
		}
	}
}

func TestRSSRequiresBaseURL(t *testing.T) {
	// A feed of relative URLs is rejected by every reader, so this is an error
	// rather than a silently broken feed.
	s := buildSite(t, "", func(c *config.Site) { c.Feeds.RSS = false }, threePosts()...)
	if _, err := RSS(s); err == nil {
		t.Error("RSS without base_url should fail")
	} else if !strings.Contains(err.Error(), "base_url") {
		t.Errorf("error should explain the cause: %v", err)
	}
}

func TestSitemap(t *testing.T) {
	s := buildSite(t, "https://example.com", nil, threePosts()...)

	raw, err := Sitemap(s)
	if err != nil {
		t.Fatalf("Sitemap: %v", err)
	}

	var doc urlset
	if err := xml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("sitemap is not valid XML: %v\n%s", err, raw)
	}
	if doc.XMLNS != "http://www.sitemaps.org/schemas/sitemap/0.9" {
		t.Errorf("xmlns = %q", doc.XMLNS)
	}

	// 3 posts + home + blog section + tags index + 3 tag terms + categories
	// index + 1 category term + archive.
	if len(doc.URLs) != s.Stats.Pages {
		t.Errorf("urls = %d, want one per page (%d)", len(doc.URLs), s.Stats.Pages)
	}

	byLoc := map[string]sitemapURL{}
	for _, u := range doc.URLs {
		if !strings.HasPrefix(u.Loc, "https://example.com/") {
			t.Errorf("loc %q is not absolute", u.Loc)
		}
		if u.LastMod != "" {
			if _, err := time.Parse("2006-01-02", u.LastMod); err != nil {
				t.Errorf("lastmod %q is not W3C date: %v", u.LastMod, err)
			}
		}
		byLoc[u.Loc] = u
	}

	if u, ok := byLoc["https://example.com/"]; !ok {
		t.Error("the home page is missing from the sitemap")
	} else if u.Changefreq != "daily" {
		t.Errorf("home changefreq = %q, want daily", u.Changefreq)
	}
	if u, ok := byLoc["https://example.com/blog/new/"]; !ok {
		t.Error("a post is missing from the sitemap")
	} else if u.Changefreq != "monthly" {
		t.Errorf("post changefreq = %q, want monthly", u.Changefreq)
	}
	if u, ok := byLoc["https://example.com/tags/"]; !ok {
		t.Error("the tags index is missing from the sitemap")
	} else if u.Changefreq != "weekly" {
		t.Errorf("tags changefreq = %q, want weekly", u.Changefreq)
	}
}

func TestSitemapExcludesNoIndex(t *testing.T) {
	pages := threePosts()
	notFound := post("/404.html", "Page not found", "<p>x</p>", "x", builtAt, nil)
	notFound.NoIndex = true
	notFound.OutputPath = "404.html"
	pages = append(pages, notFound)

	s := buildSite(t, "https://example.com", nil, pages...)
	raw, err := Sitemap(s)
	if err != nil {
		t.Fatalf("Sitemap: %v", err)
	}
	if strings.Contains(string(raw), "404") {
		t.Error("the 404 page should not be submitted to crawlers")
	}
}

func TestSitemapRequiresBaseURL(t *testing.T) {
	s := buildSite(t, "", func(c *config.Site) { c.Feeds.RSS = false }, threePosts()...)
	if _, err := Sitemap(s); err == nil {
		t.Error("Sitemap without base_url should fail")
	}
}

func TestSearch(t *testing.T) {
	s := buildSite(t, "https://example.com", nil, threePosts()...)

	raw, err := Search(s, 400)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	var idx SearchIndex
	if err := json.Unmarshal(raw, &idx); err != nil {
		t.Fatalf("search index is not valid JSON: %v", err)
	}
	if idx.Count != len(idx.Documents) {
		t.Errorf("count = %d but %d documents", idx.Count, len(idx.Documents))
	}
	if idx.Count != s.Stats.Pages {
		t.Errorf("documents = %d, want one per page (%d)", idx.Count, s.Stats.Pages)
	}
	if _, err := time.Parse(time.RFC3339, idx.Generated); err != nil {
		t.Errorf("generated %q is not RFC3339: %v", idx.Generated, err)
	}

	var found *SearchDoc
	for i := range idx.Documents {
		// Search runs client-side over relative URLs, so it must not depend on
		// base_url being set.
		if strings.HasPrefix(idx.Documents[i].URL, "http") {
			t.Errorf("document URL %q should be site-relative", idx.Documents[i].URL)
		}
		if idx.Documents[i].URL == "/blog/mid/" {
			found = &idx.Documents[i]
		}
	}
	if found == nil {
		t.Fatal("no document for /blog/mid/")
	}
	if found.Title != "Mid Post" {
		t.Errorf("title = %q", found.Title)
	}
	if found.Body != "mid body text" {
		t.Errorf("body = %q", found.Body)
	}
	if strings.Join(found.Tags, ",") != "go,testing" {
		t.Errorf("tags = %v", found.Tags)
	}
	if found.Date != "2026-02-10" {
		t.Errorf("date = %q, want 2026-02-10", found.Date)
	}
	if found.Section != "blog" {
		t.Errorf("section = %q, want blog", found.Section)
	}
}

func TestSearchClipsLongBodies(t *testing.T) {
	long := strings.Repeat("word ", 400) // far beyond any sane limit
	p := post("/blog/long/", "Long", "<p>x</p>", strings.TrimSpace(long), builtAt, nil)
	s := buildSite(t, "", func(c *config.Site) { c.Feeds.RSS = false }, p)

	raw, err := Search(s, 50)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	var idx SearchIndex
	if err := json.Unmarshal(raw, &idx); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	var body string
	for _, d := range idx.Documents {
		if d.URL == "/blog/long/" {
			body = d.Body
		}
	}
	if body == "" {
		t.Fatal("no document for the long post")
	}
	// The index is fetched in full on first keystroke, so bodies are clipped.
	if len([]rune(body)) > 60 {
		t.Errorf("body was not clipped to ~50 runes, got %d", len([]rune(body)))
	}
	if !strings.HasSuffix(body, "…") {
		t.Errorf("a clipped body should end with an ellipsis: %q", body)
	}
	if strings.HasSuffix(strings.TrimSuffix(body, "…"), " ") {
		t.Errorf("clip should not end mid-gap: %q", body)
	}
}

func TestSearchExcludesNoIndex(t *testing.T) {
	pages := threePosts()
	notFound := post("/404.html", "Page not found", "<p>x</p>", "x", builtAt, nil)
	notFound.NoIndex = true
	notFound.OutputPath = "404.html"
	pages = append(pages, notFound)

	s := buildSite(t, "", func(c *config.Site) { c.Feeds.RSS = false }, pages...)
	raw, err := Search(s, 400)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if strings.Contains(string(raw), "404") {
		t.Error("the 404 page should not be searchable")
	}
}

func TestClip(t *testing.T) {
	tests := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"shorter than limit is untouched", "a b c", 10, "a b c"},
		{"exactly the limit", "a b c", 5, "a b c"},
		{"normalises whitespace", "a\n\n  b   c", 50, "a b c"},
		{"empty", "   ", 10, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clip(tt.in, tt.n); got != tt.want {
				t.Errorf("clip(%q, %d) = %q, want %q", tt.in, tt.n, got, tt.want)
			}
		})
	}

	// A long input is cut on a word boundary rather than mid-word.
	long := strings.Join([]string{"alpha", "beta", "gamma", "delta", "epsilon"}, " ")
	got := clip(long, 17)
	if strings.Contains(got, "delt") && !strings.Contains(got, "delta") {
		t.Errorf("clip cut mid-word: %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("clip(%q) = %q, want a trailing ellipsis", long, got)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "  ", "x", "y"); got != "x" {
		t.Errorf("firstNonEmpty = %q, want x", got)
	}
	if got := firstNonEmpty("", " "); got != "" {
		t.Errorf("firstNonEmpty of blanks = %q, want empty", got)
	}
}

func TestAuthorOf(t *testing.T) {
	tests := []struct {
		name   string
		author config.Author
		want   string
	}{
		{"name only", config.Author{Name: "Someone"}, "Someone"},
		{"email only", config.Author{Email: "s@example.com"}, "s@example.com"},
		{"both", config.Author{Name: "Someone", Email: "s@example.com"}, "s@example.com (Someone)"},
		{"neither", config.Author{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Author = tt.author
			if got := authorOf(cfg); got != tt.want {
				t.Errorf("authorOf = %q, want %q", got, tt.want)
			}
		})
	}
}
