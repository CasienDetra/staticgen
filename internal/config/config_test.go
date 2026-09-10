package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

func TestLoadDefaultsWhenNoConfigFile(t *testing.T) {
	root := t.TempDir()
	cfg, err := Load(root, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Path != "" {
		t.Errorf("Path = %q, want empty when no config file exists", cfg.Path)
	}
	if cfg.Root != root {
		t.Errorf("Root = %q, want %q", cfg.Root, root)
	}
	// Defaults must be usable without any configuration at all.
	if cfg.Title == "" || cfg.Dirs.Content != "content" || cfg.Dirs.Output != "public" {
		t.Errorf("defaults = %+v", cfg)
	}
	if !cfg.Build.PrettyURLs || !cfg.Markup.Highlight.Enabled {
		t.Errorf("unexpected defaults: %+v", cfg.Build)
	}
	// Feeds are off because a bare directory has no base_url configured.
	if cfg.Feeds.RSS || cfg.Feeds.Sitemap {
		t.Errorf("feeds should be disabled without base_url: %+v", cfg.Feeds)
	}
	if cfg.Markup.Unsafe {
		t.Error("raw HTML must be disallowed by default")
	}
	if cfg.Serve.Port != 1313 {
		t.Errorf("Serve.Port = %d, want 1313", cfg.Serve.Port)
	}
}

func TestLoadFromFile(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, "site.yaml", `
title: My Blog
description: A description
base_url: https://example.com/
language: fr
author:
  name: Someone
  email: s@example.com
dirs:
  content: posts
  output: dist
build:
  drafts: true
  pretty_urls: false
  summary_length: 40
markup:
  unsafe: true
  highlight:
    style: monokai
    line_numbers: true
feeds:
  rss: false
serve:
  port: 9000
menu:
  - name: Blog
    url: /posts/
    weight: 2
  - name: Home
    url: /
    weight: 1
params:
  custom: value
`)

	cfg, err := Load(root, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Title != "My Blog" || cfg.Language != "fr" {
		t.Errorf("title/language = %q/%q", cfg.Title, cfg.Language)
	}
	// A trailing slash on base_url would produce double slashes in every
	// absolute URL, so it is normalised away.
	if cfg.BaseURL != "https://example.com" {
		t.Errorf("BaseURL = %q, want the trailing slash trimmed", cfg.BaseURL)
	}
	if cfg.Author.Name != "Someone" || cfg.Author.Email != "s@example.com" {
		t.Errorf("author = %+v", cfg.Author)
	}
	if cfg.Dirs.Content != "posts" || cfg.Dirs.Output != "dist" {
		t.Errorf("dirs = %+v", cfg.Dirs)
	}
	if !cfg.Build.Drafts || cfg.Build.PrettyURLs {
		t.Errorf("build = %+v", cfg.Build)
	}
	if cfg.Build.SummaryLength != 40 {
		t.Errorf("SummaryLength = %d, want 40", cfg.Build.SummaryLength)
	}
	if !cfg.Markup.Unsafe || cfg.Markup.Highlight.Style != "monokai" || !cfg.Markup.Highlight.LineNumbers {
		t.Errorf("markup = %+v", cfg.Markup)
	}
	if cfg.Feeds.RSS {
		t.Error("feeds.rss should be false")
	}
	if cfg.Serve.Port != 9000 {
		t.Errorf("Serve.Port = %d", cfg.Serve.Port)
	}
	if cfg.Params["custom"] != "value" {
		t.Errorf("Params = %v", cfg.Params)
	}

	// Menu is sorted by weight regardless of file order.
	if len(cfg.Menu) != 2 || cfg.Menu[0].Name != "Home" {
		t.Errorf("menu = %+v, want Home first by weight", cfg.Menu)
	}
}

func TestLoadKeepsDefaultsForOmittedKeys(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, "site.yaml", "title: Only A Title\n")

	cfg, err := Load(root, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Decoding onto the defaults means omitted keys keep their default value
	// rather than being zeroed out.
	if cfg.Title != "Only A Title" {
		t.Errorf("Title = %q", cfg.Title)
	}
	if !cfg.Build.PrettyURLs || cfg.Dirs.Output != "public" || cfg.Serve.Port != 1313 {
		t.Errorf("omitted keys lost their defaults: %+v", cfg)
	}
}

func TestLoadProbesStandardNames(t *testing.T) {
	for _, name := range []string{"site.yaml", "site.yml", "config.yaml", "staticgen.yaml"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeConfig(t, root, name, "title: From "+name+"\n")
			cfg, err := Load(root, "")
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.Title != "From "+name {
				t.Errorf("Title = %q, want it loaded from %s", cfg.Title, name)
			}
			if filepath.Base(cfg.Path) != name {
				t.Errorf("Path = %q, want it to end in %s", cfg.Path, name)
			}
		})
	}
}

func TestLoadExplicitMissingConfigIsAnError(t *testing.T) {
	root := t.TempDir()
	// Probing may miss; an explicit path must not.
	if _, err := Load(root, "nope.yaml"); err == nil {
		t.Error("an explicitly named missing config should be an error")
	}
}

func TestLoadInvalidYAMLIsAnError(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, "site.yaml", "title: [unclosed\n")
	if _, err := Load(root, ""); err == nil {
		t.Error("invalid YAML should be an error")
	} else if !strings.Contains(err.Error(), "site.yaml") {
		t.Errorf("error should name the file: %v", err)
	}
}

func TestLoadCommentsOnlyFileIsNotAnError(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, "site.yaml", "# nothing but a comment\n")
	cfg, err := Load(root, "")
	if err != nil {
		t.Fatalf("a comments-only config should fall back to defaults, got: %v", err)
	}
	if cfg.Title == "" {
		t.Error("defaults should still apply")
	}
}

func TestOverridesTakePrecedenceOverFile(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, "site.yaml", "title: From File\nbase_url: https://file.example\nbuild:\n  drafts: false\n")

	cfg, err := Load(root, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	yes := true
	cfg.Apply(Overrides{
		Title:   "From Flag",
		BaseURL: "https://flag.example/",
		Drafts:  &yes,
		Port:    4321,
	})

	if cfg.Title != "From Flag" {
		t.Errorf("Title = %q, want the flag value", cfg.Title)
	}
	if cfg.BaseURL != "https://flag.example" {
		t.Errorf("BaseURL = %q, want the flag value with the slash trimmed", cfg.BaseURL)
	}
	if !cfg.Build.Drafts {
		t.Error("a flag-set boolean must override the file")
	}
	if cfg.Serve.Port != 4321 {
		t.Errorf("Port = %d, want 4321", cfg.Serve.Port)
	}
}

func TestUnsetOverridesDoNotClobberFileValues(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, "site.yaml", "title: Keep Me\nbuild:\n  drafts: true\n  minify: true\nserve:\n  port: 8080\n")

	cfg, err := Load(root, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// An Overrides with everything unset must change nothing. This is why the
	// boolean fields are pointers: a false zero value has to mean "not set".
	cfg.Apply(Overrides{})

	if cfg.Title != "Keep Me" {
		t.Errorf("Title = %q, want it preserved", cfg.Title)
	}
	if !cfg.Build.Drafts || !cfg.Build.Minify {
		t.Errorf("build flags were clobbered: %+v", cfg.Build)
	}
	if cfg.Serve.Port != 8080 {
		t.Errorf("Port = %d, want it preserved", cfg.Serve.Port)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Site)
		wantErr bool
	}{
		{name: "defaults are valid", mutate: func(*Site) {}},
		{name: "valid base url", mutate: func(c *Site) { c.BaseURL = "https://example.com" }},
		{name: "empty base url with rss off", mutate: func(c *Site) { c.Feeds.RSS = false }},
		{
			// Feeds need absolute URLs, but a missing base_url is the normal
			// state for a site being written locally, so it is not a hard error:
			// normalize turns the feed off instead. See
			// TestNormalizeDisablesFeedsWithoutBaseURL.
			name:   "rss without base url is tolerated",
			mutate: func(c *Site) { c.Feeds.RSS = true; c.BaseURL = "" },
		},
		{name: "base url without host", mutate: func(c *Site) { c.BaseURL = "/relative" }, wantErr: true},
		{name: "base url garbage", mutate: func(c *Site) { c.BaseURL = "://nope" }, wantErr: true},
		{name: "empty dir", mutate: func(c *Site) { c.Dirs.Content = "" }, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Default() is already valid without normalising, so the mutated
			// value under test is the one Validate sees.
			cfg := Default()
			cfg.Root = "/site"
			tt.mutate(cfg)
			err := cfg.Validate()
			if tt.wantErr && err == nil {
				t.Errorf("Validate() = nil, want an error")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
		})
	}
}

func TestAbsURLAndRelURL(t *testing.T) {
	cfg := Default()
	cfg.Root = "/site"
	cfg.BaseURL = "https://example.com"

	tests := []struct{ in, want string }{
		{"/", "https://example.com/"},
		{"/blog/", "https://example.com/blog/"},
		{"blog/", "https://example.com/blog/"},
		{"", "https://example.com/"},
		// Already-absolute URLs must pass through untouched.
		{"https://example.org/new", "https://example.org/new"},
		{"http://other.io/path", "http://other.io/path"},
		{"mailto:hi@example.com", "mailto:hi@example.com"},
		{"//cdn.example.com/js", "//cdn.example.com/js"},
	}
	for _, tt := range tests {
		if got := cfg.AbsURL(tt.in); got != tt.want {
			t.Errorf("AbsURL(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}

	// Without a base_url, AbsURL degrades to a rooted relative URL rather than
	// emitting something broken.
	cfg.BaseURL = ""
	if got := cfg.AbsURL("/blog/"); got != "/blog/" {
		t.Errorf("AbsURL without base_url = %q, want /blog/", got)
	}

	if got := RelURL("blog/x"); got != "/blog/x" {
		t.Errorf("RelURL = %q", got)
	}
	if got := RelURL(""); got != "/" {
		t.Errorf("RelURL(\"\") = %q, want /", got)
	}

	// RelURL must not mangle already-absolute URLs.
	for _, abs := range []string{
		"https://example.org/new",
		"http://other.io/path",
		"mailto:hi@example.com",
		"//cdn.example.com/js",
	} {
		if got := RelURL(abs); got != abs {
			t.Errorf("RelURL(%q) = %q, want verbatim", abs, got)
		}
	}
}

func TestDirPathsResolveAgainstRoot(t *testing.T) {
	root := t.TempDir()
	cfg, err := Load(root, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.ContentPath(); got != filepath.Join(root, "content") {
		t.Errorf("ContentPath = %q", got)
	}
	if got := cfg.OutputPath(); got != filepath.Join(root, "public") {
		t.Errorf("OutputPath = %q", got)
	}
}

func TestAbsoluteOutputDirIsUsedVerbatim(t *testing.T) {
	// Building to an artefact directory outside the site is a normal CI need.
	root := t.TempDir()
	writeConfig(t, root, "site.yaml", "title: X\n")
	cfg, err := Load(root, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg.Apply(Overrides{OutputDir: "/tmp/somewhere-else"})

	if got := cfg.OutputPath(); got != "/tmp/somewhere-else" {
		t.Errorf("OutputPath = %q, want the absolute path used verbatim", got)
	}
	// Content stays anchored to the site root.
	if got := cfg.ContentPath(); got != filepath.Join(root, "content") {
		t.Errorf("ContentPath = %q, want it still relative to the root", got)
	}
}

func TestNormalizeFillsInvalidValues(t *testing.T) {
	cfg := Default()
	cfg.Root = "/site"
	cfg.Title = "   "
	cfg.Language = ""
	cfg.Build.SummaryLength = -5
	cfg.Feeds.RSSLimit = 0
	cfg.Serve.Port = -1
	cfg.Serve.DebounceMs = -10
	cfg.Params = nil
	cfg.Menu = []MenuItem{{Name: "No URL"}}

	cfg.normalize()

	if cfg.Title == "" {
		t.Error("an empty title should fall back to a default")
	}
	if cfg.Language != "en-us" {
		t.Errorf("Language = %q", cfg.Language)
	}
	if cfg.Build.SummaryLength <= 0 {
		t.Errorf("SummaryLength = %d, want a positive default", cfg.Build.SummaryLength)
	}
	if cfg.Feeds.RSSLimit <= 0 {
		t.Errorf("RSSLimit = %d, want a positive default", cfg.Feeds.RSSLimit)
	}
	if cfg.Serve.Port <= 0 || cfg.Serve.DebounceMs < 0 {
		t.Errorf("serve = %+v", cfg.Serve)
	}
	if cfg.Params == nil {
		t.Error("Params should be non-nil so templates can range over it safely")
	}
	if cfg.Menu[0].URL != "/" {
		t.Errorf("a menu entry with no URL = %q, want /", cfg.Menu[0].URL)
	}
}

func TestNormalizeDisablesFeedsWithoutBaseURL(t *testing.T) {
	// A bare directory of Markdown files has no deployment target configured, so
	// `staticgen build` must work out of the box rather than failing validation.
	cfg := Default()
	cfg.Root = "/site"
	cfg.normalize()

	if cfg.Feeds.RSS || cfg.Feeds.Sitemap {
		t.Errorf("feeds should be off without a base_url: rss=%v sitemap=%v", cfg.Feeds.RSS, cfg.Feeds.Sitemap)
	}
	// Search is fetched same-origin, so site-relative URLs are correct for it
	// and it keeps working.
	if !cfg.Feeds.Search {
		t.Error("search should stay enabled without a base_url")
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate on defaults: %v", err)
	}

	// With a base_url configured, the defaults do produce both feeds.
	cfg.BaseURL = "https://example.com"
	cfg.Feeds.RSS = true
	cfg.Feeds.Sitemap = true
	cfg.normalize()
	if !cfg.Feeds.RSS || !cfg.Feeds.Sitemap {
		t.Error("feeds should stay on once base_url is set")
	}
}

func TestMenuSortIsStableForEqualWeights(t *testing.T) {
	items := []MenuItem{
		{Name: "Bravo", Weight: 1},
		{Name: "Alpha", Weight: 1},
		{Name: "First", Weight: 0},
	}
	sortMenu(items)

	got := []string{items[0].Name, items[1].Name, items[2].Name}
	want := []string{"First", "Alpha", "Bravo"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("menu order = %v, want %v", got, want)
			break
		}
	}
}
