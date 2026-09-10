package cli

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/casien/staticgen/internal/config"
	"github.com/casien/staticgen/internal/content"
	"github.com/casien/staticgen/internal/pipeline"
)

// severity ranks a reported problem.
type severity int

const (
	sevError severity = iota
	sevWarning
)

func (s severity) String() string {
	if s == sevError {
		return "error"
	}
	return "warning"
}

// issue is one reported problem.
type issue struct {
	severity severity
	// page is the site URL the problem was found on.
	page string
	// source is the originating content file, empty for generated pages.
	source  string
	message string
}

// maxReported bounds output so a site with a systemic problem — a mistyped base
// path, say — does not flood the terminal.
const maxReported = 200

// linkAttrRe finds href and src attribute values in rendered HTML. Both quote
// styles are handled because raw HTML in Markdown may use either.
var linkAttrRe = regexp.MustCompile(`(?i)\b(?:href|src)\s*=\s*(?:"([^"]*)"|'([^']*)')`)

// idAttrRe finds id attributes, the targets of in-page anchors.
var idAttrRe = regexp.MustCompile(`(?i)\bid\s*=\s*(?:"([^"]*)"|'([^']*)')`)

// externalSchemes are link targets that leave the site and so cannot be checked.
var externalSchemes = []string{
	"http://", "https://", "//", "mailto:", "tel:", "data:",
	"javascript:", "ftp:", "sms:", "irc:",
}

func newCheckCmd(g *globalFlags) *cobra.Command {
	var strict bool

	cmd := &cobra.Command{
		Use:   "check",
		Short: "Report broken links, missing images and content problems",
		Long: `Check renders the site in memory without writing anything, then reports:

  error    an internal link or image that resolves to nothing the build produces
  error    an in-page anchor that matches no element id
  error    a content file that failed to load
  warning  a link pointing at a .md source file rather than a URL
  warning  an article with no content, or with no description for feeds and meta

Draft and future-dated pages are included, so problems surface before a page is
published. Exits non-zero when errors are found, or when --strict is set and any
warning is found, so it can gate a deploy.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Drafts and future posts are checked deliberately: the point is to
			// catch problems before publication.
			yes := true
			cfg, err := g.load(config.Overrides{Drafts: &yes, Future: &yes})
			if err != nil {
				return err
			}

			builder, err := pipeline.New(cfg, pipeline.Options{})
			if err != nil {
				return err
			}

			insp, err := builder.Inspect(cmd.Context())
			if insp == nil {
				return err
			}
			return reportIssues(g, runChecks(builder, insp), strict)
		},
	}

	cmd.Flags().BoolVar(&strict, "strict", false, "exit non-zero on warnings as well as errors")
	return cmd
}

// runChecks performs every check and returns the issues, most severe first.
func runChecks(builder *pipeline.Builder, insp *pipeline.Inspection) []issue {
	known := builder.KnownPaths()
	for _, p := range insp.Site.Pages {
		addKnown(known, p.URL)
		for _, alias := range p.Meta.Aliases {
			addKnown(known, alias)
		}
	}

	var issues []issue
	if insp.LoadErr != nil {
		issues = append(issues, issue{
			severity: sevError,
			message:  fmt.Sprintf("content failed to load: %v", insp.LoadErr),
		})
	}

	for _, rp := range insp.Pages {
		ids := documentIDs(rp.HTML)
		issues = append(issues, checkLinks(rp.Page, rp.HTML, known, ids)...)
		issues = append(issues, checkContent(rp.Page)...)
	}

	sortIssues(issues)
	return issues
}

func addKnown(known map[string]bool, p string) {
	if p == "" {
		return
	}
	clean := path.Clean(p)
	known[clean] = true
	known[strings.TrimSuffix(clean, "/")] = true
}

// checkLinks inspects every href and src in one rendered document.
func checkLinks(p *content.Page, doc []byte, known, ids map[string]bool) []issue {
	var out []issue
	// The theme repeats the same asset links on every page; dedupe per page so
	// one bad stylesheet link is reported once rather than per occurrence.
	seen := map[string]bool{}

	for _, m := range linkAttrRe.FindAllStringSubmatch(string(doc), -1) {
		raw := m[1]
		if raw == "" {
			raw = m[2]
		}
		raw = strings.TrimSpace(raw)
		if raw == "" || seen[raw] {
			continue
		}
		seen[raw] = true

		if iss, ok := checkLink(raw, p, known, ids); ok {
			out = append(out, iss)
		}
	}
	return out
}

func checkLink(raw string, p *content.Page, known, ids map[string]bool) (issue, bool) {
	lower := strings.ToLower(raw)
	for _, prefix := range externalSchemes {
		if strings.HasPrefix(lower, prefix) {
			return issue{}, false
		}
	}

	target, fragment := raw, ""
	if i := strings.Index(raw, "#"); i >= 0 {
		target, fragment = raw[:i], raw[i+1:]
	}
	if i := strings.Index(target, "?"); i >= 0 {
		target = target[:i]
	}
	target = strings.TrimSpace(target)
	src := pageSource(p)

	// A bare fragment is an in-page anchor.
	if target == "" {
		if fragment != "" && !ids[fragment] {
			return issue{sevError, p.URL, src,
				fmt.Sprintf("anchor #%s matches no element id on this page", fragment)}, true
		}
		return issue{}, false
	}

	resolved := resolveLink(p.URL, target)
	if !isKnown(resolved, known) {
		if ext := strings.ToLower(path.Ext(resolved)); ext == ".md" || ext == ".markdown" {
			return issue{sevWarning, p.URL, src,
				fmt.Sprintf("link %q points at a Markdown source file; link the page URL instead", raw)}, true
		}
		return issue{sevError, p.URL, src,
			fmt.Sprintf("broken link %q: nothing is generated at %s", raw, resolved)}, true
	}

	// A fragment on a link back to this same page must also resolve.
	if fragment != "" && samePage(resolved, p.URL) && !ids[fragment] {
		return issue{sevError, p.URL, src,
			fmt.Sprintf("anchor %q matches no element id on this page", raw)}, true
	}
	return issue{}, false
}

// checkContent reports authoring problems that do not break the build but do
// degrade the published site.
func checkContent(p *content.Page) []issue {
	if p.Kind != content.KindPage {
		return nil
	}
	var out []issue
	src := pageSource(p)

	if p.WordCount == 0 {
		out = append(out, issue{sevWarning, p.URL, src, "page has no content"})
	}
	if strings.TrimSpace(p.Meta.Description) == "" {
		out = append(out, issue{sevWarning, p.URL, src,
			"no description; the meta description and feed summary will be empty"})
	}
	return out
}

// resolveLink resolves a possibly relative link against the page's URL, the way
// a browser would.
func resolveLink(pageURL, link string) string {
	if strings.HasPrefix(link, "/") {
		return path.Clean(link)
	}
	base := pageURL
	if strings.HasSuffix(base, "/") {
		base = strings.TrimSuffix(base, "/")
	} else {
		base = path.Dir(base)
	}
	if base == "" {
		base = "/"
	}
	return path.Clean(base + "/" + link)
}

// isKnown reports whether a resolved path will exist in the output. Directory
// URLs are accepted with or without the trailing slash, since authors write both.
func isKnown(resolved string, known map[string]bool) bool {
	return known[resolved] ||
		known[resolved+"/"] ||
		known[strings.TrimSuffix(resolved, "/")]
}

func samePage(resolved, pageURL string) bool {
	trim := func(s string) string { return strings.TrimSuffix(path.Clean(s), "/") }
	a, b := trim(resolved), trim(pageURL)
	if a == b {
		return true
	}
	// The site root cleans to "/" and trims to "", which must still match "/".
	return (a == "" && b == "") || a+"/" == b || b+"/" == a
}

// documentIDs collects every id attribute in a rendered document.
//
// Anchors can target any element, not just headings — the theme's skip link
// points at <main id="main">, which is not a heading at all. Collecting from the
// rendered HTML also covers ids that come from the layout or, with markup.unsafe
// enabled, from raw HTML in the Markdown itself.
func documentIDs(doc []byte) map[string]bool {
	ids := map[string]bool{}
	for _, m := range idAttrRe.FindAllStringSubmatch(string(doc), -1) {
		id := m[1]
		if id == "" {
			id = m[2]
		}
		if id = strings.TrimSpace(id); id != "" {
			ids[id] = true
		}
	}
	return ids
}

func pageSource(p *content.Page) string {
	if p.SourcePath != "" {
		return p.SourcePath
	}
	return "(generated " + string(p.Kind) + " page)"
}

func sortIssues(issues []issue) {
	sort.SliceStable(issues, func(i, j int) bool {
		a, b := issues[i], issues[j]
		if a.severity != b.severity {
			return a.severity < b.severity
		}
		if a.page != b.page {
			return a.page < b.page
		}
		if a.source != b.source {
			return a.source < b.source
		}
		return a.message < b.message
	})
}

// reportIssues prints the findings and returns an error when the run should fail.
func reportIssues(g *globalFlags, issues []issue, strict bool) error {
	if len(issues) == 0 {
		g.printf("no problems found\n")
		return nil
	}

	var errs, warns int
	shown := issues
	if len(shown) > maxReported {
		shown = shown[:maxReported]
	}
	for _, is := range shown {
		where := is.page
		if where == "" && is.source == "" {
			g.printf("%-7s %s\n", is.severity, is.message)
		} else {
			g.printf("%-7s %s (%s): %s\n", is.severity, orDash(where), orDash(is.source), is.message)
		}
	}
	if len(issues) > maxReported {
		g.printf("… and %d more\n", len(issues)-maxReported)
	}

	for _, is := range issues {
		if is.severity == sevError {
			errs++
		} else {
			warns++
		}
	}
	g.printf("\n%d %s, %d %s\n", errs, pluralWord(errs, "error"), warns, pluralWord(warns, "warning"))

	if errs > 0 {
		return errf("check failed with %d %s", errs, pluralWord(errs, "error"))
	}
	if strict && warns > 0 {
		return errf("check failed with %d %s (--strict)", warns, pluralWord(warns, "warning"))
	}
	return nil
}

func pluralWord(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
