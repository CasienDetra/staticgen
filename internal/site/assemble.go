package site

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/casien/staticgen/internal/config"
	"github.com/casien/staticgen/internal/content"
	"github.com/casien/staticgen/internal/fsx"
)

// Assembler derives the site model from a set of loaded pages.
type Assembler struct {
	cfg    *config.Site
	mapper fsx.URLMapper
}

// NewAssembler returns an Assembler using the site's URL policy.
func NewAssembler(cfg *config.Site) *Assembler {
	return &Assembler{
		cfg:    cfg,
		mapper: fsx.URLMapper{PrettyURLs: cfg.Build.PrettyURLs},
	}
}

// Assemble builds the complete Site model.
//
// It also synthesises the pages that have no source file — tag and category
// listings, the archive, and a home page when the content root has none — so
// downstream stages can treat every output page identically.
func (a *Assembler) Assemble(loaded []*content.Page, now time.Time) (*Site, error) {
	if now.IsZero() {
		now = time.Now()
	}

	filePages := append([]*content.Page(nil), loaded...)
	s := &Site{Config: a.cfg, BuiltAt: now}

	s.Sections = a.buildSections(filePages)
	sectionPages := a.synthesizeSectionIndexes(s.Sections, now)

	home, homeSynthetic := a.resolveHome(filePages, s.Sections, now)
	s.Home = home

	taxonomy, taxPages := a.buildTaxonomies(filePages, now)
	s.Taxonomy = taxonomy

	archive, archivePages := a.buildArchive(filePages, now)
	s.Archive = archive

	all := make([]*content.Page, 0, len(filePages)+len(sectionPages)+len(taxPages)+len(archivePages)+1)
	all = append(all, filePages...)
	all = append(all, sectionPages...)
	if homeSynthetic != nil {
		all = append(all, homeSynthetic)
	}
	all = append(all, taxPages...)
	all = append(all, archivePages...)

	// Collisions are checked after synthesis so a generated listing page that
	// clashes with a real file is reported too.
	if err := checkURLCollisions(all); err != nil {
		return nil, err
	}

	s.RegularPages = regularPages(all)
	a.linkPrevNext(s)
	s.Menu = a.buildMenu(s)

	// A home page loaded from index.md arrives with no listing of its own; give
	// it the site's recent posts so the home layout renders the same way
	// whether or not the author wrote an index.md.
	if s.Home != nil && len(s.Home.Pages) == 0 {
		s.Home.Pages = s.RegularPages
	}

	sort.SliceStable(all, func(i, j int) bool {
		return all[i].OutputPath < all[j].OutputPath
	})
	s.Pages = all
	s.Stats = computeStats(s)
	return s, nil
}

// dirURL builds a URL for a generated directory page.
//
// Directory URLs always end in a slash regardless of build.pretty_urls: that
// setting controls article URLs only. Sections, taxonomies and the archive are
// directories in the output tree either way, so giving them ".html" URLs would
// make them inconsistent with section landing pages written as _index.md.
func (a *Assembler) dirURL(segments ...string) string {
	return "/" + strings.Join(segments, "/") + "/"
}

func (a *Assembler) permalink(url string) string {
	if a.cfg.BaseURL == "" {
		return url
	}
	return a.cfg.BaseURL + url
}

// listingPage synthesises a page with no source file.
func (a *Assembler) listingPage(kind content.Kind, title, url, term string, pages []*content.Page, now time.Time) *content.Page {
	return &content.Page{
		URL:        url,
		OutputPath: a.mapper.OutputPath(url),
		Permalink:  a.permalink(url),
		Meta: content.Meta{
			Title:   title,
			Date:    now,
			LastMod: now,
			Extra:   map[string]any{},
		},
		Kind:    kind,
		IsIndex: true,
		Term:    term,
		Pages:   pages,
	}
}

// buildSections groups pages by top-level directory and attaches each section's
// landing page.
func (a *Assembler) buildSections(pages []*content.Page) []*Section {
	byName := map[string]*Section{}
	var order []string

	for _, p := range pages {
		if p.Section == "" {
			continue
		}
		sec, ok := byName[p.Section]
		if !ok {
			sec = &Section{
				Name:  p.Section,
				Title: humanize(p.Section),
				URL:   a.dirURL(p.Section),
			}
			byName[p.Section] = sec
			order = append(order, p.Section)
		}
		if p.IsIndex {
			sec.Index = p
		}
	}

	out := make([]*Section, 0, len(order))
	for _, name := range order {
		sec := byName[name]
		sec.Pages = regularInSection(pages, name)
		if sec.Index != nil {
			// Section landing pages list their own members.
			sec.Index.Pages = sec.Pages
			if sec.Index.Meta.Title == "" {
				sec.Index.Meta.Title = sec.Title
			}
			sec.Title = sec.Index.Meta.Title
		}
		out = append(out, sec)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// synthesizeSectionIndexes creates a landing page for every section that has no
// _index.md. Without one, the derived navigation and the section links inside
// post listings would point at a URL nothing is ever written to.
func (a *Assembler) synthesizeSectionIndexes(sections []*Section, now time.Time) []*content.Page {
	var out []*content.Page
	for _, sec := range sections {
		if sec.Index != nil {
			continue
		}
		p := a.listingPage(content.KindSection, sec.Title, sec.URL, "", sec.Pages, now)
		p.Section = sec.Name
		sec.Index = p
		out = append(out, p)
	}
	return out
}

// resolveHome finds the root landing page, synthesising one when the content
// tree has no index.md. The synthesised page is returned separately so the
// caller can add it to the build.
func (a *Assembler) resolveHome(pages []*content.Page, sections []*Section, now time.Time) (*content.Page, *content.Page) {
	for _, p := range pages {
		if p.Kind == content.KindHome {
			if p.Meta.Title == "" {
				p.Meta.Title = a.cfg.Title
			}
			return p, nil
		}
	}

	home := a.listingPage(content.KindHome, a.cfg.Title, "/", "", nil, now)
	home.Meta.Description = a.cfg.Description
	// A synthesised home page lists the newest posts, which is what a bare
	// content directory implies the author wanted.
	home.Pages = regularPages(pages)
	return home, home
}

// buildTaxonomies groups pages by tag and category and synthesises one listing
// page per term plus one index page per taxonomy.
func (a *Assembler) buildTaxonomies(pages []*content.Page, now time.Time) (TaxonomyIndex, []*content.Page) {
	regular := regularPages(pages)

	tags, tagPages := a.buildOneTaxonomy("tags", "Tags", regular, func(p *content.Page) []string {
		return p.Meta.Tags
	}, now)
	cats, catPages := a.buildOneTaxonomy("categories", "Categories", regular, func(p *content.Page) []string {
		return p.Meta.Categories
	}, now)

	all := make([]*Term, 0, len(tags)+len(cats))
	all = append(all, tags...)
	all = append(all, cats...)

	return TaxonomyIndex{Tags: tags, Categories: cats, AllTerms: all}, append(tagPages, catPages...)
}

func (a *Assembler) buildOneTaxonomy(slug, title string, pages []*content.Page, pick func(*content.Page) []string, now time.Time) ([]*Term, []*content.Page) {
	groups := map[string]*Term{}

	for _, p := range pages {
		for _, raw := range pick(p) {
			key := fsx.Slugify(raw)
			if key == "" {
				continue
			}
			t, ok := groups[key]
			if !ok {
				t = &Term{Name: raw, Slug: key}
				groups[key] = t
			}
			t.Pages = append(t.Pages, p)
		}
	}

	terms := make([]*Term, 0, len(groups))
	for _, t := range groups {
		terms = append(terms, t)
	}
	sort.Slice(terms, func(i, j int) bool {
		if len(terms[i].Pages) != len(terms[j].Pages) {
			// Busiest terms first: the listing reads as a tag cloud.
			return len(terms[i].Pages) > len(terms[j].Pages)
		}
		return strings.ToLower(terms[i].Name) < strings.ToLower(terms[j].Name)
	})

	var synthetic []*content.Page
	for _, t := range terms {
		t.Pages = regularSorted(t.Pages)
		t.URL = a.dirURL(slug, t.Slug)
		t.Page = a.listingPage(content.KindTerm, t.Name, t.URL, t.Name, t.Pages, now)
		synthetic = append(synthetic, t.Page)
	}

	// The taxonomy index page carries no Pages of its own; templates read the
	// term list from Site.Taxonomy instead.
	indexURL := a.dirURL(slug)
	synthetic = append(synthetic, a.listingPage(content.KindTaxonomy, title, indexURL, "", nil, now))

	return terms, synthetic
}

// buildArchive groups regular pages by year, newest year first.
func (a *Assembler) buildArchive(pages []*content.Page, now time.Time) ([]*YearGroup, []*content.Page) {
	byYear := map[int][]*content.Page{}
	for _, p := range regularPages(pages) {
		d := p.Date()
		if d.IsZero() {
			continue
		}
		byYear[d.Year()] = append(byYear[d.Year()], p)
	}

	years := make([]int, 0, len(byYear))
	for y := range byYear {
		years = append(years, y)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(years)))

	groups := make([]*YearGroup, 0, len(years))
	for _, y := range years {
		groups = append(groups, &YearGroup{Year: y, Pages: regularSorted(byYear[y])})
	}

	page := a.listingPage(content.KindArchive, "Archive", a.dirURL("archive"), "", nil, now)
	return groups, []*content.Page{page}
}

// buildMenu returns the configured menu, or one derived from the site's
// sections when the config defines none.
func (a *Assembler) buildMenu(s *Site) []config.MenuItem {
	if len(a.cfg.Menu) > 0 {
		return a.cfg.Menu
	}
	menu := []config.MenuItem{{Name: "Home", URL: "/", Weight: -100}}
	for _, sec := range s.Sections {
		menu = append(menu, config.MenuItem{Name: sec.Title, URL: sec.URL, Weight: 0})
	}
	return menu
}

// linkPrevNext connects each regular page to its chronological neighbours
// within its own section. Prev is the older post, Next the newer one.
func (a *Assembler) linkPrevNext(s *Site) {
	groups := map[string][]*content.Page{}
	for _, p := range s.RegularPages {
		groups[p.Section] = append(groups[p.Section], p)
	}
	for _, group := range groups {
		// RegularPages is newest-first, so index+1 is older.
		for i, p := range group {
			if i > 0 {
				p.Next = group[i-1]
			}
			if i+1 < len(group) {
				p.Prev = group[i+1]
			}
		}
	}
}

// regularPages returns non-listing pages that are meant to be discovered,
// newest first. NoIndex pages — the 404 page and redirect stubs — are excluded
// so they never reach a listing, feed or archive.
func regularPages(pages []*content.Page) []*content.Page {
	out := make([]*content.Page, 0, len(pages))
	for _, p := range pages {
		if p.Kind == content.KindPage && !p.NoIndex {
			out = append(out, p)
		}
	}
	return regularSorted(out)
}

func regularInSection(pages []*content.Page, section string) []*content.Page {
	out := make([]*content.Page, 0)
	for _, p := range pages {
		if p.Kind == content.KindPage && !p.NoIndex && p.Section == section {
			out = append(out, p)
		}
	}
	return regularSorted(out)
}

// regularSorted orders pages newest first, then by ascending weight, then by
// title so the result is deterministic for equal dates.
func regularSorted(pages []*content.Page) []*content.Page {
	out := append([]*content.Page(nil), pages...)
	sort.SliceStable(out, func(i, j int) bool {
		di, dj := out[i].Date(), out[j].Date()
		if !di.Equal(dj) {
			return di.After(dj)
		}
		if out[i].Meta.Weight != out[j].Meta.Weight {
			return out[i].Meta.Weight < out[j].Meta.Weight
		}
		if out[i].URL != out[j].URL {
			return out[i].URL < out[j].URL
		}
		return out[i].Meta.Title < out[j].Meta.Title
	})
	return out
}

// checkURLCollisions reports distinct source files that map to one URL, which
// would otherwise mean one page silently overwriting another in the output.
func checkURLCollisions(pages []*content.Page) error {
	byURL := map[string][]string{}
	for _, p := range pages {
		src := p.SourcePath
		if src == "" {
			src = "(generated " + string(p.Kind) + " page)"
		}
		byURL[p.URL] = append(byURL[p.URL], src)
	}

	var dups []string
	for url, srcs := range byURL {
		if len(srcs) < 2 {
			continue
		}
		sort.Strings(srcs)
		dups = append(dups, fmt.Sprintf("%s is claimed by %s", url, strings.Join(srcs, " and ")))
	}
	if len(dups) == 0 {
		return nil
	}
	sort.Strings(dups)
	return fmt.Errorf("url collision: %s", strings.Join(dups, "; "))
}

func computeStats(s *Site) Stats {
	st := Stats{
		Pages:      len(s.Pages),
		Regular:    len(s.RegularPages),
		Sections:   len(s.Sections),
		Tags:       len(s.Taxonomy.Tags),
		Categories: len(s.Taxonomy.Categories),
	}
	for _, p := range s.RegularPages {
		st.Words += p.WordCount
	}
	return st
}

// humanize expands a slug into Title Case words.
func humanize(slug string) string {
	words := strings.FieldsFunc(slug, func(r rune) bool {
		return r == '-' || r == '_' || r == '.' || r == '/'
	})
	if len(words) == 0 {
		return ""
	}
	for i, w := range words {
		r := []rune(w)
		r[0] = []rune(strings.ToUpper(string(r[0])))[0]
		words[i] = string(r)
	}
	return strings.Join(words, " ")
}
