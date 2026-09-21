---
title: Conditions
date: "2026-09-21T09:12:00+02:00"
---

Conditions moved from research into the syntax on 2026-09-21. This document records what landed and its semantics, how the construct runs on each tier with the measured cost, and what stayed out with the reasons the research established. The original question, "what if we implemented conditions?", is answered for the restricted form: `if`, `else if` and `else` over braced statement lists, with a bool name, a bool field or a bool call as the operand.

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
```

The rules the research asked for, each rejected at compile time by name:

- The condition is one of a declared bool name, a bool field path, or a call returning bool (condition form). There is no operator, no literal and no init clause in the header. The check is on the kind rather than the exact type, which is Go's own rule for an `if` condition, so a named bool type qualifies.
- Flat scope: `var` and `:=` cannot appear inside an arm. The slot model is flat, so a name declared there would stay visible past the closing brace with non-Go visibility; declare it before the `if` and assign with `=`. Arms share the program's slot namespace, and a skipped arm leaves the zero value Go would.
- `else` binds only on the closing brace's line, the gofmt shape. An `else if` nests as an else arm holding a single `if`.
- `if` and `else` join the reserved words: a name cannot shadow either.

`return` inside an arm landed too, with or without a value, which the research had priced as the feature's hardest half. It travels a sentinel on the error return every statement already has, `errProgramReturn` in vm_signal.go: a return raises it, the top of the program consumes it, and nothing between the two sees anything but an error it already had to check. The value rides beside the signal, so `return s` from an arm hands back exactly what the same line at the top level would, and the statements written after it never run on either tier. A binding cannot forge the signal, because the value is unexported.

## How it runs

The reflect tier runs an arm through `runStmts`, which the program's own statement list also runs, so an arm's statements compile and execute through exactly the code the top level does. The direct tier compiles a structured node holding one `[]nodeE` per arm: no program counter, no jump, the arm runs front to back, and the only exit is the error return. A return inside an arm boxes its value into one hidden `any` field of the frame and raises the sentinel; a program with no such return allocates no field and compares against no sentinel, so a straight line keeps the frame and the cost it had.

Two planner costs the research predicted are real and priced. A program with an `if` takes a structural plan that splices nothing: `findSplice` cannot prove a producer's single reader runs when a branch boundary sits between them, so the value keeps its slot. And a slot assigned inside an arm is maybe-written, so it counts as written twice and the write-once interface aliasing in stepjit_arg.go never fires for it. `BenchmarkIfWriteCount` measures the second directly: the same string reaching the same interface parameters costs one extra boxing allocation per read when a branch touches the name, and the straight-line form that aliases instead pins the frame and gives up pooling in exchange.

The `if` fixture is a middleware-style guard over all three condition forms, an `else if` chain and a nested `if`. It reaches the direct tier and runs at allocation parity with its handwritten mirror, 560 B and 6 allocations on both sides; the changelog entry carries the timings. The shape table gained `SS_b`, the predicate shape a `strings.HasPrefix`-style condition calls.

## What stayed out

- **Operator conditions.** `x == 5` is an expression, and the AST has no expression tree; [expressions.md](expressions.md) holds that question. Every predicate is a call, which is the boolean-production problem below.
- **An init clause.** `if v, err := f(); err != nil` declares inside the header, which the flat scope rule rejects for the same reason it rejects a `:=` in an arm.
- **A returned call value from an arm.** `return f()` inside an arm declines the direct tier by name and runs on the reflect evaluator, the same rule that keeps a returned expression off that tier.
- **Block scope.** Arms share the program's slot namespace rather than getting one of their own. Tracking scopes and shadowing reopens the polymorphic-slot problem a reassignment at a different type already trips; the flat scope rule is the cheaper half of that trade and it is enforced, not silent.
- **`switch`.** An `else if` chain expresses it, and a switch over a value needs the comparison operator the language does not have.

## The boolean-production problem

The operand is a harder question than the `if` itself, and landing the construct did not answer it. Real conditions are mostly comparisons, and without operators every predicate is a call:

```go
if eq(status, 200) { ... }
if strings.Contains(host, ":") { ... }
```

This stays inside the design: `eq` is a binding, typed by its signature, and the AST holds a call it already knows how to compile. The cons the research recorded still stand:

- `eq(x, 5)` is legal Go, but Go writes `x == 5`; every comparison site reads differently from the Go it mirrors.
- A generic `eq(a, b any) bool` erases the type safety the bindings provide: both sides box to `any`, scalars allocate or hit the static-cell path, and a type mismatch that `==` would reject at compile time becomes a runtime `false` from `reflect.DeepEqual`.
- Typed variants (`eqInt`, `eqStr`, ...) restore the checking and multiply the binding surface by the scalar widths.

The stdlib predicates cover more of the real surface than the list suggests: `strings.HasPrefix`, `strings.EqualFold`, `strings.Contains` and their friends are the conditions middleware actually writes, and each is one binding with a shape.

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
