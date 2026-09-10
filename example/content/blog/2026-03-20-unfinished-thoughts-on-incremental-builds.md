---
title: Unfinished thoughts on incremental builds
description: A sketch of what would need to change to rebuild only what moved.
date: 2026-03-20T16:45:00Z
draft: true
tags: [go, staticgen]
categories: [engineering]
---

This post is marked `draft: true`, so a normal build leaves it out entirely — it
is not loaded, not rendered, and not linked from any listing.

Build with `--drafts` to include it, which is what `staticgen serve` does not do by
default but can be asked to.

## The problem

A full rebuild re-renders everything. For a few hundred pages that costs well under
a second, so it is not worth optimising. Past a few thousand it is.

## What would be needed

The blocker is that assembly is global. Changing one post can change an archive
page, a tag listing and its neighbours' prev/next links. So an incremental build
has to know the *blast radius* of a change, and that radius is not local.

A reasonable approximation:

- content change to page P → re-render P, plus every listing page that contains P
- frontmatter-only change (tags, date) → re-render P and all taxonomies
- template or config change → full rebuild

Getting the first case right would cover most edits during authoring.
