package content

import (
	"strings"
	"testing"
	"time"
)

func TestSplit(t *testing.T) {
	tests := []struct {
		name      string
		src       string
		wantOK    bool
		wantFront string
		wantBody  string
	}{
		{
			name:      "standard block",
			src:       "---\ntitle: Hello\n---\n\nBody text.\n",
			wantOK:    true,
			wantFront: "title: Hello\n",
			wantBody:  "\nBody text.\n",
		},
		{
			name:     "no frontmatter",
			src:      "# Just a heading\n\ntext\n",
			wantOK:   false,
			wantBody: "# Just a heading\n\ntext\n",
		},
		{
			name:     "horizontal rule is not frontmatter",
			src:      "--- not a fence\n\ntext\n",
			wantOK:   false,
			wantBody: "--- not a fence\n\ntext\n",
		},
		{
			// A table or rule starting a document must not swallow the page.
			name:     "bare dashes at start of line",
			src:      "---\n| a | b |\n",
			wantOK:   false,
			wantBody: "---\n| a | b |\n",
		},
		{
			name:      "ellipsis terminator",
			src:       "---\ntitle: X\n...\nBody.\n",
			wantOK:    true,
			wantFront: "title: X\n",
			wantBody:  "Body.\n",
		},
		{
			name:      "empty frontmatter block",
			src:       "---\n---\nBody.\n",
			wantOK:    true,
			wantFront: "",
			wantBody:  "Body.\n",
		},
		{
			name:      "crlf line endings",
			src:       "---\r\ntitle: X\r\n---\r\nBody.\r\n",
			wantOK:    true,
			wantFront: "title: X\r\n",
			wantBody:  "Body.\r\n",
		},
		{
			name:      "trailing whitespace on the closing fence",
			src:       "---\ntitle: X\n---   \nBody.\n",
			wantOK:    true,
			wantFront: "title: X\n",
			wantBody:  "Body.\n",
		},
		{
			// An unterminated fence is reported as "no frontmatter" rather than
			// an error: treating the rest of the file as metadata would silently
			// delete the page's content.
			name:     "unterminated fence keeps the whole document as body",
			src:      "---\ntitle: X\n\nBody with no closing fence.\n",
			wantOK:   false,
			wantBody: "---\ntitle: X\n\nBody with no closing fence.\n",
		},
		{
			name:      "dashes inside the body do not terminate early",
			src:       "---\ntitle: X\n---\nSome --- dashes --- here.\n",
			wantOK:    true,
			wantFront: "title: X\n",
			wantBody:  "Some --- dashes --- here.\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			front, body, ok := Split([]byte(tt.src))
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if string(front) != tt.wantFront {
				t.Errorf("front = %q, want %q", front, tt.wantFront)
			}
			if string(body) != tt.wantBody {
				t.Errorf("body = %q, want %q", body, tt.wantBody)
			}
		})
	}
}

func TestParseMetaFields(t *testing.T) {
	src := `
title: A Title
description: Short blurb
summary: Explicit excerpt
slug: custom-slug
layout: wide
date: 2026-03-04T05:06:07Z
lastmod: 2026-03-05
draft: true
featured: true
weight: 7
tags: [go, testing]
categories:
  - engineering
aliases: [/old/, /older/]
redirect: https://elsewhere.example/x
toc: false
`
	m, err := ParseMeta([]byte(src))
	if err != nil {
		t.Fatalf("ParseMeta: %v", err)
	}

	if m.Title != "A Title" {
		t.Errorf("Title = %q", m.Title)
	}
	if m.Description != "Short blurb" {
		t.Errorf("Description = %q", m.Description)
	}
	if m.Slug != "custom-slug" {
		t.Errorf("Slug = %q", m.Slug)
	}
	if m.Layout != "wide" {
		t.Errorf("Layout = %q", m.Layout)
	}
	if m.Date.Year() != 2026 || m.Date.Month() != time.March || m.Date.Day() != 4 {
		t.Errorf("Date = %v, want 2026-03-04", m.Date)
	}
	if !m.HasDate() {
		t.Error("HasDate = false, want true")
	}
	if m.LastMod.Day() != 5 {
		t.Errorf("LastMod = %v, want day 5", m.LastMod)
	}
	if !m.Draft || !m.Featured {
		t.Errorf("Draft/Featured = %v/%v, want true/true", m.Draft, m.Featured)
	}
	if m.Weight != 7 {
		t.Errorf("Weight = %d, want 7", m.Weight)
	}
	if strings.Join(m.Tags, ",") != "go,testing" {
		t.Errorf("Tags = %v", m.Tags)
	}
	if strings.Join(m.Categories, ",") != "engineering" {
		t.Errorf("Categories = %v", m.Categories)
	}
	if len(m.Aliases) != 2 {
		t.Errorf("Aliases = %v", m.Aliases)
	}
	if m.Redirect != "https://elsewhere.example/x" {
		t.Errorf("Redirect = %q", m.Redirect)
	}
	if m.TOC == nil || *m.TOC {
		t.Errorf("TOC = %v, want pointer to false", m.TOC)
	}
}

func TestParseMetaCapturesUnknownKeys(t *testing.T) {
	// Themes need site-specific frontmatter without this package knowing the
	// keys ahead of time.
	m, err := ParseMeta([]byte("title: X\ncustom_thing: 42\nnested:\n  a: b\n"))
	if err != nil {
		t.Fatalf("ParseMeta: %v", err)
	}
	if m.Title != "X" {
		t.Errorf("Title = %q", m.Title)
	}
	if got := m.Extra["custom_thing"]; got != 42 {
		t.Errorf("Extra[custom_thing] = %#v, want 42", got)
	}
	nested, ok := m.Extra["nested"].(map[string]any)
	if !ok {
		t.Fatalf("Extra[nested] = %#v, want a map", m.Extra["nested"])
	}
	if nested["a"] != "b" {
		t.Errorf("Extra[nested][a] = %#v, want \"b\"", nested["a"])
	}
}

func TestParseMetaEmptyAndInvalid(t *testing.T) {
	if m, err := ParseMeta(nil); err != nil || m.Title != "" {
		t.Errorf("ParseMeta(nil) = %+v, %v; want zero Meta and no error", m, err)
	}
	if m, err := ParseMeta([]byte("   \n")); err != nil || m.Title != "" {
		t.Errorf("ParseMeta(blank) = %+v, %v; want zero Meta and no error", m, err)
	}
	if _, err := ParseMeta([]byte("title: [unclosed\n")); err == nil {
		t.Error("ParseMeta on invalid YAML should fail")
	}
}

func TestMetaNormalizesLists(t *testing.T) {
	m, err := ParseMeta([]byte("tags:\n  - \" go \"\n  - go\n  - \"\"\n  - testing\n"))
	if err != nil {
		t.Fatalf("ParseMeta: %v", err)
	}
	// Trimmed, de-duplicated and empties dropped, in first-seen order.
	if got := strings.Join(m.Tags, "|"); got != "go|testing" {
		t.Errorf("Tags = %q, want %q", got, "go|testing")
	}
}

func TestMetaIsExpired(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	m, err := ParseMeta([]byte("expires: 2026-05-01T00:00:00Z\n"))
	if err != nil {
		t.Fatalf("ParseMeta: %v", err)
	}
	if !m.IsExpired(now) {
		t.Error("a page expiring in the past should be expired")
	}

	m, err = ParseMeta([]byte("expires: 2026-07-01T00:00:00Z\n"))
	if err != nil {
		t.Fatalf("ParseMeta: %v", err)
	}
	if m.IsExpired(now) {
		t.Error("a page expiring in the future should not be expired")
	}

	m, err = ParseMeta([]byte("title: no expiry\n"))
	if err != nil {
		t.Fatalf("ParseMeta: %v", err)
	}
	if m.IsExpired(now) {
		t.Error("a page with no expires key should never be expired")
	}
}
