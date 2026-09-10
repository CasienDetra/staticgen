package content

import (
	"html/template"
	"strings"
	"time"

	"github.com/casien/staticgen/internal/markdown"
)

// Kind classifies a page for layout selection.
type Kind string

const (
	// KindHome is the site's root page.
	KindHome Kind = "home"
	// KindPage is a regular article or standalone page.
	KindPage Kind = "page"
	// KindSection is a directory landing page (_index.md / index.md).
	KindSection Kind = "section"
	// KindTaxonomy lists all terms of one taxonomy, e.g. /tags/.
	KindTaxonomy Kind = "taxonomy"
	// KindTerm lists the pages carrying one term, e.g. /tags/go/.
	KindTerm Kind = "term"
	// KindArchive lists pages grouped by year.
	KindArchive Kind = "archive"
)

// IsListing reports whether the page exists to list other pages rather than to
// present an article body.
func (k Kind) IsListing() bool {
	switch k {
	case KindSection, KindTaxonomy, KindTerm, KindArchive, KindHome:
		return true
	default:
		return false
	}
}

// Page is one rendered document plus everything a template needs to place it in
// the site. Pages are produced by Loader and enriched (Prev/Next, taxonomies) by
// the site package.
type Page struct {
	// SourcePath is relative to the content root, slash-separated.
	SourcePath string
	// URL is the site-relative permalink path, e.g. "/blog/hello/".
	URL string
	// OutputPath is relative to the output root, e.g. "blog/hello/index.html".
	OutputPath string
	// Permalink is absolute (BaseURL + URL), empty when no base_url is set.
	Permalink string

	Meta Meta
	// Section is the top-level content directory; "" for root-level pages.
	Section string
	// IsIndex marks a section landing page (_index.md or index.md), which
	// templates list other pages from rather than rendering as an article.
	IsIndex bool
	// Kind classifies the page so templates can pick a layout. Only KindPage
	// and KindSection come from files on disk; the site package synthesises
	// taxonomy, term and archive pages that have no source file.
	Kind Kind
	// Pages holds the pages a listing page displays — section members, posts
	// carrying a tag, entries in a year. Empty for regular articles.
	Pages []*Page
	// Term is the taxonomy value a KindTerm page represents, e.g. "go".
	Term string
	// NoIndex keeps a page out of feeds, the sitemap, the search index and
	// every listing. It is still rendered and written, because its URL has to
	// resolve. Set for the 404 page and for redirect stubs: both exist to
	// answer a request, not to be read.
	NoIndex bool

	// BodyHTML is the rendered Markdown article without any site layout.
	BodyHTML template.HTML
	// SummaryHTML is a short rendered excerpt for index, tag and feed listings.
	SummaryHTML template.HTML
	// PlainText is the body stripped of markup, used for search and word counts.
	PlainText string
	// Headings is in document order and drives the per-page table of contents.
	Headings []markdown.Heading

	WordCount      int
	ReadingMinutes int
	ModTime        time.Time

	// Prev and Next are chronological neighbours within the page's section,
	// assigned by the site package after all pages are loaded.
	Prev, Next *Page
}

// Title returns the page title, which the loader guarantees is non-empty.
func (p *Page) Title() string { return p.Meta.Title }

// Date returns the publication date, which the loader guarantees is non-zero.
func (p *Page) Date() time.Time { return p.Meta.Date }

// LastMod returns the last modification time, falling back to Date.
func (p *Page) LastMod() time.Time {
	if p.Meta.LastMod.IsZero() {
		return p.Meta.Date
	}
	return p.Meta.LastMod
}

// Tags returns the page's tags, normalised and de-duplicated.
func (p *Page) Tags() []string { return p.Meta.Tags }

// Categories returns the page's categories.
func (p *Page) Categories() []string { return p.Meta.Categories }

// HasSummary reports whether a usable excerpt exists.
func (p *Page) HasSummary() bool { return strings.TrimSpace(string(p.SummaryHTML)) != "" }

// TOC returns the headings that should appear in a table of contents: level 2
// and deeper, excluding the page's own H1 title.
func (p *Page) TOC() []markdown.Heading {
	out := make([]markdown.Heading, 0, len(p.Headings))
	for _, h := range p.Headings {
		if h.Level >= 2 && h.ID != "" {
			out = append(out, h)
		}
	}
	return out
}

// HasTOC reports whether the page has enough structure to warrant a contents list.
func (p *Page) HasTOC() bool { return len(p.TOC()) > 0 }

// WantsTOC resolves the per-page toc frontmatter flag against the site default.
func (p *Page) WantsTOC(siteDefault bool) bool {
	if p.Meta.TOC != nil {
		return *p.Meta.TOC
	}
	return siteDefault
}
