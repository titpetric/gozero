---
title: Loops
date: "2026-09-21T09:35:00+02:00"
---

Loops moved from research into the syntax on 2026-09-21. This document records what landed and its semantics, how the construct runs on each tier with the measured cost, and what stayed out with the reasons the research established. The original question, "what if we implemented loops?", is answered for the restricted form: `for` over `range`, across slices, arrays and integers, with `break` and `continue`.

Before it landed, every program terminated structurally: n statements ran at most n calls, and the first error stopped them. That is the guarantee this feature spends, and the section below says what replaced it.

## What landed

```go
for _, c := range cookies {                   a slice, by value
	jar.SetCookie(c)
}

for i := range parts {                        a slice, by index
	log.Println(i)
}

for i := range 3 {                            the go1.22 integer range
	log.Println(i)
}

for range parts {                             neither name bound
	c.Add(1)
}

for _, s := range parts {                     the two exits
	if skip(s) { continue; }
	if done(s) { break; }
	buf.WriteString(s)
}
```

The rules, each rejected at compile time with the rule named:

- One `for` form exists, `range`. The three-clause and condition loops need operator expressions, which the AST has no tree for, so neither is in the grammar to reject.
- A slice or an array binds an index and a value; an integer binds one variable, typed as the bound, which is Go's own rule for the go1.22 form. A negative signed bound runs zero times, as in Go.
- The ranged expression needs a static type: an integer literal, a name the program bound, a field read off one, or a call. A stack name is rejected, because its type is only known at execution.
- `break` and `continue` exit the innermost loop and are only allowed inside a range body. A label after either is rejected by name.
- `return` and `var` cannot stand inside a body. A loop's exits are `break`, an error and the execution context, and a name a body declares has nowhere to go but the program's own scope.
- `for`, `range`, `break` and `continue` join the reserved words: a name cannot shadow any of them.

Flat scope is what a loop makes visible, and it is documented rather than silent. The loop variable is one program-level slot reused per iteration, so after the loop it holds the last value it took and an empty loop leaves it zero. A `:=` inside a body is allowed and the name it declares outlives the loop. Both diverge from Go's block scoping, both hold identically on the two tiers, and `TestRangeMatchesReflect` pins them there.

## How it runs

The reflect tier runs a body through `runStmts`, the same function the program's own list and an `if` arm go through. The direct tier compiles a structured node holding the body as its own `[]nodeE`, built by the same `blockNodes` an `if` arm uses: no program counter, no jump, and the only exit is the error return. Ranging a slice is a length check and a stride walk over the `sliceHdr` the closure tree already produces, with each element copied into the value slot because Go's range variable is a copy and not a view; ranging an integer is a counted loop over the bound's bits. The loop variable is written through the same typed frame stores every other statement uses.

`break` and `continue` are two more control signals beside the `return` one conditions landed: unexported sentinel errors that travel the error return every statement already has, raised by the statement and consumed by the innermost loop's step. The nil-error hot path is untouched, because the comparison only runs when a body statement returns non-nil, and a binding cannot forge one, because the values are unexported.

Two costs the research predicted are real. A program with a loop takes the structural plan that splices nothing, for the same reason a program with an `if` does: a producer's single reader cannot be proven to run once when a loop boundary sits between them. And every loop-carried slot counts as written twice, so the write-once interface aliasing turns off for anything the body touches. `BenchmarkRangeAliasing` measures the second directly, four identical calls passing a string to an `any` parameter from a write-once slot and from a loop variable.

## What the termination guarantee became

The guarantee is now context-bounded rather than structural. Every iteration checks `ctx.Err()` before it runs, on both tiers, so a cancelled `ExecContext` ends the program with `ctx.Err()` at the next iteration boundary and no iteration past the cancel; `TestRangeContextBoundsTheLoop` pins the count on both tiers. Within that, a `range` over a binding-produced value still has a bound the host set: the program runs as long as the host's data.

## What stayed out

- **Maps, strings, channels and iterator funcs.** Each is a Go range source and each is compile-rejected by name, `cannot range over map[string][]string` and the rest. The reflect tier could carry all four nearly free through `reflect.Value.Seq`; the direct tier is where they cost - a pooled `MapIter`, the armed receive repeated, a shape-cast yield closure per run - and none is needed by the shipped surface.
- **`for cond { }`.** It parses as Go, but inherits every problem in [conditions.md](conditions.md) plus non-termination, and adds nothing `range` does not.
- **The three-clause `for`.** The condition and the increment are operator expressions ([expressions.md](expressions.md)); `n++` exists as a statement, which is exactly why it cannot fill a `for` header.
- **Labels.** `break` and `continue` exit the innermost loop only. A label is a second namespace for one more control edge.
- **Block scope.** Flat scope is the rule everywhere, and the loop variable is the case where Go itself changed its mind: Go moved to per-iteration variables in 1.22 because captures observed the wrong iteration. Without [closures](closures.md) nothing can capture the slot, so sharing it is safe today and stops being safe the day it is not. The two features interact, and each is simpler without the other.

Two research findings survive unchanged. The stack hoist is observable inside a loop: a stack name read more than once is loaded once before the program runs, so "later reads" now means every iteration and a program polling a stack value never sees it change. And the cost claim changes kind - "tens of nanoseconds over native per call" described a program whose call count was its statement count, and with a loop the multiplier is the host's data.

## Alternatives, and where they still apply

**Iteration stays in the host.** The host loops, the program runs per element. This is still how the fixture suite works and how an HTTP server works: the listener loops, the handler does not. It remains the right split whenever the per-element work is a whole program, and the loop only removes the case where the program has to aggregate across elements without an accumulator binding.

**Fold bindings (`each(xs, f)`).** A binding that takes the collection and something to run per element. Without closures there is nothing to pass; with closures it is userland control flow, the path declined in [conditions.md](conditions.md). Still rejected on the same grounds.

**Range-over-func only.** The narrowest cut the research proposed, `for x := range f()` over an `iter.Seq`. It was declined for costing most of the grammar of full loops to buy one form; the shipped cut is the other narrow one, and the iterator form is now the cheapest of the sources still outside, because the blocks and the signals it needed are built.

## In other imperative settings

The imperative code this language mirrors rarely loops. A testify test is a straight line of asserts; an `http.Handler` builds, encodes and writes; middleware checks and delegates. Where those do loop, it is range-over-data the host handed over - headers, cookies, rows - which is what the shipped slice range is. Shell makes the same split: `for f in *.txt` ranges over data the filesystem provides.
