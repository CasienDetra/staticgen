// Package generate emits the ancillary site artefacts: RSS feed, XML sitemap
// and the client-side search index.
//
// Each generator is independent and reads only the assembled site model, so any
// of them can be disabled or replaced without touching the others.
package generate

import (
	"encoding/xml"
	"strings"
	"time"

	"github.com/casien/staticgen/internal/config"
	"github.com/casien/staticgen/internal/content"
	"github.com/casien/staticgen/internal/site"
)

// Output paths for the generated artefacts, relative to the output directory.
const (
	FeedPath    = "feed.xml"
	SitemapPath = "sitemap.xml"
	SearchPath  = "search.json"
)

// generator identifies staticgen in feeds and sitemaps.
const generator = "staticgen"

type rssDoc struct {
	XMLName   xml.Name   `xml:"rss"`
	Version   string     `xml:"version,attr"`
	AtomNS    string     `xml:"xmlns:atom,attr"`
	ContentNS string     `xml:"xmlns:content,attr"`
	Channel   rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title       string    `xml:"title"`
	Link        string    `xml:"link"`
	Description string    `xml:"description"`
	Language    string    `xml:"language"`
	LastBuild   string    `xml:"lastBuildDate"`
	Generator   string    `xml:"generator"`
	Self        rssSelf   `xml:"atom:link"`
	Items       []rssItem `xml:"item"`
}

type rssSelf struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr"`
}

type rssGUID struct {
	Value       string `xml:",chardata"`
	IsPermaLink string `xml:"isPermaLink,attr"`
}

type rssItem struct {
	Title       string   `xml:"title"`
	Link        string   `xml:"link"`
	GUID        rssGUID  `xml:"guid"`
	Description string   `xml:"description"`
	Encoded     string   `xml:"content:encoded"`
	PubDate     string   `xml:"pubDate"`
	Author      string   `xml:"author,omitempty"`
	Categories  []string `xml:"category,omitempty"`
}

// RSS renders an RSS 2.0 feed of the site's most recent regular pages.
//
// It returns an error when no base_url is configured: RSS requires absolute
// URLs, and emitting relative ones would produce a feed every reader rejects.
func RSS(s *site.Site) ([]byte, error) {
	cfg := s.Config
	if cfg.BaseURL == "" {
		return nil, errNoBaseURL("RSS feed")
	}

	limit := cfg.Feeds.RSSLimit
	posts := s.RegularPages
	if limit > 0 && limit < len(posts) {
		posts = posts[:limit]
	}

	items := make([]rssItem, 0, len(posts))
	var lastBuild time.Time
	for _, p := range posts {
		link := cfg.AbsURL(p.URL)
		if p.Date().After(lastBuild) {
			lastBuild = p.Date()
		}
		items = append(items, rssItem{
			Title:       p.Meta.Title,
			Link:        link,
			GUID:        rssGUID{Value: link, IsPermaLink: "true"},
			Description: firstNonEmpty(plainSummary(p), cfg.Description),
			Encoded:     string(p.BodyHTML),
			PubDate:     rfc822(p.Date()),
			Author:      authorOf(cfg),
			Categories:  p.Meta.Tags,
		})
	}
	if lastBuild.IsZero() {
		lastBuild = s.BuiltAt
	}

	feedURL := cfg.AbsURL("/" + FeedPath)
	doc := rssDoc{
		Version:   "2.0",
		AtomNS:    "http://www.w3.org/2005/Atom",
		ContentNS: "http://purl.org/rss/1.0/modules/content/",
		Channel: rssChannel{
			Title:       cfg.Title,
			Link:        cfg.AbsURL("/"),
			Description: firstNonEmpty(cfg.Description, cfg.Title),
			Language:    cfg.Language,
			LastBuild:   rfc822(lastBuild),
			Generator:   generator,
			Self:        rssSelf{Href: feedURL, Rel: "self", Type: "application/rss+xml"},
			Items:       items,
		},
	}

	return marshalXML(doc)
}

type urlset struct {
	XMLName xml.Name     `xml:"urlset"`
	XMLNS   string       `xml:"xmlns,attr"`
	URLs    []sitemapURL `xml:"url"`
}

type sitemapURL struct {
	Loc        string `xml:"loc"`
	LastMod    string `xml:"lastmod,omitempty"`
	Changefreq string `xml:"changefreq,omitempty"`
}

// Sitemap renders an XML sitemap covering every page in the build.
func Sitemap(s *site.Site) ([]byte, error) {
	cfg := s.Config
	if cfg.BaseURL == "" {
		return nil, errNoBaseURL("sitemap")
	}

	urls := make([]sitemapURL, 0, len(s.Pages))
	for _, p := range s.Pages {
		// The 404 page and redirect stubs resolve to a URL but are not content
		// worth indexing; submitting them wastes crawl budget.
		if p.NoIndex {
			continue
		}
		entry := sitemapURL{
			Loc:        cfg.AbsURL(p.URL),
			Changefreq: changeFreq(p),
		}
		if t := p.LastMod(); !t.IsZero() {
			entry.LastMod = t.UTC().Format("2006-01-02")
		}
		urls = append(urls, entry)
	}

	return marshalXML(urlset{
		XMLNS: "http://www.sitemaps.org/schemas/sitemap/0.9",
		URLs:  urls,
	})
}

func changeFreq(p *content.Page) string {
	switch p.Kind {
	case content.KindHome:
		return "daily"
	case content.KindSection, content.KindTaxonomy, content.KindTerm, content.KindArchive:
		return "weekly"
	default:
		return "monthly"
	}
}

// SearchDoc is one entry in the client-side search index.
type SearchDoc struct {
	Title   string   `json:"title"`
	URL     string   `json:"url"`
	Body    string   `json:"body"`
	Tags    []string `json:"tags,omitempty"`
	Date    string   `json:"date,omitempty"`
	Section string   `json:"section,omitempty"`
}

// SearchIndex is the JSON document the theme's search script fetches.
type SearchIndex struct {
	Generated string      `json:"generated"`
	Count     int         `json:"count"`
	Documents []SearchDoc `json:"documents"`
}

// Search renders the search index.
//
// Bodies are truncated plain text: the index is fetched in full on the first
// keystroke, so shipping whole articles would make search slower to open than
// it is to use. Unlike the feed and sitemap, search works without a base_url
// because the theme fetches it by site-relative path.
func Search(s *site.Site, bodyLimit int) ([]byte, error) {
	if bodyLimit <= 0 {
		bodyLimit = 400
	}

	docs := make([]SearchDoc, 0, len(s.Pages))
	for _, p := range s.Pages {
		if p.NoIndex {
			continue
		}
		doc := SearchDoc{
			Title:   p.Meta.Title,
			URL:     p.URL,
			Body:    clip(p.PlainText, bodyLimit),
			Tags:    p.Meta.Tags,
			Section: p.Section,
		}
		if !p.Date().IsZero() {
			doc.Date = p.Date().UTC().Format("2006-01-02")
		}
		docs = append(docs, doc)
	}

	idx := SearchIndex{
		Generated: s.BuiltAt.UTC().Format(time.RFC3339),
		Count:     len(docs),
		Documents: docs,
	}
	return marshalJSON(idx)
}

// clip truncates to n runes on a word boundary.
func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	cut := r[:n]
	// Back off to the last space so the excerpt does not end mid-word.
	if i := strings.LastIndex(string(cut), " "); i > n/2 {
		cut = cut[:i]
	}
	return strings.TrimSpace(string(cut)) + "…"
}

func plainSummary(p *content.Page) string {
	return clip(p.PlainText, 300)
}

func rfc822(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC1123Z)
}

func authorOf(cfg *config.Site) string {
	if cfg.Author.Email == "" {
		return cfg.Author.Name
	}
	// RSS 2.0 wants "email (name)" rather than the Atom-style element.
	if cfg.Author.Name == "" {
		return cfg.Author.Email
	}
	return cfg.Author.Email + " (" + cfg.Author.Name + ")"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
