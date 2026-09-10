---
title: Writing a Markdown pipeline
description: The stages a static site generator needs, and why each one belongs behind an interface.
date: 2026-03-05T11:00:00Z
tags: [go, staticgen, architecture]
categories: [engineering]
---

Most static site generators are the same program with different themes. Once you
see the stages, the shape is obvious.

<!--more-->

## The stages

A build is a sequence, and each step has one input and one output:

![The build pipeline](/images/pipeline.svg)

1. **Load** — walk the content tree, split frontmatter from body, render Markdown.
2. **Assemble** — sort, group into sections, derive taxonomies, link neighbours.
3. **Render** — run each page through a template to get a complete document.
4. **Emit** — write feeds, sitemaps and the search index.
5. **Publish** — write files atomically into the output directory.

## Why the boundaries matter

The interesting consequence is that *assemble* needs to see every page before it
can do anything useful. Prev/next links, tag listings and archive years are all
properties of the collection, not of any one document.

So the loader cannot be fused into the renderer, however tempting that is for
speed. It has to produce the whole set first.

```go
pages, err := loader.Load()
if err != nil {
    // Partial results still come back: one broken file should not
    // prevent the rest of the site from building.
    log.Printf("some pages failed to load: %v", err)
}

site, err := assembler.Assemble(pages, time.Now())
```

## What goes wrong without them

The failure mode of a fused pipeline is that features turn into conditionals
inside a render loop. Taxonomies become a special case. Pagination becomes
another. Eventually the loop knows about every feature the tool has.

With the stages separated, a new listing page is just another page: synthesise it
during assembly, give it a kind, and the existing renderer handles it like
anything else.

## Atomic writes

One detail worth getting right early. Writing directly into the output directory
means a reader can observe a half-written file — which matters as soon as you add
a dev server that serves that same directory.

Write to a temporary file beside the target and rename it:

```go
tmp, err := os.CreateTemp(filepath.Dir(dest), ".build-*")
if err != nil {
    return err
}
// ... write, close, chmod ...
return os.Rename(tmp.Name(), dest)
```

`rename` within a directory is atomic on every filesystem that matters, so the
file is either the old version or the new one, never a fragment.
