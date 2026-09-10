// Package site assembles loaded pages into the queryable model that templates
// render against: ordering, sections, taxonomies, archives and navigation.
//
// The loader produces pages from files; this package derives everything that
// only exists once all pages are known. Pages that have no source file at all
// (tag listings, the archive) are synthesised here and are indistinguishable
// from real pages as far as the renderer is concerned.
package site

import (
	"time"

	"github.com/casien/staticgen/internal/config"
	"github.com/casien/staticgen/internal/content"
)

// Site is the fully assembled model for one build.
type Site struct {
	Config *config.Site

	// Pages is every page that will be written, including synthesised listing
	// pages, sorted by output path for deterministic builds.
	Pages []*content.Page
	// RegularPages excludes listing pages, newest first.
	RegularPages []*content.Page
	// Home is the root page; always non-nil, synthesised when absent.
	Home *content.Page

	Sections []*Section
	Taxonomy TaxonomyIndex
	// Archive groups regular pages by descending year.
	Archive []*YearGroup

	Menu []config.MenuItem
	// BuiltAt is stamped into feeds and footers.
	BuiltAt time.Time
	Stats   Stats
}

// Section is one top-level content directory.
type Section struct {
	// Name is the directory name, e.g. "blog".
	Name string
	// Title is the human-readable section title.
	Title string
	URL   string
	// Index is the section landing page, nil when the directory has no
	// _index.md; the section still appears in navigation.
	Index *content.Page
	// Pages are the section's regular pages, newest first.
	Pages []*content.Page
}

// Term is one taxonomy value and the pages carrying it.
type Term struct {
	// Name is the term as written in frontmatter.
	Name string
	// Slug is the URL-safe form.
	Slug string
	URL  string
	// Page is the synthesised listing page for this term.
	Page  *content.Page
	Pages []*content.Page
}

// Count returns the number of pages carrying the term.
func (t *Term) Count() int { return len(t.Pages) }

// TaxonomyIndex holds every taxonomy the site exposes.
type TaxonomyIndex struct {
	Tags       []*Term
	Categories []*Term
	// AllTerms lets a template render a combined tag cloud without knowing
	// which taxonomies are configured.
	AllTerms []*Term
}

// YearGroup is one year of the archive.
type YearGroup struct {
	Year  int
	Pages []*content.Page
}

// NavEntry is one navigation item, marked active relative to a given page.
type NavEntry struct {
	Name   string
	URL    string
	Active bool
}

// Stats summarises a build for CLI output.
type Stats struct {
	Pages      int
	Regular    int
	Sections   int
	Tags       int
	Categories int
	Words      int
}

// Find returns the page served at the given site URL, or nil.
func (s *Site) Find(url string) *content.Page {
	for _, p := range s.Pages {
		if p.URL == url {
			return p
		}
	}
	return nil
}

// FindBySource returns the page loaded from a content-relative path, or nil.
func (s *Site) FindBySource(sourcePath string) *content.Page {
	for _, p := range s.Pages {
		if p.SourcePath == sourcePath {
			return p
		}
	}
	return nil
}

// SectionByName returns the section with the given directory name, or nil.
func (s *Site) SectionByName(name string) *Section {
	for _, sec := range s.Sections {
		if sec.Name == name {
			return sec
		}
	}
	return nil
}

// Nav renders the site menu with Active set relative to current.
//
// An entry is active when it points at current, or at an ancestor of current,
// so a section link stays highlighted on the posts inside it.
func (s *Site) Nav(current *content.Page) []NavEntry {
	out := make([]NavEntry, 0, len(s.Menu))
	for _, item := range s.Menu {
		out = append(out, NavEntry{
			Name:   item.Name,
			URL:    item.URL,
			Active: isActive(item.URL, current),
		})
	}
	return out
}

func isActive(menuURL string, current *content.Page) bool {
	if current == nil || menuURL == "" {
		return false
	}
	if menuURL == current.URL {
		return true
	}
	// The root entry is active everywhere, which is unhelpful highlighting, so
	// only match it exactly.
	if menuURL == "/" {
		return current.URL == "/"
	}
	return len(current.URL) > len(menuURL) && current.URL[:len(menuURL)] == menuURL
}

// LatestPosts returns up to n regular pages, newest first.
func (s *Site) LatestPosts(n int) []*content.Page {
	if n <= 0 || n > len(s.RegularPages) {
		n = len(s.RegularPages)
	}
	return s.RegularPages[:n]
}
