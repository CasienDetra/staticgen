package site

import (
	"strings"
	"testing"
	"time"

	"github.com/casien/staticgen/internal/config"
	"github.com/casien/staticgen/internal/content"
)

var buildTime = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

func testConfig() *config.Site {
	cfg := config.Default()
	cfg.Root = "/site"
	cfg.BaseURL = "https://example.com"
	cfg.Title = "Test Site"
	return cfg
}

// article builds a loaded regular page the way the content loader would.
func article(url, title, section string, date time.Time, tags, categories []string) *content.Page {
	return &content.Page{
		URL:        url,
		OutputPath: strings.TrimPrefix(url, "/") + "index.html",
		SourcePath: strings.TrimPrefix(url, "/") + "post.md",
		Section:    section,
		Kind:       content.KindPage,
		Meta: content.Meta{
			Title:      title,
			Date:       date,
			LastMod:    date,
			Tags:       tags,
			Categories: categories,
			Extra:      map[string]any{},
		},
		WordCount: 100,
	}
}

func day(n int) time.Time {
	return time.Date(2026, 1, n, 9, 0, 0, 0, time.UTC)
}

func mustAssemble(t *testing.T, cfg *config.Site, pages []*content.Page) *Site {
	t.Helper()
	s, err := NewAssembler(cfg).Assemble(pages, buildTime)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	return s
}

func TestAssembleSections(t *testing.T) {
	pages := []*content.Page{
		article("/blog/a/", "A", "blog", day(1), nil, nil),
		article("/blog/b/", "B", "blog", day(2), nil, nil),
		article("/docs/c/", "C", "docs", day(3), nil, nil),
		article("/root/", "Root", "", day(4), nil, nil),
	}

	s := mustAssemble(t, testConfig(), pages)

	if len(s.Sections) != 2 {
		t.Fatalf("sections = %d, want 2 (blog, docs); got %v", len(s.Sections), sectionNames(s))
	}
	blog := s.SectionByName("blog")
	if blog == nil {
		t.Fatal("no blog section")
	}
	if len(blog.Pages) != 2 {
		t.Errorf("blog has %d pages, want 2", len(blog.Pages))
	}
	if blog.URL != "/blog/" {
		t.Errorf("blog URL = %q, want /blog/", blog.URL)
	}
	// The root page belongs to no section.
	if s.SectionByName("") != nil {
		t.Error("root-level pages should not form a section")
	}

	// A section with no _index.md still needs a page at its URL, or navigation
	// and the section links in post listings would 404.
	if blog.Index == nil {
		t.Fatal("blog section has no index page; navigation would link to nothing")
	}
	if blog.Index.Kind != content.KindSection {
		t.Errorf("synthesised index Kind = %q, want %q", blog.Index.Kind, content.KindSection)
	}
	found := false
	for _, p := range s.Pages {
		if p.URL == "/blog/" {
			found = true
		}
	}
	if !found {
		t.Error("no page will be written at /blog/")
	}
}

func TestAssembleKeepsAuthoredSectionIndex(t *testing.T) {
	authored := &content.Page{
		URL: "/blog/", OutputPath: "blog/index.html", SourcePath: "blog/_index.md",
		Section: "blog", Kind: content.KindSection, IsIndex: true,
		Meta: content.Meta{Title: "My Blog", Extra: map[string]any{}},
	}
	pages := []*content.Page{authored, article("/blog/a/", "A", "blog", day(1), nil, nil)}

	s := mustAssemble(t, testConfig(), pages)

	blog := s.SectionByName("blog")
	if blog.Index != authored {
		t.Error("an authored _index.md must be used instead of a synthesised page")
	}
	if blog.Title != "My Blog" {
		t.Errorf("section title = %q, want the authored title", blog.Title)
	}
	if len(blog.Index.Pages) != 1 {
		t.Errorf("authored index lists %d pages, want 1", len(blog.Index.Pages))
	}
	// Only one page may exist at /blog/.
	count := 0
	for _, p := range s.Pages {
		if p.URL == "/blog/" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("%d pages at /blog/, want exactly 1", count)
	}
}

func TestAssembleTaxonomies(t *testing.T) {
	pages := []*content.Page{
		article("/blog/a/", "A", "blog", day(1), []string{"go", "testing"}, []string{"eng"}),
		article("/blog/b/", "B", "blog", day(2), []string{"go"}, nil),
		article("/blog/c/", "C", "blog", day(3), []string{"Rust"}, nil),
	}

	s := mustAssemble(t, testConfig(), pages)

	if len(s.Taxonomy.Tags) != 3 {
		t.Fatalf("tags = %v, want go, testing, Rust", termNames(s.Taxonomy.Tags))
	}
	// Busiest term first.
	if s.Taxonomy.Tags[0].Name != "go" {
		t.Errorf("first tag = %q, want the most-used one (go)", s.Taxonomy.Tags[0].Name)
	}
	if got := s.Taxonomy.Tags[0].Count(); got != 2 {
		t.Errorf("go count = %d, want 2", got)
	}
	if s.Taxonomy.Tags[0].URL != "/tags/go/" {
		t.Errorf("tag URL = %q, want /tags/go/", s.Taxonomy.Tags[0].URL)
	}
	// The slug is normalised but the display name is preserved as written.
	rust := findTerm(s.Taxonomy.Tags, "Rust")
	if rust == nil {
		t.Fatal("no Rust term")
	}
	if rust.Slug != "rust" {
		t.Errorf("Rust slug = %q, want rust", rust.Slug)
	}
	if rust.URL != "/tags/rust/" {
		t.Errorf("Rust URL = %q, want /tags/rust/", rust.URL)
	}
	if len(s.Taxonomy.Categories) != 1 {
		t.Errorf("categories = %v, want one (eng)", termNames(s.Taxonomy.Categories))
	}

	// Every term and both taxonomy indexes must have a page to write.
	for _, want := range []string{"/tags/", "/tags/go/", "/tags/testing/", "/tags/rust/", "/categories/", "/categories/eng/"} {
		if s.Find(want) == nil {
			t.Errorf("no page generated at %s", want)
		}
	}
}

func TestAssembleTermPagesListNewestFirst(t *testing.T) {
	pages := []*content.Page{
		article("/blog/old/", "Old", "blog", day(1), []string{"go"}, nil),
		article("/blog/new/", "New", "blog", day(20), []string{"go"}, nil),
	}
	s := mustAssemble(t, testConfig(), pages)

	term := s.Find("/tags/go/")
	if term == nil {
		t.Fatal("no /tags/go/ page")
	}
	if len(term.Pages) != 2 {
		t.Fatalf("term lists %d pages, want 2", len(term.Pages))
	}
	if term.Pages[0].Meta.Title != "New" {
		t.Errorf("first listed = %q, want the newest", term.Pages[0].Meta.Title)
	}
}

func TestAssemblePrevNext(t *testing.T) {
	pages := []*content.Page{
		article("/blog/first/", "First", "blog", day(1), nil, nil),
		article("/blog/second/", "Second", "blog", day(2), nil, nil),
		article("/blog/third/", "Third", "blog", day(3), nil, nil),
		article("/docs/other/", "Other", "docs", day(2), nil, nil),
	}

	s := mustAssemble(t, testConfig(), pages)

	// RegularPages is newest first; equal dates break deterministically on URL,
	// so Second (/blog/) precedes Other (/docs/) even across sections.
	if got := titles(s.RegularPages); strings.Join(got, ",") != "Third,Second,Other,First" {
		t.Errorf("RegularPages order = %v, want newest first", got)
	}

	first := s.Find("/blog/first/")
	if first.Prev != nil {
		t.Errorf("oldest post Prev = %q, want nil", first.Prev.Meta.Title)
	}
	if first.Next == nil || first.Next.Meta.Title != "Second" {
		t.Errorf("oldest post Next = %v, want Second", first.Next)
	}

	second := s.Find("/blog/second/")
	if second.Prev == nil || second.Prev.Meta.Title != "First" {
		t.Errorf("middle post Prev = %v, want First", second.Prev)
	}
	if second.Next == nil || second.Next.Meta.Title != "Third" {
		t.Errorf("middle post Next = %v, want Third", second.Next)
	}

	third := s.Find("/blog/third/")
	if third.Next != nil {
		t.Errorf("newest post Next = %q, want nil", third.Next.Meta.Title)
	}

	// Neighbours are scoped to a section: the docs post is alone.
	other := s.Find("/docs/other/")
	if other.Prev != nil || other.Next != nil {
		t.Error("a lone post in its section should have no neighbours")
	}
}

func TestAssembleSynthesizesHomeWhenMissing(t *testing.T) {
	pages := []*content.Page{article("/blog/a/", "A", "blog", day(1), nil, nil)}
	s := mustAssemble(t, testConfig(), pages)

	if s.Home == nil {
		t.Fatal("Home is nil")
	}
	if s.Home.Kind != content.KindHome || s.Home.URL != "/" {
		t.Errorf("Home = %+v, want a home page at /", s.Home)
	}
	if s.Find("/") == nil {
		t.Error("no page will be written at /")
	}
	// A synthesised home lists recent posts so the layout is not empty.
	if len(s.Home.Pages) != 1 {
		t.Errorf("synthesised home lists %d pages, want 1", len(s.Home.Pages))
	}
}

func TestAssembleUsesAuthoredHome(t *testing.T) {
	home := &content.Page{
		URL: "/", OutputPath: "index.html", SourcePath: "_index.md",
		Kind: content.KindHome, IsIndex: true,
		Meta: content.Meta{Title: "Welcome", Extra: map[string]any{}},
	}
	pages := []*content.Page{home, article("/blog/a/", "A", "blog", day(1), nil, nil)}

	s := mustAssemble(t, testConfig(), pages)
	if s.Home != home {
		t.Error("an authored index.md must be used as the home page")
	}
	if s.Home.Meta.Title != "Welcome" {
		t.Errorf("home title = %q, want the authored title", s.Home.Meta.Title)
	}
	if len(s.Home.Pages) != 1 {
		t.Errorf("authored home lists %d pages, want 1", len(s.Home.Pages))
	}
}

func TestAssembleArchive(t *testing.T) {
	pages := []*content.Page{
		article("/blog/a/", "A", "blog", time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC), nil, nil),
		article("/blog/b/", "B", "blog", time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC), nil, nil),
		article("/blog/c/", "C", "blog", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), nil, nil),
	}
	s := mustAssemble(t, testConfig(), pages)

	if len(s.Archive) != 2 {
		t.Fatalf("archive years = %d, want 2", len(s.Archive))
	}
	if s.Archive[0].Year != 2026 || s.Archive[1].Year != 2025 {
		t.Errorf("archive order = %d,%d, want newest year first", s.Archive[0].Year, s.Archive[1].Year)
	}
	if len(s.Archive[1].Pages) != 2 {
		t.Errorf("2025 has %d pages, want 2", len(s.Archive[1].Pages))
	}
	if s.Find("/archive/") == nil {
		t.Error("no page generated at /archive/")
	}
}

func TestAssembleNoIndexExcludedFromListings(t *testing.T) {
	// The 404 page is written but must not surface as content anywhere.
	notFound := article("/404.html", "Not found", "", buildTime, []string{"go"}, nil)
	notFound.NoIndex = true
	notFound.OutputPath = "404.html"

	pages := []*content.Page{notFound, article("/blog/a/", "A", "blog", day(1), []string{"go"}, nil)}
	s := mustAssemble(t, testConfig(), pages)

	if len(s.RegularPages) != 1 {
		t.Errorf("RegularPages = %v, want only the real post", titles(s.RegularPages))
	}
	if s.Find("/404.html") == nil {
		t.Error("the 404 page must still be written")
	}
	term := s.Find("/tags/go/")
	if len(term.Pages) != 1 {
		t.Errorf("tag listing has %d pages, want 1 (NoIndex excluded)", len(term.Pages))
	}
	if s.Stats.Regular != 1 {
		t.Errorf("Stats.Regular = %d, want 1", s.Stats.Regular)
	}
}

func TestAssembleDetectsURLCollision(t *testing.T) {
	a := article("/blog/same/", "A", "blog", day(1), nil, nil)
	b := article("/blog/same/", "B", "blog", day(2), nil, nil)
	b.SourcePath = "blog/other.md"

	_, err := NewAssembler(testConfig()).Assemble([]*content.Page{a, b}, buildTime)
	if err == nil {
		t.Fatal("two pages at one URL should be an error, not a silent overwrite")
	}
	// The message must name both sources so the collision can be found.
	for _, want := range []string{"/blog/same/", "post.md", "blog/other.md"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("collision error %q should mention %q", err, want)
		}
	}
}

func TestAssembleDetectsCollisionWithGeneratedPage(t *testing.T) {
	// A real file at /tags/go/ would clash with the generated term page.
	clash := article("/tags/go/", "My Page", "tags", day(1), nil, nil)
	tagged := article("/blog/a/", "A", "blog", day(2), []string{"go"}, nil)

	_, err := NewAssembler(testConfig()).Assemble([]*content.Page{clash, tagged}, buildTime)
	if err == nil {
		t.Fatal("a file colliding with a generated taxonomy URL should be an error")
	}
	if !strings.Contains(err.Error(), "generated") {
		t.Errorf("error %q should say the clash involves a generated page", err)
	}
}

func TestNavDerivedFromSections(t *testing.T) {
	cfg := testConfig()
	cfg.Menu = nil
	pages := []*content.Page{
		article("/blog/a/", "A", "blog", day(1), nil, nil),
		article("/docs/b/", "B", "docs", day(2), nil, nil),
	}
	s := mustAssemble(t, cfg, pages)

	if len(s.Menu) != 3 {
		t.Fatalf("derived menu = %v, want Home plus two sections", s.Menu)
	}
	if s.Menu[0].Name != "Home" || s.Menu[0].URL != "/" {
		t.Errorf("first menu item = %+v, want Home at /", s.Menu[0])
	}
}

func TestNavUsesConfiguredMenu(t *testing.T) {
	cfg := testConfig()
	cfg.Menu = []config.MenuItem{{Name: "Custom", URL: "/x/", Weight: 5}}
	s := mustAssemble(t, cfg, nil)

	if len(s.Menu) != 1 || s.Menu[0].Name != "Custom" {
		t.Errorf("menu = %v, want the configured entry", s.Menu)
	}
}

func TestNavActiveMarking(t *testing.T) {
	cfg := testConfig()
	cfg.Menu = []config.MenuItem{
		{Name: "Home", URL: "/"},
		{Name: "Blog", URL: "/blog/"},
		{Name: "About", URL: "/about/"},
	}
	s := mustAssemble(t, cfg, []*content.Page{article("/blog/a/", "A", "blog", day(1), nil, nil)})

	post := s.Find("/blog/a/")
	nav := s.Nav(post)
	active := map[string]bool{}
	for _, e := range nav {
		active[e.Name] = e.Active
	}
	// A section link stays highlighted on the posts inside it.
	if !active["Blog"] {
		t.Error("Blog should be active on /blog/a/")
	}
	if active["About"] {
		t.Error("About should not be active on /blog/a/")
	}
	// The root entry must not claim to be active everywhere.
	if active["Home"] {
		t.Error("Home should not be active on a post URL")
	}

	homeNav := s.Nav(s.Home)
	if !homeNav[0].Active {
		t.Error("Home should be active on the home page")
	}
}

func TestLatestPosts(t *testing.T) {
	pages := []*content.Page{
		article("/blog/a/", "A", "blog", day(1), nil, nil),
		article("/blog/b/", "B", "blog", day(2), nil, nil),
		article("/blog/c/", "C", "blog", day(3), nil, nil),
	}
	s := mustAssemble(t, testConfig(), pages)

	if got := titles(s.LatestPosts(2)); strings.Join(got, ",") != "C,B" {
		t.Errorf("LatestPosts(2) = %v, want the two newest", got)
	}
	if got := s.LatestPosts(99); len(got) != 3 {
		t.Errorf("LatestPosts(99) = %d pages, want all 3", len(got))
	}
	if got := s.LatestPosts(0); len(got) != 3 {
		t.Errorf("LatestPosts(0) = %d pages, want all (0 means no limit)", len(got))
	}
}

func TestAssembleIsDeterministic(t *testing.T) {
	// Two builds of the same input must produce the same page order, or output
	// files are rewritten pointlessly and diffs are meaningless.
	pages := []*content.Page{
		article("/blog/a/", "A", "blog", day(1), []string{"x"}, nil),
		article("/blog/b/", "B", "blog", day(1), []string{"y"}, nil),
		article("/docs/c/", "C", "docs", day(1), nil, nil),
	}
	first := mustAssemble(t, testConfig(), pages)
	second := mustAssemble(t, testConfig(), pages)

	var a, b []string
	for _, p := range first.Pages {
		a = append(a, p.OutputPath)
	}
	for _, p := range second.Pages {
		b = append(b, p.OutputPath)
	}
	if strings.Join(a, "|") != strings.Join(b, "|") {
		t.Errorf("page order differs between runs:\n %v\n %v", a, b)
	}
}

func sectionNames(s *Site) []string {
	out := make([]string, 0, len(s.Sections))
	for _, sec := range s.Sections {
		out = append(out, sec.Name)
	}
	return out
}

func termNames(terms []*Term) []string {
	out := make([]string, 0, len(terms))
	for _, t := range terms {
		out = append(out, t.Name)
	}
	return out
}

func findTerm(terms []*Term, name string) *Term {
	for _, t := range terms {
		if t.Name == name {
			return t
		}
	}
	return nil
}

func titles(pages []*content.Page) []string {
	out := make([]string, 0, len(pages))
	for _, p := range pages {
		out = append(out, p.Meta.Title)
	}
	return out
}
