package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/casien/staticgen/internal/config"
	"github.com/casien/staticgen/internal/generate"
	"github.com/casien/staticgen/internal/pipeline"
)

func TestPaginationBuildAndCheck(t *testing.T) {
	for _, pretty := range []bool{true, false} {
		t.Run(fmt.Sprintf("pretty=%t", pretty), func(t *testing.T) {
			cfg := config.Default()
			cfg.Root = t.TempDir()
			cfg.BaseURL = "https://example.com"
			cfg.Build.Paginate = 2
			cfg.Build.PrettyURLs = pretty
			write := func(name, text string) {
				t.Helper()
				path := filepath.Join(cfg.ContentPath(), name)
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(text), 0644); err != nil {
					t.Fatal(err)
				}
			}
			write("_index.md", "---\ntitle: Home\naliases: [/welcome/]\n---\nHome introduction.")
			write("blog/_index.md", "---\ntitle: Blog\naliases: [/posts/]\n---\nBlog introduction.")
			for i := 1; i <= 5; i++ {
				write(fmt.Sprintf("blog/post-%d.md", i), fmt.Sprintf("---\ntitle: Post %d\ndescription: A post.\ndate: 2026-01-%02d\ntags: [go]\n---\nPost body %d.", i, i, i))
			}
			write("blog/draft.md", "---\ntitle: Draft\ndescription: Draft.\ndraft: true\n---\nDraft body.")
			write("blog/future.md", "---\ntitle: Future\ndescription: Future.\ndate: 2099-01-01\n---\nFuture body.")
			builder, err := pipeline.New(cfg, pipeline.Options{Now: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)})
			if err != nil {
				t.Fatal(err)
			}
			result, err := builder.Build(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Warnings) != 0 || result.Redirects != 2 || len(result.Site.RegularPages) != 5 {
				t.Fatalf("unexpected build: warnings=%v redirects=%d posts=%d", result.Warnings, result.Redirects, len(result.Site.RegularPages))
			}
			read := func(name string) string {
				t.Helper()
				data, err := os.ReadFile(filepath.Join(cfg.OutputPath(), name))
				if err != nil {
					t.Fatal(err)
				}
				return string(data)
			}
			for _, base := range []string{"", "blog/"} {
				for number := 1; number <= 3; number++ {
					path := base
					if number > 1 {
						path += fmt.Sprintf("page/%d/", number)
					}
					html := read(path + "index.html")
					if !strings.Contains(html, fmt.Sprintf("Page %d of 3", number)) {
						t.Errorf("%s missing pagination status", path)
					}
					if number > 1 {
						end := strings.Index(html, "</title>")
						title := html[strings.Index(html, "<title>"):end]
						if !strings.Contains(title, fmt.Sprintf("Page %d", number)) {
							t.Errorf("%s missing page number in title: %s", path, title)
						}
						// Home listings always carry the configured site title;
						// section listings carry their own heading.
						if base == "" && !strings.Contains(title, "My Site") {
							t.Errorf("%s lost the site title: %s", path, title)
						}
						if base != "" && !strings.Contains(title, "Blog") {
							t.Errorf("%s lost the section title: %s", path, title)
						}
					}
					if !strings.Contains(html, `rel="canonical" href="https://example.com/`+path+`"`) {
						t.Errorf("%s missing self canonical", path)
					}
					for _, p := range result.Site.Find("/" + path).Pages {
						if !strings.Contains(html, `href="`+p.URL+`"`) {
							t.Errorf("%s does not link to %s", path, p.URL)
						}
					}
				}
			}
			if strings.Count(read("feed.xml"), "<item>") != 5 {
				t.Error("feed no longer contains exactly five posts")
			}
			if !strings.Contains(read("sitemap.xml"), "https://example.com/blog/page/3/") {
				t.Error("sitemap missing pagination page")
			}
			var index generate.SearchIndex
			if err := json.Unmarshal([]byte(read("search.json")), &index); err != nil {
				t.Fatal(err)
			}
			for _, doc := range index.Documents {
				if strings.Contains(doc.URL, "/page/") {
					t.Errorf("search contains duplicate listing %s", doc.URL)
				}
			}
			inspection, err := builder.Inspect(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if issues := runChecks(builder, inspection); len(issues) != 0 {
				t.Fatalf("paginated site has check issues: %+v", issues)
			}
			cfg.Build.Paginate = 0
			if _, err := builder.Build(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(cfg.OutputPath(), "page/2/index.html")); !os.IsNotExist(err) {
				t.Error("clean rebuild left stale pagination output")
			}
		})
	}
}
