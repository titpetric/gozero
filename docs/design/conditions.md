---
title: What if we implemented conditions?
date: "2026-09-09T00:00:00+02:00"
---

The language has one conditional today, and it is invisible: a
non-nil trailing error ends the program. Every call is an implicit
`if err != nil { return }`. This document asks what a visible `if`
would cost, under the constraints the design already carries: Go
syntax only, no operator expressions in the AST, no userland
evaluation, stdlib only.

## How it would look

Go's `if` accepts any boolean expression. Without operators, the
subset that remains is a bool name, a bool field, or a bool-returning
call. All three are legal Go and all three are values the compiler
already types from the bindings:

```gozero
ok := strings.HasPrefix(path, "/api")
if ok {
	next.ServeHTTP(w, r)
}

if req.Close {
	conn.Close()
}

if strings.EqualFold(method, "GET") {
	json.NewEncoder(w).Encode(cached)
}
```

`bool` is already a layout class (`lBool` in stepjit.go), so the
condition costs nothing new on any tier: the JIT evaluates a `nodeN`
and branches on its bits, the reflect evaluator calls `Bool()`. An
if-node holds two statement lists and runs one of them. The machinery
is small; the damage is elsewhere.

## What it costs the design

**The grammar stops being line-oriented.** A statement is currently
one line, closed by EOL; the parser has no nesting. Blocks bring
brace tracking, multi-line statements, and a statement tree where
there is now a flat list. This is the parsing extension the design
avoids on purpose, and it does not stop at `if`: `else`, `else if`
and nesting come with the syntax, or the language ships a visibly
incomplete `if`.

**Early return breaks the control contract.** `planInline` rejects a
return before the last statement, because the compiled form is a flat
`[]nodeE` run front to back and the only exit is an error. A return
inside a branch needs a second out-of-band signal - a sentinel error,
or a done flag every node checks - threaded through all seven node
types. The reflect evaluator needs the same change. Both tiers change
shape for the feature's least common case.

**Branches poison the write counts.** The interface-aliasing
optimization in stepjit_arg.go is legal only for a slot written
exactly once; that is what lets an interface argument point into the
frame instead of copying. A slot assigned inside a branch is
maybe-written, so the planner must count it conservatively and the
aliasing that produces allocation parity turns off for any name a
branch touches. The cost is silent: programs keep working and
allocate more.

**Splicing stops at the branch boundary.** A producer whose single
reader sits in the other arm, or past the join, cannot be spliced
without proving the branch executes; the planner keeps the slot. More
slots, larger frames, more stores.

**Scope has to be decided.** Go gives a block its own scope; the slot
model is flat. Either a name declared inside `if` stays visible after
it, which is Go syntax with non-Go semantics, or the compiler tracks
scopes and shadowing, which reopens the polymorphic-slot problem a
reassignment at a different type already trips.

## The boolean-production problem

The harder question is not `if` but its operand. Real conditions are
mostly comparisons, and `x == 5` is an expression
([expressions.md](expressions.md)). Without operators, every
predicate is a call:

```gozero
if eq(status, 200) { ... }
if strings.Contains(host, ":") { ... }
```

This stays inside the current design: `eq` is a binding, typed by its
signature, and the AST holds a call it already knows how to compile.
The cons are real:

- `eq(x, 5)` is legal Go but not the Go anyone writes; the language
  reads as itself only until the first comparison.
- A generic `eq(a, b any) bool` erases the type safety the bindings
  provide: both sides box to `any`, scalars allocate or hit the
  static-cell path, and a type mismatch that `==` would reject at
  compile time becomes a runtime `false` from `reflect.DeepEqual`.
- Typed variants (`eqInt`, `eqStr`, ...) restore the checking and
  multiply the binding surface by the scalar widths.

## Alternatives

**Guard bindings, no syntax at all.** The error contract is already a
one-armed conditional. A binding that returns a non-nil error ends
the program; a binding that returns nil lets it continue. Middleware
written this way needs no `if`:

```gozero
auth.Require(w, r)
next.ServeHTTP(w, r)
```

`auth.Require` writes the 401 and returns an error when the header is
missing; the program stops on the spot, exactly as `Scan` and `Exec`
already behave for a failed `http.NewRequest`. Cons:

- Only one arm. There is no else; the other branch is "the rest of
  the program", so two-way branching needs two programs or a binding
  that takes both continuations, which needs
  [closures](closures.md).
- The branch policy lives in the host. The program cannot express a
  condition the host did not anticipate as a binding, which is either
  the sandbox working as designed or a limitation, depending on who
  is writing the program.
- Abusing error for control flow makes a real failure and a declined
  branch look identical to the caller.

**Branch combinators (`iff(cond, then)`).** Pure abstraction of the
request into userland: the binding receives a bool and something to
run. Without closures there is nothing to pass as the something;
with closures this is Tcl, where control flow is a library and
evaluation order is whatever the binding does. Both halves are
userland control flow, which is the stated non-goal. Rejected.

## In other imperative settings

The precedent for conditions-as-calls is old. Shell's `if` branches
on the exit status of a command, and `[` is a command; gozero's error
contract is the same shape with `set -e` built in. testify is the
same move inside Go tests: `assert.Equal(t, want, got)` is a
comparison as a call, and the fixture suite under `testdata/` already
runs on it. An `http.Handler` body is naturally imperative and mostly
condition-free - build, encode, write - which is why handlers fit the
current language. Middleware is where conditions genuinely live
(header equality, path prefixes, status classes), and the guard
binding covers the abort-or-continue half of it today; the half it
does not cover is choosing between two continuations, which is not a
conditions problem but a closures problem.

## Verdict

An `if` restricted to bool names and bool calls is implementable on
every tier and stays a strict Go subset. It costs the line-oriented
grammar, the single-exit control contract, part of the aliasing
optimization, and it immediately raises the operand question that
[expressions.md](expressions.md) declines to solve. The guard-binding
pattern covers the dominant real use (middleware abort) with zero
language change, and is where this design stops.
