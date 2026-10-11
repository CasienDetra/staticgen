package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runCLI executes the root command with args and returns everything it
// printed. Commands report through the flag writer and some write straight to
// stdout, so both are captured.
func runCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w

	root := newRootCommand()
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	execErr := root.Execute()

	os.Stdout = stdout
	w.Close()
	captured, _ := io.ReadAll(r)
	r.Close()

	return out.String() + string(captured), execErr
}

// site writes a minimal site and returns its root.
func site(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(name, body string) {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("site.yaml", "title: Test Site\nbase_url: https://example.com\n")
	write("content/_index.md", "---\ntitle: Home\ndescription: The front page.\n---\nWelcome home.")
	write("content/about.md", "---\ntitle: About\ndescription: About this site.\n---\nAbout body.")
	return root
}

func TestBuildCommandWritesSite(t *testing.T) {
	root := site(t)
	out := filepath.Join(t.TempDir(), "out")

	got, err := runCLI(t, "build", "-s", root, "-o", out)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	for _, want := range []string{"assembled", "wrote", "output: " + out} {
		if !strings.Contains(got, want) {
			t.Errorf("build output missing %q\n%s", want, got)
		}
	}
	for _, name := range []string{"index.html", "about/index.html", "feed.xml", "sitemap.xml", "search.json"} {
		if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(name))); err != nil {
			t.Errorf("build did not produce %s", name)
		}
	}
}

// A base_url given only on the command line must reach the feed and sitemap,
// which cannot be produced without it.
func TestBuildCommandBaseURLFlagEnablesFeeds(t *testing.T) {
	root := t.TempDir()
	write := func(name, body string) {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// No base_url in site.yaml at all.
	write("site.yaml", "title: No Base\n")
	write("content/_index.md", "---\ntitle: Home\n---\nHello.")
	out := filepath.Join(t.TempDir(), "out")

	if _, err := runCLI(t, "build", "-s", root, "-o", out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "feed.xml")); !os.IsNotExist(err) {
		t.Error("without a base_url the feed should be skipped")
	}

	if _, err := runCLI(t, "build", "-s", root, "-o", out, "-b", "https://example.com"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(out, "feed.xml"))
	if err != nil {
		t.Fatalf("feed not produced after -b: %v", err)
	}
	if !strings.Contains(string(data), "https://example.com") {
		t.Error("feed should use the base_url from the flag")
	}
}

func TestCheckCommandReportsBrokenLinks(t *testing.T) {
	root := site(t)
	path := filepath.Join(root, "content", "broken.md")
	if err := os.WriteFile(path, []byte("---\ntitle: Broken\ndescription: d\n---\nSee [nowhere](/missing/).\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := runCLI(t, "check", "-s", root)
	if err == nil {
		t.Fatal("check should fail on a broken link")
	}
	if !strings.Contains(err.Error(), "1 error") {
		t.Errorf("error should report the count, got %v", err)
	}
}

// Warnings alone pass by default and fail the deploy gate under --strict.
func TestCheckCommandStrictGatesOnWarnings(t *testing.T) {
	root := site(t)
	path := filepath.Join(root, "content", "nodesc.md")
	if err := os.WriteFile(path, []byte("---\ntitle: No Description\n---\nBody without a description.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := runCLI(t, "check", "-s", root); err != nil {
		t.Fatalf("warnings should not fail a plain check: %v", err)
	}
	_, err := runCLI(t, "check", "-s", root, "--strict")
	if err == nil {
		t.Fatal("--strict should fail on a warning")
	}
	if !strings.Contains(err.Error(), "--strict") {
		t.Errorf("strict failure should mention the flag, got %v", err)
	}
}

func TestCheckCommandCleanSite(t *testing.T) {
	root := site(t)
	got, err := runCLI(t, "check", "-s", root)
	if err != nil {
		t.Fatalf("check on a clean site: %v", err)
	}
	if !strings.Contains(got, "no problems found") {
		t.Errorf("expected a clean report, got %q", got)
	}
}

func TestNewPostCommand(t *testing.T) {
	root := site(t)

	if _, err := runCLI(t, "new", "post", "Hello: World", "-s", root,
		"--section", "blog", "--tags", "go,tips", "--categories", "notes",
		"--date", "2026-01-02"); err != nil {
		t.Fatalf("new post: %v", err)
	}

	// new post prefixes the filename with the date, so find it rather than
	// hard-coding a slug.
	data, err := os.ReadFile(filepath.Join(root, "content", "blog", "2026-01-02-hello-world.md"))
	if err != nil {
		t.Fatalf("post not created in the section: %v", err)
	}
	body := string(data)
	for _, want := range []string{
		"title: \"Hello: World\"", // a colon must not break the YAML
		"draft: true",
		"tags: [\"go\", \"tips\"]",
		"categories: [\"notes\"]",
		"2026-01-02",
		"<!--more-->",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scaffold missing %q\n%s", want, body)
		}
	}

	// A second run must not clobber existing work.
	if _, err := runCLI(t, "new", "post", "Hello: World", "-s", root, "--section", "blog", "--date", "2026-01-02"); err == nil {
		t.Error("creating an existing post should fail without --force")
	}
	if _, err := runCLI(t, "new", "post", "Hello: World", "-s", root, "--section", "blog", "--date", "2026-01-02", "--force"); err != nil {
		t.Errorf("--force should overwrite: %v", err)
	}

	// --published drops the draft marker.
	if _, err := runCLI(t, "new", "post", "Live Post", "-s", root, "--published"); err != nil {
		t.Fatal(err)
	}
	live, err := os.ReadFile(filepath.Join(root, "content", "2026-10-11-live-post.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(live), "draft: true") {
		t.Error("--published should not mark the post as a draft")
	}
}

func TestNewPostRejectsBadDate(t *testing.T) {
	root := site(t)
	if _, err := runCLI(t, "new", "post", "Bad Date", "-s", root, "--date", "not-a-date"); err == nil {
		t.Fatal("an unparseable date should fail")
	}
}

// The scaffolded post must be something the generator can actually build.
func TestNewPostBuilds(t *testing.T) {
	root := site(t)
	if _, err := runCLI(t, "new", "post", "Round Trip", "-s", root, "--section", "blog", "--published"); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "out")
	if _, err := runCLI(t, "build", "-s", root, "-o", out); err != nil {
		t.Fatalf("a scaffolded post should build: %v", err)
	}
	// Not --strict: the scaffold deliberately ships an empty description,
	// which check reports as a warning.
	if _, err := runCLI(t, "check", "-s", root); err != nil {
		t.Fatalf("a scaffolded post should pass check: %v", err)
	}
}

func TestParseDate(t *testing.T) {
	for _, in := range []string{"2026-01-02", "2026-01-02T15:04:05", "2026-01-02 15:04", "2026-01-02T15:04:05Z"} {
		if _, err := parseDate(in); err != nil {
			t.Errorf("parseDate(%q): %v", in, err)
		}
	}
	if _, err := parseDate(" 2026-01-02 "); err != nil {
		t.Errorf("surrounding space should be tolerated: %v", err)
	}
	for _, bad := range []string{"", "tomorrow", "02-01-2026"} {
		if _, err := parseDate(bad); err == nil {
			t.Errorf("parseDate(%q) should fail", bad)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	for _, tc := range []struct {
		in   int64
		want string
	}{
		{512, "512 B"},
		{2048, "2.0 KiB"},
		{5 * 1024 * 1024, "5.0 MiB"},
	} {
		if got := humanBytes(tc.in); got != tc.want {
			t.Errorf("humanBytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRootCommandHelp(t *testing.T) {
	got, err := runCLI(t)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"build", "serve", "check", "new"} {
		if !strings.Contains(got, want) {
			t.Errorf("help output missing the %q command", want)
		}
	}
}
