// Package cli implements the staticgen command line interface.
//
// Commands live here and nothing else; every package under internal is free of
// Cobra so the build pipeline can be driven programmatically or from tests.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/casien/staticgen/internal/config"
)

// version is overridden at link time with -ldflags "-X .../cli.version=...".
var version = "dev"

// globalFlags holds the persistent flags shared by every command.
type globalFlags struct {
	configPath string
	source     string
	output     string
	baseURL    string
	verbose    bool
	out        io.Writer
}

// Execute runs the CLI and returns a process exit error.
func Execute() error {
	return newRootCommand().ExecuteContext(context.Background())
}

func newRootCommand() *cobra.Command {
	g := &globalFlags{out: os.Stdout}

	root := &cobra.Command{
		Use:   "staticgen",
		Short: "Build a static website from Markdown files",
		Long: `staticgen converts a directory of Markdown files into a complete static
website: HTML pages, tag and archive listings, an RSS feed, a sitemap and a
search index.

Typical use:

  staticgen build              # write the site to ./public
  staticgen serve              # build, watch, and serve with live reload
  staticgen new post "Title"   # scaffold a new Markdown file
  staticgen check              # report broken links and content problems`,
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: false,
		// The root command has no work of its own; running it bare should
		// show help rather than silently succeeding.
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	pf := root.PersistentFlags()
	pf.StringVarP(&g.configPath, "config", "c", "", "config file (default: auto-detect site.yaml)")
	pf.StringVarP(&g.source, "source", "s", "", "site root directory (default: current directory)")
	pf.StringVarP(&g.output, "output", "o", "", "output directory (overrides dirs.output)")
	pf.StringVarP(&g.baseURL, "base-url", "b", "", "absolute site URL, e.g. https://example.com")
	pf.BoolVarP(&g.verbose, "verbose", "v", false, "print detail about each build step")

	root.AddCommand(newBuildCmd(g))
	root.AddCommand(newServeCmd(g))
	root.AddCommand(newNewCmd(g))
	root.AddCommand(newCheckCmd(g))

	return root
}

// load resolves configuration by applying, in order: built-in defaults, the
// config file, then CLI flags.
func (g *globalFlags) load(over config.Overrides) (*config.Site, error) {
	cfg, err := config.Load(g.source, g.configPath)
	if err != nil {
		return nil, err
	}
	if g.baseURL != "" {
		over.BaseURL = g.baseURL
	}
	if g.output != "" {
		over.OutputDir = g.output
	}
	cfg.Apply(over)
	// Apply can introduce a value that needs re-validating, e.g. a base_url
	// supplied on the command line.
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// changedBool returns a pointer to a flag's value only when the user set it, so
// an unset flag cannot override the config file with its zero value.
func changedBool(cmd *cobra.Command, name string) *bool {
	if !cmd.Flags().Changed(name) {
		return nil
	}
	v, err := cmd.Flags().GetBool(name)
	if err != nil {
		return nil
	}
	return &v
}

// printf writes to the CLI's output stream.
func (g *globalFlags) printf(format string, args ...any) {
	fmt.Fprintf(g.out, format, args...)
}

// errf formats an error for display, stripping Cobra's own prefix noise.
func errf(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}

// humanBytes renders a byte count in the largest sensible unit.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// splitList parses a comma-separated flag value.
func splitList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
