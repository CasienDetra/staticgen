// Package fsx maps source files to URLs and output paths, and copies static
// assets. It knows nothing about Markdown; it only reasons about paths.
package fsx

import (
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// ContentExtensions are the file extensions treated as Markdown source.
var ContentExtensions = map[string]bool{
	".md":       true,
	".markdown": true,
	".mdown":    true,
}

// SectionIndexNames name a section's landing page rather than a leaf article.
// Both the Hugo convention (_index.md) and the plain convention (index.md) are
// accepted so existing content trees work without renaming.
var SectionIndexNames = map[string]bool{
	"_index.md":       true,
	"_index.markdown": true,
	"index.md":        true,
	"index.markdown":  true,
}

// datePrefix matches a leading YYYY-MM-DD- on a filename, the Jekyll/Hugo
// convention for dating posts from the filename.
var datePrefix = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})-(.+)$`)

// DatePrefix holds a date parsed out of a filename.
type DatePrefix struct {
	Year, Month, Day int
	Rest             string
	Found            bool
}

// SplitDatePrefix extracts a leading YYYY-MM-DD- from a base filename.
func SplitDatePrefix(base string) DatePrefix {
	m := datePrefix.FindStringSubmatch(base)
	if m == nil {
		return DatePrefix{Rest: base}
	}
	return DatePrefix{
		Year:  atoi(m[1]),
		Month: atoi(m[2]),
		Day:   atoi(m[3]),
		Rest:  m[4],
		Found: true,
	}
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}

// IsContent reports whether relPath is a Markdown source file.
func IsContent(relPath string) bool {
	return ContentExtensions[strings.ToLower(filepath.Ext(relPath))]
}

// IsSectionIndex reports whether relPath is a section landing page.
func IsSectionIndex(relPath string) bool {
	return SectionIndexNames[strings.ToLower(filepath.Base(relPath))]
}

// ShouldSkip reports whether a content-relative path is a partial or hidden
// file that must never become a page: anything whose base or any of whose
// directories begins with "_" or ".".
//
// Section index files are the exception — they start with "_" but are pages.
func ShouldSkip(relPath string) bool {
	relPath = filepath.ToSlash(relPath)
	if IsSectionIndex(relPath) {
		return false
	}
	for _, seg := range strings.Split(relPath, "/") {
		if seg == "" {
			continue
		}
		if strings.HasPrefix(seg, "_") || strings.HasPrefix(seg, ".") {
			return true
		}
	}
	return false
}

// Slugify turns arbitrary text into a URL-safe slug. It is used for filenames
// and for frontmatter slugs that were written with spaces or punctuation.
func Slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))

	var b strings.Builder
	b.Grow(len(s))
	prevDash := true // suppress leading separator
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			prevDash = false
		case r == '-' || r == '_' || r == ' ' || r == '/' || r == '.':
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		default:
			// Drop everything else rather than percent-encoding it: URLs with
			// escapes are awkward to type and to link from Markdown.
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// SlugifyPath slugifies each "/"-separated segment of a slug independently.
//
// A frontmatter slug may contain slashes to place a page deeper in the URL tree,
// so running the whole value through Slugify would flatten them into dashes.
func SlugifyPath(s string) string {
	parts := strings.Split(s, "/")
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = Slugify(p); p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, "/")
}

// BaseSlug derives a page slug from a filename, stripping the extension and any
// date prefix.
func BaseSlug(relPath string) string {
	base := filepath.Base(relPath)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	return Slugify(SplitDatePrefix(base).Rest)
}

// URLMapper turns content-relative paths into site URLs and output file paths.
type URLMapper struct {
	// PrettyURLs emits /blog/hello/index.html instead of /blog/hello.html,
	// which lets the site be served without extension-rewriting rules.
	PrettyURLs bool
}

// Section returns the top-level directory of a content-relative path, or ""
// for files at the content root.
func Section(relPath string) string {
	relPath = filepath.ToSlash(relPath)
	dir := path.Dir(relPath)
	if dir == "." || dir == "/" {
		return ""
	}
	return strings.Split(strings.Trim(dir, "/"), "/")[0]
}

// DirSegments returns the directory components of a content-relative path.
func DirSegments(relPath string) []string {
	dir := path.Dir(filepath.ToSlash(relPath))
	if dir == "." || dir == "/" {
		return nil
	}
	return strings.Split(strings.Trim(dir, "/"), "/")
}

// URLPath builds the site URL for a content file.
//
// slug overrides the filename-derived slug when non-empty. Section index files
// map to their directory URL, so content/blog/_index.md becomes /blog/.
func (m URLMapper) URLPath(relPath, slug string) string {
	relPath = filepath.ToSlash(relPath)
	dirs := DirSegments(relPath)

	if IsSectionIndex(relPath) {
		if len(dirs) == 0 {
			return "/"
		}
		return "/" + strings.Join(dirs, "/") + "/"
	}

	if slug == "" {
		slug = BaseSlug(relPath)
	} else {
		slug = SlugifyPath(slug)
	}

	// The error page is a special case: hosts like GitHub Pages and Netlify
	// look for /404.html specifically, so it must not become /404/index.html
	// even when pretty URLs are on. Only a root-level 404 qualifies — a
	// blog/404.md is an ordinary post and must not claim the site's error URL.
	if slug == "404" && len(dirs) == 0 {
		return "/404.html"
	}

	// A slug may itself contain slashes, to let a post control its full path
	// beneath its section directory.
	segments := append(append([]string{}, dirs...), strings.Split(slug, "/")...)
	url := "/" + strings.Join(segments, "/")
	if m.PrettyURLs {
		return url + "/"
	}
	return url + ".html"
}

// OutputPath maps a site URL to a path relative to the output directory.
func (m URLMapper) OutputPath(urlPath string) string {
	urlPath = strings.TrimPrefix(urlPath, "/")
	if urlPath == "" {
		return "index.html"
	}
	if strings.HasSuffix(urlPath, "/") {
		// A pretty URL always resolves to a directory index; serving
		// /blog/hello/ must not depend on rewrite rules.
		return filepath.FromSlash(urlPath + "index.html")
	}
	if path.Ext(urlPath) == "" {
		urlPath += ".html"
	}
	return filepath.FromSlash(urlPath)
}

// StaticDest maps a static-relative source path to its output path. Static
// assets keep their directory structure verbatim so Markdown can link to them
// with a predictable URL.
func StaticDest(relPath string) string {
	return filepath.FromSlash(filepath.ToSlash(relPath))
}
