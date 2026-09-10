package pipeline

import (
	"bytes"
	"strings"
)

// This file implements deliberately conservative minification.
//
// Both functions collapse whitespace rather than removing it, and neither
// attempts to rewrite markup or selectors. Removing whitespace between inline
// elements would change what the page looks like ("<em>a</em>\n<em>b</em>"
// renders as "a b"), and hand-rolled CSS restructuring breaks constructs like
// calc(1em + 2px). Minification is off by default; when enabled it trims
// indentation and comments without altering rendering.

// protectedElements hold content in which whitespace is significant and markup
// must not be parsed. The scanner copies each of these spans verbatim.
var protectedElements = map[string]bool{
	"pre":      true,
	"script":   true,
	"style":    true,
	"textarea": true,
}

// minify collapses whitespace runs in HTML outside protected elements.
func minify(src []byte) []byte {
	var out bytes.Buffer
	out.Grow(len(src))

	i := 0
	for i < len(src) {
		c := src[i]

		switch {
		case c == '<' && hasPrefixAt(src, i, "<!--"):
			end := indexCommentEnd(src, i)
			out.Write(src[i:end])
			i = end

		case c == '<':
			end := indexTagEnd(src, i)
			tag := src[i:end]
			name, closing := tagName(tag)

			if !closing && protectedElements[name] {
				// Copy the element and its whole body verbatim: its
				// whitespace is significant and its content may include
				// characters that look like markup.
				stop := indexCloseTag(src, end, name)
				out.Write(src[i:stop])
				i = stop
				break
			}

			out.Write(tag)
			i = end

		case isSpaceByte(c):
			// Collapse any run to a single space. Never drop it entirely:
			// a newline between two inline elements is a rendered space.
			for i < len(src) && isSpaceByte(src[i]) {
				i++
			}
			out.WriteByte(' ')

		default:
			out.WriteByte(c)
			i++
		}
	}
	return out.Bytes()
}

// minifyCSS strips comments and collapses whitespace outside string literals.
func minifyCSS(src []byte) []byte {
	var out bytes.Buffer
	out.Grow(len(src))

	i := 0
	for i < len(src) {
		c := src[i]

		switch {
		case c == '/' && hasPrefixAt(src, i, "/*"):
			end := indexCSSEnd(src, i)
			i = end
			// A comment between two tokens still separates them, so it
			// becomes a space rather than nothing.
			out.WriteByte(' ')

		case c == '"' || c == '\'':
			end := indexStringEnd(src, i, c)
			out.Write(src[i:end])
			i = end

		case isSpaceByte(c):
			for i < len(src) && isSpaceByte(src[i]) {
				i++
			}
			out.WriteByte(' ')

		default:
			out.WriteByte(c)
			i++
		}
	}
	return out.Bytes()
}

func hasPrefixAt(src []byte, i int, prefix string) bool {
	return i+len(prefix) <= len(src) && string(src[i:i+len(prefix)]) == prefix
}

func isSpaceByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}

// indexTagEnd returns the offset just past the '>' that closes the tag at i.
func indexTagEnd(src []byte, i int) int {
	for j := i + 1; j < len(src); j++ {
		if src[j] == '>' {
			return j + 1
		}
	}
	return len(src)
}

func indexCommentEnd(src []byte, i int) int {
	if j := bytes.Index(src[i:], []byte("-->")); j >= 0 {
		return i + j + 3
	}
	return len(src)
}

func indexCSSEnd(src []byte, i int) int {
	if j := bytes.Index(src[i:], []byte("*/")); j >= 0 {
		return i + j + 2
	}
	return len(src)
}

// indexStringEnd returns the offset just past the closing quote, honouring
// backslash escapes.
func indexStringEnd(src []byte, i int, quote byte) int {
	for j := i + 1; j < len(src); j++ {
		switch src[j] {
		case '\\':
			j++
		case quote:
			return j + 1
		}
	}
	return len(src)
}

// indexCloseTag finds the end of </name>, or the end of input when the element
// is never closed (which would be malformed, but must not hang the build).
//
// The search is case-sensitive on the lowercase needle. Uppercase closing tags
// in hand-written HTML therefore fall through to the end of input, which copies
// the remainder verbatim — minification is skipped rather than made incorrect.
func indexCloseTag(src []byte, from int, name string) int {
	needle := []byte("</" + name)
	idx := from
	for {
		j := bytes.Index(src[idx:], needle)
		if j < 0 {
			return len(src)
		}
		at := idx + j
		rest := at + len(needle)
		if rest >= len(src) {
			return len(src)
		}
		if c := src[rest]; isSpaceByte(c) || c == '>' {
			return indexTagEnd(src, at)
		}
		idx = at + 1
	}
}

// tagName extracts the lowercased element name from a tag, and reports whether
// it is a closing tag. Declarations and comments yield an empty name.
func tagName(tag []byte) (name string, closing bool) {
	s := string(tag)
	if strings.HasPrefix(s, "<!") || strings.HasPrefix(s, "<?") {
		return "", false
	}
	closing = strings.HasPrefix(s, "</")
	s = strings.TrimPrefix(s, "</")
	s = strings.TrimPrefix(s, "<")

	k := 0
	for k < len(s) && !isSpaceByte(s[k]) && s[k] != '>' && s[k] != '/' {
		k++
	}
	return strings.ToLower(s[:k]), closing
}
