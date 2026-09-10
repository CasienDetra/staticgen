package cli

import (
	"time"

	"github.com/spf13/cobra"

	"github.com/casien/staticgen/internal/config"
	"github.com/casien/staticgen/internal/pipeline"
)

func newBuildCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build the site into the output directory",
		Long: `Build reads every Markdown file under the content directory, renders it
through the theme, and writes a complete static website to the output directory.

Tag listings, category listings, the archive, the RSS feed, the sitemap and the
search index are generated as part of the same pass. The output directory is
cleared first unless --clean=false, so pages deleted from the content tree do not
linger in the published site.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := g.load(config.Overrides{
				Drafts:      changedBool(cmd, "drafts"),
				Future:      changedBool(cmd, "future"),
				Minify:      changedBool(cmd, "minify"),
				PrettyURLs:  changedBool(cmd, "pretty-urls"),
				CleanOutput: changedBool(cmd, "clean"),
				Unsafe:      changedBool(cmd, "unsafe"),
			})
			if err != nil {
				return err
			}

			builder, err := pipeline.New(cfg, pipeline.Options{})
			if err != nil {
				return err
			}

			if g.verbose {
				g.printf("site root: %s\n", cfg.Root)
				g.printf("config:    %s\n", describeConfigPath(cfg))
				g.printf("content:   %s\n", cfg.ContentPath())
				g.printf("templates: %s\n", cfg.TemplatesPath())
				g.printf("static:    %s\n", cfg.StaticPath())
				g.printf("output:    %s\n", cfg.OutputPath())
			}

			res, err := builder.Build(cmd.Context())
			reportBuild(g, res)
			return err
		},
	}

	f := cmd.Flags()
	f.Bool("drafts", false, "include pages marked draft: true")
	f.Bool("future", false, "include pages dated in the future")
	f.Bool("minify", false, "collapse whitespace in generated HTML and CSS")
	f.Bool("pretty-urls", true, "emit /path/index.html instead of /path.html")
	f.Bool("clean", true, "clear the output directory before building")
	f.Bool("unsafe", false, "allow raw HTML embedded in Markdown")
	return cmd
}

func describeConfigPath(cfg *config.Site) string {
	if cfg.Path == "" {
		return "(none found, using defaults)"
	}
	return cfg.Path
}

// reportBuild prints the outcome of a build. Errors are left to Cobra so they
// are not reported twice.
func reportBuild(g *globalFlags, res *pipeline.Result) {
	if res == nil {
		return
	}
	if res.Site != nil {
		st := res.Site.Stats
		g.printf("assembled %d pages (%d posts, %d sections, %d tags, %d categories)\n",
			st.Pages, st.Regular, st.Sections, st.Tags, st.Categories)
	}
	g.printf("wrote %d pages, %d assets, %d static files (%s) in %s\n",
		res.PagesWritten, res.AssetsWritten, res.StaticCopied,
		humanBytes(res.Bytes), res.Duration.Round(time.Millisecond))
	if res.Redirects > 0 {
		g.printf("wrote %d redirect pages\n", res.Redirects)
	}
	for _, w := range res.Warnings {
		g.printf("warning: %s\n", w)
	}
	g.printf("output: %s\n", res.Output)
}
