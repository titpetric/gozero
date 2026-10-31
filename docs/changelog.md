---
title: Changelog
date: "2026-09-08T10:18:28+02:00"
---

Changes to the language and the runtime that postdate the chapters,
newest first. Each entry records when it landed, what the syntax
gains, and how a program uses it.

## 2026-10-17 13:15 +02:00: channel receive and send removed

The channel statements leave the syntax: receive, send, the bare
receive and the chan spellings in a var typeref do not parse, and
vm_chan.go and stepjit_chan.go go with them. A channel is still a
value with a layout class: a binding produces one, a name holds it, a
call takes it, and goroutines and topology stay with the host as they
always did. The operations never escaped reflect: each receive and
send paid a context-armed select and an element box, which held the
channels fixture at 2.1x its mirror with inlining disabled while
every other fixture sat at 1.1x-1.5x, and made it the one fixture
above its mirror's allocation count. The concurrency chapter retires
with the syntax; the MutexMap binding stays, measured in
BenchmarkConcurrency. [design/channels.md](design/channels.md) keeps
the research record.

## 2026-09-23 00:00 +02:00: Bind takes values

`Bind` carried funcs. Data had no way into a program except as a
literal or through a nullary getter, at the cost of a call per read.
A host could not expose `time.Hour`, `io.EOF` or `os.Args` at all.

`Bind` now takes either. A program calls a func; anything else is a
value, read in argument position like any other name and
type-checked against the parameter when the program compiles. After
`Bind("time.Hour", time.Hour)` a binding taking a `time.Duration`
accepts it and one taking `int64` rejects it. `BindScope` loops over
`Bind` and carries values for free.

A value binding is also a receiver, with Go's method sets in full.
It owns the longest dotted prefix of a path the way a func binding
does. `Bind("u", u)` with a `*url.URL` makes both `u.Path` and
`u.String()` compile; a pointer-receiver method is callable on a
bound value, because a bound name is addressable the way a Go
variable is. The address is the per-run cell. The mutation is the
program's and not the host's.

A callee can be a value: a func-typed name, a func-typed field of
one, or a func bound under a name. `f := getFn()` then `f()`
compiles, as does `cfg.Fn()`. The signature is static either way.
The compiler checks the arguments when the program compiles and each
run reads only the func value. The call bridges, because
there is no funcval to cast at compile time.

Whether a program reaches the host is Go's rule and needs no API for
it. Binding a value copies it in. The name is the program's own
for the run: `label = x` and `&label` both reach a per-run copy,
and the host's variable keeps what it had. Binding an address opts
in. The name is then a `*T`. `*os.Args` reads the host's slice,
`*os.Args = xs` writes it, and a binding taking a `*[]string` takes
the name directly. The shallow copy is Go's too. A slice or a
map bound by value still shares its elements with the host.

The copy is per run and the binding holds none: a compiled program
keeps no per-run state. Two concurrent `Exec`s must not see each
other's writes and a second run must not start from what the first
appended. One cell per name per program means two `&label` address
the same storage the way two `&x` do in Go.

`&` and `*` are the only operators the language gained. `&name`
takes the address of a program name, a field of one, or a value
binding; `*p` reads through a pointer and `*p = v` writes through
one. A value never fills a `*T` parameter and a pointer never fills
a `T` one, and nothing auto-references an argument - the one
implicit address gozero takes is still the receiver of a
pointer-method call.

Slice literals landed with them, because `*os.Args = []string{...}`
needs one. `[]T{a, b}` builds a slice per evaluation, with the
element checks an argument of type `T` gets, and stands anywhere a
value stands.

`gozero.Delete(m, key)` removes a key from a map held in an `any`
and `gozero.Append(&v, x)` appends through a pointer held in one;
neither is something the builtin can do once the value has been
through an interface. Both type-assert the common shapes and fall
back to reflect for the rest. A missing key, a nil map and a nil
slice are no-ops, as in Go.

Reading a value reaches the direct tier as one load from the
binding's fixed address, with no frame slot and no boxing; an
addressed one becomes a frame slot seeded per run, and `&name`
filling an interface parameter carries a compile-time itab like any
other interface argument. A write does not yet: `planInline`
refuses the statement. The program falls back to the reflect
evaluator whole and the write still lands. `testdata/vars.txt` runs
the surface end to end.

A rebind is a new binding, not an update: neither a compiled program
nor the cached compilation of the same source sees it, which is the
rule `Bind` already had for funcs.

## 2026-09-10 16:58 +02:00: argument pooling under the binding contract

A call borrows its arguments. A binding receives values that are valid for
the duration of the call, and a binding that keeps a received `any`,
slice or literal pointer copies it first - a type assertion copies a
value out of its box, where a plain interface assignment copies only
the box's address. Plain string and scalar parameters carry their
values directly and need no copy. The Bind documentation states
the contract, the [glossary](GLOSSARY.md) defines it under borrowed
argument, and TestBindingContractCopy pins it from the caller's
side.

Under that contract the JIT pools three allocation kinds that
every call otherwise makes fresh per run: the variadic pack
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

A sync.Pool recycles the per-run frame when the compiler proves no
pointer into it leaves a run. The proof is the missing piece
[overheads.md](overheads.md) records next to its pooling measurement,
which leaves pooling unimplemented:

| frame source (overheads.md) | time per frame |
|-----------------------------|----------------|
| sync.Pool, cleared on reuse | 22.85ns        |
| unsafeNew                   | 62.57ns        |

An indirectly aliased interface argument or an addressed receiver
passes the callee a frame pointer it may keep. Those programs need
a fresh frame. The compiler marks exactly those nodes, and
jitCompileProgram attaches a pool only when the build marks none. The
pool clears a reused frame on the way out with the typed clear reflect performs
(reflect.typedmemclr, a pull linkname like unsafe_New, with the same
toolchain caveat). Every run still starts from the zero frame the
declarations and literal builders rely on.

Verified with benchstat over four runs against the pre-pool tree.
The mutex round trip in the concurrency chapter drops 14%
(p=0.029) and its allocations halve; the channel round trip keeps its
time and runs one allocation lighter:

| mutex round trip | time    | memory | allocations |
|------------------|---------|--------|-------------|
| pre-pool tree    | 151.0ns | 24 B   | 2           |
| frame pool       | 129.5ns | 8 B    | 1           |

 Six of the eight fixtures qualify and each loses
one allocation per run: http, json and url reach allocation parity
with their handwritten mirrors, http byte for byte. The first reset
used reflect.Value.SetZero and cost 6% on the smallest frames; the
linkname clear removed that. TestFramePoolGate pins the eligibility
rule and that a reused frame starts from zero.

Verifying the counts with an allocation profile also found a
benchmark defect: the native mirror built its context.WithValue per
iteration where the vm side built one before the loop. One allocation
in http/native belonged to the harness.
The mirror builds its context once, and the http pair is
symmetric.

## 2026-09-10 12:30 +02:00: MutexMap binding with a concurrency chapter

`gozero.MutexMap` is a mutex-protected `map[string]int` exported for
hosts to bind: shared state as method calls, the alternative to
moving values through a channel. Its `Set` and `Get` land on the
direct tier through two shape-table entries added for their
signatures.

The concurrency chapter measures the two against each
other:

| program         | cost                                         |
|-----------------|----------------------------------------------|
| native channel  | within 5ns of the native mutex round trip    |
| compiled channel| +192ns over native                           |
| compiled mutex  | +106ns over native                           |

The channel itself is not the overhead; the difference is the
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
every receive, and a closed channel ends the program with io.EOF. A
host loops Exec until errors.Is(err, io.EOF). The execution context
arms both operations: a blocked receive or send ends the
program with ctx.Err() when the ExecContext deadline passes, which
also bounds a nil channel. A send on a closed channel panics as in
Go, arriving as *PanicError. The channel itself must have a static
type - a name, a field, or a call result; the compiler rejects a stack
name the way it rejects a method on one. The language has no go
statement:
goroutines and channel construction stay in the host, and a compiled
program is safe to run from as many goroutines as the host starts.

On the direct tier the operations compile to a try fast path
(reflect TryRecv/TrySend, no allocation when the channel is ready)
falling back to a two-case reflect.Select with the context's Done.
The channels fixture runs at 1.6x its handwritten mirror in a
default build and 2.1x with inlining off:

| build            | channels fixture | mirror  |
|------------------|------------------|---------|
| default          | 1977ns           | 1253ns  |

The gap is the per-operation element boxing reflect requires, 15
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
does not. f().Bump() is a compile error naming the fix. The
receiver resolves to the variable's real address - on the direct
tier the frame slot's offset, with no load at all, and a method that
writes through its receiver writes the program's variable. The reflect
tier stores a slot whose address a method takes through an addressable
cell. The planner never splices such a slot away and never lets it
back an aliased interface argument, the same hazard class as a
rewritten slot. The existing fixture benchmarks keep their numbers: the
checks run at compile time.

## 2026-09-09 11:27 +02:00: declaration follows Go's rule

`:=` declares, `var` declares with a type, and `=` assigns to a name
one of those declared. The compiler enforces both halves:

```
x = 5;                  compile error: x is not defined, use := or var
x := 5; x := 6;         compile error: no new variables on left side of :=

x := 5;                 declares, type from first use or parser width
y := int32(7); y = 9;   the hint declares; the assignment converts
var n int64; n = 7;     unchanged
```

The initial prototype had no short declaration for literals.
`x = 5` declared `x` and the typo guard existed only for call
results; a misspelled assignment target silently declared a second
name while the later read of the real one saw a stale value. The
literal, composite-literal and conversion-hint paths now run the
same check the call path always had, plus Go's reverse rule that a
`:=` must declare something. `testdata/` and the inference examples
in [types.md](types.md) follow the rule; reassignment after `var` or a
hint keeps its meaning.

## 2026-09-09 11:00 +02:00: methods on interface-typed names

`ctx := ctxOf(); d := ctx.Done();` compiles. `MethodByName` on an
interface type returns the signature without a receiver and a zero
`Func`. Before this change that `Func` reached `compileCall` and
panicked `Compile` on the zero `reflect.Value`, and every method call
on an interface-typed name (`ctx.Err()`, a method on an `io.Reader`
slot) hit it. The compiler
now synthesizes the callable with `ifaceMethodFunc`: a
`reflect.MakeFunc` whose first parameter is the interface and whose
body dispatches on the dynamic value. The call compiles like any
method call on every tier. A method call on a nil interface panics
the way it does in Go and arrives as `*PanicError` through the
guard.

Two consequences of the error contract, pinned in
`TestInterfaceMethodCall`: `ctx.Err()` binds no value, because a
lone error result is never a value, and as a bare statement it ends
the program once the caller cancels the context - a one-line
cancellation guard.

The panic also exposed that `compileUncached` held the runtime's
read lock without a defer. A compile-time panic leaked it and the
next cache store deadlocked; the fix defers the release.

## 2026-09-09 10:44 +02:00: docs split, syntax reference, design research

Documentation only; the language and the runtime stay as they are.

[DESIGN.md](DESIGN.md) now documents the implementation: the
parse-compile-JIT pipeline, the parser's approach (recursive descent,
no token stream, nothing resolved), what the compiler checks against
the bindings' types, and how the three execution tiers relate. The
language surface moved to [syntax.md](syntax.md): the grammar, the
implicit rules (error stripping, context auto-fill, zero-filled
trailing arguments), conversion hints, composite literals, variadic
pack and spread, `dest` and the stack - each shown as a Go snippet
and its gozero counterpart side by side, drawn from the `testdata/`
fixtures.

A new [design/](design/) directory holds the research on the syntax
the language leaves out by design, one document per feature:
[conditions](design/conditions.md), [loops](design/loops.md),
[closures](design/closures.md) and
[expressions](design/expressions.md). Each answers what the feature
would look like in the Go-subset grammar, which invariants it would
break (the single-exit control contract, the write-once aliasing
rule, the straight-line planner), and the alternatives that keep the
AST call-only, with their cons: guard bindings for branching,
range-over-func for iteration, operation bindings for math.

## 2026-09-08 19:30 +02:00: composite literals on the direct tier

A composite literal keeps the program off the reflect
evaluator. The step JIT compiles a literal to typed stores at field
offsets, the technique the frame already uses. Every element write
keeps its write barrier and the block behind `&T{}` comes from the
same allocator with the struct's own pointer map.

A value literal assigned to a name builds in place in its frame slot
and allocates nothing; the JIT clears a name assigned a second literal
first. The fresh value starts from zero the way Go's does. `&T{}`,
a literal in argument position, and a literal filling an interface
each get one fresh struct per evaluation, which is the allocation the
Go compiler makes for a literal that escapes. A literal in return
position stays on the reflect evaluator, like every returned
expression.

The structs fixture benchmark, the only fixture off the direct tier
before this entry, moves from 4.6x native to 1.4x, in line with the
other fixtures:

| structs fixture      | time per run | allocations |
|----------------------|--------------|-------------|
| reflect evaluator    | 27.2us       | 72          |
| direct tier          | 7.0us        | 26          |
| native               | 5.9us        | 19          |

## 2026-09-08 10:29 +02:00: composite literals

A program builds a struct the way Go writes it: `T{}`, `&T{}`, keyed and
positional elements, for any struct type in the registry.

```
u = url.URL{};
u = url.URL{Path: "/x", Host: "h"};
u = &url.URL{Path: "/p"};
r = &http.Request{Method: "POST", URL: &url.URL{Path: "/n"}};
u = url.URL{"https"};                 positional, declaration order
```

Elements follow Go's rules: keyed and positional do not mix, a literal
names a field once, and a promoted field is not a field of the literal's
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
as an argument, written to a field, returned. Every run of the compiled
program builds the value fresh, matching Go's per-evaluation
allocation. The value is addressable. Later statements can assign
its fields:

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
builds a struct. The direct-call tier declines the whole program
and leaves it to the evaluator.

## 2026-09-08 10:18 +02:00: conversion hints

A literal assignment can now carry its type at the assignment:

```
x = int32(0);
```

The right-hand side parses as a call. At compile time a path that
names a registered type and no binding has nothing to call. The
compiler reads it as a conversion hint: the literal takes the named
type, the way a var declaration would fix it. The var form stays; the
two spellings compile to the same statement:

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
type. Converting one is a compile error, never a guess:

```
y = 5;
x = int32(y);         a conversion takes a literal
```

A binding wins over a same-named type. No program that compiled
before hints existed changed meaning. Hydration and the three
inference rules in [types.md](types.md) stay as they are; the hint
slots in ahead of them, as an explicit declaration on the assignment
itself.
