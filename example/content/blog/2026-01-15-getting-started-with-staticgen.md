---
title: Getting started with staticgen
description: How a directory of Markdown becomes a website, and what to configure first.
date: 2026-01-15T10:30:00Z
tags: [staticgen, tooling, getting-started]
categories: [guides]
aliases: [/posts/getting-started/, /intro/]
---

A static site generator has one job: read files, produce files. Everything else is
a detail about how those two ends meet.

<!--more-->

## The short version

Point it at a directory and build:

```bash
staticgen build --source ./mysite --output ./mysite/public
```

That reads every `.md` file under `mysite/content/`, renders it through a theme,
and writes HTML into `mysite/public/`. No configuration file is required — the
defaults are enough to produce a working site.

## Adding configuration

Drop a `site.yaml` in the site root when you want to change something:

```yaml
title: My Site
base_url: https://example.com

build:
  pretty_urls: true
  drafts: false

markup:
  highlight:
    enabled: true
    style: github
```

`base_url` is the one that matters most. Feeds and sitemaps are made of absolute
URLs, so enabling RSS without a base URL is a configuration error rather than
something the tool quietly guesses at.

## How a file becomes a URL

The mapping is deterministic, which is what makes links between pages reliable:

| Source file              | URL                    |
| ------------------------ | ---------------------- |
| `content/index.md`       | `/`                    |
| `content/about.md`       | `/about/`              |
| `content/blog/_index.md` | `/blog/`               |
| `content/blog/post.md`   | `/blog/post/`          |

Set `build.pretty_urls: false` and the last row becomes `/blog/post.html` instead,
for hosts that do not serve directory indexes.

## What you get for free

Beyond the pages themselves, a build emits tag and category listings, an archive
grouped by year, an RSS feed, an XML sitemap and a JSON search index. Each is a
separate toggle under `feeds:` in the config.

## Next

Read [how the pipeline is structured](/blog/writing-a-markdown-pipeline/) if you
want to change how rendering works.
