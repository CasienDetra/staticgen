// Package content turns Markdown files on disk into typed Page values.
package content

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Meta is a page's YAML frontmatter.
//
// Extra captures every key that has no dedicated field, so a theme can read
// site-specific values without this package having to know about them. It works
// because yaml.v3 supports an inline map.
type Meta struct {
	Title       string    `yaml:"title"`
	Description string    `yaml:"description"`
	Summary     string    `yaml:"summary"`
	Slug        string    `yaml:"slug"`
	Layout      string    `yaml:"layout"`
	Date        time.Time `yaml:"date"`
	LastMod     time.Time `yaml:"lastmod"`
	Expires     time.Time `yaml:"expires"`
	Draft       bool      `yaml:"draft"`
	Featured    bool      `yaml:"featured"`
	Weight      int       `yaml:"weight"`
	Tags        []string  `yaml:"tags"`
	Categories  []string  `yaml:"categories"`
	Aliases     []string  `yaml:"aliases"`
	Redirect    string    `yaml:"redirect"`
	// TOC opts a single page in or out of a table of contents; nil means
	// "use the site default".
	TOC   *bool          `yaml:"toc"`
	Extra map[string]any `yaml:",inline"`
}

// frontmatterDelims are the opening and closing fences. "..." is accepted as a
// closer because YAML itself treats it as a document end marker.
var (
	openDelim  = []byte("---")
	closeDelim = []byte("---")
	altClose   = []byte("...")
)

// Split separates leading YAML frontmatter from the Markdown body.
//
// ok is false when the source has no frontmatter, in which case body is the
// whole source. A malformed block — an opening fence with no closer — is also
// reported as absent rather than an error, because treating the rest of the
// document as frontmatter would silently delete the page's content.
func Split(src []byte) (front, body []byte, ok bool) {
	if !bytes.HasPrefix(src, openDelim) {
		return nil, src, false
	}
	rest := src[len(openDelim):]
	rest = bytes.TrimPrefix(rest, []byte("\r"))
	if len(rest) == 0 || rest[0] != '\n' {
		// "---" not followed by a newline is a horizontal rule or table, not
		// frontmatter.
		return nil, src, false
	}
	rest = rest[1:]

	offset := 0
	for offset <= len(rest) {
		var line []byte
		nl := bytes.IndexByte(rest[offset:], '\n')
		if nl < 0 {
			line = rest[offset:]
		} else {
			line = rest[offset : offset+nl]
		}
		trimmed := bytes.TrimRight(line, "\r \t")
		if bytes.Equal(trimmed, closeDelim) || bytes.Equal(trimmed, altClose) {
			front = rest[:offset]
			if nl < 0 {
				body = nil
			} else {
				body = rest[offset+nl+1:]
			}
			return front, body, true
		}
		if nl < 0 {
			break
		}
		offset += nl + 1
	}
	return nil, src, false
}

// ParseMeta decodes frontmatter YAML into a Meta. Empty input yields a zero
// Meta rather than an error, since frontmatter is optional.
func ParseMeta(front []byte) (Meta, error) {
	var m Meta
	if len(bytes.TrimSpace(front)) == 0 {
		return m, nil
	}
	dec := yaml.NewDecoder(bytes.NewReader(front))
	if err := dec.Decode(&m); err != nil {
		if errors.Is(err, io.EOF) {
			return m, nil
		}
		return Meta{}, fmt.Errorf("invalid frontmatter: %w", err)
	}
	m.normalize()
	return m, nil
}

// normalize tidies values so callers can trust them.
func (m *Meta) normalize() {
	m.Title = strings.TrimSpace(m.Title)
	m.Description = strings.TrimSpace(m.Description)
	m.Summary = strings.TrimSpace(m.Summary)
	m.Slug = strings.TrimSpace(m.Slug)
	m.Layout = strings.TrimSpace(m.Layout)
	m.Tags = cleanList(m.Tags)
	m.Categories = cleanList(m.Categories)
	m.Aliases = cleanList(m.Aliases)
	if m.Extra == nil {
		m.Extra = map[string]any{}
	}
}

// cleanList trims entries, drops empties and de-duplicates while preserving
// first-seen order.
func cleanList(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// HasDate reports whether the frontmatter supplied an explicit date.
func (m Meta) HasDate() bool { return !m.Date.IsZero() }

// IsExpired reports whether the page carries an expiry time in the past.
func (m Meta) IsExpired(now time.Time) bool {
	return !m.Expires.IsZero() && m.Expires.Before(now)
}
