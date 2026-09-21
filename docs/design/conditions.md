---
title: Conditions
date: "2026-09-21T10:09:00+02:00"
---

Conditions moved from research into the syntax on 2026-09-21, in two steps the same day: the construct first, then one comparison in its header. This document records what landed and its semantics, how the construct runs on each tier with the measured cost, and what stayed out with the reasons the research established. The original question, "what if we implemented conditions?", is answered for the restricted form: `if`, `else if` and `else` over braced statement lists, with a bool name, a bool field, a bool call or one comparison as the operand.

Before it landed the language had one conditional, and it was invisible: a non-nil trailing error ends the program, so every call is an implicit `if err != nil { return }`. That contract is unchanged and still carries middleware-style guarding; the visible `if` sits beside it.

## What landed

```go
ok := strings.HasPrefix(req.URL.Path, "/api");   a bool a binding produced
route := "";
if ok {                                          a declared bool name
	route = "api";
} else if req.Close {                            a bool field path
	route = "closed";
} else {
	route = "static";
}
if strings.HasPrefix(p, "/api") {                a call returning bool
	if req.Close { return; }                     nesting, and a return
}
t := time.Now();
if time.Since(t) < time.Hour {                   one comparison, two operands
	age = "fresh";
}
```

The rules the research asked for, each rejected at compile time by name:

- The condition is one of a declared bool name, a bool field path, or a call returning bool (condition form). The check is on the kind rather than the exact type, which is Go's own rule for an `if` condition, so a named bool type qualifies.
- Or it is exactly one comparison, `==`, `!=`, `<`, `<=`, `>` or `>=` between two operands, each a name, a field path, a call, or a literal. There is no init clause, no `&&` and no nesting in the header.
- Both sides of a comparison carry identical static types (identical types), with a literal side adopting the other side's type. The comparison runs on the underlying kind, so a named scalar type such as `time.Duration` orders like the int64 it is; integers, floats and strings compare and order, bool compares with `==` and `!=` only, and everything else is out of the rung (comparable scalar). Two literal sides are a constant condition (constant comparison), and an operand naming nothing the program bound is rejected too (comparison operand).
- The comparison operators exist in the header and nowhere else. Every statement position that could swallow one rejects it at parse time (comparison placement), the bare form, the assignment right side and the return value among them.
- A comparison operand reads a value binding, which is how a package constant such as `time.Hour` reaches a program: `BindValue` registers a typed value under a dotted name, and the value keeps the static type it was bound with.
- Flat scope: `var` and `:=` cannot appear inside an arm. The slot model is flat, so a name declared there would stay visible past the closing brace with non-Go visibility; declare it before the `if` and assign with `=`. Arms share the program's slot namespace, and a skipped arm leaves the zero value Go would.
- `else` binds only on the closing brace's line, the gofmt shape. An `else if` nests as an else arm holding a single `if`.
- `if` and `else` join the reserved words: a name cannot shadow either.

`return` inside an arm landed too, with or without a value, which the research had priced as the feature's hardest half. It travels a sentinel on the error return every statement already has, `errProgramReturn` in vm_signal.go: a return raises it, the top of the program consumes it, and nothing between the two sees anything but an error it already had to check. The value rides beside the signal, so `return s` from an arm hands back exactly what the same line at the top level would, and the statements written after it never run on either tier. A binding cannot forge the signal, because the value is unexported.

## How it runs

The reflect tier runs an arm through `runStmts`, which the program's own statement list also runs, so an arm's statements compile and execute through exactly the code the top level does. The direct tier compiles a structured node holding one `[]nodeE` per arm: no program counter, no jump, the arm runs front to back, and the only exit is the error return. A return inside an arm boxes its value into one hidden `any` field of the frame and raises the sentinel; a program with no such return allocates no field and compares against no sentinel, so a straight line keeps the frame and the cost it had.

Two planner costs the research predicted are real and priced. A program with an `if` takes a structural plan that splices nothing: `findSplice` cannot prove a producer's single reader runs when a branch boundary sits between them, so the value keeps its slot. And a slot assigned inside an arm is maybe-written, so it counts as written twice and the write-once interface aliasing in stepjit_arg.go never fires for it. `BenchmarkIfWriteCount` measures the second directly: the same string reaching the same interface parameters costs one extra boxing allocation per read when a branch touches the name, and the straight-line form that aliases instead pins the frame and gives up pooling in exchange.

A comparison lowers to one node closed over the operands' shared layout class: two loads and a machine compare, allocating nothing. One correctness detail is worth naming, because it is invisible until it is wrong. The direct tier carries a narrow integer load zero-extended while the constant path sign-extends, so the two bit conventions have to meet before any compare; `signN` truncates to the class width and sign-extends, which is what keeps `int8(-1)` below zero. `TestCmpMatchesReflect` pins that case and the other widths against the reflect evaluator.

The `if` fixture is a middleware-style guard over all three condition forms, an `else if` chain and a nested `if`. It reaches the direct tier and runs at allocation parity with its handwritten mirror, 560 B and 6 allocations on both sides; the changelog entry carries the timings. The shape table gained `SS_b`, the predicate shape a `strings.HasPrefix`-style condition calls, and `i64_b` beside it for an integer predicate.

The `since` fixture prices the comparison rung, and its cost is not the comparison. `time.Now` returns a `time.Time`, a struct with no layout class, so a node cannot carry the value; rather than decline the program, the statement bridges - reflect invokes the call and sets the result into the frame slot's typed storage, with the call named in `Supports` output. The two `time.Time` crossings are the fixture's entire allocation delta against its mirror, and every comparison-free stanza in it holds parity.

## What stayed out

- **Operator conditions beyond one comparison.** `a == b && c < d` is an expression tree, and the AST has no tree; [expressions.md](expressions.md) holds that question. One comparison needs no tree, which is why it is the form that landed: two operands and an operator, read by the header and nowhere else.
- **Arithmetic in an operand.** `if n+1 > limit` is the same expression tree by another name, and it stays out for the same reason. An operand is a name, a field path, a call, or a literal.
- **An init clause.** `if v, err := f(); err != nil` declares inside the header, which the flat scope rule rejects for the same reason it rejects a `:=` in an arm.
- **A returned call value from an arm.** `return f()` inside an arm declines the direct tier by name and runs on the reflect evaluator, the same rule that keeps a returned expression off that tier.
- **Block scope.** Arms share the program's slot namespace rather than getting one of their own. Tracking scopes and shadowing reopens the polymorphic-slot problem a reassignment at a different type already trips; the flat scope rule is the cheaper half of that trade and it is enforced, not silent.
- **`switch`.** An `else if` chain expresses it. The comparison it would switch on now exists, so what stays out is the construct, not the operator: a case list is several comparisons against one operand, which is the expression tree again.

## The boolean-production problem, answered

The operand was the harder question, and the research recorded it as open: real conditions are mostly comparisons, and without operators every predicate had to be a call.

```go
if eq(status, 200) { ... }          the form the research priced
if status == 200 { ... }            the form that landed
```

Each objection the research raised against `eq` is what the comparison removes. `eq(x, 5)` is legal Go but Go writes `x == 5`, so every comparison site read differently from the Go it mirrors; the header now spells it Go's way. A generic `eq(a, b any) bool` erased the type safety the bindings provide, boxing both sides and turning a compile-time mismatch into a runtime `false`; the header instead requires identical static types and names the rule when they differ. Typed variants restored the checking at the cost of one binding per scalar width; one class-closed node covers every width instead.

A predicate call is still the form for everything a comparison is not, and the stdlib covers most of it: `strings.HasPrefix`, `strings.EqualFold`, `strings.Contains` and their friends are the conditions middleware actually writes, and each is one binding with a shape. What has no answer yet is the compound condition, `a && b`, which is the expression tree above.

## Alternatives, and where they still apply

**Guard bindings, no syntax at all.** The error contract is a one-armed conditional already, and it did not go away:

```go
auth.Require(w, r)
next.ServeHTTP(w, r)
```

`auth.Require` writes the 401 and returns an error when the header is missing; the program stops on the spot. This is still the lightest form when the second arm is "the rest of the program", and it needs no condition in the source at all. Its limits are what the `if` now covers: it has no else, and the branch policy lives in the host rather than in the program.

**Branch combinators (`iff(cond, then)`).** Rejected then and rejected now: without closures there is nothing to pass as the something, and with closures it is Tcl, where control flow is a library and evaluation order is whatever the binding does. Userland control flow is the stated non-goal.

## In other imperative settings

The precedent for conditions-as-calls is old. Shell's `if` branches on the exit status of a command, and `[` is a command; the error contract is the same shape with `set -e` built in. testify is the same move inside Go tests: `assert.Equal(t, want, got)` is a comparison as a call, and the fixture suite under `testdata/` runs on it. An `http.Handler` body is imperative and mostly condition-free; middleware is where the conditions are, and both halves of it are now expressible: abort through a guard binding, choose through an `if`.
