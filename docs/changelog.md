---
title: Changelog
date: "2026-09-08T10:18:28+02:00"
---

Changes to the language and the runtime after the chapters were
written, newest first. Each entry records when it landed, what the
syntax gained, and how it is used.

## 2026-09-09 11:27 +02:00: declaration follows Go's rule

`:=` declares, `var` declares with a type, and `=` assigns to a name
one of those declared. Both halves are enforced:

```
x = 5;                  compile error: x is not defined, use := or var
x := 5; x := 6;         compile error: no new variables on left side of :=

x := 5;                 declares, type from first use or parser width
y := int32(7); y = 9;   the hint declares; the assignment converts
var n int64; n = 7;     unchanged
```

The initial prototype had no short declaration for literals, so
`x = 5` declared `x` and the typo guard existed only for call
results; a misspelled assignment target silently declared a second
name while the later read of the real one saw a stale value. The
literal, composite-literal and conversion-hint paths now run the
same check the call path always had, plus Go's reverse rule that a
`:=` must declare something. `testdata/` and the inference examples
in [types.md](types.md) are migrated; reassignment after `var` or a
hint is unchanged.

## 2026-09-09 11:00 +02:00: methods on interface-typed names

`ctx := ctxOf(); d := ctx.Done();` compiles. `MethodByName` on an
interface type returns the signature without a receiver and a zero
`Func`, which used to reach `compileCall` and panic `Compile` on the
zero `reflect.Value`; every method call on an interface-typed name
(`ctx.Err()`, a method on an `io.Reader` slot) hit it. The compiler
now synthesizes the callable with `ifaceMethodFunc`: a
`reflect.MakeFunc` whose first parameter is the interface and whose
body dispatches on the dynamic value, so the call compiles like any
method call on every tier. A method call on a nil interface panics
the way it does in Go and arrives as `*PanicError` through the
guard.

Two consequences of the error contract, pinned in
`TestInterfaceMethodCall`: `ctx.Err()` binds no value, because a
lone error result is never a value, and as a bare statement it ends
the program when the context is cancelled - a one-line cancellation
guard.

The panic also exposed that `compileUncached` held the runtime's
read lock without a defer, so a compile-time panic leaked it and the
next cache store deadlocked; the unlock is deferred now.

## 2026-09-09 10:44 +02:00: docs split, syntax reference, design research

Documentation only; the language and the runtime are unchanged.

[DESIGN.md](DESIGN.md) now documents the implementation: the
parse-compile-JIT pipeline, the parser's approach (recursive descent,
no token stream, nothing resolved), what the compiler checks against
the bindings' types, and how the three execution tiers relate. The
language surface moved to [syntax.md](syntax.md): the grammar, the
implicit rules (error stripping, context auto-fill, zero-filled
trailing arguments), conversion hints, composite literals, variadic
pack and spread, `dest` and the stack - each shown as a Go snippet
and its gozero equivalent side by side, drawn from the `testdata/`
fixtures.

A new [design/](design/) directory holds the research on the syntax
the language deliberately leaves out, one document per feature:
[conditions](design/conditions.md), [loops](design/loops.md),
[closures](design/closures.md) and
[expressions](design/expressions.md). Each answers what the feature
would look like in the Go-subset grammar, which invariants it would
break (the single-exit control contract, the write-once aliasing
rule, the straight-line planner), and the alternatives that keep the
AST call-only, with their cons: guard bindings for branching,
range-over-func for iteration, operation bindings for math.

## 2026-09-08 19:30 +02:00: composite literals on the direct tier

A composite literal no longer sends the program to the reflect
evaluator. The step JIT compiles a literal to typed stores at field
offsets, the technique the frame already uses, so every element write
keeps its write barrier and the block behind `&T{}` comes from the
same allocator with the struct's own pointer map.

A value literal assigned to a name builds in place in its frame slot
and allocates nothing; a name assigned a second literal is cleared
first, so the fresh value starts from zero the way Go's does. `&T{}`,
a literal in argument position, and a literal filling an interface
allocate one fresh struct per evaluation, which is the allocation the
Go compiler makes for a literal that escapes. A literal in return
position stays on the reflect evaluator, like every returned
expression.

The structs fixture benchmark, previously the only fixture off the
direct tier, went from 27.2us and 72 allocations per run to 7.0us and
26 against 5.9us and 19 native: from 4.6x native to 1.4x, in line
with the other fixtures.

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
