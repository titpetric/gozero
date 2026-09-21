---
title: Changelog
date: "2026-09-08T10:18:28+02:00"
---

Changes to the language and the runtime after the chapters were written, newest first. Each entry records when it landed, what the syntax gained, and how it is used.

## 2026-09-21 09:12 +02:00: if, else if and else, with return inside an arm

The syntax gains its first visible conditional: `if`, `else if` and `else` over braced statement lists, with the block grammar general enough for any later construct with a braced body. The condition is exactly one of a declared bool name, a bool field path like `req.Close`, or a call returning bool; there is no operator, no literal and no init clause in the header. One rule rejects at compile time with the rule named: flat scope, meaning `var` and `:=` cannot appear inside an arm, because a flat slot would leak the name past the brace. Arms share the program's slot namespace, so every name an arm writes is declared before the `if`, and a skipped arm leaves the zero value Go would.

`return` inside an arm works, with or without a value. It travels an unexported sentinel on the error return every statement already has, which the top of the program consumes: the value rides beside the signal, the statements written after it never run, and a program without one compares against nothing, so the nil-error hot path is unchanged. Equivalence tests pin return from a then arm, from an else arm, from a nested `if`, and mid-arm with trailing statements, against the reflect evaluator.

The reflect tier runs the arm the condition picks through `runStmts`, which both the program and the arms share. The direct tier compiles a structured node holding one statement list per arm: no program counter, and the only exit stays an error. Programs with an `if` take a structural plan that does not splice and counts a slot written inside an arm as written twice, so the write-once interface aliasing never fires for a maybe-written name; the shape table gains `SS_b` for the string predicates conditions call.

Measured on the new `if` fixture, a middleware-style guard: 2845 ns/op, 560 B/op, 6 allocs/op against its handwritten mirror at 2079 ns/op, 560 B/op, 6 allocs/op, allocation parity at 1.37x time. The conservative write count costs one boxing allocation per interface read of a branch-touched name where a straight line would alias: 491.5 ns, 32 B, 2 allocs against 402.8 ns, 16 B, 1 alloc on BenchmarkIfWriteCount. Every existing benchmark keeps identical allocation counts.

## 2026-09-21 08:48 +02:00: step statements, n++ and n--

The two IncDecStmt forms land as statements, which is what Go makes them: they produce no value, so the grammar stays operator-free and no expression tree appears. A step compiles only against a program-bound name of an integer or float type; a non-numeric type, an undefined name, a field target and a step in value position are each rejected at compile time with the rule named. Every scalar class steps at its own width and wraps the way compiled Go wraps, uint8 255 to 0 included, pinned by an equivalence table that runs both tiers.

On the direct tier a step is a load, an add and a store at the slot's frame offset: nothing allocates, nothing bridges, and a step never appears in Supports output. The interface-aliasing gate is untouched, because it only ever covers non-scalar slots and a step only compiles at a scalar. The reflect tier steps an addressed slot in place and copies any other into a fresh cell first, so the prebuilt literal a compiled program shares between runs is never mutated.

Measured with the pinned harness: the incdec fixture runs 4203 ns/op at 7 allocs against its handwritten mirror's 3427 ns/op at 14, the difference on both sides being fmt.Sprintf's argument packs and result strings. Every existing benchmark keeps its allocation count exactly (benchstat, 3 samples each, all equal); BenchmarkCostWithoutCaching moves 32 B/op, which is the parsed statement struct growing from 144 to 168 bytes for the name and the step and landing one size class up.

## 2026-09-20 16:11 +02:00: one-time reflow of the committed markdown

The mdox configuration dropped soft wraps, so the formatter joins hard-wrapped prose into one line per paragraph. Every committed chapter and the README predate that setting, which meant any run of the format gate reflowed all of them and mixed formatter churn into unrelated diffs.

This entry marks the one-time reflow: all sixteen documents and the README formatted in a single commit, no content edits. The formatter's output is now canonical; a second run produces no diff, so from here a documentation diff contains only what its author changed.

## 2026-09-20 15:59 +02:00: Runtime.BindValue, typed value bindings

Bind carries funcs, so a Go constant such as time.Hour had no way into a program: a host could wrap it in a getter, at the cost of a call, or leave it out. BindValue registers a typed value under a dotted name, and the name compiles to that value wherever a call argument reads a dotted path, on both the single-statement path and the program compiler.

The value keeps the static type it was bound with: after BindValue("time.Hour", time.Hour), a binding taking time.Duration accepts time.Hour and a binding taking int64 rejects it at compile time. The name's root joins the same roots map Bind maintains, so a program cannot shadow it, and a rebind overwrites, the rule Bind has. The value is captured once, at bind time; a func is rejected toward Bind, a nil toward a typed value.

On the direct tier a value binding compiles like a literal: a constant node, no per-call work. A dotted path in a flat call that names no value binding now fails as "not a name bound by the program" instead of the unsupported-argument catch-all.

## 2026-09-20 15:41 +02:00: call-shape table additions

Three target-independent call shapes, extending the direct tier to signatures common host bindings already have. The syntax is unchanged; a binding of one of these signatures now compiles direct instead of bridging its call through reflect.

PS_i64E is the io writer form bytes.Buffer's WriteString has: func(*T, string) (int, error), the count as i64 and the trailing error checked at the call. _S is a niladic string getter, func() string. IL_i64E is fmt.Fprint and the io.Writer print family: func(io.Writer, ...any) (int, error), an interface, a variadic pack and the count-and-error pair. The string-parameter scalar-result family covers func(string) T for every scalar width T through the sN and sF constructors, dispatched generically beside the pointer family rather than one table case per width.

Each shape has a test asserting via Supports that a binding of that signature compiles direct, and existing fixtures keep identical allocation counts.

## 2026-09-20 15:19 +02:00: mechanical moves ahead of the merged ladder

Five behaviour-preserving refactors the ladder branches each carried mid-feature, landed once. The syntax and the run-time paths are unchanged; every benchmark keeps identical allocation counts.

The field assignment lives in vm_field.go: the statement's type, compiler and apply in one file, the way vm_chan.go holds the channel forms. On the direct tier, fieldNode splits its address computation into fieldAddr and both move to stepjit_field.go, so a new field source lands beside the two existing ones instead of growing stepjit_arg.go.

compileProgram's statement loop moves onto progCompiler.compileStmts and the frame post-pass onto assignStmts, so a nested statement list can compile and be walked through exactly the code the top level uses. The scalar-family pre-dispatch moves out of the shape table into scalarFamilyCall beside the families it dispatches, and classOf sits next to layout.String, its inverse.

Reserved-name checking reads a roots map Bind maintains instead of building a reserved set per compile, so a future keyword is a switch case at compile time rather than a map entry that can tip the bucket boundary TestParseAllocBudget guards.

## 2026-09-20 14:51 +02:00: four latent fixes from the ladder experiment

Four defects the design-ladder branches surfaced, each fixed on the base with a failing test first. The syntax is unchanged.

A return before the last statement declines the direct tier in the value form (`return name;`) and the bare form (`return;`), as the call form always did. The planner waved both through, so the direct tier ran the statements after the return, which the reflect evaluator never reaches.

An argument kind the single-statement path does not carry, such as a dotted path or a `nil` literal in a flat call, is a named compile error. It used to reach `reflect.Value.Type` on a zero value and panic.

An interface-typed slot filling a parameter of a different interface type rebuilds the interface pair through the itab's concrete type word. The raw two-word copy handed the callee the slot type's method table, so a method call on the parameter dispatched the wrong method; `http.ResponseWriter` into `io.Writer` is the common shape. TestItabType pins the type word's offset, the same toolchain risk class as the linknamed allocator.

The reflect evaluator writes an address-taken slot through its existing cell. Every write allocated a fresh cell, so a pointer taken before a reassignment kept reading the old value, where the step JIT's frame memory and Go both show the new one.

Every existing benchmark keeps identical allocation counts, benchstat all equal at n=3.

## 2026-09-10 16:58 +02:00: argument pooling under the binding contract

Arguments are borrowed. A binding receives values that are valid for the duration of the call, and a binding that keeps a received `any`, slice or literal pointer copies it first - a type assertion copies a value out of its box, where a plain interface assignment copies only the box's address. Plain string and scalar parameters carry their values directly and need no copy. The contract is documented on Bind, defined in the [glossary](GLOSSARY.md) under borrowed argument, and pinned from the caller's side by TestBindingContractCopy.

Under that contract the JIT pools three allocation kinds it previously made fresh per run, for every call: the variadic pack slice, the heap cell a string boxes into for an interface parameter, and the block behind a composite literal in argument position. Each pooled site holds its block through a hidden frame field, and run releases every site after the statements finish, clearing the block so it repools zeroed and referencing nothing.

Verified with benchstat over four paired runs: allocations drop 25% geometric mean across the affected fixtures - fmt 6 to 2 per run (native: 4), types 20 to 8 (native: 11), structs 25 to 18 (native: 19), json 16 to 14, variadic 3 to 2 - with time down 3.4% geometric mean (fmt -7.4%, variadic +2.9% the one regression). With the frame pool and argument pooling together, seven of the eight fixtures run at or below their handwritten mirrors' allocation counts.

## 2026-09-10 15:29 +02:00: escape-gated frame pooling

The per-run frame is recycled through a sync.Pool when the compiler proves no pointer into it leaves a run. The proof was the missing piece [overheads.md](overheads.md) recorded when it measured pooling at 22.85ns against 62.57ns and left it unimplemented: an indirectly aliased interface argument or an addressed receiver hands the callee a frame pointer it may keep, so those programs must allocate fresh. The compiler now marks exactly those nodes, and jitCompileProgram attaches a pool only when none were built. A reused frame is cleared on the way out of the pool with the typed clear reflect performs (reflect.typedmemclr, a pull linkname like unsafe_New, with the same toolchain caveat), so every run still starts from the zero frame the declarations and literal builders rely on.

Verified with benchstat over four runs against the pre-pool tree. The mutex round trip in [concurrency.md](concurrency.md) drops 14% (151.0ns to 129.5ns, p=0.029) and its allocations halve (24 B, 2 to 8 B, 1); the channel round trip is unchanged in time and one allocation lighter. Six of the eight fixtures qualify and each loses one allocation per run: http, json and url reach allocation parity with their handwritten mirrors, http byte for byte. The first reset used reflect.Value.SetZero and cost 6% on the smallest frames; the linkname clear removed that. TestFramePoolGate pins the eligibility rule and that a reused frame starts from zero.

Verifying the counts with an allocation profile also found a benchmark defect: the native mirror built its context.WithValue per iteration where the vm side built one before the loop, so http/native measured one allocation that belonged to the harness. The mirror's context is built once now, and the http pair is symmetric.

## 2026-09-10 12:30 +02:00: MutexMap binding and the concurrency chapter

`gozero.MutexMap` is a mutex-protected `map[string]int` exported for hosts to bind: shared state as method calls, the alternative to moving values through a channel. Its `Set` and `Get` land on the direct tier through two shape-table entries added for their signatures.

[concurrency.md](concurrency.md) measures the two against each other: an uncontended native channel round trip costs within 5ns of the native mutex round trip, so the channel itself is not the overhead; the compiled channel program pays +192ns over native against the mutex program's +106ns, and the difference is the reflect layer channel operations run through, one element box per operation. Shared state belongs behind the mutex binding; handoff, blocking and end-of-stream stay on channels.

## 2026-09-10 11:31 +02:00: channel receive and send

Channels moved from research ([design/channels.md](design/channels.md)) into the syntax, on every tier:

```
c := chanOf("a", "b");
v := <-c;               receive, binds the element
c <- "sent";            send
<-c;                    bare receive, for its blocking effect
var d chan string;      chan, <-chan and chan<- in a typeref
```

The implicit rules carry over. The ok of Go's two-value receive is implicit the way a trailing error is: never a value, checked after every receive, and a closed channel ends the program with io.EOF, so a host loops Exec until errors.Is(err, io.EOF). Both operations are armed with the execution context: a blocked receive or send ends the program with ctx.Err() when the ExecContext deadline passes, which also bounds a nil channel. A send on a closed channel panics as in Go, arriving as *PanicError. The channel itself must have a static type - a name, a field, or a call result; a stack name is rejected at compile the way a method on one is. The language has no go statement: goroutines and channel construction stay in the host, and a compiled program is safe to run from as many goroutines as the host starts.

On the direct tier the operations compile to a try fast path (reflect TryRecv/TrySend, no allocation when the channel is ready) falling back to a two-case reflect.Select with the context's Done. The channels fixture runs at 1.6x its handwritten mirror in a default build (1977ns vs 1253ns) and 2.1x with inlining off; the gap is the per-operation element boxing reflect requires, 15 allocations against the mirror's 8. A typed fast path per element class, reinterpreting the frame word as the concrete channel type, is the unexplored next step. select, go and range stay outside the language; the research records why.

## 2026-09-10 11:20 +02:00: full Go method sets

Methods resolve against Go's method sets in full. Value receivers worked; what landed is pointer receivers on value-typed names and promoted methods from embedded types through both receiver kinds. Given a host type with a pointer-receiver method behind an embedding:

```go
type counter struct{ n int }

func (c *counter) Add() int { c.n++; return c.n }

type stats struct{ counter }

rt.Bind("newStats", func() stats { return stats{} })
```

a program calls the promoted method on the value and the writes reach the variable:

```
var u url.URL;
u.Path = "/x";
s := u.String();        pointer receiver on a var-declared value

st := newStats();
st.Add();               promoted pointer receiver, st.n is 1
n := st.Add();          n is 2: both writes reached st
```

Addressability is Go's rule: a name is a variable and has an address, a field of one does too, and the direct result of a call does not, so f().Bump() is a compile error naming the fix. The receiver resolves to the variable's real address - on the direct tier the frame slot's offset, with no load at all, and a method that writes through its receiver writes the program's variable. A slot whose address is taken is stored through an addressable cell on the reflect tier, is never spliced away by the planner, and does not back an aliased interface argument, the same hazard class as a rewritten slot. The existing fixture benchmarks are unchanged: the checks are compile-time.

## 2026-09-09 11:27 +02:00: declaration follows Go's rule

`:=` declares, `var` declares with a type, and `=` assigns to a name one of those declared. Both halves are enforced:

```
x = 5;                  compile error: x is not defined, use := or var
x := 5; x := 6;         compile error: no new variables on left side of :=

x := 5;                 declares, type from first use or parser width
y := int32(7); y = 9;   the hint declares; the assignment converts
var n int64; n = 7;     unchanged
```

The initial prototype had no short declaration for literals, so `x = 5` declared `x` and the typo guard existed only for call results; a misspelled assignment target silently declared a second name while the later read of the real one saw a stale value. The literal, composite-literal and conversion-hint paths now run the same check the call path always had, plus Go's reverse rule that a `:=` must declare something. `testdata/` and the inference examples in [types.md](types.md) are migrated; reassignment after `var` or a hint is unchanged.

## 2026-09-09 11:00 +02:00: methods on interface-typed names

`ctx := ctxOf(); d := ctx.Done();` compiles. `MethodByName` on an interface type returns the signature without a receiver and a zero `Func`, which used to reach `compileCall` and panic `Compile` on the zero `reflect.Value`; every method call on an interface-typed name (`ctx.Err()`, a method on an `io.Reader` slot) hit it. The compiler now synthesizes the callable with `ifaceMethodFunc`: a `reflect.MakeFunc` whose first parameter is the interface and whose body dispatches on the dynamic value, so the call compiles like any method call on every tier. A method call on a nil interface panics the way it does in Go and arrives as `*PanicError` through the guard.

Two consequences of the error contract, pinned in `TestInterfaceMethodCall`: `ctx.Err()` binds no value, because a lone error result is never a value, and as a bare statement it ends the program when the context is cancelled - a one-line cancellation guard.

The panic also exposed that `compileUncached` held the runtime's read lock without a defer, so a compile-time panic leaked it and the next cache store deadlocked; the unlock is deferred now.

## 2026-09-09 10:44 +02:00: docs split, syntax reference, design research

Documentation only; the language and the runtime are unchanged.

[DESIGN.md](DESIGN.md) now documents the implementation: the parse-compile-JIT pipeline, the parser's approach (recursive descent, no token stream, nothing resolved), what the compiler checks against the bindings' types, and how the three execution tiers relate. The language surface moved to [syntax.md](syntax.md): the grammar, the implicit rules (error stripping, context auto-fill, zero-filled trailing arguments), conversion hints, composite literals, variadic pack and spread, `dest` and the stack - each shown as a Go snippet and its gozero equivalent side by side, drawn from the `testdata/` fixtures.

A new [design/](design/) directory holds the research on the syntax the language deliberately leaves out, one document per feature: [conditions](design/conditions.md), [loops](design/loops.md), [closures](design/closures.md) and [expressions](design/expressions.md). Each answers what the feature would look like in the Go-subset grammar, which invariants it would break (the single-exit control contract, the write-once aliasing rule, the straight-line planner), and the alternatives that keep the AST call-only, with their cons: guard bindings for branching, range-over-func for iteration, operation bindings for math.

## 2026-09-08 19:30 +02:00: composite literals on the direct tier

A composite literal no longer sends the program to the reflect evaluator. The step JIT compiles a literal to typed stores at field offsets, the technique the frame already uses, so every element write keeps its write barrier and the block behind `&T{}` comes from the same allocator with the struct's own pointer map.

A value literal assigned to a name builds in place in its frame slot and allocates nothing; a name assigned a second literal is cleared first, so the fresh value starts from zero the way Go's does. `&T{}`, a literal in argument position, and a literal filling an interface allocate one fresh struct per evaluation, which is the allocation the Go compiler makes for a literal that escapes. A literal in return position stays on the reflect evaluator, like every returned expression.

The structs fixture benchmark, previously the only fixture off the direct tier, went from 27.2us and 72 allocations per run to 7.0us and 26 against 5.9us and 19 native: from 4.6x native to 1.4x, in line with the other fixtures.

## 2026-09-08 10:29 +02:00: composite literals

A struct is allocated the way Go writes it: `T{}`, `&T{}`, keyed and positional elements, for any struct type in the registry.

```
u = url.URL{};
u = url.URL{Path: "/x", Host: "h"};
u = &url.URL{Path: "/p"};
r = &http.Request{Method: "POST", URL: &url.URL{Path: "/n"}};
u = url.URL{"https"};                 positional, declaration order
```

Elements follow Go's rules: keyed and positional do not mix, a field is named once, and a promoted field is not a field of the literal's type. One relaxation, consistent with how a call fills missing arguments: a positional list may stop early, and the remaining fields stay zero.

An element's value is anything an argument can be: a literal, a name, a field read, a call, or another composite literal. A numeric literal converts to its field's width the way it does at a call:

```
r = &http.Request{ProtoMajor: 1};                 int, from the field
r = &http.Request{URL: url.Parse("http://h/c")};  a call as a value
```

The literal works wherever a value does: assigned to a name, passed as an argument, written to a field, returned. The value is built fresh on every run of the compiled program, matching Go's per-evaluation allocation, and it is addressable, so fields can be assigned afterwards:

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

Composite literals cover structs; there are no map or slice literal forms. A program using one runs on the reflect evaluator: no JIT node builds a struct, so the direct-call tier declines the whole program rather than mishandling it.

## 2026-09-08 10:18 +02:00: conversion hints

A literal assignment can now carry its type at the assignment:

```
x = int32(0);
```

The right-hand side parses as a call. At compile time a path that names a registered type rather than a binding cannot be called, so it is read as a conversion hint: the literal takes the named type, the way a var declaration would fix it. The var form stays; the two spellings compile to the same statement:

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

A hint binds the name to the type the way var does. Later assignments convert to it, and use does not override it:

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

The hint wraps exactly one literal. A name or a call already has a type, so converting one is rejected rather than guessed at:

```
y = 5;
x = int32(y);         a conversion takes a literal
```

A binding wins over a type of the same name, so no program that compiled before hints existed changed meaning. Hydration and the three inference rules in [types.md](types.md) are unchanged; the hint slots in ahead of them, as an explicit declaration on the assignment itself.
