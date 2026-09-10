package markdown

import (
	"bytes"
	"html"
	"io"
	"strings"

	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/yuin/goldmark/v2/ast"
	"github.com/yuin/goldmark/v2/renderer"
	gmhtml "github.com/yuin/goldmark/v2/renderer/html"
)

// HighlightOptions configures chroma-based syntax highlighting.
type HighlightOptions struct {
	Enabled bool
	// Style is a chroma style name; unknown names fall back to chroma's default.
	Style       string
	LineNumbers bool
	// GuessLanguage runs chroma's content analysis when a fenced block has no
	// info string, instead of rendering it as plain text.
	GuessLanguage bool
}

// highlighter renders code blocks through chroma.
//
// Chroma is configured with WithClasses(true), so emitted markup references CSS
// classes rather than inlining a style attribute on every token. That keeps
// generated HTML small and lets a theme restyle code, at the cost of requiring
// the stylesheet from Renderer.HighlightCSS to be written once per build.
type highlighter struct {
	style     *chroma.Style
	formatter *chromahtml.Formatter
	guess     bool
}

func newHighlighter(o HighlightOptions) *highlighter {
	// styles.Get returns chroma's Fallback style for an unknown name, so this
	// is never nil; the guard is for a future chroma that changes that contract.
	style := styles.Get(o.Style)
	if style == nil {
		style = styles.Fallback
	}
	return &highlighter{
		style: style,
		formatter: chromahtml.New(
			chromahtml.WithClasses(true),
			chromahtml.WithLineNumbers(o.LineNumbers),
		),
		guess: o.GuessLanguage,
	}
}

// RendererOptions makes the highlighter a goldmark html.Extension. Registering
// it this way — instead of passing WithNodeRenderer directly to html.New — is
// what lets it override commonMark's built-in code block renderer; see the note
// in New.
func (h *highlighter) RendererOptions(_ *gmhtml.Config) []gmhtml.Option {
	return []gmhtml.Option{
		gmhtml.WithNodeRenderer(ast.KindCodeBlock, h.nodeRenderer()),
	}
}

// nodeRenderer adapts the highlighter into goldmark's NodeRenderer interface.
//
// The whole block is emitted on the entering pass and WalkContinue is returned
// for both passes: in goldmark v2 a CodeBlock keeps its content in a
// text.Lines value rather than in child nodes, so there are no children to skip
// and nothing to close on the way out.
func (h *highlighter) nodeRenderer() gmhtml.NodeRenderer {
	return gmhtml.NodeRendererFunc(func(w io.Writer, source []byte, n ast.Node, entering bool, _ renderer.Context) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		cb, ok := n.(*ast.CodeBlock)
		if !ok {
			return ast.WalkContinue, nil
		}
		if err := h.render(w, source, cb); err != nil {
			return ast.WalkStop, err
		}
		return ast.WalkContinue, nil
	})
}

func (h *highlighter) render(w io.Writer, source []byte, cb *ast.CodeBlock) error {
	lang, hasLang := cb.Language(source)
	lang = strings.TrimSpace(lang)
	code := string(cb.Value.Bytes(source))

	lexer := lexers.Get(lang)
	if lexer == nil && h.guess && strings.TrimSpace(code) != "" {
		lexer = lexers.Analyse(code)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}

	// Coalesce collapses runs of adjacent same-type tokens, which produces
	// materially smaller HTML for no change in appearance.
	tokens, err := chroma.Coalesce(lexer).Tokenise(nil, code)
	if err != nil {
		return err
	}

	// The wrapper records the source language so themes can label the block or
	// style per-language, and gives CSS a hook independent of chroma's markup.
	if err := writeString(w, `<div class="highlight"`); err != nil {
		return err
	}
	if hasLang && lang != "" {
		if err := writeString(w, ` data-lang="`+html.EscapeString(lang)+`"`); err != nil {
			return err
		}
	}
	if err := writeString(w, ">"); err != nil {
		return err
	}

	if err := h.formatter.Format(w, h.style, tokens); err != nil {
		return err
	}
	return writeString(w, "</div>\n")
}

// css renders the stylesheet for the configured chroma style.
func (h *highlighter) css() (string, error) {
	var buf bytes.Buffer
	if err := h.formatter.WriteCSS(&buf, h.style); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func writeString(w io.Writer, s string) error {
	_, err := io.WriteString(w, s)
	return err
}
