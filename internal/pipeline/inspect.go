package pipeline

import (
	"context"
	"errors"
	"path"
	"sort"
	"strings"

	"github.com/casien/staticgen/internal/content"
	"github.com/casien/staticgen/internal/fsx"
	"github.com/casien/staticgen/internal/generate"
	"github.com/casien/staticgen/internal/site"
)

// RenderedPage pairs a page with its complete HTML document.
type RenderedPage struct {
	Page *content.Page
	HTML []byte
}

// Inspection is the result of rendering a site without writing it.
type Inspection struct {
	Site     *site.Site
	Pages    []RenderedPage
	Warnings []string
	// LoadErr is set when some pages failed to load. The pages that did load are
	// still inspected, so one broken file does not hide other problems.
	LoadErr error
}

// Inspect loads, assembles and renders the whole site in memory without writing
// anything to disk.
//
// It exists for the check command, which needs the final rendered HTML in order
// to find broken links and missing images without the side effect of a build.
func (b *Builder) Inspect(ctx context.Context) (*Inspection, error) {
	pages, loadErr := b.loader.Load()

	s, err := b.assembler.Assemble(pages, b.now)
	if err != nil {
		if loadErr != nil {
			return nil, errors.Join(loadErr, err)
		}
		return nil, err
	}

	insp := &Inspection{Site: s, LoadErr: loadErr}
	insp.Pages = make([]RenderedPage, 0, len(s.Pages))

	for _, p := range s.Pages {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		doc, err := b.engine.RenderPage(p, s)
		if err != nil {
			return nil, err
		}
		if b.cfg.Build.Minify {
			doc = minify(doc)
		}
		insp.Pages = append(insp.Pages, RenderedPage{Page: p, HTML: doc})
	}
	return insp, nil
}

// StaticPaths lists the site-relative URLs served from the static directory, so
// a link checker can distinguish a missing image from a missing page.
func (b *Builder) StaticPaths() []string {
	var out []string
	err := fsx.Walk(b.cfg.StaticPath(), func(f fsx.SourceFile) error {
		out = append(out, "/"+f.RelPath)
		return nil
	})
	if err != nil {
		return nil
	}
	sort.Strings(out)
	return out
}

// KnownPaths returns every site-relative URL the build will produce: page URLs,
// theme assets, copied static files and enabled generated artefacts.
//
// A link checker resolves internal links against this set. Both the trailing-
// slash and bare forms of each directory URL are included, because authors write
// links both ways.
func (b *Builder) KnownPaths() map[string]bool {
	known := map[string]bool{}
	add := func(p string) {
		if p == "" {
			return
		}
		if !strings.HasPrefix(p, "/") {
			p = "/" + p
		}
		known[path.Clean(p)] = true
		trimmed := strings.TrimSuffix(path.Clean(p), "/")
		if trimmed != "" {
			known[trimmed] = true
		}
	}

	// Page URLs come from a load+assemble pass; callers that already have an
	// Inspection should add its page URLs themselves rather than repeat the work.
	for name := range b.engine.Assets() {
		add("/assets/" + name)
	}
	if b.cfg.Markup.Highlight.Enabled {
		add("/assets/css/chroma.css")
	}
	for _, p := range b.StaticPaths() {
		add(p)
	}
	if b.cfg.Feeds.RSS {
		add("/" + generate.FeedPath)
	}
	if b.cfg.Feeds.Sitemap {
		add("/" + generate.SitemapPath)
	}
	if b.cfg.Feeds.Search {
		add("/" + generate.SearchPath)
	}
	return known
}
