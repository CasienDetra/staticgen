# staticgen

A static site generator written in Go. It turns a directory of Markdown files
into a complete website: HTML pages, tag and category listings, an archive, an
RSS feed, a sitemap and a client-side search index.

```
staticgen build              # write the site to ./public
staticgen serve              # build, watch, and serve with live reload
staticgen new post "Title"   # scaffold a new Markdown file
staticgen check              # report broken links and content problems
```

## Features

- **Markdown rendering** — [goldmark](https://github.com/yuin/goldmark) with
  GFM extensions, a typographer, optional raw HTML, and syntax highlighting
  via [chroma](https://github.com/alecthomas/chroma) (35+ styles).
- **Taxonomies and listings** — tag and category pages, per-section listing
  pages, a chronological archive, and a home page with recent posts.
- **Generated artefacts** — RSS 2.0 feed, XML sitemap, and a JSON search
  index fetched by the theme's search script.
- **Dev server** — rebuilds on every file change and live-reloads browsers
  over Server-Sent Events.
- **Link checking** — `staticgen check` renders the site in memory and
  reports broken internal links, dead images, dangling anchors, and content
  problems. Exits non-zero so it can gate a deploy.
- **Publishing niceties** — pretty URLs, minification, redirects from
  `aliases`, drafts and future-dated posts excluded by default.

## Installation

Requires Go 1.27+.

```sh
go install github.com/casien/staticgen/cmd/staticgen@latest
```

## Quick start

```sh
git clone https://github.com/casien/staticgen example && cd example && rm -rf .git
staticgen serve               # open http://127.0.0.1:1313
```

A site is a directory with:

```
mysite/
├── site.yaml       # site configuration
├── content/        # Markdown pages
├── static/         # files copied verbatim to the output
└── templates/      # theme templates (optional; a default theme is built in)
```

### Writing content

Each file is Markdown with optional YAML frontmatter:

```markdown
---
title: Hello, world
description: The first post.
date: 2026-09-10T09:00:00Z
tags: [getting-started]
draft: false
---

Body text in Markdown.
```

Recognised frontmatter keys: `title`, `description`, `summary`, `slug`,
`layout`, `date`, `lastmod`, `expires`, `draft`, `featured`, `weight`,
`tags`, `categories`, `aliases`, `redirect`, `toc`. Any other key is passed
through to templates untouched.

- A file at `content/blog/hello.md` renders at `/blog/hello/`.
- A directory with an `_index.md` becomes a section with a listing page.
- Root-level files like `content/about.md` render at `/about/`.
- `content/404.md` is served as the not-found page.

### Configuration

`site.yaml` controls everything; every key has a sensible default:

```yaml
title: Field Notes
base_url: https://example.com   # required for RSS and the sitemap
language: en-us

author:
  name: Casien
  email: hello@example.com

dirs:
  content: content
  static: static
  templates: templates
  output: public

build:
  drafts: false        # include draft pages
  future: false        # include pages dated in the future
  minify: false        # minify HTML/CSS/JS on write
  pretty_urls: true    # /page/ instead of /page.html
  clean_output: true   # clear the output directory before building
  toc: true            # tables of contents
  paginate: 0          # posts per home/section page; 0 disables pagination

markup:
  unsafe: false        # allow raw HTML in Markdown
  highlight:
    enabled: true
    style: github

feeds:
  rss: true
  sitemap: true
  search: true

serve:
  port: 1313
  live_reload: true

menu:
  - name: Blog
    url: /blog/
    weight: 2
```

### Listing pagination

Set `build.paginate: 10` to show up to ten posts per home or section listing
page. Pagination is disabled by default (`0`); listings that fit on one page
stay unchanged. Tags, categories, and archives are not paginated.

Page one keeps its original URL (`/` or `/blog/`). Later pages use `/page/2/`,
`/page/3/`, or `/blog/page/2/`, and so on—even with `pretty_urls: false`.
The default theme shows previous/next links and a “Page N of M” indicator.
Numbered URLs are stable, but adding posts can shift which posts they contain.

An index file's introduction and aliases appear only on page one. Generated
pagination URLs must not collide with content URLs. Pagination pages appear in
the sitemap, but later pages are excluded from the search index to avoid
repeated listing results. RSS retains the full post collection, subject to its
existing item limit. Keep `clean_output: true` to remove obsolete pagination
pages when the page count shrinks or pagination is disabled.

Custom home and section templates should iterate `.Page.Pages` for the current
slice and include `{{ template "partials/pagination.html" . }}` for navigation.
`.Page.Pagination` is nil unless the listing spans multiple pages; otherwise it
provides `Number`, `TotalPages`, `TotalItems`, `PrevURL`, and `NextURL`.
`.Site.RegularPages` and each section's `.Pages` still contain their complete
collections. Override `templates/partials/pagination.html` to customize the
navigation independently of the adjacent-post pager.

### Command reference

| Command | Purpose |
|---|---|
| `build` | Render and write the site. Flags: `--drafts`, `--future`, `--minify`, `--clean=false` |
| `serve` | Build, watch for changes, rebuild, live-reload. Flags: `--host`, `--port` |
| `new post "Title"` | Create a Markdown file with frontmatter filled in. Flags: `--section`, `--tags`, `--categories`, `--date`, `--force` |
| `check` | Find broken links, dead images, dangling anchors, thin content. `--strict` fails on warnings |

Global flags: `-c/--config`, `-s/--source`, `-o/--output`, `-b/--base-url`,
`-v/--verbose`.

## Layout

```
cmd/staticgen/       entry point
internal/cli/        Cobra commands
internal/pipeline/   build stage wiring, inspection, minify, redirects
internal/content/    Markdown + frontmatter loading
internal/site/       site model assembly (sections, taxonomies, archive)
internal/render/     html/template engine and default theme
internal/generate/   RSS, sitemap, search index
internal/publish/    writing output to disk
internal/serve/      dev server and file watcher
```

A runnable example site lives in `example/`.

## Development

```sh
go test ./...
go build ./cmd/staticgen
```

## Licence

MIT
