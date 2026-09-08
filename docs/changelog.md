---
title: Changelog
date: "2026-09-08T10:18:28+02:00"
---

Changes to the language and the runtime after the chapters were
written, newest first. Each entry records when it landed, what the
syntax gained, and how it is used.

## 2026-09-08 10:29 +02:00: composite literals

A struct is allocated the way Go writes it: `T{}`, `&T{}`, keyed and
positional elements, for any struct type in the registry.

```
u = url.URL{};
u = url.URL{Path: "/x", Host: "h"};
u = &url.URL{Path: "/p"};
r = &http.Request{Method: "POST", URL: &url.URL{Path: "/n"}};
u = url.URL{"https"};                 positional, declaration order
```

Elements follow Go's rules: keyed and positional do not mix, a field
is named once, and a promoted field is not a field of the literal's
type. One relaxation, consistent with how a call fills missing
arguments: a positional list may stop early, and the remaining fields
stay zero.

An element's value is anything an argument can be: a literal, a name,
a field read, a call, or another composite literal. A numeric literal
converts to its field's width the way it does at a call:

```
r = &http.Request{ProtoMajor: 1};                 int, from the field
r = &http.Request{URL: url.Parse("http://h/c")};  a call as a value
```

The literal works wherever a value does: assigned to a name, passed
as an argument, written to a field, returned. The value is built
fresh on every run of the compiled program, matching Go's
per-evaluation allocation, and it is addressable, so fields can be
assigned afterwards:

```
u := url.URL{};
u.Path = "/w";

takesAny(url.URL{Path: "/a"});
return &http.Request{Method: "GET"};
```

Multi-line literals and the trailing comma work as in Go:

```
u = url.URL{
	Path: "/t",
};
```

The mismatches are compile errors:

```
u = nope.Thing{};              unknown type "nope.Thing"
x = int64{};                   int64 is not a struct type
u = url.URL{Nope: 1};          url.URL has no field Nope
u = url.URL{Path: "/", "x"};   cannot mix keyed and positional elements
u = url.URL{Path: 5};          field Path: cannot use a number as string
```

Composite literals cover structs; there are no map or slice literal
forms. A program using one runs on the reflect evaluator: no JIT node
builds a struct, so the direct-call tier declines the whole program
rather than mishandling it.

## 2026-09-08 10:18 +02:00: conversion hints

A literal assignment can now carry its type at the assignment:

```
x = int32(0);
```

The right-hand side parses as a call. At compile time a path that
names a registered type rather than a binding cannot be called, so it
is read as a conversion hint: the literal takes the named type, the
way a var declaration would fix it. The var form stays; the two
spellings compile to the same statement:

```
var x int32;
x = 0;

x = int32(0);
```

Any type in the registry works, at any width the literal fits:

```
x = int32(7);
x = uint8(255);
x = float32(1.5);
x = int8(-5);
s = string("hi");
x := int32(3);        := works the same as =
```

A hint binds the name to the type the way var does. Later assignments
convert to it, and use does not override it:

```
x = int32(5);
x = 9;                x is int32(9)

x = int32(5);
takesInt(x);          compile error: cannot use int32 as int
```

The mismatches a var declaration reports, a hint reports too:

```
x = int8(300);        300 overflows int8
x = uint32(-1);       cannot use -1 as uint32, it is negative
x = int32(1.5);       cannot use 1.5 as int32, it has a decimal point

var x int64;
x = int32(5);         cannot use int32 as int64
```

The hint wraps exactly one literal. A name or a call already has a
type, so converting one is rejected rather than guessed at:

```
y = 5;
x = int32(y);         a conversion takes a literal
```

A binding wins over a type of the same name, so no program that
compiled before hints existed changed meaning. Hydration and the
three inference rules in [types.md](types.md) are unchanged; the hint
slots in ahead of them, as an explicit declaration on the assignment
itself.
