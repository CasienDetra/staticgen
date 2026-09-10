package serve

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/casien/staticgen/internal/config"
	"github.com/casien/staticgen/internal/pipeline"
)

// liveReloadPath is the SSE endpoint the theme's inline script connects to.
const liveReloadPath = "/__livereload"

// Deps are the callbacks the server uses to rebuild. They are supplied by the
// CLI so this package does not own pipeline construction.
type Deps struct {
	// Build runs the pipeline once.
	Build func(ctx context.Context) (*pipeline.Result, error)
	// Reload re-reads the config file and rebuilds every stage, including the
	// template engine. Required for config and template edits to take effect;
	// may be nil, in which case those changes need a restart.
	Reload func() error
}

// Options configures a Server.
type Options struct {
	Config    *config.Site
	OutputDir string
	// WatchDirs are the source trees to watch: content, static and templates.
	WatchDirs  []string
	ConfigPath string
	Deps       Deps
	Debounce   time.Duration
	LiveReload bool
	Logf       func(string, ...any)
}

// Server serves the build output and rebuilds it when sources change.
type Server struct {
	cfg        *config.Site
	outDir     string
	watchDirs  []string
	configPath string
	tmplDir    string
	deps       Deps
	debounce   time.Duration
	live       bool
	logf       func(string, ...any)

	watch  *Watcher
	broker *broker
	http   *http.Server
	addr   string

	// mu serialises rebuilds against request handling. Without it a browser
	// refreshing mid-rebuild could be served a 404 for a page that exists,
	// because the output directory is cleaned before it is repopulated.
	mu sync.RWMutex
}

// New validates options and returns a Server. It does not bind a port.
func New(opts Options) (*Server, error) {
	if opts.Config == nil {
		return nil, errors.New("serve: config is required")
	}
	if opts.OutputDir == "" {
		return nil, errors.New("serve: output directory is required")
	}
	if opts.Deps.Build == nil {
		return nil, errors.New("serve: a Build function is required")
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	debounce := opts.Debounce
	if debounce <= 0 {
		debounce = 100 * time.Millisecond
	}
	return &Server{
		cfg:        opts.Config,
		outDir:     opts.OutputDir,
		watchDirs:  opts.WatchDirs,
		configPath: opts.ConfigPath,
		tmplDir:    opts.Config.TemplatesPath(),
		deps:       opts.Deps,
		debounce:   debounce,
		live:       opts.LiveReload,
		logf:       logf,
		broker:     newBroker(),
	}, nil
}

// Address returns the listening address; valid only after Start has called
// onReady.
func (s *Server) Address() string { return s.addr }

// Start listens, serves and watches until ctx is cancelled. onReady is called
// once the port is bound, with the URL to open.
func (s *Server) Start(ctx context.Context, onReady func(url string)) error {
	watcher, err := NewWatcher(s.logf, s.isIgnored)
	if err != nil {
		return err
	}
	defer watcher.Close()
	s.watch = watcher

	for _, dir := range s.watchDirs {
		if err := watcher.AddTree(dir); err != nil {
			s.logf("watch %s: %v", dir, err)
		}
	}
	if err := watcher.AddFile(s.configPath); err != nil {
		s.logf("watch %s: %v", s.configPath, err)
	}

	host := s.cfg.Serve.Host
	if host == "" {
		host = "127.0.0.1"
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(s.cfg.Serve.Port)))
	if err != nil {
		return fmt.Errorf("listen on %s:%d: %w", host, s.cfg.Serve.Port, err)
	}
	s.addr = "http://" + ln.Addr().String()

	s.http = &http.Server{
		Handler: s.handler(),
		// A dev server should not let a slow client hold a goroutine forever.
		ReadHeaderTimeout: 15 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		err := s.http.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveErr <- err
	}()

	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		s.watchLoop(ctx)
	}()

	if onReady != nil {
		onReady(s.addr)
	}
	s.logf("watching %d director%s", len(watcher.Watched()), plural(len(watcher.Watched()), "y", "ies"))

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.http.Shutdown(shutdownCtx); err != nil {
		s.logf("shutdown: %v", err)
	}
	<-watchDone
	return ctx.Err()
}

func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	if s.live {
		mux.HandleFunc(liveReloadPath, s.handleLiveReload)
	}
	mux.HandleFunc("/", s.handleFile)
	return mux
}

// handleFile serves the output directory with pretty-URL resolution:
//
//	/blog/hello/        -> blog/hello/index.html
//	/blog/hello         -> blog/hello/index.html (via 301 to the slash form)
//	/feed.xml           -> feed.xml
//	/legacy             -> legacy.html, when no directory matches
func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	root, err := filepath.Abs(s.outDir)
	if err != nil {
		http.Error(w, "server misconfigured", http.StatusInternalServerError)
		return
	}

	// Capture the trailing slash before cleaning: path.Clean removes it, and it
	// is the only thing that distinguishes "/blog/" (serve the index) from
	// "/blog" (redirect to the slash form).
	hadSlash := strings.HasSuffix(r.URL.Path, "/")

	urlPath := path.Clean(r.URL.Path)
	if !strings.HasPrefix(urlPath, "/") {
		urlPath = "/" + urlPath
	}
	rel := strings.TrimPrefix(urlPath, "/")
	target := filepath.Join(root, filepath.FromSlash(rel))

	// Reject anything that resolved outside the output tree.
	if target != root && !strings.HasPrefix(target, root+string(filepath.Separator)) {
		http.NotFound(w, r)
		return
	}

	if rel == "" || hadSlash {
		index := filepath.Join(target, "index.html")
		if isFile(index) {
			s.serveFile(w, r, index)
			return
		}
		s.notFound(w, r, root)
		return
	}

	switch {
	case isFile(target):
		s.serveFile(w, r, target)
	case isDir(target):
		// Redirect to the trailing-slash form so relative asset links in the
		// served document resolve correctly.
		http.Redirect(w, r, urlPath+"/", http.StatusMovedPermanently)
	case isFile(target + ".html"):
		s.serveFile(w, r, target+".html")
	default:
		s.notFound(w, r, root)
	}
}

// serveFile serves a file from the output tree.
//
// It deliberately uses http.ServeContent rather than http.ServeFile. ServeFile
// canonicalises the request URL against the file it was handed, so serving
// blog/index.html for a request of /blog/ makes it emit a 301 to a path derived
// from that filename — which resolves back to /blog/ and loops forever.
// ServeContent performs no such redirect, and this handler has already decided
// which URL maps to which file.
func (s *Server) serveFile(w http.ResponseWriter, r *http.Request, file string) {
	f, err := os.Open(file)
	if err != nil {
		s.notFound(w, r, filepath.Dir(file))
		return
	}
	defer func() { _ = f.Close() }()

	st, err := f.Stat()
	if err != nil {
		s.notFound(w, r, filepath.Dir(file))
		return
	}
	http.ServeContent(w, r, filepath.Base(file), st.ModTime(), f)
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request, root string) {
	custom := filepath.Join(root, "404.html")
	data, err := os.ReadFile(custom)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// Written by hand rather than delegated to net/http, which would emit a
	// second WriteHeader and log a superfluous-header warning.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write(data)
}

// handleLiveReload streams reload notifications over Server-Sent Events.
func (s *Server) handleLiveReload(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// Stops nginx and similar proxies from buffering the stream.
	w.Header().Set("X-Accel-Buffering", "no")

	ch := s.broker.subscribe()
	defer s.broker.unsubscribe(ch)

	// A leading comment flushes headers and confirms the stream is open.
	if _, err := fmt.Fprint(w, ": connected\n\n"); err != nil {
		return
	}
	flusher.Flush()

	// Heartbeats keep intermediaries from closing an idle connection and let
	// the browser notice a dead server.
	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-ch:
			if _, err := fmt.Fprintf(w, "data: %s\n\n", msg); err != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// watchLoop coalesces filesystem events into debounced rebuilds.
func (s *Server) watchLoop(ctx context.Context) {
	var timer *time.Timer
	fired := make(chan struct{}, 1)
	// reloadNeeded latches whether a config or template change was seen during
	// the current debounce window.
	reloadNeeded := false

	stopTimer := func() {
		if timer != nil {
			timer.Stop()
			timer = nil
		}
	}
	defer stopTimer()

	for {
		select {
		case <-ctx.Done():
			stopTimer()
			return

		case event, ok := <-s.watch.Events():
			if !ok {
				return
			}
			// Chmod-only events are noise: editors and git touch modes without
			// changing content, and reacting to them causes pointless rebuilds.
			if event.Has(fsnotify.Chmod) && !event.Has(fsnotify.Write) {
				continue
			}
			if s.isIgnored(event.Name) || isTempFile(event.Name) {
				continue
			}
			if event.Has(fsnotify.Create) {
				// A new subdirectory is invisible to fsnotify until added.
				s.watch.AddIfDir(event.Name)
			}
			if s.needsReload(event.Name) {
				reloadNeeded = true
			}

			if timer == nil {
				timer = time.AfterFunc(s.debounce, func() {
					select {
					case fired <- struct{}{}:
					default:
					}
				})
			} else {
				timer.Reset(s.debounce)
			}

		case err := <-s.watch.Errors():
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				s.logf("watch: event overflow, some changes may be missed")
			} else {
				s.logf("watch error: %v", err)
			}

		case <-fired:
			reload := reloadNeeded
			reloadNeeded = false
			s.rebuild(ctx, reload)
		}
	}
}

// rebuild runs the pipeline and notifies browsers. The output lock is held for
// the duration so requests are never served from a half-written tree.
func (s *Server) rebuild(ctx context.Context, reload bool) {
	start := time.Now()

	if reload && s.deps.Reload != nil {
		if err := s.deps.Reload(); err != nil {
			s.logf("reload failed: %v", err)
			return
		}
		s.logf("reloaded config and templates")
	}

	s.mu.Lock()
	res, err := s.deps.Build(ctx)
	s.mu.Unlock()

	if err != nil {
		s.logf("rebuild failed: %v", err)
		// Still notify: the browser may be showing a stale page and the user
		// should see the failure reflected rather than silently kept old output.
		return
	}

	s.logf("rebuilt %d pages in %s", res.PagesWritten, time.Since(start).Round(time.Millisecond))
	for _, w := range res.Warnings {
		s.logf("warning: %s", w)
	}
	if s.live {
		s.broker.publish("reload")
	}
}

// needsReload reports whether a changed path requires rebuilding the pipeline
// stages rather than merely re-running a build.
func (s *Server) needsReload(changed string) bool {
	abs, err := filepath.Abs(changed)
	if err != nil {
		return false
	}
	if s.configPath != "" {
		cfgAbs, err := filepath.Abs(s.configPath)
		if err == nil && abs == cfgAbs {
			return true
		}
	}
	return underDir(abs, s.tmplDir)
}

// isIgnored reports whether a path is inside the build output, which must never
// trigger a rebuild or the server would loop forever.
func (s *Server) isIgnored(p string) bool {
	abs, err := filepath.Abs(p)
	if err != nil {
		return false
	}
	return underDir(abs, s.outDir)
}

func underDir(abs, dir string) bool {
	if dir == "" {
		return false
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	return abs == root || strings.HasPrefix(abs, root+string(filepath.Separator))
}

// isTempFile filters editor swap files, atomic-write temporaries and VCS noise.
func isTempFile(p string) bool {
	base := filepath.Base(p)
	switch {
	case strings.HasPrefix(base, "."), strings.HasSuffix(base, "~"):
		return true
	case strings.HasPrefix(base, "#") && strings.HasSuffix(base, "#"):
		return true
	}
	return filepath.Ext(base) == ".swp" || filepath.Ext(base) == ".tmp"
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// broker fans reload notifications out to connected browsers.
type broker struct {
	mu      sync.Mutex
	clients map[chan string]struct{}
}

func newBroker() *broker {
	return &broker{clients: map[chan string]struct{}{}}
}

func (b *broker) subscribe() chan string {
	ch := make(chan string, 4)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *broker) unsubscribe(ch chan string) {
	b.mu.Lock()
	if _, ok := b.clients[ch]; ok {
		delete(b.clients, ch)
		close(ch)
	}
	b.mu.Unlock()
}

func (b *broker) publish(msg string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.clients {
		// Never block a rebuild on a slow client; a missed reload is recovered
		// by the next one.
		select {
		case ch <- msg:
		default:
		}
	}
}

// Clients reports the number of connected browsers, for diagnostics and tests.
func (b *broker) Clients() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.clients)
}
