package cli

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/casien/staticgen/internal/config"
	"github.com/casien/staticgen/internal/pipeline"
	"github.com/casien/staticgen/internal/serve"
)

// builderRef owns the live pipeline builder.
//
// Content edits only need another Build, but a config or template edit changes
// how pages are rendered, so Reload reconstructs every stage. Keeping both
// behind one mutex means a rebuild can never race a reload.
type builderRef struct {
	mu  sync.Mutex
	b   *pipeline.Builder
	cfg *config.Site

	g    *globalFlags
	over config.Overrides
	opts pipeline.Options
}

func (r *builderRef) Build(ctx context.Context) (*pipeline.Result, error) {
	r.mu.Lock()
	b := r.b
	r.mu.Unlock()
	return b.Build(ctx)
}

func (r *builderRef) Reload() error {
	cfg, err := r.g.load(r.over)
	if err != nil {
		return err
	}
	builder, err := pipeline.New(cfg, r.opts)
	if err != nil {
		return err
	}
	r.mu.Lock()
	previous := r.cfg
	r.b, r.cfg = builder, cfg
	r.mu.Unlock()

	if previous != nil && dirsChanged(previous, cfg) {
		// Watched directories were fixed when the server started, so a dirs
		// change cannot take effect without a restart. Saying so beats silently
		// watching the wrong tree.
		return errors.New("configuration reloaded, but dirs.* changed: restart serve to watch the new paths")
	}
	return nil
}

func dirsChanged(a, b *config.Site) bool {
	return a.ContentPath() != b.ContentPath() ||
		a.StaticPath() != b.StaticPath() ||
		a.TemplatesPath() != b.TemplatesPath() ||
		a.OutputPath() != b.OutputPath()
}

func newServeCmd(g *globalFlags) *cobra.Command {
	var host string
	var port int
	var noReload bool

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Build, watch and serve the site with live reload",
		Long: `Serve builds the site, then watches the content, static and template
directories and rebuilds on every change. Connected browsers reload automatically
over a Server-Sent Events stream.

A failed initial build does not stop the server: it keeps serving so an edit can
fix the problem and be picked up immediately.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			over := config.Overrides{
				Drafts: changedBool(cmd, "drafts"),
				Future: changedBool(cmd, "future"),
				Minify: changedBool(cmd, "minify"),
				Host:   host,
				Port:   port,
			}
			if cmd.Flags().Changed("no-reload") {
				live := !noReload
				over.LiveReload = &live
			}

			cfg, err := g.load(over)
			if err != nil {
				return err
			}

			opts := pipeline.Options{LiveReload: cfg.Serve.LiveReload}
			builder, err := pipeline.New(cfg, opts)
			if err != nil {
				return err
			}
			ref := &builderRef{b: builder, cfg: cfg, g: g, over: over, opts: opts}

			res, err := builder.Build(cmd.Context())
			reportBuild(g, res)
			if err != nil {
				// Keep serving: the watch loop will rebuild once the file is
				// fixed, which is the whole point of a dev server.
				g.printf("initial build failed; serving anyway so edits can fix it\n")
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			logf := func(format string, args ...any) {
				g.printf(format+"\n", args...)
			}

			srv, err := serve.New(serve.Options{
				Config:     cfg,
				OutputDir:  cfg.OutputPath(),
				WatchDirs:  []string{cfg.ContentPath(), cfg.StaticPath(), cfg.TemplatesPath()},
				ConfigPath: cfg.Path,
				Deps:       serve.Deps{Build: ref.Build, Reload: ref.Reload},
				Debounce:   time.Duration(cfg.Serve.DebounceMs) * time.Millisecond,
				LiveReload: cfg.Serve.LiveReload,
				Logf:       logf,
			})
			if err != nil {
				return err
			}

			g.printf("serving %s\n", cfg.OutputPath())
			if cfg.Serve.LiveReload {
				g.printf("live reload: enabled\n")
			} else {
				g.printf("live reload: disabled\n")
			}
			g.printf("press Ctrl-C to stop\n")

			err = srv.Start(ctx, func(url string) {
				g.printf("ready: %s\n", url)
			})
			if errors.Is(err, context.Canceled) {
				g.printf("stopped\n")
				return nil
			}
			return err
		},
	}

	f := cmd.Flags()
	f.StringVar(&host, "host", "", "address to bind (default 127.0.0.1)")
	f.IntVar(&port, "port", 0, "port to serve on (default 1313)")
	f.BoolVar(&noReload, "no-reload", false, "disable live reload")
	f.Bool("drafts", false, "include pages marked draft: true")
	f.Bool("future", false, "include pages dated in the future")
	f.Bool("minify", false, "collapse whitespace in generated output")
	return cmd
}
