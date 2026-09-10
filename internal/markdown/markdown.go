// Package markdown renders Markdown to HTML using goldmark v2.
//
// The goldmark parser and renderer are configured once and reused. Two site
// policies live here rather than in the pipeline: which Markdown extensions are
// enabled, and whether raw HTML in source is trusted (see Markup.Unsafe).
//
// Heading anchors are produced by goldmark itself: parsing with a
// parser.Context installs a generated "id" attribute on every heading, which
// both the HTML renderer and collectHeadings read back.
package markdown

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/yuin/goldmark/v2/ast"
	"github.com/yuin/goldmark/v2/extension"
	"github.com/yuin/goldmark/v2/parser"
	gmhtml "github.com/yuin/goldmark/v2/renderer/html"
)

// Options configures a Renderer. It is a flattened view of config.Markup so
// this package does not depend on the config package.
type Options struct {
	// Unsafe allows raw HTML embedded in Markdown. Leave false for untrusted input.
	Unsafe bool
	// Typographer substitutes straight quotes and dashes with typographic entities.
	Typographer bool
	Highlight   HighlightOptions
}

// Result is the output of rendering one document.
type Result struct {
	// HTML is the rendered body, without any site layout applied.
	HTML string
	// Headings are in document order, for building a table of contents.
	Headings []Heading
	// Text is the document's plain text, used for word counts, reading time,
	// summaries and the search index.
	Text string
}

// Heading is one entry in a page's table of contents.
type Heading struct {
	Level int
	Text  string
	// ID is the anchor goldmark assigned, matching the rendered <h*> id attribute.
	ID string
}

// WordCount reports the number of whitespace-separated words in Text.
func (r Result) WordCount() int {
	if strings.TrimSpace(r.Text) == "" {
		return 0
	}
	return len(strings.Fields(r.Text))
}

// ReadingMinutes estimates reading time at a conventional 200 words per minute,
// rounding up so a non-empty page is never reported as zero minutes.
func (r Result) ReadingMinutes() int {
	w := r.WordCount()
	if w == 0 {
		return 0
	}
	m := (w + 199) / 200
	if m < 1 {
		m = 1
	}
	return m
}

// Renderer converts Markdown source to HTML.
//
// A Renderer is safe for concurrent use once constructed: New warms the
// internal sync.Once guards in goldmark's parser and renderer so no
// first-render initialisation can race.
type Renderer struct {
	parser   parser.Parser
	renderer gmhtml.Renderer
	high     *highlighter
}

// New builds a Renderer. Panics are impossible here; all failure modes are
// deferred to Render.
func New(opts Options) *Renderer {
	parserExts := []parser.Extension{extension.GFMParser}
	if opts.Typographer {
		parserExts = append(parserExts, extension.TypographerParser)
	}

	p := parser.New(
		parser.WithAttribute(),
		// Without this the heading parser never calls generateAutoHeadingID and
		// headings render with no id attribute, breaking every TOC anchor.
		parser.WithAutoHeadingID(),
		parser.WithExtensions(parserExts...),
	)

	renderExts := []gmhtml.Extension{extension.GFMHTMLRenderer}

	var h *highlighter
	if opts.Highlight.Enabled {
		h = newHighlighter(opts.Highlight)
		// Registered as an extension rather than through WithNodeRenderer.
		// html.New applies direct options first and extension RendererOptions
		// afterwards, so commonMark's built-in KindCodeBlock renderer would
		// silently overwrite a direct registration. Extensions are applied in
		// list order and commonMark is always prepended first, so appending
		// ours last makes it win.
		renderExts = append(renderExts, h)
	}

	renderOpts := []gmhtml.Option{gmhtml.WithExtensions(renderExts...)}
	if opts.Unsafe {
		renderOpts = append(renderOpts, gmhtml.WithUnsafe())
	}

	r := &Renderer{parser: p, renderer: gmhtml.New(renderOpts...), high: h}
	r.warm()
	return r
}

// warm forces goldmark's lazy initialisation to happen now, on the caller's
// goroutine, so concurrent Render calls cannot race inside sync.Once.
func (r *Renderer) warm() {
	_, _ = r.Render([]byte("# warm\n"))
}

// Render converts Markdown source into HTML plus the metadata the pipeline
// needs (headings for a TOC, plain text for summaries and search).
func (r *Renderer) Render(source []byte) (Result, error) {
	ctx := parser.NewContext()
	doc := r.parser.Parse(source, parser.WithContext(ctx))

	// The parse context is not passed to the renderer: goldmark v2's
	// parser.Context and renderer.Context are distinct interfaces. That is
	// harmless here because heading anchors are installed as AST attributes
	// during the parse pass, so they are already present on the document.
	var buf bytes.Buffer
	if err := r.renderer.Render(&buf, source, doc); err != nil {
		return Result{}, fmt.Errorf("render markdown: %w", err)
	}

	return Result{
		HTML:     buf.String(),
		Headings: collectHeadings(doc, source),
		Text:     extractText(doc, source),
	}, nil
}

// HighlightCSS returns the stylesheet chroma needs when rendering with CSS
// classes instead of inline styles. It must be written to the output once per
// build; without it highlighted code renders unstyled.
//
// It returns "" when highlighting is disabled.
func (r *Renderer) HighlightCSS() (string, error) {
	if r.high == nil {
		return "", nil
	}
	return r.high.css()
}

// collectHeadings walks the AST in document order gathering heading level,
// text and goldmark-generated anchor ID.
func collectHeadings(doc ast.Node, source []byte) []Heading {
	var out []Heading
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		h, ok := n.(*ast.Heading)
		if !ok {
			return ast.WalkContinue, nil
		}
		out = append(out, Heading{
			Level: h.Level,
			Text:  nodeText(h, source),
			ID:    headingID(h, source),
		})
		return ast.WalkContinue, nil
	})
	return out
}

// headingID reads back the "id" attribute goldmark's heading parser installed.
func headingID(h *ast.Heading, source []byte) string {
	if v, ok := h.Attribute("id"); ok {
		return strings.TrimSpace(v.Str(source))
	}
	return ""
}

// extractText concatenates the document's text, separating blocks but joining
// inline runs without a separator.
//
// The distinction matters: goldmark splits a single paragraph into several Text
// nodes, and the typographer breaks off trailing punctuation as its own node.
// Inserting a space per node would turn "five." into "five .", inflating word
// counts and putting stray spaces into summaries and search snippets.
func extractText(doc ast.Node, source []byte) string {
	var blocks []string

	var walk func(n ast.Node)
	walk = func(n ast.Node) {
		if isTextBlock(n) {
			if s := strings.TrimSpace(inlineText(n, source)); s != "" {
				blocks = append(blocks, s)
			}
			return
		}
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			walk(c)
		}
	}
	walk(doc)

	return strings.Join(blocks, " ")
}

// isTextBlock reports whether n is a leaf block whose text should be collected
// as one unit — a paragraph, heading, code block or table cell. Container blocks
// such as list items and documents are descended into instead, so their children
// stay separately spaced.
func isTextBlock(n ast.Node) bool {
	if n.Kind() == ast.KindDocument {
		return false
	}
	if _, ok := n.(ast.BlockNode); !ok {
		return false
	}
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if _, ok := c.(ast.BlockNode); ok {
			return false
		}
	}
	return true
}

// inlineText renders a node's inline content with no separators inserted.
func inlineText(n ast.Node, source []byte) string {
	var sb strings.Builder
	appendInlineText(n, source, &sb)
	return sb.String()
}

func appendInlineText(n ast.Node, source []byte, sb *strings.Builder) {
	switch t := n.(type) {
	case *ast.Text:
		sb.WriteString(t.Value.Str(source))
		return
	case *ast.CodeBlock:
		sb.WriteString(string(t.Value.Bytes(source)))
		return
	}
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		appendInlineText(c, source, sb)
	}
}

// nodeText renders the inline content of a node to plain text.
func nodeText(n ast.Node, source []byte) string {
	var sb strings.Builder
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if t, ok := c.(*ast.Text); ok {
			sb.WriteString(t.Value.Str(source))
			continue
		}
		if c.HasChildren() {
			sb.WriteString(nodeText(c, source))
		}
	}
	return strings.TrimSpace(sb.String())
}
