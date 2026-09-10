---
title: Go generics in practice
description: Where type parameters earn their keep in a real codebase, and where they do not.
date: 2026-02-20T14:00:00Z
lastmod: 2026-02-22T09:15:00Z
tags: [go, generics, tooling]
categories: [engineering]
toc: true
---

Generics arrived in Go 1.18 and the language got noticeably better at one specific
thing: containers that do not care what they contain.

<!--more-->

## Where they help

The clearest win is a function whose behaviour depends only on structure, not on
the element type. A `Map` over slices is the canonical example:

```go
func Map[T, U any](in []T, f func(T) U) []U {
	out := make([]U, len(in))
	for i, v := range in {
		out[i] = f(v)
	}
	return out
}
```

Nothing here needs to know whether `T` is an `int` or a `*Page`. That is exactly
the case where a type parameter replaces a code generator.

## Where they do not

The temptation is to make every interface generic. Resist it. A type parameter
that only ever gets instantiated once is indirection with no payoff:

```go
// This buys nothing over a concrete *Builder.
type Pipeline[T any] struct {
	Stages []Stage[T]
}
```

If there is one implementation and one caller, write the concrete type. Generics
are for the second caller you cannot predict.

## Constraints

Constraints are interfaces, and the useful ones are small. `cmp.Ordered` covers
the sortable types; anything wider usually means you are describing behaviour the
caller does not actually need:

```go
import "cmp"

func Max[T cmp.Ordered](a, b T) T {
	if a > b {
		return a
	}
	return b
}
```

## A note on readability

Error messages involving type inference can be long. When a generic function fails
to compile, the first thing to check is usually not the constraint but the
argument order — inference works left to right, so a parameter that can only be
determined from a later argument will not unify.

## Summary

Reach for generics when a function's logic is genuinely independent of its element
type, and stop there. Most application code is about specific types doing specific
things, and concrete code says that more clearly.

