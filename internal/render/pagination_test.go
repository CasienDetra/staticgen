package render

import (
	"fmt"
	"strings"
	"testing"

	"github.com/casien/staticgen/internal/content"
)

func TestRenderListingPagination(t *testing.T) {
	for _, kind := range []content.Kind{content.KindHome, content.KindSection} {
		for _, number := range []int{1, 2, 3} {
			t.Run(fmt.Sprintf("%s/page-%d", kind, number), func(t *testing.T) {
				cfg := testConfig(t)
				cfg.BaseURL = "https://example.com"
				e, err := New(Options{Config: cfg})
				if err != nil {
					t.Fatal(err)
				}
				s := testSite(t, cfg)
				root := "/"
				if kind == content.KindSection {
					root = "/blog/"
				}
				pageURL := func(n int) string {
					if n == 1 {
						return root
					}
					return fmt.Sprintf("%spage/%d/", root, n)
				}
				p := testPage("Blog", pageURL(number), "")
				p.Kind = kind
				p.Pages = []*content.Page{testPage("Visible post", "/blog/visible/", "")}
				p.Pagination = &content.Pagination{Number: number, TotalPages: 3, TotalItems: 5}
				if number > 1 {
					p.Pagination.PrevURL = pageURL(number - 1)
				}
				if number < 3 {
					p.Pagination.NextURL = pageURL(number + 1)
				}
				p.Prev = testPage("Article older", "/article-older/", "")
				p.Next = testPage("Article newer", "/article-newer/", "")
				got := render(t, e, p, s)
				for _, want := range []string{
					`aria-label="Pagination"`,
					fmt.Sprintf("Page %d of 3", number),
					`href="/blog/visible/"`,
					`<link rel="canonical" href="https://example.com` + p.URL + `">`,
				} {
					if !strings.Contains(got, want) {
						t.Errorf("rendered listing missing %q", want)
					}
				}
				for _, link := range []struct {
					url, rel, label string
				}{
					{p.Pagination.PrevURL, "prev", "Previous"},
					{p.Pagination.NextURL, "next", "Next"},
				} {
					if link.url == "" {
						if strings.Contains(got, `rel="`+link.rel+`"`) {
							t.Errorf("unexpected %s link at pagination boundary", link.rel)
						}
						continue
					}
					want := `href="` + link.url + `" rel="` + link.rel + `"`
					if !strings.Contains(got, want) || !strings.Contains(got, link.label) {
						t.Errorf("missing accessible %s link to %s", link.rel, link.url)
					}
				}
				if strings.Contains(got, "Adjacent posts") || strings.Contains(got, "/article-older/") || strings.Contains(got, "/article-newer/") {
					t.Error("listing pagination must not use article neighbors")
				}
			})
		}
	}
}

func TestRenderUnsplitListingHasNoPagination(t *testing.T) {
	cfg := testConfig(t)
	e, err := New(Options{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	s := testSite(t, cfg)
	for _, kind := range []content.Kind{content.KindHome, content.KindSection} {
		t.Run(string(kind), func(t *testing.T) {
			p := testPage("Blog", "/blog/", "<p>Listing introduction.</p>")
			p.Kind = kind
			got := render(t, e, p, s)
			if strings.Contains(got, `aria-label="Pagination"`) || strings.Contains(got, "Page 1 of") {
				t.Error("unsplit listing must not render pagination")
			}
			if !strings.Contains(got, "<p>Listing introduction.</p>") {
				t.Error("listing introduction must still render")
			}
		})
	}
}

func TestPaginationDocumentTitle(t *testing.T) {
	for _, tc := range []struct {
		name, title, siteTitle string
		kind                   content.Kind
		number                 int
		want                   string
	}{
		{"home unsplit", "Home", "Render Test", content.KindHome, 0, "Render Test"},
		{"home first", "Home", "Render Test", content.KindHome, 1, "Render Test"},
		{"home later", "Home", "Render Test", content.KindHome, 2, "Render Test · Page 2"},
		{"section first", "Blog", "Render Test", content.KindSection, 1, "Blog · Render Test"},
		{"section later", "Blog", "Render Test", content.KindSection, 2, "Blog · Page 2 · Render Test"},
		{"same title", "Render Test", "Render Test", content.KindSection, 2, "Render Test · Page 2"},
		{"no site title", "Blog", "", content.KindSection, 2, "Blog · Page 2"},
		{"article", "Post", "Render Test", content.KindPage, 0, "Post · Render Test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig(t)
			cfg.Title = tc.siteTitle
			p := testPage(tc.title, "/", "")
			p.Kind = tc.kind
			if tc.number > 0 {
				p.Pagination = &content.Pagination{Number: tc.number, TotalPages: 3, TotalItems: 5}
			}
			if got := documentTitle(p, cfg); got != tc.want {
				t.Errorf("documentTitle = %q, want %q", got, tc.want)
			}
			if p.Meta.Title != tc.title {
				t.Error("document title formatting must not mutate the page title")
			}
		})
	}
}
