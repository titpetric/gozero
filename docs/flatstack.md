---
title: What flatstack solved first
date: 2026-09-05T16:06:34+02:00
---

[phpscript](https://github.com/titpetric/phpscript) is a PHP interpreter
written in Go, and its
[flatstack](https://github.com/titpetric/phpscript/tree/main/flatstack)
module is an opt-in bytecode backend for it. It answers the same
question this repository does, for a language with a much larger
surface: what does it cost to call Go from an interpreter, and how much
of that cost can be removed. It got there earlier, so this is what
reading it changed here.

Its design document is
[docs/flatstack.md](https://github.com/titpetric/phpscript/blob/main/docs/flatstack.md).
The commit that matters most is `a9b076e`, "skip reflect on common
bindings, pool flatstack scratch", which made both of the moves this
repository later made, in the same order.

## Shape

`flatstack/` is a 63 line facade re-exporting `runner`'s types so the
backend can be swapped by changing one import. The substance is
`flatstack/engine/`:

| File | Lines | What |
|------|------:|------|
| `program.go` | 193 | opcodes, instruction, `Program`, the `Host` interface |
| `compiler.go` | 1128 | model AST to bytecode |
| `vm.go` | 1152 | the execution loop |

Two structural decisions carry the rest. `Host` is an interface of
around thirty methods holding every PHP value semantic, so the engine
owns only compilation, operand and local storage, jumps and iteration.
And `Program.localNames` is per program rather than per function, so a
slot number means the same name in every frame, which is what lets a
closure capture be copied by number without a name lookup.

## Lesson 1: boxing is what makes pooling possible

flatstack pools its per-run storage:

```go
var scratchPool = sync.Pool{...}
type vmScratch struct {
	stack, locals []any
	initialized   []bool
	deferred      []any
}
```

This repository had concluded pooling was unavailable, because an
interface argument taken from a frame slot points at the slot and
nothing proves the callee does not keep it. flatstack has no such
problem: `locals` is `[]any`, so a value is copied into the interface
and no pointer into the frame ever escapes.

The two designs are the same trade seen from opposite sides. flatstack
boxes and pools; this repository aliased and allocated. Their answer
does not port, because PHP values are already `any` and boxing costs
them nothing, while these slots hold typed Go values and boxing to
enable pooling would add the allocation pooling just removed. Measured
here, that is `sync.Pool` at 22.85n plus a box against `unsafeNew` at
62.57n and no box.

**What it changed.** Not the pooling decision, but the diagnosis behind
it. Asking why flatstack can pool made the real problem visible: the
frame should not exist. Values that flow from one call to the next
belong in Go locals, not in storage. The step JIT was rewritten from a
flat list of steps writing into a `reflect.StructOf` frame into a tree
of typed closures, one per layout class, where a value travels as a
return value. A frame slot now exists only for a name a later statement
reads, and `planInline` removes most of those by splicing a single-use
producer into its reader.

**Gain**, measured by interleaving two test binaries because the machine
was drifting 30% between runs:

| | flat list and frame | closure tree |
|---|---|---|
| ns/op, four runs | 2253, 1928, 1934, 1850 | 1811, 1734, 1750, 1742 |
| median | 1931n | 1746n |
| spread | 403n | 77n |
| B/op | 712 | 688, native's exact figure |

## Lesson 2: clear a pooled buffer to capacity, not length

```go
// The clears run to capacity rather than to length. The pool holds
// these buffers for the life of the process, so a value left above the
// high-water mark of a later, smaller program stays reachable from the
// pool and is never collected.
```

**What it changed.** Nothing yet, because nothing here is pooled. It is
recorded because the mistake is invisible: clearing to length passes
every test and leaks whatever the largest program touched, for the life
of the process.

## Lesson 3: publish the gate that says which tier ran

`flatstack.Supports(p) error` is public and documented as "call this in
benchmarks to prevent measuring an accidental fallback".

**What it changed.** `Runtime.Supports(src) error` is now public here
too, and reports why rather than only whether:

```
a name of type int has no layout class
json.Encoder.Encode: shape "PI_" is not in the table
```

**Gain.** A benchmark or a test can make a silent fall back to the
reflect evaluator a failure instead of a slow number. The equivalence
test uses it for the same reason: without it, comparing the two tiers
would pass by running reflect twice.

## Lesson 4: a fast path must keep the panic boundary

`runner.invokeAny` wraps both dispatch paths in `defer recover()` so a
panic inside a binding becomes a catchable error. The same commit that
added the fast path had to fix it, because `invokeFast` returned before
the recover was installed, and a panic then unwound the VM instead of
arriving as an exception. The commit message records that the existing
panic tests missed it, since they covered a constructor and a method,
neither of which the fast path intercepted.

**What it changed.** There was no boundary here at all. A binding panic
unwound straight through the JIT's raw frame stores, on both tiers.
`Compile` now wraps what it returns, so one `defer recover()` covers
both tiers by construction rather than by remembering, and a panic
arrives as `*PanicError` carrying the value and a stack.

## Lesson 5: put an allocation ceiling in the test suite

`TestFlatstackPrecompiledAllocationBudget` asserts a hard limit with
`testing.AllocsPerRun`.

**What it changed.** Allocation counts here were only visible in
benchmark output, so a regression would not fail anything.
`TestAllocationBudget` now compares the compiled program against the
native code it stands for on every run of the suite.

**Gain.** It immediately reported something the benchmarks had hidden:
6 allocations against native's 5 in an ordinary build. The results table
in [overheads.md](overheads.md) shows 6 against 6, which is true only under
`-gcflags=all=-l`, the flag the benchmarks use. With inlining the Go
compiler proves the cookie slice does not outlive the `Encode` call and
keeps it on the stack; the JIT hands that value to a func it
reinterpreted and has to assume it escapes. The budget is therefore
native plus one, with the reason written next to it, and that one
allocation is the floor for this design.

## What did not port: the type switch

`runner.invokeFast` avoids `reflect.Value.Call` with a type switch over
about eleven concrete signatures, `func(any) any`, `func(string) string`
and so on, falling through to reflection for the rest. No unsafe, no ABI
assumption, no equivalence test needed.

It cannot be copied here. PHP values are already `any`, so a handful of
literal cases covers nearly every binding. The bindings here are typed
Go APIs, where a type switch needs one case per concrete signature. That
is why this repository went to layout classes instead: one `SSI_PE` case
covers every `(string, string, iface) -> (ptr, error)` binding, at the
cost of `TestStepJITMatchesReflect` to hold the assumption down.

The same difference explains the two value representations. A stack
machine over `[]any` is right when every value is already an interface.
A tree of typed closures is right when the values are Go types and
boxing is a cost rather than the status quo.

## Two bugs the rewrite surfaced

Recorded because both were found by tests written earlier for other
reasons, which is the argument for writing them.

`planInline` first recorded its decision by editing the call tree. That
tree is also what the reflect evaluator runs, so a program that inlined
and then failed to JIT reached the fallback with the producer both
spliced into its reader and still standing as its own statement, and ran
it twice. It now records splices in a side table.
`TestPlanInlineLeavesTheTreeAlone` counts the calls.

The rewrite dropped the guard that refuses to alias a slot written more
than once. `TestStepJITRefusesReassignedAlias`, written when the
aliasing was introduced, failed on the first run of the new code.

## Learnings

- Boxing is what makes pooling possible: flatstack pools because its
  locals are already `any`; these slots hold typed Go values, so
  pooling would add the box it removed. The diagnosis mattered more
  than the decision: the frame should barely exist, and the closure
  tree with `planInline` is what came of asking why.
- Clear a pooled buffer to capacity, not length; the leak passes every
  test.
- Publish the tier gate. `Supports` is what stops a benchmark or an
  equivalence test measuring an accidental fallback, and it should say
  why, not only whether.
- A fast path must keep the panic boundary, and the boundary should
  cover every tier by construction: `Compile` wraps what it returns.
- Put an allocation ceiling in the test suite; it found the one
  allocation the benchmark flags were hiding.
- The prior art's central trick, a type switch over concrete
  signatures, is exactly what does not port to typed Go bindings, and
  the layout classes are the answer to that.
