---
title: What if we implemented loops?
date: "2026-09-09T00:00:00+02:00"
---

Every gozero program terminates today: n statements run at most n
calls, and the first error stops them. A loop is the first construct
that breaks that guarantee. This document weighs both the machinery
and what the language stops being able to promise. The constraints
stand: Go syntax, no operator expressions in the AST, no userland
control flow, stdlib only.

## How it would look

The three-clause `for i := 0; i < n; i++` stays out: the
condition and the increment are operator expressions
([expressions.md](expressions.md)). What Go supports without a single
operator is `range`. Since go1.22 and go1.23 range covers more:

```go
for _, c := range req.Cookies() {
	jar.SetCookie(c)
}

for i := range 3 {
	log.Println(i)
}

for k, v := range hdr {
	buf.WriteString(k)
}

for line := range strings.Lines(text) {
	out.Write(line)
}
```

Slices, maps, integers, and from go1.23 iterator functions:
`strings.Lines` returns an `iter.Seq[string]`, as any binding may. Range-over-func is the natural fit for this design,
because it keeps the imperative principle intact - the loop still
iterates a value a bound Go function produced.

The tiers can carry it. The reflect evaluator gets range nearly free:
`reflect.Value.Seq` and `Seq2` (go1.23 stdlib) return
`iter.Seq[reflect.Value]` over slices, maps, integers, channels and
funcs alike. On the JIT, ranging a slice is a length check and a
stride walk over a `sliceHdr` the closure tree already produces;
ranging a func is a shape-cast of `func(yield func(T) bool)` and a
real Go closure as the callback. `for cond { }` with a bool-returning
call as the condition also parses as Go, but inherits every problem
in [conditions.md](conditions.md) plus non-termination. Range already
covers every iteration it would express.

## What it costs the design

**The termination guarantee goes.** The sandbox guarantee has two
parts today: a program reaches only what the host binds, then halts.
`for range xs` over a binding-produced value keeps a bound: the
program runs as long as the host's data. `for cond { }` does not, and
an iterator binding can be infinite by intent. The runtime would owe
a `ctx.Err()` check so `ExecContext` deadlines end a spinning
program. That check lands on every iteration.

**Loop-carried slots lose the aliasing win.** Because a name assigned
each iteration takes more than one write by definition, the
write-once rule that lets interface arguments alias the frame
(stepjit_arg.go) turns off for everything the loop body touches. A
loop body also multiplies per-iteration allocations. The optimization
turns off where the allocation count is highest.

**One frame, many iterations.** Each run allocates the frame once
and the loop variable is one slot, reused. Go moved to per-iteration
variables in 1.22 because captures of the shared variable observed
the wrong iteration; without closures nothing can capture the slot.
Sharing it holds up today and stops holding up the day
[closures](closures.md) can capture it. The two features interact:
each is simpler without the other.

**break, continue, early return.** Same problem as conditions: the
compiled form's only exit is an error. Structured loop nodes (a body
as its own `[]nodeE`) avoid a program counter, but every control
keyword needs an out-of-band signal threaded through all seven node
types on both tiers.

**The stack hoist becomes observable.** A stack name read more than
once loads once, before the program runs (stepjit.go); later reads
miss a binding's mutation of the stack. The documentation records
that, and a straight line suffers no harm. Inside a loop, "later reads" means every
iteration: a program polling a stack value never sees it change.

**The cost claim changes kind.** "Tens of nanoseconds over native per
call" is a statement about a program whose call count is its
statement count. With loops the multiplier is data-dependent and the
fixture benchmarks stop pinning a per-program number.

## Alternatives

**Iteration stays in the host.** The host loops, the program runs per
element. This is how the fixture suite already works - the test
harness iterates files, each program runs straight-line - and how an
HTTP server works: the listener loops, the handler does not.

- Con: the program cannot sum or collect across elements without the
  host binding an accumulator.
- Con: per-element `Exec` pays the stack map and frame each time;
  amortizing that cost is the host's job.

**Fold bindings (`each(xs, f)`).** A binding that takes the
collection and something to run per element. Without closures there
is no something to pass; with closures this becomes userland control flow,
the Tcl path declined in [conditions.md](conditions.md). Rejected on
the same grounds.

**Range-over-func only, no general `for`.** The narrowest cut:
`for x := range f()` where `f` is a binding returning an
`iter.Seq`. Termination is the iterator author's problem, stated at
the binding, where this design puts every other capability
decision. Cons:

- Still needs blocks, a body statement list, and the break/continue
  signal - most of the grammar cost of full loops for one form.
- Because the loop body cannot produce a value without multi-write
  slots, the aliasing loss stands.
- An `iter.Seq` binding is a closure the host wrote; a program that
  needs a custom iteration order pushes that code back into the
  host.

## In other imperative settings

The imperative code this language mirrors rarely loops. A testify
test is a straight line of asserts; an `http.Handler` builds,
encodes and writes; middleware checks and delegates. Where those do
loop, they range over data from the host - headers, cookies,
rows - which is the range-over-func cut above. Shell makes the same
split: `for f in *.txt` ranges over data the filesystem lists.
The imperative way to process a stream in Go is increasingly an
iterator someone else wrote, consumed by range; a gozero with loops
would be that consumer.

## Verdict

Loops are the most expensive of the four features relative to what
the target programs need. Range-over-func is the only form that pays
for itself, and even that form requires blocks, control signals, multi-write
slots and a termination caveat. The host-loops-program-runs split
covers handlers, tests and middleware today with no language change,
and is the recorded position.
