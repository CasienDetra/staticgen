package content

import (
	"errors"
	"fmt"
	"html/template"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/casien/staticgen/internal/fsx"
	"github.com/casien/staticgen/internal/markdown"
)

// LoadOptions controls which files become pages and how their URLs are shaped.
// Now is injectable so draft/future/expiry filtering is testable.
type LoadOptions struct {
	IncludeDrafts bool
	IncludeFuture bool
	PrettyURLs    bool
	// SummaryWords is the auto-excerpt length; 0 disables auto-excerpts.
	SummaryWords int
	BaseURL      string
	Now          time.Time
}

// Loader discovers and renders Markdown files into Pages.
type Loader struct {
	root   string
	md     *markdown.Renderer
	mapper fsx.URLMapper
	opts   LoadOptions
}

// NewLoader returns a Loader reading from root.
func NewLoader(root string, md *markdown.Renderer, opts LoadOptions) *Loader {
	return &Loader{
		root:   root,
		md:     md,
		mapper: fsx.URLMapper{PrettyURLs: opts.PrettyURLs},
		opts:   opts,
	}
}

// moreMarker matches an excerpt separator on its own line. Splitting the source
// rather than looking for a comment in the rendered HTML keeps the feature
// working with markup.unsafe disabled, where goldmark omits raw HTML.
var moreMarker = regexp.MustCompile(`(?m)^[ \t]*<!--+\s*more\s*-->+[ \t]*\r?$`)

// Load reads every eligible Markdown file under the content root.
//
// Files that are filtered out (drafts, future-dated, expired) are skipped
// silently. Files that fail to read or render are reported together via
// errors.Join so one broken page does not mask the others, and the pages that
// did load are still returned for callers that want partial results.
func (l *Loader) Load() ([]*Page, error) {
	now := l.opts.Now
	if now.IsZero() {
		now = time.Now()
	}

	var (
		pages []*Page
		errs  []error
	)

	walkErr := fsx.Walk(l.root, func(f fsx.SourceFile) error {
		if !fsx.IsContent(f.RelPath) || fsx.ShouldSkip(f.RelPath) {
			return nil
		}
		p, err := l.loadFile(f, now)
		if err != nil {
			errs = append(errs, err)
			return nil
		}
		if p != nil {
			pages = append(pages, p)
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	if len(errs) > 0 {
		return pages, fmt.Errorf("%d page(s) failed to load: %w", len(errs), errors.Join(errs...))
	}
	return pages, nil
}

// loadFile renders one file, returning (nil, nil) when it is filtered out.
func (l *Loader) loadFile(f fsx.SourceFile, now time.Time) (*Page, error) {
	raw, err := os.ReadFile(f.AbsPath)
	if err != nil {
		return nil, fmt.Errorf("%s: read: %w", f.RelPath, err)
	}

	front, body, _ := Split(raw)
	meta, err := ParseMeta(front)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", f.RelPath, err)
	}

	if meta.Draft && !l.opts.IncludeDrafts {
		return nil, nil
	}
	var explicitDate bool
	meta.Date, explicitDate = l.resolveDate(meta, f)
	// Only an explicitly dated page can be "scheduled". A date inherited from
	// the file's mtime is a fallback for ordering, not a publication intent, so
	// filtering on it would silently drop pages whenever a checkout, restore or
	// clock skew produced a future timestamp.
	if explicitDate && meta.Date.After(now) && !l.opts.IncludeFuture {
		return nil, nil
	}
	if meta.IsExpired(now) {
		return nil, nil
	}
	if meta.LastMod.IsZero() {
		meta.LastMod = f.Info.ModTime()
	}

	mainBody, hasMarker, prefix := splitSummary(body)
	res, err := l.md.Render(mainBody)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", f.RelPath, err)
	}

	url := l.mapper.URLPath(f.RelPath, meta.Slug)
	if meta.Title == "" {
		meta.Title = deriveTitle(res.Headings, url)
	}

	p := &Page{
		SourcePath:     f.RelPath,
		URL:            url,
		OutputPath:     l.mapper.OutputPath(url),
		Permalink:      l.permalink(url),
		Meta:           meta,
		Section:        fsx.Section(f.RelPath),
		IsIndex:        fsx.IsSectionIndex(f.RelPath),
		BodyHTML:       template.HTML(res.HTML),
		PlainText:      res.Text,
		Headings:       res.Headings,
		WordCount:      res.WordCount(),
		ReadingMinutes: res.ReadingMinutes(),
		ModTime:        f.Info.ModTime(),
	}
	p.Kind = KindPage
	if p.IsIndex {
		if p.Section == "" {
			p.Kind = KindHome
		} else {
			p.Kind = KindSection
		}
	}
	// The error page is written to /404.html but is not content: without this
	// it would appear in the feed, the sitemap, search and every listing, and
	// because it carries no date it would sort as the newest post.
	p.NoIndex = p.URL == "/404.html" || p.Meta.Redirect != ""
	p.SummaryHTML = l.summarize(p, prefix, hasMarker)
	return p, nil
}

// resolveDate picks the publication date: explicit frontmatter, then a
// YYYY-MM-DD filename prefix, then the file's modification time. Every page
// ends up with a usable date so feeds and archives can order them.
//
// The second result reports whether the date was stated by the author rather
// than inferred from the filesystem, which decides whether the future-post
// filter applies.
func (l *Loader) resolveDate(meta Meta, f fsx.SourceFile) (time.Time, bool) {
	if meta.HasDate() {
		return meta.Date, true
	}
	base := filepath.Base(f.RelPath)
	dp := fsx.SplitDatePrefix(strings.TrimSuffix(base, filepath.Ext(base)))
	if dp.Found {
		t := time.Date(dp.Year, time.Month(dp.Month), dp.Day, 0, 0, 0, 0, time.UTC)
		// A nonsense date in a filename normalises into a different year;
		// falling through to mtime beats publishing a post dated 2027 because
		// someone typed month 13.
		if !t.IsZero() && t.Year() == dp.Year {
			return t, true
		}
	}
	return f.Info.ModTime(), false
}

func (l *Loader) permalink(url string) string {
	if l.opts.BaseURL == "" {
		return url
	}
	return strings.TrimRight(l.opts.BaseURL, "/") + url
}

// summarize resolves a page excerpt in precedence order: explicit frontmatter
// summary, then a <!--more--> marker, then the leading words of the body.
func (l *Loader) summarize(p *Page, prefix []byte, hasMarker bool) template.HTML {
	if p.Meta.Summary != "" {
		// Explicit summaries are plain text and escaped, so frontmatter can
		// never inject markup into a listing.
		return paragraph(p.Meta.Summary)
	}
	if hasMarker {
		if res, err := l.md.Render(prefix); err == nil && strings.TrimSpace(res.HTML) != "" {
			return template.HTML(res.HTML)
		}
	}
	return truncateWords(p.PlainText, l.opts.SummaryWords)
}

// splitSummary removes the <!--more--> marker from the body and returns the
// text preceding it.
func splitSummary(body []byte) (main []byte, hasMarker bool, prefix []byte) {
	loc := moreMarker.FindIndex(body)
	if loc == nil {
		return body, false, nil
	}
	prefix = body[:loc[0]]
	// The marker regex stops before the newline, so the remainder still starts
	// with one and the rejoined source stays valid Markdown.
	main = append(append([]byte{}, prefix...), body[loc[1]:]...)
	return main, true, prefix
}

func truncateWords(text string, n int) template.HTML {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	words := strings.Fields(text)
	if n <= 0 || len(words) <= n {
		return paragraph(strings.Join(words, " "))
	}
	return paragraph(strings.Join(words[:n], " ") + "…")
}

func paragraph(s string) template.HTML {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	return template.HTML("<p>" + template.HTMLEscapeString(s) + "</p>")
}

// deriveTitle falls back to the document's H1, then to a humanised slug, so a
// page always has a title for <title> and listings.
func deriveTitle(headings []markdown.Heading, url string) string {
	for _, h := range headings {
		if h.Level == 1 && strings.TrimSpace(h.Text) != "" {
			return h.Text
		}
	}
	base := strings.TrimSuffix(strings.Trim(path.Base(url), "/"), ".html")
	return humanize(base)
}

// humanize expands a slug into Title Case words.
func humanize(slug string) string {
	words := strings.FieldsFunc(slug, func(r rune) bool {
		return r == '-' || r == '_' || r == '.'
	})
	if len(words) == 0 {
		return "Untitled"
	}
	for i, w := range words {
		r := []rune(w)
		r[0] = []rune(strings.ToUpper(string(r[0])))[0]
		words[i] = string(r)
	}
	return strings.Join(words, " ")
}
