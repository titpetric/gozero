---
title: Changelog
date: "2026-09-08T10:18:28+02:00"
---

Changes to the language and the runtime after the chapters were
written, newest first. Each entry records when it landed, what the
syntax gained, and how it is used.

## 2026-09-19 16:09 +02:00: step statements, n++ and n--

The two IncDecStmt forms land as statements, which is what Go makes
them: they produce no value, so the grammar stays operator-free and
no expression tree appears. A step compiles only against a
program-bound name of an integer or float type; a non-numeric type,
an undefined name, a field target and a step in value position are
each rejected at compile time with the rule named. Every scalar
class steps at its own width and wraps the way compiled Go wraps,
uint8 255 to 0 included, pinned by an equivalence table that runs
both tiers.

On the direct tier a step is a load, an add and a store at the
slot's frame offset: nothing allocates, nothing bridges, and a step
never appears in Supports output. The interface-aliasing gate is
untouched, because it only ever covers non-scalar slots and a step
only compiles at a scalar. The reflect tier steps an addressed slot
in place and copies any other into a fresh cell first, so the
prebuilt literal a compiled program shares between runs is never
mutated.

Measured with the pinned harness: the incdec fixture runs 3951
ns/op at 7 allocs against its handwritten mirror's 3092 ns/op at
14, the difference on both sides being fmt.Sprintf's argument packs
and result strings, and every existing benchmark keeps its
allocation count exactly (benchstat, 3 samples each, all equal).

## 2026-09-10 16:58 +02:00: argument pooling under the binding contract

Arguments are borrowed. A binding receives values that are valid for
the duration of the call, and a binding that keeps a received `any`,
slice or literal pointer copies it first - a type assertion copies a
value out of its box, where a plain interface assignment copies only
the box's address. Plain string and scalar parameters carry their
values directly and need no copy. The contract is documented on
Bind, defined in the [glossary](GLOSSARY.md) under borrowed
argument, and pinned from the caller's side by
TestBindingContractCopy.

Under that contract the JIT pools three allocation kinds it
previously made fresh per run, for every call: the variadic pack
slice, the heap cell a string boxes into for an interface parameter,
and the block behind a composite literal in argument position. Each
pooled site holds its block through a hidden frame field, and run
releases every site after the statements finish, clearing the block
so it repools zeroed and referencing nothing.

Verified with benchstat over four paired runs: allocations drop 25%
geometric mean across the affected fixtures - fmt 6 to 2 per run
(native: 4), types 20 to 8 (native: 11), structs 25 to 18 (native:
19), json 16 to 14, variadic 3 to 2 - with time down 3.4% geometric
mean (fmt -7.4%, variadic +2.9% the one regression). With the frame
pool and argument pooling together, seven of the eight fixtures run
at or below their handwritten mirrors' allocation counts.

## 2026-09-10 15:29 +02:00: escape-gated frame pooling

The per-run frame is recycled through a sync.Pool when the compiler
proves no pointer into it leaves a run. The proof was the missing
piece [overheads.md](overheads.md) recorded when it measured pooling
at 22.85ns against 62.57ns and left it unimplemented: an indirectly
aliased interface argument or an addressed receiver hands the callee
a frame pointer it may keep, so those programs must allocate fresh.
The compiler now marks exactly those nodes, and jitCompileProgram
attaches a pool only when none were built. A reused frame is cleared
on the way out of the pool with the typed clear reflect performs
(reflect.typedmemclr, a pull linkname like unsafe_New, with the same
toolchain caveat), so every run still starts from the zero frame the
declarations and literal builders rely on.

Verified with benchstat over four runs against the pre-pool tree.
The mutex round trip in [concurrency.md](concurrency.md) drops 14%
(151.0ns to 129.5ns, p=0.029) and its allocations halve (24 B, 2 to
8 B, 1); the channel round trip is unchanged in time and one
allocation lighter. Six of the eight fixtures qualify and each loses
one allocation per run: http, json and url reach allocation parity
with their handwritten mirrors, http byte for byte. The first reset
used reflect.Value.SetZero and cost 6% on the smallest frames; the
linkname clear removed that. TestFramePoolGate pins the eligibility
rule and that a reused frame starts from zero.

Verifying the counts with an allocation profile also found a
benchmark defect: the native mirror built its context.WithValue per
iteration where the vm side built one before the loop, so
http/native measured one allocation that belonged to the harness.
The mirror's context is built once now, and the http pair is
symmetric.

## 2026-09-10 12:30 +02:00: MutexMap binding and the concurrency chapter

`gozero.MutexMap` is a mutex-protected `map[string]int` exported for
hosts to bind: shared state as method calls, the alternative to
moving values through a channel. Its `Set` and `Get` land on the
direct tier through two shape-table entries added for their
signatures.

[concurrency.md](concurrency.md) measures the two against each
other: an uncontended native channel round trip costs within 5ns of
the native mutex round trip, so the channel itself is not the
overhead; the compiled channel program pays +192ns over native
against the mutex program's +106ns, and the difference is the
reflect layer channel operations run through, one element box per
operation. Shared state belongs behind the mutex binding; handoff,
blocking and end-of-stream stay on channels.

## 2026-09-10 11:31 +02:00: channel receive and send

Channels moved from research ([design/channels.md](design/channels.md))
into the syntax, on every tier:

```
c := chanOf("a", "b");
v := <-c;               receive, binds the element
c <- "sent";            send
<-c;                    bare receive, for its blocking effect
var d chan string;      chan, <-chan and chan<- in a typeref
```

The implicit rules carry over. The ok of Go's two-value receive is
implicit the way a trailing error is: never a value, checked after
every receive, and a closed channel ends the program with io.EOF, so
a host loops Exec until errors.Is(err, io.EOF). Both operations are
armed with the execution context: a blocked receive or send ends the
program with ctx.Err() when the ExecContext deadline passes, which
also bounds a nil channel. A send on a closed channel panics as in
Go, arriving as *PanicError. The channel itself must have a static
type - a name, a field, or a call result; a stack name is rejected
at compile the way a method on one is. The language has no go
statement:
goroutines and channel construction stay in the host, and a compiled
program is safe to run from as many goroutines as the host starts.

On the direct tier the operations compile to a try fast path
(reflect TryRecv/TrySend, no allocation when the channel is ready)
falling back to a two-case reflect.Select with the context's Done.
The channels fixture runs at 1.6x its handwritten mirror in a
default build (1977ns vs 1253ns) and 2.1x with inlining off;
the gap is the per-operation element boxing reflect requires, 15
allocations against the mirror's 8. A typed fast path per element
class, reinterpreting the frame word as the concrete channel type,
is the unexplored next step. select, go and range stay outside the
language; the research records why.

## 2026-09-10 11:20 +02:00: full Go method sets

Methods resolve against Go's method sets in full. Value receivers
worked; what landed is pointer receivers on value-typed names and
promoted methods from embedded types through both receiver kinds.
Given a host type with a pointer-receiver method behind an embedding:

```go
type counter struct{ n int }

func (c *counter) Add() int { c.n++; return c.n }

type stats struct{ counter }

rt.Bind("newStats", func() stats { return stats{} })
```

a program calls the promoted method on the value and the writes
reach the variable:

```
var u url.URL;
u.Path = "/x";
s := u.String();        pointer receiver on a var-declared value

st := newStats();
st.Add();               promoted pointer receiver, st.n is 1
n := st.Add();          n is 2: both writes reached st
```

Addressability is Go's rule: a name is a variable and has an
address, a field of one does too, and the direct result of a call
does not, so f().Bump() is a compile error naming the fix. The
receiver resolves to the variable's real address - on the direct
tier the frame slot's offset, with no load at all, and a method that
writes through its receiver writes the program's variable. A slot
whose address is taken is stored through an addressable cell on the
reflect tier, is never spliced away by the planner, and does not
back an aliased interface argument, the same hazard class as a
rewritten slot. The existing fixture benchmarks are unchanged: the
checks are compile-time.

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
