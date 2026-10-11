package config

import (
	"os"
	"path/filepath"
	"testing"
)

// A feed toggle the author set must survive a base_url override.
func TestFeedTogglesSurviveBaseURLOverride(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		yaml                   string
		wantRSS, wantSitemapOn bool
	}{
		{"defaults kept", "title: T\n", true, true},
		{"rss off", "title: T\nfeeds:\n  rss: false\n", false, true},
		{"sitemap off", "title: T\nfeeds:\n  sitemap: false\n", true, false},
		{"both off", "title: T\nfeeds:\n  rss: false\n  sitemap: false\n", false, false},
		{"both on", "title: T\nfeeds:\n  rss: true\n  sitemap: true\n", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "site.yaml")
			if err := os.WriteFile(p, []byte(tc.yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(dir, p)
			if err != nil {
				t.Fatal(err)
			}
			// Without a base_url both are suppressed; -b must bring back
			// only what the author wanted.
			if cfg.Feeds.RSS || cfg.Feeds.Sitemap {
				t.Errorf("feeds should be suppressed before the override: rss=%v sitemap=%v",
					cfg.Feeds.RSS, cfg.Feeds.Sitemap)
			}
			cfg.Apply(Overrides{BaseURL: "https://example.com"})
			if cfg.Feeds.RSS != tc.wantRSS {
				t.Errorf("RSS = %v, want %v", cfg.Feeds.RSS, tc.wantRSS)
			}
			if cfg.Feeds.Sitemap != tc.wantSitemapOn {
				t.Errorf("Sitemap = %v, want %v", cfg.Feeds.Sitemap, tc.wantSitemapOn)
			}
		})
	}
}

// No config file at all must still get feeds once a base_url is supplied.
func TestFeedsEnabledByFlagWithoutConfig(t *testing.T) {
	cfg, err := Load(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Apply(Overrides{BaseURL: "https://example.com"})
	if !cfg.Feeds.RSS || !cfg.Feeds.Sitemap {
		t.Errorf("defaults should apply with -b: rss=%v sitemap=%v", cfg.Feeds.RSS, cfg.Feeds.Sitemap)
	}
}

// Default() and Apply() used directly, without Load, must keep the feeds.
func TestDefaultKeepsFeedsOnBaseURLOverride(t *testing.T) {
	cfg := Default()
	cfg.Apply(Overrides{BaseURL: "https://example.com"})
	if !cfg.Feeds.RSS || !cfg.Feeds.Sitemap {
		t.Errorf("rss=%v sitemap=%v, want both true", cfg.Feeds.RSS, cfg.Feeds.Sitemap)
	}
}
