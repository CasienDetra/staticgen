package site

import (
	"fmt"
	"strings"
	"testing"

	"github.com/casien/staticgen/internal/content"
)

func TestPaginationSplitsHomeAndSections(t *testing.T) {
	for _, pretty := range []bool{true, false} {
		for _, authored := range []bool{true, false} {
			t.Run(fmt.Sprintf("pretty=%t/authored=%t", pretty, authored), func(t *testing.T) {
				cfg := testConfig()
				cfg.Build.Paginate = 2
				cfg.Build.PrettyURLs = pretty
				var pages []*content.Page
				for i := 1; i <= 5; i++ {
					pages = append(pages, article(fmt.Sprintf("/blog/post-%d/", i), fmt.Sprintf("Post %d", i), "blog", day(i), []string{"go"}, []string{"notes"}))
				}
				if authored {
					for _, base := range []string{"", "blog"} {
						kind := content.KindSection
						url := "/blog/"
						if base == "" {
							kind, url = content.KindHome, "/"
						}
						pages = append(pages, &content.Page{
							Kind: kind, IsIndex: true, Section: base, URL: url, Permalink: cfg.BaseURL + url,
							OutputPath: strings.TrimPrefix(url, "/") + "index.html",
							SourcePath: base + "/_index.md", BodyHTML: "<p>Introduction</p>",
							Meta: content.Meta{Title: "Listing", Aliases: []string{"/old-" + base + "/"}},
						})
					}
				}
				s := mustAssemble(t, cfg, pages)
				for _, base := range []string{"/", "/blog/"} {
					var titles []string
					for number, count := range []int{2, 2, 1} {
						url := base
						if number > 0 {
							url += fmt.Sprintf("page/%d/", number+1)
						}
						p := s.Find(url)
						if p == nil {
							t.Fatalf("missing listing %s", url)
						}
						if p.Pagination == nil || p.Pagination.Number != number+1 || p.Pagination.TotalPages != 3 || p.Pagination.TotalItems != 5 {
							t.Fatalf("incorrect pagination metadata at %s: %+v", url, p.Pagination)
						}
						prev, next := "", ""
						if number == 1 {
							prev = base
						} else if number > 1 {
							prev = base + fmt.Sprintf("page/%d/", number)
						}
						if number < 2 {
							next = base + fmt.Sprintf("page/%d/", number+2)
						}
						if p.Pagination.PrevURL != prev || p.Pagination.NextURL != next {
							t.Errorf("%s navigation = %+v, want prev=%s next=%s", url, p.Pagination, prev, next)
						}
						if len(p.Pages) != count {
							t.Fatalf("%s lists %d posts, want %d", url, len(p.Pages), count)
						}
						if p.OutputPath != strings.TrimPrefix(url, "/")+"index.html" || p.Permalink != cfg.BaseURL+url {
							t.Errorf("incorrect output path or permalink for %s: %s, %s", url, p.OutputPath, p.Permalink)
						}
						if number > 0 && (len(p.Meta.Aliases) != 0 || p.BodyHTML != "") {
							t.Errorf("%s repeats aliases or introduction", url)
						}
						for _, post := range p.Pages {
							titles = append(titles, post.Title())
						}
					}
					if got := strings.Join(titles, ","); got != "Post 5,Post 4,Post 3,Post 2,Post 1" {
						t.Errorf("%s post sequence = %s", base, got)
					}
					if s.Find(base+"page/1/") != nil || s.Find(base+"page/4/") != nil {
						t.Errorf("%s has an unnecessary pagination page", base)
					}
				}
				if len(s.RegularPages) != 5 || len(s.SectionByName("blog").Pages) != 5 || len(s.Find("/tags/go/").Pages) != 5 || len(s.Find("/categories/notes/").Pages) != 5 || len(s.Archive[0].Pages) != 5 {
					t.Error("pagination changed the complete content collections")
				}
			})
		}
	}
}

func TestPaginationBoundaries(t *testing.T) {
	for _, tc := range []struct{ count, size, listings int }{
		{5, 0, 1}, {0, 2, 1}, {1, 2, 1}, {2, 2, 1}, {4, 2, 2}, {3, 1, 3}, {2, 100, 1},
	} {
		t.Run(fmt.Sprintf("count=%d/size=%d", tc.count, tc.size), func(t *testing.T) {
			cfg := testConfig()
			cfg.Build.Paginate = tc.size
			var pages []*content.Page
			for i := 0; i < tc.count; i++ {
				pages = append(pages, article(fmt.Sprintf("/post-%d/", i), "Post", "", day(i+1), nil, nil))
			}
			s := mustAssemble(t, cfg, pages)
			listings := 0
			for _, p := range s.Pages {
				if p.Kind == content.KindHome {
					listings++
				}
			}
			if tc.listings == 1 && s.Home.Pagination != nil {
				t.Error("unsplit listing should not have pagination metadata")
			}
			if listings != tc.listings {
				t.Errorf("home listings = %d, want %d", listings, tc.listings)
			}
		})
	}
}

// Repeated assembly must be stable: pagination slices Pages in place, so a
// second pass over the same loaded pages would otherwise see an already
// truncated home listing and skip it.
func TestPaginationRepeatedAssemble(t *testing.T) {
	cfg := testConfig()
	cfg.Build.Paginate = 2
	pages := []*content.Page{
		article("/blog/a/", "A", "blog", day(1), nil, nil),
		article("/blog/b/", "B", "blog", day(2), nil, nil),
		article("/blog/c/", "C", "blog", day(3), nil, nil),
		article("/blog/d/", "D", "blog", day(4), nil, nil),
		article("/blog/e/", "E", "blog", day(5), nil, nil),
	}
	pages = append(pages, &content.Page{
		Kind: content.KindHome, IsIndex: true, URL: "/", Permalink: cfg.BaseURL + "/",
		OutputPath: "index.html", SourcePath: "index.md",
		Meta: content.Meta{Title: "Home"},
	})

	s := mustAssemble(t, cfg, pages)
	listings := 0
	for _, p := range s.Pages {
		if p.Kind == content.KindHome {
			listings++
		}
	}
	if listings != 3 {
		t.Fatalf("first assembly produced %d home listings, want 3", listings)
	}

	again := mustAssemble(t, cfg, pages)
	if len(again.Home.Pages) != 2 {
		t.Errorf("second assembly home lists %d posts, want 2", len(again.Home.Pages))
	}
	second := 0
	for _, p := range again.Pages {
		if p.Kind == content.KindHome && p.Pagination != nil && p.Pagination.Number == 2 {
			second++
			if len(p.Pages) != 2 {
				t.Errorf("second assembly page 2 lists %d posts, want 2", len(p.Pages))
			}
		}
	}
	if second != 1 {
		t.Errorf("second assembly produced %d second home pages, want 1", second)
	}

	cfg.Build.Paginate = 0
	off := mustAssemble(t, cfg, pages)
	if off.Home.Pagination != nil {
		t.Error("disabling pagination must clear stale metadata")
	}
	if len(off.Home.Pages) != 5 {
		t.Errorf("pagination-off assembly lists %d posts, want the full 5", len(off.Home.Pages))
	}
}

// With pretty_urls off, an article URL like /page/2/index.html and a
// pagination URL like /page/2/ are distinct but both map to the output file
// page/2/index.html. Assembly must fail rather than silently overwrite the
// article with the listing.
func TestPaginationRejectsOutputPathCollision(t *testing.T) {
	cfg := testConfig()
	cfg.Build.Paginate = 1
	cfg.Build.PrettyURLs = false
	pages := []*content.Page{
		article("/page/2/index.html", "Reserved", "", day(1), nil, nil),
		article("/other.html", "Other", "", day(2), nil, nil),
	}
	pages[0].OutputPath = "page/2/index.html"
	pages[1].OutputPath = "other.html"
	_, err := NewAssembler(cfg).Assemble(pages, buildTime)
	if err == nil || !strings.Contains(err.Error(), "page/2/index.html") {
		t.Fatalf("expected output path collision error, got %v", err)
	}
}

func TestPaginationRejectsContentURLCollision(t *testing.T) {
	cfg := testConfig()
	cfg.Build.Paginate = 1
	_, err := NewAssembler(cfg).Assemble([]*content.Page{
		article("/page/2/", "Reserved", "", day(1), nil, nil),
		article("/other/", "Other", "", day(2), nil, nil),
	}, buildTime)
	if err == nil || !strings.Contains(err.Error(), "/page/2/") {
		t.Fatalf("expected pagination collision error, got %v", err)
	}
}
