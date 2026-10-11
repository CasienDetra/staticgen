package serve

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/casien/staticgen/internal/config"
	"github.com/casien/staticgen/internal/pipeline"
)

// fixtureServer builds an output tree on disk and returns a Server serving it.
func fixtureServer(t *testing.T, files map[string]string, live bool) *Server {
	t.Helper()
	out := t.TempDir()
	for name, body := range files {
		p := filepath.Join(out, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := config.Default()
	cfg.Root = t.TempDir()
	srv, err := New(Options{
		Config:    cfg,
		OutputDir: out,
		Deps: Deps{Build: func(context.Context) (*pipeline.Result, error) {
			return &pipeline.Result{}, nil
		}},
		LiveReload: live,
	})
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

func get(t *testing.T, srv *Server, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)
	return rec
}

func TestServesPrettyURLIndex(t *testing.T) {
	srv := fixtureServer(t, map[string]string{"blog/hello/index.html": "<h1>Hello</h1>"}, false)

	if rec := get(t, srv, "GET", "/blog/hello/"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Hello") {
		t.Errorf("GET /blog/hello/ = %d %q", rec.Code, rec.Body.String())
	}

	rec := get(t, srv, "GET", "/blog/hello")
	if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/blog/hello/" {
		t.Errorf("GET /blog/hello = %d, Location %q; want 301 to /blog/hello/", rec.Code, rec.Header().Get("Location"))
	}
}

func TestServesLegacyHTMLFallback(t *testing.T) {
	srv := fixtureServer(t, map[string]string{"legacy.html": "old page"}, false)

	if rec := get(t, srv, "GET", "/legacy"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "old page") {
		t.Errorf("GET /legacy = %d %q", rec.Code, rec.Body.String())
	}
}

func TestRejectsTraversal(t *testing.T) {
	srv := fixtureServer(t, map[string]string{"index.html": "home"}, false)

	// A sentinel outside the output tree must never be served. The handler is
	// called directly: the net/http mux cleans dot-dot segments into a redirect
	// before a handler ever runs, so only a direct call exercises this guard.
	parent := filepath.Dir(srv.outDir)
	if err := os.WriteFile(filepath.Join(parent, "secret.txt"), []byte("SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"/../secret.txt", "/blog/../../secret.txt"} {
		req := httptest.NewRequest("GET", "/", nil)
		req.URL.Path = target
		rec := httptest.NewRecorder()
		srv.handleFile(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", target, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "SECRET") {
			t.Errorf("GET %s leaked a file from outside the output tree", target)
		}
	}
}

func TestCustom404(t *testing.T) {
	srv := fixtureServer(t, map[string]string{"404.html": "custom missing page"}, false)

	rec := get(t, srv, "GET", "/nope/")
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "custom missing page") {
		t.Errorf("GET /nope/ = %d %q", rec.Code, rec.Body.String())
	}
}

func TestMethodNotAllowed(t *testing.T) {
	srv := fixtureServer(t, map[string]string{"index.html": "home"}, false)

	if rec := get(t, srv, "POST", "/"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST / = %d, want 405", rec.Code)
	}
}

func TestRebuildPublishesReload(t *testing.T) {
	srv := fixtureServer(t, map[string]string{"index.html": "home"}, true)

	ch := srv.broker.subscribe()
	defer srv.broker.unsubscribe(ch)

	built := false
	srv.deps.Build = func(context.Context) (*pipeline.Result, error) {
		built = true
		return &pipeline.Result{}, nil
	}
	srv.rebuild(context.Background(), false)

	if !built {
		t.Fatal("rebuild did not call the Build dependency")
	}
	select {
	case msg := <-ch:
		if msg != "reload" {
			t.Errorf("published %q, want reload", msg)
		}
	case <-time.After(2 * time.Second):
		t.Error("no reload notification after rebuild")
	}
}

// syncRecorder is a concurrency-safe ResponseRecorder for streaming handlers.
// The stock recorder buffers the body in a bytes.Buffer the handler keeps
// writing while the test polls it, which the race detector rightly flags.
type syncRecorder struct {
	*httptest.ResponseRecorder
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncRecorder) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncRecorder) bodyString() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func TestLiveReloadStreamsPublishedMessages(t *testing.T) {
	srv := fixtureServer(t, map[string]string{}, true)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", liveReloadPath, nil).WithContext(ctx)
	rec := &syncRecorder{ResponseRecorder: httptest.NewRecorder()}

	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.handler().ServeHTTP(rec, req)
	}()

	// Publish only after the handler has subscribed: the broker drops messages
	// for no subscribers, so publishing before the stream is open is a race.
	deadline := time.Now().Add(2 * time.Second)
	for srv.broker.Clients() == 0 {
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("stream handler never subscribed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	srv.broker.publish("reload")

	deadline = time.Now().Add(2 * time.Second)
	for !strings.Contains(rec.bodyString(), "data: reload") {
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatalf("stream never delivered the message, body %q", rec.bodyString())
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done

	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
}

func TestLiveReloadAbsentWhenDisabled(t *testing.T) {
	srv := fixtureServer(t, map[string]string{}, false)

	if rec := get(t, srv, "GET", liveReloadPath); rec.Code != http.StatusNotFound {
		t.Errorf("GET %s with live reload off = %d, want 404", liveReloadPath, rec.Code)
	}
}
