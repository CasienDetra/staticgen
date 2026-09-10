package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/casien/staticgen/internal/config"
	"github.com/casien/staticgen/internal/fsx"
)

func newNewCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "new",
		Short: "Scaffold new content",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newNewPostCmd(g))
	return cmd
}

func newNewPostCmd(g *globalFlags) *cobra.Command {
	var (
		section    string
		tags       string
		categories string
		dateStr    string
		force      bool
	)

	cmd := &cobra.Command{
		Use:   "post [title]",
		Short: "Create a new Markdown post with frontmatter",
		Long: `Create a new Markdown file with frontmatter filled in.

The filename is derived from the date and a slug of the title, matching the
YYYY-MM-DD-slug.md convention the loader understands: a date in the filename is
used as the publication date when frontmatter does not set one.

New posts are created as drafts, so they stay out of the build until the draft
flag is removed or --drafts is passed.`,
		Example: `  staticgen new post "Understanding Go generics"
  staticgen new post "Release notes" --section blog --tags go,release
  staticgen new post "Scheduled" --date 2026-12-01 --published`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := g.load(config.Overrides{})
			if err != nil {
				return err
			}

			title := strings.TrimSpace(args[0])
			if title == "" {
				return errf("title must not be empty")
			}

			when := time.Now()
			if dateStr != "" {
				when, err = parseDate(dateStr)
				if err != nil {
					return err
				}
			}

			dir := cfg.ContentPath()
			if section != "" {
				dir = filepath.Join(dir, filepath.FromSlash(fsx.Slugify(section)))
			}
			name := fmt.Sprintf("%s-%s.md", when.Format("2006-01-02"), fsx.Slugify(title))
			target := filepath.Join(dir, name)

			if _, statErr := os.Stat(target); statErr == nil && !force {
				return errf("%s already exists; pass --force to overwrite", target)
			}

			published, _ := cmd.Flags().GetBool("published")
			body := postTemplate(title, when, !published, splitList(tags), splitList(categories))

			if err := fsx.EnsureDir(dir); err != nil {
				return err
			}
			if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
				return fmt.Errorf("write %s: %w", target, err)
			}

			g.printf("created %s\n", target)
			if !published {
				g.printf("it is marked draft: true; remove that line or build with --drafts to publish\n")
			}
			return nil
		},
	}

	f := cmd.Flags()
	f.StringVar(&section, "section", "", "content subdirectory to create the post in, e.g. blog")
	f.StringVar(&tags, "tags", "", "comma-separated tags")
	f.StringVar(&categories, "categories", "", "comma-separated categories")
	f.StringVar(&dateStr, "date", "", "publication date: YYYY-MM-DD or RFC3339 (default: now)")
	f.BoolVar(&force, "force", false, "overwrite the file if it already exists")
	f.Bool("published", false, "create without draft: true")
	return cmd
}

// parseDate accepts the date formats worth supporting on a command line.
func parseDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	layouts := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errf("could not parse date %q; want YYYY-MM-DD or RFC3339", s)
}

// postTemplate renders the frontmatter and a minimal body for a new post.
func postTemplate(title string, when time.Time, draft bool, tags, categories []string) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("title: " + yamlQuote(title) + "\n")
	b.WriteString("date: " + when.Format(time.RFC3339) + "\n")
	b.WriteString("description: " + yamlQuote("") + "\n")
	if draft {
		b.WriteString("draft: true\n")
	}
	if len(tags) > 0 {
		b.WriteString("tags: [" + strings.Join(yamlQuoteAll(tags), ", ") + "]\n")
	}
	if len(categories) > 0 {
		b.WriteString("categories: [" + strings.Join(yamlQuoteAll(categories), ", ") + "]\n")
	}
	b.WriteString("---\n\n")
	b.WriteString("## Introduction\n\n")
	b.WriteString("Write something worth reading.\n\n")
	b.WriteString("<!--more-->\n\n")
	b.WriteString("## Details\n\n")
	b.WriteString("Everything above the marker becomes the post's excerpt in listings.\n")
	return b.String()
}

// yamlQuote renders s as a YAML double-quoted scalar. strconv.Quote produces
// escaping that YAML's double-quoted style also accepts, so titles containing
// colons or quotes cannot break the generated frontmatter.
func yamlQuote(s string) string { return strconv.Quote(s) }

func yamlQuoteAll(vals []string) []string {
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		out = append(out, yamlQuote(v))
	}
	return out
}
