// Package config loads and validates site configuration.
//
// Precedence, lowest to highest:
//
//	built-in defaults  <  config file (site.yaml)  <  CLI overrides
//
// CLI flags are applied through Overrides rather than by mutating flags into
// the struct directly, so the loader stays a pure function of the file on disk.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// configFileNames are probed in order when no explicit --config is given.
var configFileNames = []string{"site.yaml", "site.yml", "config.yaml", "staticgen.yaml"}

// Site is the fully resolved configuration for one build.
type Site struct {
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
	BaseURL     string `yaml:"base_url"`
	Language    string `yaml:"language"`
	Copyright   string `yaml:"copyright"`
	Author      Author `yaml:"author"`

	Dirs Dirs `yaml:"dirs"`

	Menu   []MenuItem     `yaml:"menu"`
	Build  Build          `yaml:"build"`
	Markup Markup         `yaml:"markup"`
	Feeds  Feeds          `yaml:"feeds"`
	Serve  Serve          `yaml:"serve"`
	Params map[string]any `yaml:"params"`

	// Root is the directory the config was loaded from; all Dirs are relative to it.
	Root string `yaml:"-"`
	// Path is the config file that was read, or "" when defaults were used.
	Path string `yaml:"-"`
}

// Author describes the site or feed author.
type Author struct {
	Name  string `yaml:"name"`
	Email string `yaml:"email"`
	URL   string `yaml:"url"`
}

// Dirs holds directory names relative to Site.Root.
type Dirs struct {
	Content   string `yaml:"content"`
	Static    string `yaml:"static"`
	Templates string `yaml:"templates"`
	Output    string `yaml:"output"`
}

// MenuItem is one entry in the site navigation.
type MenuItem struct {
	Name       string `yaml:"name"`
	URL        string `yaml:"url"`
	Weight     int    `yaml:"weight"`
	Identifier string `yaml:"identifier"`
}

// Build controls which pages are emitted and how URLs are shaped.
type Build struct {
	Drafts      bool `yaml:"drafts"`
	Future      bool `yaml:"future"`
	Minify      bool `yaml:"minify"`
	PrettyURLs  bool `yaml:"pretty_urls"`
	CleanOutput bool `yaml:"clean_output"`
	// SummaryLength is the word count used for auto-generated excerpts.
	SummaryLength int `yaml:"summary_length"`
	// Paginate is the number of posts per index page; 0 disables pagination.
	Paginate int `yaml:"paginate"`
	// TOC renders a table of contents on articles with two or more headings.
	// Individual pages override it with a toc frontmatter key.
	TOC bool `yaml:"toc"`
}

// Markup controls Markdown rendering.
type Markup struct {
	// Unsafe permits raw HTML embedded in Markdown. Off by default: untrusted
	// Markdown would otherwise be able to inject script tags.
	Unsafe      bool      `yaml:"unsafe"`
	Typographer bool      `yaml:"typographer"`
	Highlight   Highlight `yaml:"highlight"`
}

// Highlight configures chroma-based syntax highlighting.
type Highlight struct {
	Enabled       bool   `yaml:"enabled"`
	Style         string `yaml:"style"`
	LineNumbers   bool   `yaml:"line_numbers"`
	GuessLanguage bool   `yaml:"guess_language"`
}

// Feeds toggles the ancillary emitters.
type Feeds struct {
	RSS      bool `yaml:"rss"`
	RSSLimit int  `yaml:"rss_limit"`
	Sitemap  bool `yaml:"sitemap"`
	Search   bool `yaml:"search"`
}

// Serve configures the dev server.
type Serve struct {
	Host       string `yaml:"host"`
	Port       int    `yaml:"port"`
	LiveReload bool   `yaml:"live_reload"`
	// DebounceMs coalesces editor save bursts into one rebuild.
	DebounceMs int `yaml:"debounce_ms"`
}

// Default returns the baseline configuration used when a field is unset.
func Default() *Site {
	return &Site{
		Title:    "My Site",
		Language: "en-us",
		Dirs: Dirs{
			Content:   "content",
			Static:    "static",
			Templates: "templates",
			Output:    "public",
		},
		Build: Build{
			PrettyURLs:    true,
			CleanOutput:   true,
			SummaryLength: 70,
			Paginate:      0,
			TOC:           true,
		},
		Markup: Markup{
			Typographer: true,
			Highlight: Highlight{
				Enabled:       true,
				Style:         "github",
				LineNumbers:   false,
				GuessLanguage: true,
			},
		},
		Feeds: Feeds{
			RSS:      true,
			RSSLimit: 20,
			Sitemap:  true,
			Search:   true,
		},
		Serve: Serve{
			Host:       "127.0.0.1",
			Port:       1313,
			LiveReload: true,
			DebounceMs: 100,
		},
		Params: map[string]any{},
	}
}

// Load reads configuration from path. When path is empty the standard config
// file names are probed in Root. A missing file is not an error: the defaults
// are returned with Root recorded, so `staticgen build` works in a bare
// directory of Markdown files.
func Load(root, path string) (*Site, error) {
	site := Default()

	if root == "" {
		root = "."
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve root %q: %w", root, err)
	}
	site.Root = absRoot

	resolved, err := resolveConfigPath(absRoot, path)
	if err != nil {
		return nil, err
	}

	if resolved != "" {
		data, err := os.ReadFile(resolved)
		if err != nil {
			return nil, fmt.Errorf("read config %s: %w", resolved, err)
		}
		// Decoding onto the defaults means omitted keys keep their default
		// value instead of being zeroed.
		dec := yaml.NewDecoder(strings.NewReader(string(data)))
		dec.KnownFields(false)
		if err := dec.Decode(site); err != nil && !errors.Is(err, os.ErrClosed) {
			if !isEOF(err) {
				return nil, fmt.Errorf("parse config %s: %w", resolved, err)
			}
		}
		site.Path = resolved
	}

	site.normalize()
	if err := site.Validate(); err != nil {
		return nil, err
	}
	return site, nil
}

// isEOF reports whether err is the empty-input decode error, which is benign
// for a config file containing only comments.
func isEOF(err error) bool {
	return err != nil && strings.Contains(err.Error(), "EOF")
}

// resolveConfigPath returns the config file to read, or "" for none.
// An explicitly requested path that does not exist is an error; a probed
// path that does not exist is not.
func resolveConfigPath(root, path string) (string, error) {
	if path != "" {
		p := path
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		if _, err := os.Stat(p); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return "", fmt.Errorf("config file not found: %s", p)
			}
			return "", fmt.Errorf("stat config %s: %w", p, err)
		}
		return p, nil
	}

	for _, name := range configFileNames {
		p := filepath.Join(root, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", nil
}

// normalize canonicalises values so the rest of the program can trust them.
func (s *Site) normalize() {
	s.Title = strings.TrimSpace(s.Title)
	if s.Title == "" {
		s.Title = "My Site"
	}
	if s.Language == "" {
		s.Language = "en-us"
	}
	s.BaseURL = strings.TrimRight(strings.TrimSpace(s.BaseURL), "/")

	for dir, def := range map[*string]string{
		&s.Dirs.Content:   "content",
		&s.Dirs.Static:    "static",
		&s.Dirs.Templates: "templates",
		&s.Dirs.Output:    "public",
	} {
		*dir = strings.TrimSpace(*dir)
		if *dir == "" {
			*dir = def
		}
		*dir = filepath.Clean(*dir)
	}

	if s.Build.SummaryLength <= 0 {
		s.Build.SummaryLength = 70
	}
	if s.Build.Paginate < 0 {
		s.Build.Paginate = 0
	}
	if s.Markup.Highlight.Style == "" {
		s.Markup.Highlight.Style = "github"
	}
	if s.Feeds.RSSLimit <= 0 {
		s.Feeds.RSSLimit = 20
	}
	if s.Serve.Port <= 0 {
		s.Serve.Port = 1313
	}
	if s.Serve.DebounceMs < 0 {
		s.Serve.DebounceMs = 0
	}
	if s.Params == nil {
		s.Params = map[string]any{}
	}

	// A feed and a sitemap carry absolute URLs, so without base_url they cannot
	// be produced. Turn them off rather than failing the build: base_url is
	// legitimately unset while a site is being written locally, and the search
	// index still works because the theme fetches it by site-relative path.
	if s.BaseURL == "" {
		s.Feeds.RSS = false
		s.Feeds.Sitemap = false
	}

	// Stable menu order regardless of how the file was written.
	for i := range s.Menu {
		if s.Menu[i].URL == "" {
			s.Menu[i].URL = "/"
		}
	}
	sortMenu(s.Menu)
}

// Validate reports configuration that cannot produce a correct build.
func (s *Site) Validate() error {
	if s.BaseURL != "" {
		u, err := url.Parse(s.BaseURL)
		if err != nil {
			return fmt.Errorf("invalid base_url %q: %w", s.BaseURL, err)
		}
		if u.Host == "" {
			return fmt.Errorf("base_url %q must include a scheme and host", s.BaseURL)
		}
	}
	for _, dir := range []string{s.Dirs.Content, s.Dirs.Static, s.Dirs.Templates, s.Dirs.Output} {
		if strings.TrimSpace(dir) == "" {
			return errors.New("dirs entries must not be empty")
		}
	}
	return nil
}

// resolveDir returns dir as an absolute path. A relative dir is resolved against
// the site root; an absolute dir is used verbatim, which lets --output point at a
// build directory anywhere on disk (a CI artefact path, for instance).
func resolveDir(root, dir string) string {
	if filepath.IsAbs(dir) {
		return filepath.Clean(dir)
	}
	return filepath.Join(root, dir)
}

// AbsURL joins a site-relative path onto BaseURL.
// Already-absolute URLs (those with a scheme or a protocol-relative "//" prefix)
// are returned verbatim so callers can safely pass external URLs without
// double-encoding them.
func (s *Site) AbsURL(path string) string {
	if isAbsoluteURL(path) {
		return path
	}
	if s.BaseURL == "" {
		return RelURL(path)
	}
	return s.BaseURL + RelURL(path)
}

// RelURL ensures a path is rooted and single-slashed.
// Already-absolute URLs are returned verbatim.
func RelURL(path string) string {
	if isAbsoluteURL(path) {
		return path
	}
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
}

// isAbsoluteURL reports whether s already carries a scheme or is
// protocol-relative, meaning it should never be rewritten by RelURL/AbsURL.
func isAbsoluteURL(s string) bool {
	for _, prefix := range []string{"http://", "https://", "mailto:", "//"} {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

// ContentPath returns the absolute content directory.
func (s *Site) ContentPath() string { return resolveDir(s.Root, s.Dirs.Content) }

// StaticPath returns the absolute static asset directory.
func (s *Site) StaticPath() string { return resolveDir(s.Root, s.Dirs.Static) }

// TemplatesPath returns the absolute user template directory.
func (s *Site) TemplatesPath() string { return resolveDir(s.Root, s.Dirs.Templates) }

// OutputPath returns the absolute build output directory.
func (s *Site) OutputPath() string { return resolveDir(s.Root, s.Dirs.Output) }

// Overrides carries CLI flag values that take precedence over the config file.
// Pointer fields distinguish "flag not set" from "flag set to zero value".
type Overrides struct {
	Title       string
	BaseURL     string
	OutputDir   string
	ContentDir  string
	Theme       string
	Drafts      *bool
	Future      *bool
	Minify      *bool
	PrettyURLs  *bool
	CleanOutput *bool
	Unsafe      *bool
	Host        string
	Port        int
	LiveReload  *bool
}

// Apply layers CLI overrides on top of the loaded configuration.
func (s *Site) Apply(o Overrides) {
	if o.Title != "" {
		s.Title = o.Title
	}
	if o.BaseURL != "" {
		s.BaseURL = strings.TrimRight(o.BaseURL, "/")
	}
	if o.OutputDir != "" {
		s.Dirs.Output = filepath.Clean(o.OutputDir)
	}
	if o.ContentDir != "" {
		s.Dirs.Content = filepath.Clean(o.ContentDir)
	}
	setBool := func(dst *bool, src *bool) {
		if src != nil {
			*dst = *src
		}
	}
	setBool(&s.Build.Drafts, o.Drafts)
	setBool(&s.Build.Future, o.Future)
	setBool(&s.Build.Minify, o.Minify)
	setBool(&s.Build.PrettyURLs, o.PrettyURLs)
	setBool(&s.Build.CleanOutput, o.CleanOutput)
	setBool(&s.Markup.Unsafe, o.Unsafe)
	setBool(&s.Serve.LiveReload, o.LiveReload)
	if o.Host != "" {
		s.Serve.Host = o.Host
	}
	if o.Port != 0 {
		s.Serve.Port = o.Port
	}

	s.normalize()
}
