---
title: What if we implemented the builtins?
date: "2026-10-30T00:00:00+01:00"
---

The compiler recognises no builtin. There is no builtin table: `append`
and `len` are ordinary identifiers that resolve through the binding
lookup like any other name, and the parser knows two statement keywords,
`var` and `return`, and three expression keywords, `true`, `false` and
`nil`.

Two builtins are nonetheless reachable from a program today, as host
bindings. `Append` takes a pointer to a slice and an element; `Delete`
takes a map and a key. A fixture binds them under their Go names, so
`append(&frozen, "appended")` runs. `len` for one element type is a
one-line binding: `func(v []string) int { return len(v) }`.

This document weighs making the rest of them reachable, and says which
form each one should take.

## Why binding stops short of a builtin

A Go program names whatever its imports export. A program here names
whatever a host binds. Those two sets differ in one way that decides
this whole document, so the table sets them side by side.

| a Go program names | a program here names | through |
| --- | --- | --- |
| a func | a func, called | `Bind` |
| a var, a const | a value, read | `Bind` |
| `&v` on a var | a pointer, written through | `Bind("x", &v)` |
| a method | a method on a bound value | the selector link |
| a field | a field on a bound value | the selector link |
| a type | a type, in `var` and `[]T{}` | `BindType`, discovery |
| a declaration | nothing: a host declares | - |
| 18 builtins | 2, as bindings | `Append`, `Delete` |
| `if`, `for`, `switch` | a non-nil trailing error | the call contract |
| `go`, `select`, `chan` | nothing | - |
| **a type parameter** | **nothing** | **-** |

Binding covers every row but the last. A host exposes a func, a value, a
pointer, a method, a field and a type; a program reads all six. A
program declares nothing of its own, so a host owns every type, which
costs a program nothing it misses.

The last row is the one that bites. A builtin is generic over its
argument's type: `len` reads a slice, a string, a map or a channel, and
one Go signature cannot say that. A binding has one signature. So
`Bind("len", func(v []string) int)` reaches a `[]string` and nothing
else, and `Bind("len", func(v any) int)` leaves the compiler nothing to
refuse, so `len(42)` compiles and fails when it runs.

That is the gap this document prices: for each builtin, whether one
signature suffices, and what a compiler case costs where it does not.

Two smaller notes from the table. Discovery widens the type row without
a host naming anything, since every `Bind` walks the bound type five
levels deep and registers what it finds. And a program branches on an
error alone, so a builtin that reads as a statement costs more here than
its Go counterpart suggests: `clear` and `close` each need a statement
node, of which the language holds few.

## How it would look

A compiler case reads at the head of `compileExpr`, before the binding
prefix scan, so the compiler resolves a name the language owns before it
looks at a host's binding of the same name:

```go
xs := strings.Fields(line)
n := len(xs)
m := cap(xs)
copy(dst, xs)
clear(seen)
close(done)
```

A binding reads the same in the program and differently in the host:

```go
rt.Bind("len", func(v []string) int { return len(v) })
```

The two forms are not interchangeable. A builtin is generic over its
argument's type; a binding has one signature. Binding `len` for
`[]string` leaves a program without `len` of a map, and binding it as
`func(v any) int` leaves the compiler nothing to check: `len(42)`
compiles and fails at run time.

## What it costs the design

Three groups resist binding entirely, each for its own reason.

`make` and `new` take a type as their first argument. The grammar admits
a type in exactly two places, after `var` and before the brace of a
slice literal: the parser resolves nothing, so it holds no type system.
Admitting a type in argument position changes the shape of the grammar.
The backends are the easy half - `reflect.MakeSlice` already runs for
slice literals and `unsafeNew` already runs for frames.

`panic` and `recover` already have a mechanism, which is not a call.
`guard` turns any panic into a `*PanicError`, and a non-nil trailing
error is the language's only conditional. A program
that could recover would need somewhere to put the branch.

`len` and `cap` of an array are constants in Go, with no runtime
function behind them. The grammar has no arrays and no untyped-constant
arithmetic, so the constant cases are unreachable from a program here
and the restriction costs nothing.

For everything else the cost of the binding form is three things. The
`any` parameter drops the static element type, so a mistake moves from
compile time to run time - `Append`'s own error strings exist to report
what the compiler could have refused. The result has nowhere to go:
`append` returns a new header and a value parameter cannot hold it, so
`Append` takes `&` and returns an `error`. And an `any` signature
reaches the bridge, so the call carries reflect's cost on a path where
every other call runs on the direct tier.

The cost of the compiler-case form is six places per builtin, the same
six every time: a branch at the head of `compileExpr`, a
kind in `vmArgKind`, an arm in `vmArg.get`, an arm in `assignArg` if the
kind has children, a case or a refusal in `planInline`, and a node in
`argNode` or `stmtNode`.

A missing refusal in `planInline` breaks a program silently. A
statement-shaped builtin with no case there does not fall back: it falls
through the `s.call == nil` check and the statement vanishes. The
value-binding write already hit that bug, so `planInline` names its
refusal explicitly and leaves nothing to the default.

## What each one costs

Non-test lines, calibrated against two features the tree has carried:
slice literals cost 115 non-test and 98 test; channels cost 245 non-test
and 222 test at the commit that removed them.

| builtin | form it should take | implementation | test |
| --- | --- | --- | --- |
| `len`, `cap` | compiler case | 40-120 | 40-100 |
| `copy` | compiler case | 60-100 | 60-90 |
| `clear` | compiler case | 50-80 | 50-80 |
| `close` | compiler case | 60-100 | 60-90 |
| `delete` | compiler case, binding today | 70-110 | 70-110 |
| `min`, `max` | compiler case | 80-130 each | 70-110 each |
| `append` | binding, as today | 180-260 | 150-220 |
| `make` | compiler case, grammar first | 160-240 | 130-200 |
| `new` | compiler case, grammar first | 90-140 | 80-120 |
| `print`, `println` | binding | 20-40 | 20-30 |
| `complex`, `real`, `imag` | neither | 350-550 | 250-400 |
| `panic`, `recover` | neither | - | - |

Two totals come out of that table. The five cheap cases, `len`, `cap`,
`copy`, `clear` and `close`, run 210 to 400 non-test lines and 210 to
360 test lines, over one shared insertion point. Every case this
document recommends or allows, which adds `delete`, `min`, `max`,
`make` and `new` and leaves out the three it declines, runs 690 to 1150
non-test and 630 to 1010 test. `make` and `new` hold the grammar change
inside their own figures and share it, so doing both costs less than the
two rows added together.

`len` and `cap` are the cheapest and the most used. Both are nearly free
on the direct tier: `sliceHdr` already carries a length and a capacity,
so the node is a load at a known offset, and a string's length is its
second word. `len` of a map or a channel reads through `reflect.Value`
and is the only part that is not a load.

`copy`, `clear` and `close` are the same shape as each other: one
statement, no result, an `lNone` node. `copy` needs the `string` to
`[]byte` case Go has; `clear` behaves differently for a map and for a
slice and reads the static kind to decide.

`append` should stay a binding. Its usable Go form assigns its own
argument's type back to itself, and the compiler cannot express that -
it types every result off a concrete `reflect.Type`'s outputs - so
`append` would need a statement node of its own in place of a call.
Growing a slice on the direct tier means reproducing `growslice`, which
is allocation the JIT has no concept of, so it would bridge anyway. The
`&` and `error` shape is the compromise this design keeps.

`complex`, `real` and `imag` are the only builtins that force a change
to the layout enum. `layout` is a closed enum and every scalar switch in
the tree covers each of its classes; a complex is sixteen bytes and fits
neither `uint64` nor `float64`, so it needs two new classes, new node
closure types, new shape keys and new arms in `loadN`, `storeN` and
`scalarBits`. Nothing in the tree, in any fixture or in any document
mentions a complex number. The cost is the highest and the demand is
zero.

## Alternatives

**Bind them per type and name them plainly.** A host that wants `len`
binds `strlen`, `countArgs` or whatever the program actually counts. The
program reads worse and the compiler checks more. This is what the tree
does today and it costs nothing.

**One `builtin` branch and the five cheap cases.** `len`, `cap`, `copy`,
`clear` and `close` together are around 300 non-test lines and one new
insertion point shared between them. Each gets a static type rule where
its compile case sits, so the compiler refuses `len(42)` where a binding
would have accepted it. This is the only part of this document worth
doing on its own.

**Wait for the type argument.** `make` and `new` wait on the grammar
admitting a type in argument position. So does a slice literal whose
element type no binding has named. Three features turn on the same
change. That argues for one grammar change, made on its own terms.

## In other imperative settings

Every language that has builtins recognises them in the compiler, and
the ones that tried making them ordinary functions moved them back.
Python's `len` reads a type slot, and CPython keeps it there because a
method call costs measurable time. Go's own spec puts the builtins in a
separate section from functions precisely because they are generic over
types and some produce constants, which is the same wall this document
hits at `make`.

An interpreter embedded in a host has one option the compiler writers do
not: the host already has the real builtin, compiled, and can expose it.
That is the binding form; two of these already work that way.

## Verdict

Bind what a host can bind and leave it there, with one exception.

`append`, `delete`, `print` and `println` are right as bindings, and two
of them already are. This document declines `complex`, `real` and `imag`
on cost against zero demand, and declines `make`, `new`, `panic` and
`recover` until something else forces the grammar or the control flow to
change.

`len`, `cap`, `copy`, `clear` and `close` are worth a compiler case, and
worth doing as one change that covers all five. They are the builtins a
program reaches for first, the binding form cannot type-check them, and
the five share one insertion point and one set of tests. Around 300
non-test lines, and `len` and `cap` reach the direct tier as a load at a
known offset because the slice header already holds both numbers.
