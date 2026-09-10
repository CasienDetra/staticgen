package fsx

import "testing"

func TestURLPath(t *testing.T) {
	tests := []struct {
		name   string
		pretty bool
		rel    string
		slug   string
		want   string
	}{
		{"root index pretty", true, "index.md", "", "/"},
		{"root index flat", false, "index.md", "", "/"},
		{"root underscore index", true, "_index.md", "", "/"},
		{"root page pretty", true, "about.md", "", "/about/"},
		{"root page flat", false, "about.md", "", "/about.html"},
		{"section index pretty", true, "blog/_index.md", "", "/blog/"},
		// A section is a directory in the output tree either way, so its URL
		// keeps the trailing slash even with pretty URLs off.
		{"section index flat", false, "blog/_index.md", "", "/blog/"},
		{"post pretty", true, "blog/hello.md", "", "/blog/hello/"},
		{"post flat", false, "blog/hello.md", "", "/blog/hello.html"},
		{"date prefix stripped", true, "blog/2026-01-15-hello.md", "", "/blog/hello/"},
		{"slug override", true, "blog/hello.md", "custom-name", "/blog/custom-name/"},
		{"slug is normalised", true, "blog/hello.md", "My Great Post", "/blog/my-great-post/"},
		{"slug may nest", true, "blog/hello.md", "nested/deep", "/blog/nested/deep/"},
		{"deep nesting", true, "a/b/c/page.md", "", "/a/b/c/page/"},
		{"404 at root", true, "404.md", "", "/404.html"},
		{"404 at root flat", false, "404.md", "", "/404.html"},
		// A 404.md inside a section is an ordinary post: letting it claim the
		// site's error URL would collide with a root-level 404.md.
		{"404 in section is a normal post", true, "blog/404.md", "", "/blog/404/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := URLMapper{PrettyURLs: tt.pretty}
			if got := m.URLPath(tt.rel, tt.slug); got != tt.want {
				t.Errorf("URLPath(%q, %q) pretty=%v = %q, want %q", tt.rel, tt.slug, tt.pretty, got, tt.want)
			}
		})
	}
}

func TestOutputPath(t *testing.T) {
	tests := []struct {
		urlPath string
		want    string
	}{
		{"/", "index.html"},
		{"/about/", "about/index.html"},
		{"/about.html", "about.html"},
		{"/404.html", "404.html"},
		{"/blog/x/", "blog/x/index.html"},
		{"/blog/x.html", "blog/x.html"},
		{"/a/b/c/", "a/b/c/index.html"},
	}
	m := URLMapper{PrettyURLs: true}
	for _, tt := range tests {
		if got := m.OutputPath(tt.urlPath); got != tt.want {
			t.Errorf("OutputPath(%q) = %q, want %q", tt.urlPath, got, tt.want)
		}
	}
}

func TestOutputPathRoundTripsURLPath(t *testing.T) {
	// Every URL the mapper produces must map to a distinct output path, or two
	// pages would overwrite each other.
	for _, pretty := range []bool{true, false} {
		m := URLMapper{PrettyURLs: pretty}
		rels := []string{"index.md", "about.md", "404.md", "blog/_index.md", "blog/a.md", "blog/b.md", "x/y/z.md"}
		seen := map[string]string{}
		for _, rel := range rels {
			u := m.URLPath(rel, "")
			out := m.OutputPath(u)
			if prev, dup := seen[out]; dup {
				t.Errorf("pretty=%v: %q and %q both write %s", pretty, prev, rel, out)
			}
			seen[out] = rel
		}
	}
}

func TestSlugify(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Hello World", "hello-world"},
		{"  spaced  ", "spaced"},
		{"multiple---dashes", "multiple-dashes"},
		{"trailing-", "trailing"},
		{"-leading", "leading"},
		{"a/b", "a-b"},
		{"under_score", "under-score"},
		{"dots.in.name", "dots-in-name"},
		{"Ünïcödé keeps letters", "ünïcödé-keeps-letters"},
		{"emoji 🎉 dropped", "emoji-dropped"},
		{"", ""},
		{"!!!", ""},
	}
	for _, tt := range tests {
		if got := Slugify(tt.in); got != tt.want {
			t.Errorf("Slugify(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSplitDatePrefix(t *testing.T) {
	got := SplitDatePrefix("2026-01-15-hello.md")
	if !got.Found {
		t.Fatal("expected a date prefix")
	}
	if got.Year != 2026 || got.Month != 1 || got.Day != 15 {
		t.Errorf("date = %d-%02d-%02d, want 2026-01-15", got.Year, got.Month, got.Day)
	}
	if got.Rest != "hello.md" {
		t.Errorf("Rest = %q, want %q", got.Rest, "hello.md")
	}

	if dp := SplitDatePrefix("hello.md"); dp.Found {
		t.Errorf("hello.md should have no date prefix, got %+v", dp)
	}
	// A two-digit year is not the convention and must not be half-parsed.
	if dp := SplitDatePrefix("26-01-15-hello.md"); dp.Found {
		t.Errorf("26-01-15-hello.md should have no date prefix, got %+v", dp)
	}
}

func TestBaseSlug(t *testing.T) {
	tests := []struct{ rel, want string }{
		{"blog/hello.md", "hello"},
		{"blog/2026-01-15-hello.md", "hello"},
		{"Hello World.md", "hello-world"},
		{"a.markdown", "a"},
	}
	for _, tt := range tests {
		if got := BaseSlug(tt.rel); got != tt.want {
			t.Errorf("BaseSlug(%q) = %q, want %q", tt.rel, got, tt.want)
		}
	}
}

func TestShouldSkip(t *testing.T) {
	tests := []struct {
		rel  string
		want bool
	}{
		{"blog/hello.md", false},
		{"hello.md", false},
		{"blog/_index.md", false}, // section index is the documented exception
		{"_index.md", false},
		{"index.md", false},
		{"_drafts/wip.md", true},
		{"blog/_partial.md", true},
		{".hidden/x.md", true},
		{"blog/.secret.md", true},
	}
	for _, tt := range tests {
		if got := ShouldSkip(tt.rel); got != tt.want {
			t.Errorf("ShouldSkip(%q) = %v, want %v", tt.rel, got, tt.want)
		}
	}
}

func TestIsContent(t *testing.T) {
	for _, ext := range []string{"a.md", "a.markdown", "a.mdown", "A.MD"} {
		if !IsContent(ext) {
			t.Errorf("IsContent(%q) = false, want true", ext)
		}
	}
	for _, ext := range []string{"a.html", "a.txt", "a.png", "a"} {
		if IsContent(ext) {
			t.Errorf("IsContent(%q) = true, want false", ext)
		}
	}
}

func TestIsSectionIndex(t *testing.T) {
	for _, p := range []string{"_index.md", "index.md", "blog/_index.md", "blog/index.markdown"} {
		if !IsSectionIndex(p) {
			t.Errorf("IsSectionIndex(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"blog/post.md", "index.html"} {
		if IsSectionIndex(p) {
			t.Errorf("IsSectionIndex(%q) = true, want false", p)
		}
	}
}

func TestSection(t *testing.T) {
	tests := []struct{ rel, want string }{
		{"about.md", ""},
		{"index.md", ""},
		{"blog/post.md", "blog"},
		{"blog/nested/post.md", "blog"},
		{"a/b/c.md", "a"},
	}
	for _, tt := range tests {
		if got := Section(tt.rel); got != tt.want {
			t.Errorf("Section(%q) = %q, want %q", tt.rel, got, tt.want)
		}
	}
}

func TestWalkMissingRootIsNotAnError(t *testing.T) {
	// The static and templates directories are optional; a missing one must
	// yield no files rather than failing the build.
	var n int
	if err := Walk(t.TempDir()+"/does-not-exist", func(SourceFile) error {
		n++
		return nil
	}); err != nil {
		t.Fatalf("Walk on a missing directory: %v", err)
	}
	if n != 0 {
		t.Errorf("walked %d files in a missing directory, want 0", n)
	}
}
