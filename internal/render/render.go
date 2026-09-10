// Package render turns assembled pages into complete HTML documents.
//
// A default theme is embedded in the binary so a bare content directory builds
// a usable site. Every theme file can be overridden by placing a file at the
// same relative path under the site's templates directory, which is how a user
// customises output without forking the tool.
package render

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/casien/staticgen/internal/config"
	"github.com/casien/staticgen/internal/content"
	"github.com/casien/staticgen/internal/fsx"
	"github.com/casien/staticgen/internal/site"
)

//go:embed theme
var themeFS embed.FS

const (
	embeddedTemplatesDir = "theme/templates"
	embeddedAssetsDir    = "theme/assets"

	baseTemplate = "base.html"
	// mainBlock is the block each layout defines; base.html invokes it. Every
	// layout gets its own template set, so there is never more than one
	// definition of "main" in a set.
	mainBlock = "main"
)

// Options configures an Engine.
type Options struct {
	Config *config.Site
	// TemplatesDir overrides embedded theme files by relative path. It may be
	// empty or nonexistent, in which case the embedded theme is used as-is.
	TemplatesDir string
	// LiveReload injects the dev-server reload script. Never enable in a
	// published build.
	LiveReload bool
	// AssetVersion is appended to asset URLs for cache busting.
	AssetVersion string
}

// Engine renders pages using a resolved set of templates.
type Engine struct {
	cfg        *config.Site
	sets       map[string]*template.Template
	layouts    map[string]string
	assets     map[string][]byte
	liveReload bool
	version    string
}

// PageData is the value every template receives.
type PageData struct {
	Config     *config.Site
	Site       *site.Site
	Page       *content.Page
	Nav        []site.NavEntry
	LiveReload bool
	// DocumentTitle is the full <title>, already suffixed with the site name.
	DocumentTitle string
	// AssetURL resolves a theme asset path to a versioned site URL.
	assetURL func(string) string
}

// New resolves the theme and compiles one template set per layout.
//
// Template parse errors surface here rather than mid-build, so a broken
// override fails fast with the offending file named.
func New(opts Options) (*Engine, error) {
	if opts.Config == nil {
		return nil, fmt.Errorf("render: config is required")
	}

	sources, err := loadTemplates(opts.TemplatesDir)
	if err != nil {
		return nil, err
	}
	assets, err := loadAssets(opts.TemplatesDir)
	if err != nil {
		return nil, err
	}

	version := opts.AssetVersion
	if version == "" {
		version = time.Now().UTC().Format("20060102150405")
	}
	e := &Engine{
		cfg:        opts.Config,
		layouts:    sources,
		assets:     assets,
		liveReload: opts.LiveReload,
		version:    version,
	}

	funcs := e.funcMap()

	// Shared files: the shell plus every partial. Layouts are parsed one at a
	// time on top of a clone so each defines "main" without conflict.
	baseNames := []string{baseTemplate}
	for name := range sources {
		if strings.HasPrefix(name, "partials/") {
			baseNames = append(baseNames, name)
		}
	}
	sort.Strings(baseNames)

	base, err := parseSet(sources, funcs, baseNames)
	if err != nil {
		return nil, err
	}

	e.sets = map[string]*template.Template{}
	for name, body := range sources {
		if name == baseTemplate || strings.HasPrefix(name, "partials/") {
			continue
		}
		set, err := base.Clone()
		if err != nil {
			return nil, fmt.Errorf("clone base for %s: %w", name, err)
		}
		if _, err := set.New(name).Parse(body); err != nil {
			return nil, fmt.Errorf("parse template %s: %w", name, err)
		}
		// Fail on an override that forgot to define the main block: without
		// this the page would silently render as an empty shell.
		if set.Lookup(mainBlock) == nil {
			return nil, fmt.Errorf("template %s does not define the %q block", name, mainBlock)
		}
		e.sets[name] = set
	}

	if _, ok := e.sets["page.html"]; !ok {
		return nil, fmt.Errorf("theme is missing page.html, the fallback layout")
	}
	return e, nil
}

// parseSet compiles the named templates into one set.
func parseSet(sources map[string]string, funcs template.FuncMap, names []string) (*template.Template, error) {
	root := template.New("").Funcs(funcs)
	for _, name := range names {
		body, ok := sources[name]
		if !ok {
			return nil, fmt.Errorf("theme is missing required template %s", name)
		}
		if _, err := root.New(name).Parse(body); err != nil {
			return nil, fmt.Errorf("parse template %s: %w", name, err)
		}
	}
	return root, nil
}

// loadTemplates merges embedded theme templates with user overrides, keyed by
// path relative to the templates directory.
func loadTemplates(userDir string) (map[string]string, error) {
	out := map[string]string{}

	err := fs.WalkDir(themeFS, embeddedTemplatesDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".html") {
			return nil
		}
		rel, relErr := filepath.Rel(embeddedTemplatesDir, filepath.FromSlash(p))
		if relErr != nil {
			return relErr
		}
		b, readErr := themeFS.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("load embedded theme: %w", err)
	}

	// User files win by relative path, so dropping templates/page.html into the
	// site replaces the article layout and nothing else.
	if err := walkUserTemplates(userDir, func(rel string, data []byte) {
		out[rel] = string(data)
	}); err != nil {
		return nil, err
	}
	return out, nil
}

func walkUserTemplates(dir string, fn func(rel string, data []byte)) error {
	if dir == "" {
		return nil
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		// A missing override directory is normal: most sites use the built-in theme.
		return nil
	}
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := filepath.Base(p)
			// "assets" under the templates dir holds theme static files, not
			// templates, and is handled by loadAssets.
			if p != dir && (strings.HasPrefix(base, ".") || base == "assets") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".html") {
			return nil
		}
		rel, relErr := filepath.Rel(dir, p)
		if relErr != nil {
			return relErr
		}
		b, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		fn(filepath.ToSlash(rel), b)
		return nil
	})
}

// loadAssets merges embedded theme assets with user overrides, keyed by path
// relative to the assets directory.
func loadAssets(userDir string) (map[string][]byte, error) {
	out := map[string][]byte{}

	err := fs.WalkDir(themeFS, embeddedAssetsDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(embeddedAssetsDir, filepath.FromSlash(p))
		if relErr != nil {
			return relErr
		}
		b, readErr := themeFS.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		out[filepath.ToSlash(rel)] = b
		return nil
	})
	if err != nil && !isMissingEmbed(err) {
		return nil, fmt.Errorf("load embedded assets: %w", err)
	}

	if userDir != "" {
		userAssets := filepath.Join(userDir, "assets")
		if st, err := os.Stat(userAssets); err == nil && st.IsDir() {
			walkErr := filepath.WalkDir(userAssets, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					return nil
				}
				rel, relErr := filepath.Rel(userAssets, p)
				if relErr != nil {
					return relErr
				}
				b, readErr := os.ReadFile(p)
				if readErr != nil {
					return readErr
				}
				out[filepath.ToSlash(rel)] = b
				return nil
			})
			if walkErr != nil {
				return nil, fmt.Errorf("load assets from %s: %w", userAssets, walkErr)
			}
		}
	}
	return out, nil
}

func isMissingEmbed(err error) bool {
	return strings.Contains(err.Error(), "no such file or directory")
}

// Assets returns the theme's static files to be written under /assets/.
func (e *Engine) Assets() map[string][]byte { return e.assets }

// Layouts lists the resolved template names, for diagnostics and the check command.
func (e *Engine) Layouts() []string {
	out := make([]string, 0, len(e.sets))
	for name := range e.sets {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// layoutFor picks the template for a page: an explicit frontmatter layout, else
// the layout matching the page kind, else the article layout.
func (e *Engine) layoutFor(p *content.Page) string {
	if p.Meta.Layout != "" {
		name := p.Meta.Layout
		if !strings.HasSuffix(name, ".html") {
			name += ".html"
		}
		if _, ok := e.sets[name]; ok {
			return name
		}
	}
	byKind := map[content.Kind]string{
		content.KindHome:     "home.html",
		content.KindSection:  "section.html",
		content.KindTaxonomy: "taxonomy.html",
		content.KindTerm:     "term.html",
		content.KindArchive:  "archive.html",
		content.KindPage:     "page.html",
	}
	if name, ok := byKind[p.Kind]; ok {
		if _, exists := e.sets[name]; exists {
			return name
		}
	}
	return "page.html"
}

// RenderPage renders one page into a complete HTML document.
func (e *Engine) RenderPage(p *content.Page, s *site.Site) ([]byte, error) {
	name := e.layoutFor(p)
	set, ok := e.sets[name]
	if !ok {
		return nil, fmt.Errorf("no template set for %s", name)
	}

	data := PageData{
		Config:        e.cfg,
		Site:          s,
		Page:          p,
		Nav:           s.Nav(p),
		LiveReload:    e.liveReload,
		DocumentTitle: documentTitle(p, e.cfg),
	}
	data.assetURL = func(asset string) string {
		return e.AssetURL(asset)
	}

	var buf bytes.Buffer
	if err := set.ExecuteTemplate(&buf, baseTemplate, data); err != nil {
		return nil, fmt.Errorf("render %s with %s: %w", p.URL, name, err)
	}
	return buf.Bytes(), nil
}

// AssetURL resolves a theme asset path to a versioned, site-relative URL.
func (e *Engine) AssetURL(asset string) string {
	asset = strings.TrimPrefix(asset, "/")
	url := "/assets/" + asset
	if e.version != "" {
		url += "?v=" + e.version
	}
	return url
}

func documentTitle(p *content.Page, cfg *config.Site) string {
	title := strings.TrimSpace(p.Meta.Title)
	if title == "" || title == cfg.Title || p.Kind == content.KindHome {
		if cfg.Title == "" {
			return title
		}
		return cfg.Title
	}
	if cfg.Title == "" {
		return title
	}
	return title + " · " + cfg.Title
}

// funcMap exposes helpers to templates. Each is deliberately small and total:
// a template function that can panic would take down a build over one page.
func (e *Engine) funcMap() template.FuncMap {
	cfg := e.cfg
	return template.FuncMap{
		"absURL":     func(p string) string { return cfg.AbsURL(p) },
		"relURL":     func(p string) string { return config.RelURL(p) },
		"asset":      func(p string) string { return e.AssetURL(p) },
		"now":        func() time.Time { return time.Now() },
		"hasPrefix":  strings.HasPrefix,
		"hasSuffix":  strings.HasSuffix,
		"trimPrefix": strings.TrimPrefix,
		"trimSuffix": strings.TrimSuffix,
		"lower":      strings.ToLower,
		"upper":      strings.ToUpper,
		"replace":    func(old, new, s string) string { return strings.ReplaceAll(s, old, new) },

		// dateFormat renders a time, returning "" for the zero time so templates
		// need no guard around undated pages.
		"dateFormat": func(layout string, t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return t.Format(layout)
		},
		"dateISO": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return t.Format(time.RFC3339)
		},

		"truncate": func(n int, s string) string {
			r := []rune(s)
			if len(r) <= n {
				return s
			}
			return string(r[:n]) + "…"
		},

		"readingTime": func(words int) int {
			if words <= 0 {
				return 0
			}
			return (words + 199) / 200
		},

		"pluralize": func(n int, one, many string) string {
			if n == 1 {
				return one
			}
			return many
		},

		"add": func(a, b int) int { return a + b },
		"sub": func(a, b int) int { return a - b },
		"seq": func(n int) []int {
			if n <= 0 {
				return nil
			}
			out := make([]int, n)
			for i := range out {
				out[i] = i + 1
			}
			return out
		},

		// joinPath builds URLs from segments without doubling slashes.
		"joinPath": func(parts ...string) string {
			cleaned := make([]string, 0, len(parts))
			for _, p := range parts {
				p = strings.Trim(p, "/")
				if p != "" {
					cleaned = append(cleaned, p)
				}
			}
			return "/" + path.Join(cleaned...)
		},

		"safeHTML": func(s string) template.HTML { return template.HTML(s) },

		// slugify and the term URL helpers keep taxonomy links correct in
		// templates without exposing the fsx package to theme authors.
		"slugify": fsx.Slugify,
		"tagURL": func(name string) string {
			return "/" + "tags/" + fsx.Slugify(name) + "/"
		},
		"categoryURL": func(name string) string {
			return "/" + "categories/" + fsx.Slugify(name) + "/"
		},
	}
}
