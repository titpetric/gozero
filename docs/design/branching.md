---
title: What if we precompiled the branches?
date: "2026-10-30T00:00:00+01:00"
---

A proposal: drop the program-counter shift on a condition and inline the
code after every condition into both outcomes, so a program becomes N
straight-line step sequences and the condition picks which one runs. No
jump, no program-counter housekeeping, one path per outcome.

This document answers whether that differs from a jump. It does not: the
proposal is a jump with the program duplicated behind it. The
transformation has a name, and the literature carries the experiment and
its numbers.

## What it already is

Copying the code after a join so each path gets its own copy is **tail
duplication**, the duplication half of superblock formation (Hwu et al.,
*The Superblock: An Effective Technique for VLIW and Superscalar
Compilation*, J. Supercomputing 7(1-2), 1993). The proposal applies that
transformation eagerly to a whole program, where superblock formation
applies it to profiled hot traces.

It does not remove the control transfer. Where the condition picks a
sequence, the machine assigns to the program counter from a table. One
`pc += offset` has become one `pc = table[cond]` plus N copies of the
tail. The copies are the cost; the transfer stays.

The transformation that does remove a branch is the opposite one:
**if-conversion** and predication (Allen, Kennedy, Porterfield and
Warren, *Conversion of Control Dependence to Data Dependence*, POPL
1983) compute both arms and select a result. It merges paths where tail
duplication splits them, and it pays when both arms are a few
instructions.

Taken to its limit the proposal is the **first Futamura projection**
(Futamura 1971): specialising an interpreter for a fixed program
produces that program compiled. The projection that produces a compiler
is the second, which specialises the specialiser for the interpreter. So
the proposal describes compiled output, and the machinery that produces
it for any program is a compiler. Driving the tree of possible
computation histories without a generalisation step is
**supercompilation** (Turchin, *The Concept of a Supercompiler*, TOPLAS
8(3), 1986) with the part that makes it halt left out.

The forms that work in practice are the lazy, guarded ones. A **trace
JIT** records a linear path at run time, guards every condition it
recorded, and bails out to the interpreter when a guard fails (Bala,
Duesterwald and Banerjia, *Dynamo*, PLDI 2000; Gal et al., *Trace-based
Just-in-Time Type Specialization*, PLDI 2009). **Basic block
versioning** duplicates a block per context, lazily, capped by a version
limit (Chevalier-Boisvert and Feeley, ECOOP 2015).

## What it costs the design

**The eager form runs slower.** Chevalier-Boisvert and Feeley
implemented both. Lazy versioning removed 71% of type tests for up to
47% speedup, at a mean code-size cost of 0.25%. Eager versioning at a
five-version cap produced code at **223% of baseline and ran 5%
slower**. The proposal here is the eager form with no cap.

**A loop has no finite duplication.** A counting loop with a runtime
trip count needs one straight-line sequence per trip. With k conditions
in the body the path count is n·2^k. The options are a back edge, which
is a jump; unrolling to a static bound with a residual loop, which still
has a jump; or generating steps at run time, which is a trace JIT. Even
bounded, the first condition inside the loop has to rejoin a shared
continuation, and that rejoin is the assignment the proposal set out to
remove.

**The duplicates carry one bit.** Each copy records "this condition
held". That is worth code only if something downstream re-tests it. If
nothing does, the copies are bloat, and that is exactly the criterion a
version limit encodes.

**Hardware caps the payoff before any of this.** The branch in question
is a conditional branch on a condition that leans one way in most
programs, and modern predictors handle that. Rohou, Swamy and Seznec
(*Branch Prediction and the Performance of Interpreters - Don't Trust
Folklore*, CGO 2015) showed that Haswell-class hardware predicts even
the indirect dispatch branch well at modest working sets. Zheng and
Sasaki (IEEE CAL 2026) bound what remains: removing 73.7% of CPython's
bytecode-jump mispredictions bought 3.2% harmonic-mean IPC.

Duplication has one measured win, and it lands on the dispatch tail.
Clang lowers a computed goto to a shared indirect branch and then
tail-duplicates the dispatch back into every opcode handler; when LLVM
19 stopped doing that, CPython lost around 9 to 11%. Duplicating the
dispatch tail is worth roughly ten percent. Duplicating the user
program's paths is worth nothing.

## What a bool already buys

Equality needs no builtin and no case. `bool` is a layout class, so a
binding that returns one reaches the direct tier as a load of a single
byte, and a host writes it in one line. `reflect.DeepEqual` is the
general form; `assertEqual` in the test tree shows the cheaper one,
comparing two comparable values as interfaces and keeping `DeepEqual`
for the rest.

The bool is not the problem. A program can assign it to a slot, pass it
to another binding, and return it. Nothing consumes it as a condition,
because the language has one conditional and that conditional reads an
error.

Which names the hack this document otherwise declines. A one-armed
condition already works, spelled as a binding that returns an `error`:

```go
requireEqual(want, got)   // a non-nil error ends the program here
next(w, r)
```

That is a guard, and a guard is most of what a condition does in the
programs this runtime runs. It costs nothing, it reaches the direct
tier, and the branch it needs is the one the call contract already
carries. A two-armed `if`, where both outcomes continue, is the thing
that needs `conditions.md`.

## What this costs here, today

Nothing, because there is no jump to replace. The dispatch loop ranges
over the statement slice and never assigns to the index. There is no
branch op and no program counter. `planInline` refuses a return before
the last statement because that is not a straight line, and the
non-nil trailing error is the language's only conditional.

So the question is not whether to replace the jump. The question is what
a visible `if` should compile to when one arrives, and `conditions.md`
already prices that: two statement lists and a node that runs one of
them. On the direct tier `bool` is already a layout class, so the
condition itself costs nothing new.

Precompiling both arms would be a decision taken at that point, and the
literature points one way: take the branch.

## Alternatives

Ranked by cost against measured payoff. The first two are the ones
worth having.

**Superinstructions.** One handler for each of the top static step
sequences, found by a peephole pass over the flat slice. Selective
inlining measured 1.20 to 2.41x over a switch interpreter (Piumarta and
Riccardi, PLDI 1998; Gagnon and Hendren, SableVM); combined with
replication, Casey, Ertl and Gregg reported up to 4.55x over efficient
threaded code (TOPLAS 29(6), 2007). This is the cheapest real win in the
literature and it fits a flat statement slice directly.

**Remove the remaining reflect on bridged calls.** A call outside the
shape table bridges, and the bridge carries reflect's per-argument cost
on a path where nothing else does. Widening the shape table is what
`prep/3-shapes` does and it needs no new concepts.

**Quickening and type specialisation.** Brunthaler measured a 1.4x
median (DLS 2010); PEP 659 attributes most of its 10 to 60% to
specialisation. Already spent here: monomorphisation by ABI layout class
is the core of the direct tier, and because the subset is statically
typed it happens at compile time with no guard and no deoptimisation.

**Closure compilation.** Replace the step slice with closures built once
at compile time, each specialised over its constants (Feeley and
Lapalme, *Using Closures for Code Generation*, 1987). Also already
spent: that is what the direct tier is.

**Lazy guarded versioning.** If a later design needs path
specialisation, the shape that works is a trace recorded at run time
with a guard at each exit, a version cap per block, and a bail-out to
the step walker. It needs runtime code generation, which in Go means
closures or copy-and-patch (Xu and Kjolstad, OOPSLA 2021). That is a
project in its own right.

## In other imperative settings

Every system that shipped this transformation shipped it lazily and
guarded, and every one of them caps the duplication. Dynamo averaged
about 9% over optimised binaries and bails out entirely on two SPEC
benchmarks. TraceMonkey linked side exits to new traces, duplicating
nothing ahead of time. YJIT in CRuby is basic block versioning with a
version limit. The static variant of versioning (Melançon, Feeley and
Serrano, ECOOP 2024) reports that without a cap the result is code-size
explosion.

The pattern across every one of them: observed execution drives the
duplication, a budget bounds it, and a guard reverses it.

## Verdict

Declined, on the grounds that the proposal is a jump with extra copies.

The transformation is tail duplication; the published figure for the
eager whole-program form is 223% code size and 5% slower than the
baseline it replaced; loops make it unbounded; and the branch it removes
costs nothing on current hardware. The proposal's own framing answered
it: this is branch precompilation, and branch precompilation taken to
its limit is a compiler.

Nothing here applies to the tree today, which has no branch, no program
counter and no loop. When a visible `if` arrives it should compile to a
node holding two statement lists and a condition, and the condition
should branch.

What is worth taking from this research is the alternative list above,
and the first entry in particular: superinstructions over the top static
statement sequences, as a peephole pass over the slice that already
exists.
