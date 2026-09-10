package markdown

import (
	"strings"
	"testing"
)

func newTestRenderer(unsafe bool) *Renderer {
	return New(Options{
		Unsafe:      unsafe,
		Typographer: true,
		Highlight: HighlightOptions{
			Enabled:       true,
			Style:         "github",
			GuessLanguage: true,
		},
	})
}

func TestRenderBasicMarkdown(t *testing.T) {
	got, err := newTestRenderer(false).Render([]byte("# Hello\n\nSome **bold** text.\n"))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{"<h1", "Hello", "<strong>bold</strong>"} {
		if !strings.Contains(got.HTML, want) {
			t.Errorf("HTML missing %q\n got: %s", want, got.HTML)
		}
	}
}

func TestRenderGFMTable(t *testing.T) {
	src := "| a | b |\n|---|---|\n| 1 | 2 |\n"
	got, err := newTestRenderer(false).Render([]byte(src))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(got.HTML, "<table>") {
		t.Errorf("GFM table extension not active\n got: %s", got.HTML)
	}
}

// TestRenderHighlightedCode is the core check on the hand-written chroma
// integration: goldmark-highlighting could not be used because it targets
// goldmark v1.
func TestRenderHighlightedCode(t *testing.T) {
	src := "```go\nfunc main() { println(\"hi\") }\n```\n"
	got, err := newTestRenderer(false).Render([]byte(src))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{
		`<div class="highlight" data-lang="go">`,
		`class="chroma"`,
		// "func" is a keyword; with WithClasses(true) it must carry a token
		// class rather than an inline style attribute.
		`<span class="kd">func</span>`,
	} {
		if !strings.Contains(got.HTML, want) {
			t.Errorf("highlighted output missing %q\n got: %s", want, got.HTML)
		}
	}
	if strings.Contains(got.HTML, "style=") {
		t.Errorf("expected class-based highlighting, got inline styles:\n%s", got.HTML)
	}
}

func TestRenderCodeWithoutLanguageIsGuessed(t *testing.T) {
	src := "```\n<!DOCTYPE html>\n<html><body>x</body></html>\n```\n"
	got, err := newTestRenderer(false).Render([]byte(src))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(got.HTML, `<div class="highlight">`) {
		t.Errorf("unlabelled block should still be wrapped, got:\n%s", got.HTML)
	}
	if strings.Contains(got.HTML, "data-lang=") {
		t.Errorf("no info string means no data-lang attribute, got:\n%s", got.HTML)
	}
}

func TestHeadingIDsAndTOC(t *testing.T) {
	src := "# Intro Text\n\nbody\n\n## Sub Section\n\nmore\n"
	got, err := newTestRenderer(false).Render([]byte(src))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if len(got.Headings) != 2 {
		t.Fatalf("want 2 headings, got %d: %+v", len(got.Headings), got.Headings)
	}

	want := []Heading{
		{Level: 1, Text: "Intro Text"},
		{Level: 2, Text: "Sub Section"},
	}
	for i, w := range want {
		if got.Headings[i].Level != w.Level || got.Headings[i].Text != w.Text {
			t.Errorf("heading %d = %+v, want level %d text %q", i, got.Headings[i], w.Level, w.Text)
		}
		if got.Headings[i].ID == "" {
			t.Errorf("heading %d has no generated anchor ID", i)
		}
	}

	// The anchor goldmark reports for the TOC must match the rendered markup,
	// otherwise every table-of-contents link would 404 within the page.
	id := got.Headings[0].ID
	if !strings.Contains(got.HTML, `id="`+id+`"`) {
		t.Errorf("TOC id %q not present in rendered HTML:\n%s", id, got.HTML)
	}
}

func TestPlainTextAndWordCount(t *testing.T) {
	src := "# Title\n\nOne two three **four** five.\n\n```go\nx := 1\n```\n"
	got, err := newTestRenderer(false).Render([]byte(src))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{"One two three", "four", "five"} {
		if !strings.Contains(got.Text, want) {
			t.Errorf("plain text missing %q\n got: %q", want, got.Text)
		}
	}
	// No markup should leak into the text used for summaries and search.
	for _, bad := range []string{"<", ">", "**", "#"} {
		if strings.Contains(got.Text, bad) {
			t.Errorf("plain text contains markup %q\n got: %q", bad, got.Text)
		}
	}
	if got.WordCount() < 6 {
		t.Errorf("word count = %d, want at least 6", got.WordCount())
	}
	if got.ReadingMinutes() < 1 {
		t.Errorf("reading minutes = %d, want at least 1", got.ReadingMinutes())
	}
}

func TestUnsafeHTMLIsOptIn(t *testing.T) {
	src := "<script>alert(1)</script>\n\nsafe\n"

	safe, err := newTestRenderer(false).Render([]byte(src))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(safe.HTML, "<script>") {
		t.Errorf("raw HTML rendered with Unsafe=false:\n%s", safe.HTML)
	}

	unsafe, err := newTestRenderer(true).Render([]byte(src))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(unsafe.HTML, "<script>") {
		t.Errorf("raw HTML suppressed with Unsafe=true:\n%s", unsafe.HTML)
	}
}

func TestPlainTextKeepsInlineRunsIntact(t *testing.T) {
	// The typographer splits trailing punctuation into its own Text node. Text
	// used for word counts and search snippets must not gain a space there.
	got, err := newTestRenderer(false).Render([]byte("One two three four five.\n"))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if want := "One two three four five."; got.Text != want {
		t.Errorf("Text = %q, want %q", got.Text, want)
	}
	if got.WordCount() != 5 {
		t.Errorf("WordCount = %d, want 5", got.WordCount())
	}

	tests := []struct{ src, want string }{
		// Blocks stay separated so paragraphs do not run together.
		{"Para one.\n\nPara two.\n", "Para one. Para two."},
		{"# Heading\n\nBody text.\n", "Heading Body text."},
		{"- first item\n- second item\n", "first item second item"},
		{"Emphasis **inside** a sentence.\n", "Emphasis inside a sentence."},
	}
	for _, tt := range tests {
		res, err := newTestRenderer(false).Render([]byte(tt.src))
		if err != nil {
			t.Fatalf("Render(%q): %v", tt.src, err)
		}
		if res.Text != tt.want {
			t.Errorf("Render(%q).Text = %q, want %q", tt.src, res.Text, tt.want)
		}
	}
}

func TestHighlightCSS(t *testing.T) {
	css, err := newTestRenderer(false).HighlightCSS()
	if err != nil {
		t.Fatalf("HighlightCSS: %v", err)
	}
	// WithClasses(true) means the markup depends on this sheet existing.
	for _, want := range []string{".chroma", ".kd"} {
		if !strings.Contains(css, want) {
			t.Errorf("chroma CSS missing %q", want)
		}
	}

	// Disabled highlighting must yield no stylesheet, and the caller writes
	// nothing rather than an empty file.
	plain := New(Options{Highlight: HighlightOptions{Enabled: false}})
	css, err = plain.HighlightCSS()
	if err != nil {
		t.Fatalf("HighlightCSS: %v", err)
	}
	if css != "" {
		t.Errorf("want empty CSS when highlighting disabled, got %d bytes", len(css))
	}
}

func TestUnknownStyleFallsBack(t *testing.T) {
	r := New(Options{Highlight: HighlightOptions{Enabled: true, Style: "not-a-real-style"}})
	got, err := r.Render([]byte("```go\nx := 1\n```\n"))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(got.HTML, `class="chroma"`) {
		t.Errorf("unknown style should still highlight, got:\n%s", got.HTML)
	}
}

func TestRenderIsConcurrencySafe(t *testing.T) {
	r := newTestRenderer(false)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			_, err := r.Render([]byte("# Head\n\n```go\ny := 2\n```\n"))
			errs <- err
		}()
	}
	for i := 0; i < 8; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent Render: %v", err)
		}
	}
}
