---
title: Changelog
date: "2026-09-08T10:18:28+02:00"
---

Changes to the language and the runtime after the chapters were written, newest first. Each entry records when it landed, what the syntax gained, and how it is used.

## 2026-09-21 14:05 +02:00: one operator per assignment

The right side of an assignment may combine exactly two values with `+`, `==` or `!=`: `s := a + b` concatenates strings and adds integers and floats at every width, `ok := a == b` compares booleans, numbers and strings. That pair is the whole expression surface. A second operator, parentheses, every other Go operator and an operator in any other value position are rejected at parse with the rule named, so no expression tree exists for a later feature to inherit, and the four ordering comparisons keep the header placement they landed with. An operand is a program-bound name or a literal, never a call, a field read or a stack name; the two sides carry identical types, a literal side adopts the other under the usual representability checks, and two literals are rejected rather than folded, because folding is `go/constant`'s job.

Both tiers run an oracle table whose expected values are computed by the same expressions in compiled Go, so a divergence fails against the compiler rather than passing as a shared misunderstanding between tiers: `int8` 120 + 10 wraps to -126, `uint8` 255 + 1 to 0, a `float32` sum rounds once at 32 bits and overflows to `+Inf`, and NaN compares unequal to itself. The comparison half is the `if` header's kit on both tiers - `cmpEval` on the reflect side, `cmpNodeN` and `cmpNodeOf` on the direct side - so `==` has one implementation, and the signed canonicalization that kit already carries is what makes `int8(-1) == -1` true where a raw bit compare reads false. On the direct tier an operator is two loads and a native operation between machine words: scalar operators allocate nothing per run (`TestBinopScalarZeroAlloc`), and `s := a + b` allocates exactly the one string compiled Go allocates (`TestConcatAllocParity`).

Measured with the pinned harness, medians of three 1s runs: the concat fixture runs 3.641 us at 4 allocations against its handwritten mirror's 3.144 us at 6, the two being the `[]any` packs behind the mirror's `fmt.Sprintf` calls that argument pooling recycles. Every pre-existing benchmark keeps its allocation count exactly; `BenchmarkCostWithoutCaching`, which parses on every iteration, moves 32 B/op, 1776 to 1808, the parsed `stmt` growing by the operator and its two operand pointers. A result slot read through an `any` parameter either aliases the frame (written once: no box, frame unpooled) or copies into a box (rewritten: one box per read, frame pooled); `TestBinopAliasingCost` pins all four corners at 2, 3, 1 and 2 allocations per run.

## 2026-09-21 12:30 +02:00: func literals in argument position

`mux.HandleFunc("/health", func(w, r) { ... })` compiles: a func literal stands where an argument does, its parameter types inferred from the func signature of the parameter it fills, the capture-free form of [closures.md](design/closures.md). The body is the braced statement list every construct with a body already reads, with the parameters as typed names, so `w.WriteHeader(201)` resolves against `http.ResponseWriter` at compile time and an `if` chain or a loop stands in a body like anywhere else. Reading any enclosing name, an outer literal's parameter included, is a compile error naming the rule, as is a literal in a position whose expected type is not a func signature. Nothing captured means nothing per-run to close over: the value is a compile-time constant on both tiers, built once per compilation. `func` joins the reserved words, because a name spelled `func` parses as a literal when read back.

Materialization is per tier. The reflect evaluator wraps the body in `reflect.MakeFunc` through the same `materializeVia` FuncOf uses; the step JIT replaces that value with an ordinary Go closure of the signature's layout shape over the body's compiled nodes when the signature is in the closure table (no results, no context parameter), leaving no MakeFunc in the call path. The materialized `fmt.Fprint(w, "ok")` handler runs in 170.3 ns with zero allocations per call against 77.36 ns for the identical native closure, and against 876.0 ns and 2 allocations for the same body through FuncOf's MakeFunc bridge, medians of three pinned 1s runs.

The funclit fixture serves a recorded request through a ServeMux in 14.98 us and 34 allocations against 10.60 us and 33 for the handwritten mirror. The one-allocation gap is `w.WriteHeader(201)` inside the body: the shape `Ii64_`, an interface receiver and an int64 with no result, is not in the call table, so that statement bridges through reflect while every other call in the fixture, the literal's own materialization included, is direct. Every pre-existing benchmark keeps its allocation count exactly; `BenchmarkCostWithoutCaching`, which parses on every iteration, moves 48 B/op, 1728 to 1776, the parsed `arg` struct growing from 128 to 136 bytes for the literal pointer and two of its allocations crossing a size class.

## 2026-09-21 11:56 +02:00: FuncOf materializes a program as a func value

`rt.FuncOf[F](src, params...)` wraps a compiled program in a `reflect.MakeFunc` bridge of any non-variadic func type F, so a program serves `mux.HandleFunc` or any other func-shaped API without a hand-written adapter per signature. This is the host API step [closures.md](design/closures.md) recommends, with no grammar change: parameters enter the run as named stack values (`params` in order, `arg0..argN-1` when omitted), the first `context.Context` parameter doubles as the execution context, one result besides a trailing error fills from `return`, and the error result carries the program's error - without one a failing run panics with it. A parameter stays an opaque stack value: it is read in argument position, and a method call on one takes a binding that accepts the value.

Every signature rule is a `FuncOf`-time error naming it: a non-func F, a variadic F, more than one result besides error, an error result that is not last, a name count that does not match the parameter count, and a parameter name that is empty, repeats, or shadows a binding or a keyword. The last rule reads the same predicate an assignment does, so the words the statement grammar took - `if`, `for`, `range`, `type` among them - are out of bounds for a parameter too, and the two lists cannot drift. A result the program produces that does not fit F reports through the error result at run time rather than panicking.

The bridge is built once and the per-call stack map is pooled, so a call costs `reflect.MakeFunc`'s own marshalling and nothing else the runtime adds: the materialized `fmt.Fprint(w, "ok")` handler runs in 946.8 ns with 2 allocations and 64 B per call against 74.42 ns and zero for the identical native closure, both medians of three pinned 1s runs. The two allocations are the argument `[]reflect.Value` and the box the interface argument is copied into, both inside reflect; the pooled stack map, the pooled frame and the pooled argument pack contribute zero, and the program itself runs on the direct tier through the `IL_i64E` shape. Every pre-existing benchmark keeps identical bytes and allocation counts under benchstat.

## 2026-09-21 11:39 +02:00: struct field tags

A declared struct's fields can carry Go tags: a raw backquoted string, the lexer's first raw-string token, or a double-quoted string, on the field's own line. The parser records the spelling unchanged and the compiler hands it to `reflect.StructField.Tag`, so the json output is fully shaped: keys renamed by tag, a field omitted when empty, a `"-"` field never emitted. The tag is part of the type's identity: identical declarations still canonicalize to one runtime type, and two differing only in a tag mint two. A single-quoted tag is a parse error naming the two legal spellings, a tag on an embedded field falls under the embedding rejection, and an unterminated raw string is named as such. A raw string is admitted nowhere else in the grammar.

Measured with the pinned harness, medians of three. The new reply fixture encodes a `Reply` with a tagged `Status` nested inside, byte for byte, on the direct tier: 7728 ns/op, 976 B/op and 13 allocs/op against the handwritten mirror's 5898 ns/op, 768 B/op and 14 allocs/op. The vm aliases each write-once struct slot into the encoder's interface argument where Go boxes a copy, and pays the escaped frame instead. A struct slot written more than once cannot alias and has no transport class, so encoding it whole bridges through reflect: the same encode of a `var`-declared slot filled by a field write costs 10 allocs/op and 352 B/op against the write-once literal's 5 and 240, a delta of 5 allocations and 112 B. `TestTypeDeclSupports` pins that decline by name. benchstat over the paired sweeps shows every existing benchmark keeping its allocation count and its bytes exactly.

## 2026-09-21 11:22 +02:00: struct type declarations

A program can declare its own struct types with the grammar's first declaration form: `type Name struct { ... }`, one field per line or per semicolon, each an exported name and a typeref resolving through the registry. The compiler builds every shape once per source string with `reflect.StructOf` into a per-compilation registry consulted by `lookupType`, so `var` statements, composite literals, field access and json encoding work on a declared type unchanged. Declarations resolve in dependency order over as many passes as it takes, so a struct can hold one declared after it, by value only. The registry lives on a copy of the compiler, so concurrent compilations under the read lock never see another program's names.

Every `reflect.StructOf` ceiling is a compile error naming the rule: unexported fields, duplicate fields, recursion even through a pointer, and shadowing a registered type or binding. Tags, field name lists and embedded fields are parse errors until a later rung, a declaration inside an `if` arm or a loop body is a parse error too, and pointer, slice and chan spellings of a declared type do not resolve. `type` and `struct` join the reserved words. The declared name never reaches reflect: `%T` prints the unnamed struct spelling, and two structurally identical declarations are one runtime type.

The direct tier gains one field-table case for the composition declarations make common: a read through a struct held by value inside another struct compiles to added offsets, bottoming out at a frame slot or a nil-checked pointer load. A write through the same chain, and a whole struct written into a field, stay on the reflect evaluator, each declined by name.

Measured with the pinned harness, medians of three. The new typedecl fixture runs at 4866 ns/op, 240 B/op and 6 allocs/op against its handwritten mirror's 3785 ns/op, 240 B/op and 6 allocs/op: byte-for-byte and allocation-for-allocation parity, because a declared struct costs what a composite literal of a host type costs. Every existing benchmark keeps its allocation count and its bytes exactly, with one exception: `BenchmarkCostWithoutCaching` moves 24 B/op, 1704 to 1728, the parsed program struct growing by the slice header the declarations are collected in.

## 2026-09-21 10:55 +02:00: condition and three-clause for loops

`for` gains Go's other two forms, both header-scoped. A condition loop runs while a bool name, field or call holds, the operand set an `if` header already had; the three-clause header admits exactly an init assignment, one comparison, and the loop variable stepped with `++` or `--`. Neither operator is new: the comparison is the `if` header's rung and the post clause is the step statement, so statements and arguments stay operator-free and a comparison still parses nowhere but a header. `continue` still runs the post clause, `break` skips it, and the loop variable keeps its last value after the loop, one flat slot as everywhere.

The loop variable is an integer, typed by the init value or, for an integer literal, by the operand it is compared with, the rule an untyped constant follows. It steps at that width and truncates, so a `uint8` counter wraps at 255 the way Go's does, identically on both tiers, because both tiers step it through the same two pieces the step statement landed. Every header rule is rejected at compile time with the rule named: a bare `for`, a constant condition, a comparison in the condition form, a clause left out, a post clause on another name, a loop variable that is not an integer, and a body that reassigns a header name at another type, which would otherwise read the new value through the old type on the next iteration.

Neither form carries a data bound, so non-termination is now expressible; the per-iteration `ctx.Err()` check is the bound, and a cancelled or expired context ends a spinning loop with its error on both tiers, having run no iteration past the cancel.

Measured with the pinned harness, medians of three. The new `for` fixture runs at 4416 ns/op, 168 B/op and 9 allocs/op against its handwritten mirror's 2560 ns/op, 184 B/op and 10 allocs/op: the headers themselves allocate nothing and the one-alloc win is the pooled string box the mirror pays. `BenchmarkForAliasing` prices the write count at four `any`-parameter calls: 114.5 ns, 16 B and 1 alloc from a write-once slot against 441.0 ns, 64 B and 4 allocs from a slot a loop body rewrites, which is what the Go compiler boxes at the same site. Every existing benchmark keeps its allocation count exactly; `BenchmarkCostWithoutCaching` moves 16 B/op, 1688 to 1704, the parsed statement struct growing from 192 to 200 bytes for the header pointer.

## 2026-09-21 10:09 +02:00: comparisons in the if header

The `if` header gains one comparison: `==`, `!=`, `<`, `<=`, `>` or `>=` between two operands, each a name, a field path, a call, or a literal. Both sides carry identical static types, a literal side adopts the other side's type, and the comparison runs on the underlying kind, so `time.Since(t) < time.Hour` orders a named `time.Duration` like the int64 it is. The operators do not exist outside the header; every other position rejects them at parse time (comparison placement). `Runtime.BindValue` is what carries `time.Hour` into a program, and its value now reads as a comparison operand as well as a call argument.

The operand set is closed and every rejection is named at compile time: integers, floats and strings compare and order, bool compares with `==` and `!=` only, and pointers, interfaces, structs and `nil` are out of the rung (comparable scalar). Mixed static types do not compare (identical types), two literal sides are a constant condition (constant comparison), and an operand naming nothing the program bound is rejected (comparison operand).

On the direct tier a comparison compiles to one class-closed node, two loads and a machine compare, allocating nothing. Narrow signed classes canonicalize through one sign-extension step first, because the tier carries loads zero-extended while the constant path sign-extends; without it `int8(-1)` would compare above zero, and an equivalence test pins that case on both tiers. The condition shape table gains `i64_b` for an integer predicate. A call whose struct result has no layout class, `time.Now` among them, now bridges as a whole statement instead of declining the whole program to the reflect evaluator, with the call named in `Supports` output.

Measured with the pinned harness, medians of three. The new `since` fixture runs at 5471 ns/op, 680 B/op and 13 allocs/op against its handwritten mirror's 2625 ns/op, 592 B/op and 8 allocs/op, where the entire 5-alloc, 88-byte delta is the two reflect bridges through `time.Time`; the comparison-free stanzas hold allocation parity. `BenchmarkCmpHeader` prices the comparison against the L1 way of writing the same guard, a bound bool predicate: 196.4 against 178.7 ns/op at the same one boxing allocation. Every existing fixture keeps identical allocation counts and identical bytes per operation.

## 2026-09-21 09:35 +02:00: range loops over slices, arrays and integers, with break and continue

The language gains its first loop: `for x := range xs` over slices and arrays, and `for i := range n` over integers, with the braced statement list conditions landed. No other `for` form exists, because the three-clause and condition loops need operator expressions the AST has no tree for. `break` and `continue` work and exit the innermost loop; a label after either, and either outside a range body, is rejected by name. `return` and `var` cannot stand inside a body, each with its rule named. Maps, strings, channels and iterator funcs stay out and reject with `cannot range over <type>`.

The termination guarantee changes kind. A program without a loop halted structurally, n statements running at most n calls; with one it halts on the execution context, which every iteration checks on both tiers. A cancelled `ExecContext` ends the program with `ctx.Err()` at the next iteration boundary, having run no iteration past the cancel, and the count is pinned on both tiers.

Scope stays flat and the loop is where that is visible, so it is documented rather than silent: the loop variable is one program-level slot reused per iteration, it still holds its last value after the loop, an empty loop leaves it zero, and a `:=` inside a body declares a name that outlives the loop. All of it holds identically on the two tiers.

The reflect tier runs a body through `runStmts`, the same function the program and the `if` arms use. The direct tier compiles a structured node whose body is the `[]nodeE` an arm gets: a slice range is a stride walk over the `sliceHdr` with the element copied into the value slot, an integer range is a counted loop over the bound's bits, and the loop variable is written through the typed frame stores every statement uses. A program with a loop takes the same structural plan a program with an `if` takes, which splices nothing and counts a loop-carried slot as written twice, so the write-once interface aliasing turns off for it. An array bound or an element type with no layout class declines with the reason and runs on the reflect evaluator.

Measured with the pinned harness, medians of three. The range fixture runs at 2560 ns/op, 120 B/op and 5 allocs/op against its handwritten mirror's 1574 ns/op, 136 B/op and 6 allocs/op: the loop body itself adds no allocation, and the one-alloc win is the pooled string box the mirror pays as `convTstring`. `BenchmarkRangeAliasing` prices the write count at the same four `any`-parameter calls: 103.4 ns, 16 B and 1 alloc from a write-once slot against 575.5 ns, 128 B and 5 allocs from a loop variable, which is what the Go compiler boxes at the same site. Every existing benchmark keeps its allocation count exactly; `BenchmarkCostWithoutCaching` moves 16 B/op, the parsed statement struct growing from 176 to 192 bytes for the loop and its two exits.

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
