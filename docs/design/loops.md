---
title: What if we implemented loops?
date: "2026-09-09T00:00:00+02:00"
---

Every gozero program terminates today: n statements run at most n
calls, and the first error stops them. A loop is the first construct
that breaks that guarantee, so this document weighs both the
machinery and what the language stops being able to promise. The
constraints stand: Go syntax, no operator expressions in the AST, no
userland control flow, stdlib only.

## How it would look

The classic `for i := 0; i < n; i++` is out before it starts: the
condition and the increment are operator expressions
([expressions.md](expressions.md)). What Go offers without a single
operator is `range`, and range covers more than it used to:

```gozero
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

Slices, maps, integers, and since go1.23 iterator functions:
`strings.Lines` returns an `iter.Seq[string]`, and any binding can
return one. Range-over-func is the natural fit for this design,
because it keeps the imperative principle intact - the thing being
iterated is still a value a bound Go function produced.

The tiers can carry it. The reflect evaluator gets range nearly free:
`reflect.Value.Seq` and `Seq2` (go1.23 stdlib) yield
`iter.Seq[reflect.Value]` over slices, maps, integers, channels and
funcs alike. On the JIT, ranging a slice is a length check and a
stride walk over a `sliceHdr` the closure tree already produces;
ranging a func is a shape-cast of `func(yield func(T) bool)` and a
real Go closure as the yield. `for cond { }` with a bool-returning
call as the condition also parses as Go, but inherits every problem
in [conditions.md](conditions.md) plus non-termination, and adds
nothing range does not.

## What it costs the design

**Termination leaves the sandbox story.** The binding set is the
sandbox boundary, and today the boundary comes with "and it halts".
`for range xs` over a binding-produced value keeps a bound: the
program runs as long as the host's data. `for cond { }` does not, and
an iterator binding can be infinite on purpose. The runtime would owe
a per-iteration `ctx.Err()` check so `ExecContext` deadlines actually
end a spinning program, which is a new cost on the hot path of the
one construct added for performance-insensitive convenience.

**Loop-carried slots lose the aliasing win.** A name assigned each
iteration is written more than once by definition, so the write-once
rule that lets interface arguments alias the frame
(stepjit_arg.go) turns off for everything the loop body touches.
Loops are also exactly where per-call allocations multiply, so the
optimization dies where it would matter most.

**One frame, many iterations.** The frame is allocated once per run
and the loop variable is one slot, reused. Go itself moved to
per-iteration variables in 1.22 because the shared slot bit everyone
who captured it; without closures nothing can capture it, so the
shared slot is safe today - and becomes a landmine the day
[closures.md](closures.md) lands. The two features interact: each is
simpler without the other.

**break, continue, early return.** Same problem as conditions: the
compiled form's only exit is an error. Structured loop nodes (a body
as its own `[]nodeE`) avoid a program counter, but every control
keyword needs an out-of-band signal threaded through all seven node
types on both tiers.

**The stack hoist becomes observable.** A stack name read more than
once is loaded once, before the program runs (stepjit.go); a binding
mutating the stack is not seen by later reads. That is documented and
harmless in a straight line. Inside a loop, "later reads" means every
iteration, and the surprise stops being theoretical.

**The cost claim changes kind.** "Tens of nanoseconds over native per
call" is a statement about a program whose call count is its
statement count. With loops the multiplier is data-dependent and the
fixture benchmarks stop pinning a per-program number.

## Alternatives

**Iteration stays in the host.** The host loops, the program runs per
element. This is how the fixture suite already works - the test
harness iterates files, each program runs straight-line - and how an
HTTP server works: the listener loops, the handler does not.

- Con: the program cannot aggregate across elements without the host
  providing an accumulator binding.
- Con: per-element `Exec` pays the stack map and frame each time;
  amortizing it is the host's job.

**Fold bindings (`each(xs, f)`).** A binding that takes the
collection and something to run per element. Without closures there
is no something to pass; with closures it is userland control flow,
the Tcl path declined in [conditions.md](conditions.md). Rejected on
the same grounds.

**Range-over-func only, no general `for`.** The narrowest honest
cut: `for x := range f()` where `f` is a binding returning an
`iter.Seq`. Termination is the iterator author's problem, stated at
the binding, which is where this design puts every other capability
decision. Cons:

- Still needs blocks, a body statement list, and the break/continue
  signal - most of the grammar cost of full loops for one form.
- The loop body cannot produce a value without multi-write slots, so
  the aliasing loss stands.
- An `iter.Seq` binding is a closure the host wrote; programs that
  need a custom iteration order push that code back into the host,
  which may be the design working or a treadmill, depending on the
  program.

## In other imperative settings

The imperative code this language mirrors rarely loops. A testify
test is a straight line of asserts; an `http.Handler` builds,
encodes and writes; middleware checks and delegates. Where those do
loop, it is range-over-data the host handed over - headers, cookies,
rows - which is exactly the range-over-func cut above. Shell makes
the same split: `for f in *.txt` ranges data, while unbounded
`while` loops are rare and usually a bug. The imperative way to
process a stream in Go is increasingly an iterator someone else
wrote, consumed by range; a gozero with loops would want to be that
consumer and nothing more.

## Verdict

Loops are the most expensive of the four features relative to what
the target programs need. Range-over-func is the only form that pays
for itself, and even it drags in blocks, control signals, multi-write
slots and a termination caveat. The host-loops-program-runs split
covers handlers, tests and middleware today with no language change,
and is the recorded position.
