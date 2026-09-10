// Package pipeline wires the build stages together.
//
// The stages are: load content, assemble the site model, render pages, emit
// ancillary artefacts and write everything to disk. Each stage is an interface
// defined here so a build can be driven with fakes in tests, and so the serve
// command can reuse the exact same path as a one-shot build.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/casien/staticgen/internal/config"
	"github.com/casien/staticgen/internal/content"
	"github.com/casien/staticgen/internal/fsx"
	"github.com/casien/staticgen/internal/generate"
	"github.com/casien/staticgen/internal/markdown"
	"github.com/casien/staticgen/internal/publish"
	"github.com/casien/staticgen/internal/render"
	"github.com/casien/staticgen/internal/site"
)

// Options tunes a build without changing the site configuration.
type Options struct {
	// LiveReload injects the dev-server reload script into every page.
	LiveReload bool
	// Now overrides the build timestamp; zero means time.Now(). Injecting it
	// keeps builds reproducible in tests.
	Now time.Time
}

// Result summarises one build.
type Result struct {
	Output        string
	PagesWritten  int
	Redirects     int
	AssetsWritten int
	StaticCopied  int
	Bytes         int64
	Duration      time.Duration
	Warnings      []string
	Site          *site.Site
}

// Builder holds the fully wired stages for repeated builds.
type Builder struct {
	cfg       *config.Site
	md        *markdown.Renderer
	loader    *content.Loader
	assembler *site.Assembler
	engine    *render.Engine
	mapper    fsx.URLMapper
	now       time.Time
	live      bool
}

// New constructs every stage. Template and configuration errors surface here so
// a build fails before reading any content.
func New(cfg *config.Site, opts Options) (*Builder, error) {
	if cfg == nil {
		return nil, errors.New("pipeline: site config is required")
	}

	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}

	md := markdown.New(markdown.Options{
		Unsafe:      cfg.Markup.Unsafe,
		Typographer: cfg.Markup.Typographer,
		Highlight: markdown.HighlightOptions{
			Enabled:       cfg.Markup.Highlight.Enabled,
			Style:         cfg.Markup.Highlight.Style,
			LineNumbers:   cfg.Markup.Highlight.LineNumbers,
			GuessLanguage: cfg.Markup.Highlight.GuessLanguage,
		},
	})

	engine, err := render.New(render.Options{
		Config:       cfg,
		TemplatesDir: cfg.TemplatesPath(),
		LiveReload:   opts.LiveReload,
		AssetVersion: now.UTC().Format("20060102150405"),
	})
	if err != nil {
		return nil, err
	}

	loader := content.NewLoader(cfg.ContentPath(), md, content.LoadOptions{
		IncludeDrafts: cfg.Build.Drafts,
		IncludeFuture: cfg.Build.Future,
		PrettyURLs:    cfg.Build.PrettyURLs,
		SummaryWords:  cfg.Build.SummaryLength,
		BaseURL:       cfg.BaseURL,
		Now:           now,
	})

	return &Builder{
		cfg:       cfg,
		md:        md,
		loader:    loader,
		assembler: site.NewAssembler(cfg),
		engine:    engine,
		mapper:    fsx.URLMapper{PrettyURLs: cfg.Build.PrettyURLs},
		now:       now,
		live:      opts.LiveReload,
	}, nil
}

// Config returns the resolved configuration the builder uses.
func (b *Builder) Config() *config.Site { return b.cfg }

// OutputDir returns the directory builds are written to.
func (b *Builder) OutputDir() string { return b.cfg.OutputPath() }

// Build runs the whole pipeline once.
//
// A partial failure while loading pages does not abort the build: the pages that
// did load are still emitted and the load error is returned alongside the
// result, so a single broken file is reported without producing nothing.
func (b *Builder) Build(ctx context.Context) (*Result, error) {
	start := time.Now()
	res := &Result{Output: b.cfg.OutputPath()}

	w, err := publish.NewWriter(res.Output)
	if err != nil {
		return nil, err
	}
	if b.cfg.Build.CleanOutput {
		if err := w.Clean(b.cfg.Root); err != nil {
			return nil, err
		}
	}

	pages, loadErr := b.loader.Load()

	s, err := b.assembler.Assemble(pages, b.now)
	if err != nil {
		if loadErr != nil {
			return nil, errors.Join(loadErr, err)
		}
		return nil, err
	}
	res.Site = s

	if err := b.writeAssets(w, res); err != nil {
		return nil, err
	}

	staticStats, err := fsx.CopyTree(b.cfg.StaticPath(), res.Output)
	if err != nil {
		return nil, err
	}
	res.StaticCopied = staticStats.Files
	res.Bytes += staticStats.Bytes

	if err := b.writePages(ctx, w, s, res); err != nil {
		return nil, err
	}
	b.writeGenerated(w, s, res)

	res.Duration = time.Since(start)
	if loadErr != nil {
		return res, loadErr
	}
	return res, nil
}

// writePages renders every page and any redirects it declares.
func (b *Builder) writePages(ctx context.Context, w *publish.Writer, s *site.Site, res *Result) error {
	// Index output paths up front so an alias that would silently overwrite a
	// real page can be reported instead.
	taken := make(map[string]string, len(s.Pages))
	for _, p := range s.Pages {
		taken[p.OutputPath] = p.SourcePath
	}

	for _, p := range s.Pages {
		if err := ctx.Err(); err != nil {
			return err
		}

		if p.Meta.Redirect != "" {
			// A redirect stub replaces the page entirely: rendering the body
			// too would leave two competing documents at one URL.
			target := b.resolveTarget(p.Meta.Redirect)
			body, err := redirectPage(target)
			if err != nil {
				return fmt.Errorf("%s: %w", p.SourcePath, err)
			}
			if err := w.Write(p.OutputPath, body); err != nil {
				return err
			}
			res.Redirects++
			res.Bytes += int64(len(body))
			continue
		}

		doc, err := b.engine.RenderPage(p, s)
		if err != nil {
			return err
		}
		if b.cfg.Build.Minify {
			doc = minify(doc)
		}
		if err := w.Write(p.OutputPath, doc); err != nil {
			return err
		}
		res.PagesWritten++
		res.Bytes += int64(len(doc))

		for _, alias := range p.Meta.Aliases {
			out := b.mapper.OutputPath(normalizeAlias(alias))
			if out == p.OutputPath {
				continue
			}
			if owner, clash := taken[out]; clash {
				res.Warnings = append(res.Warnings, fmt.Sprintf(
					"alias %q of %s collides with %s and was skipped", alias, p.SourcePath, owner))
				continue
			}
			body, err := redirectPage(b.cfg.AbsURL(p.URL))
			if err != nil {
				return fmt.Errorf("%s: alias %q: %w", p.SourcePath, alias, err)
			}
			if err := w.Write(out, body); err != nil {
				return err
			}
			taken[out] = p.SourcePath + " (alias)"
			res.Redirects++
			res.Bytes += int64(len(body))
		}
	}
	return nil
}

// writeAssets emits the theme's static files and, when highlighting is on, the
// chroma stylesheet that class-based highlighting depends on.
func (b *Builder) writeAssets(w *publish.Writer, res *Result) error {
	assets := b.engine.Assets()
	names := make([]string, 0, len(assets))
	for name := range assets {
		names = append(names, name)
	}
	// Sorted so two builds of the same site write files in the same order.
	sort.Strings(names)

	for _, name := range names {
		data := assets[name]
		if b.cfg.Build.Minify && hasCSSSuffix(name) {
			data = []byte(minifyCSS(data))
		}
		dest := "assets/" + name
		if err := w.Write(dest, data); err != nil {
			return err
		}
		res.AssetsWritten++
		res.Bytes += int64(len(data))
	}

	if b.cfg.Markup.Highlight.Enabled {
		css, err := b.md.HighlightCSS()
		if err != nil {
			return fmt.Errorf("generate highlight stylesheet: %w", err)
		}
		if css != "" {
			if err := w.Write("assets/css/chroma.css", []byte(css)); err != nil {
				return err
			}
			res.AssetsWritten++
		}
	}
	return nil
}

// writeGenerated emits the feed, sitemap and search index. Each is optional, and
// a failure in one is a warning rather than a build failure: the HTML site is
// already complete and usable without them.
func (b *Builder) writeGenerated(w *publish.Writer, s *site.Site, res *Result) {
	write := func(path string, data []byte) {
		if err := w.Write(path, data); err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("could not write %s: %v", path, err))
			return
		}
		res.AssetsWritten++
		res.Bytes += int64(len(data))
	}

	if b.cfg.Feeds.RSS {
		data, err := generate.RSS(s)
		if err != nil {
			res.Warnings = append(res.Warnings, err.Error())
		} else {
			write(generate.FeedPath, data)
		}
	}
	if b.cfg.Feeds.Sitemap {
		data, err := generate.Sitemap(s)
		if err != nil {
			res.Warnings = append(res.Warnings, err.Error())
		} else {
			write(generate.SitemapPath, data)
		}
	}
	if b.cfg.Feeds.Search {
		data, err := generate.Search(s, 400)
		if err != nil {
			res.Warnings = append(res.Warnings, err.Error())
		} else {
			write(generate.SearchPath, data)
		}
	}
}

// resolveTarget turns a redirect value into a URL. Values that already carry a
// scheme are used verbatim so a page can redirect off-site.
func (b *Builder) resolveTarget(target string) string {
	if isAbsoluteURL(target) {
		return target
	}
	return b.cfg.AbsURL(config.RelURL(target))
}

func hasCSSSuffix(name string) bool {
	return len(name) > 4 && name[len(name)-4:] == ".css"
}
