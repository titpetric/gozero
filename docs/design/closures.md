---
title: What if we implemented closures?
date: "2026-09-09T00:00:00+02:00"
---

A closure is the one extension the architecture is already shaped for. A compiled program is a Go closure - `CompiledFunc` is a func value built at runtime over the bindings - and a func literal inside a program is a smaller instance of the thing the compiler already builds. The machinery exists; the open questions are what capture does to the frame, and what closures make expressible that the design elsewhere declines.

## How it would look

A func literal whose body is the same straight-line statement list a program is, typed entirely by the parameter it fills - no return type or parameter type syntax needed when the target signature is known from the binding:

```go
mux.HandleFunc("/health", func(w, r) {
	w.WriteHeader(200)
})

slices.SortFunc(xs, func(a, b) {
	return cmp.Compare(a.Name, b.Name)
})
```

The types of `w`, `r`, `a`, `b` come from `http.HandlerFunc` and `SortFunc`'s signature, the way every other type in the language comes from a binding. That is the rule the compiler already applies to arguments, extended to parameters; closures add no type surface.

Materialization has a stdlib answer on each tier. Generally, `reflect.MakeFunc` wraps the compiled body in a func of any signature; the body runs on the reflect evaluator with the incoming `[]reflect.Value` as its outer slots. On the direct tier, a signature in the shape table needs no MakeFunc at all: the compiler builds an ordinary Go closure of the shape type over the frame pointer and the body's `[]nodeE`, and `castFn` already proves a closure's funcval passes anywhere a func does. Out-of-table signatures bridge, exactly as out-of-table calls do now.

## What it costs the design

**Capture forces reference semantics onto by-value slots.** Go closures capture variables, not values; a captured name must observe later writes. The frame gives that almost for free - capture the slot's address - but an address into the frame is precisely the aliasing that stepjit_arg.go only permits for write-once slots. Every captured slot is potentially written after capture, so capture is aliasing without the write-once proof.

**The frame escapes, permanently.** A closure handed to a binding may outlive the run: `HandleFunc` keeps it forever. The frame it points into must live as long, so frame pooling - already foreclosed by interface aliasing, per [overheads.md](../overheads.md) - goes from foreclosed to impossible, and a long-lived closure pins the whole frame including slots it never reads.

**Construction moves into the run.** A compiled program is immutable and cached per source string; a closure closes over a per-run frame, so the func value must be built on every execution. One allocation per closure per run - the same cost the Go compiler emits for an escaping func literal, so parity survives, but the "compile once, zero construction at exec" story does not.

**The grammar grows blocks and parameter lists.** Same nesting cost as [conditions.md](conditions.md), plus parameters. Inferring parameter types from the target keeps type syntax out, at the price of rejecting a func literal in any position whose expected type is unknown - which the language can afford, since every position is a binding's parameter.

**The equivalence suite doubles.** Both tiers must agree not only on results but on what a callee observes when it calls the closure late, concurrently, or more than once. Re-entrancy is new: a closure running while its defining program still runs shares the frame with it.

## Userland control flow

Closures make the other three features expressible without further syntax:

```go
iff(ok, func() { w.WriteHeader(403) })
each(req.Cookies(), func(c) { jar.SetCookie(c) })
```

That is Tcl: control flow as library calls, evaluation order and branch semantics defined by whatever the binding does, different per host. It is the userland control flow this design rejects in [conditions.md](conditions.md) and [loops.md](loops.md), and closures would make it available whether or not it is endorsed. The pattern comes with the feature; the binding set is the only control over it, and a host that binds `iff` has chosen it.

## Alternatives

**Programs as the closure, host does the plumbing.** Today's answer. `Compile` returns a func; the host wraps it into whatever shape a callee wants:

```go
fn, _ := rt.Compile(src)
mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
	fn.ExecContext(r.Context(), map[string]any{"w": w, "r": r}, nil)
})
```

The handler is imperative and it is a gozero program; only the adapter is Go. Cons:

- One program per func value: a program cannot define two handlers, or hand a comparator to a sort it also calls.
- Nothing is captured: the closure's inputs travel through the stack map by name, dynamically checked, instead of statically typed capture.
- The adapter is host code per signature, though `reflect.MakeFunc` could generalize it as a `BindProgram(sig, src)` helper without any language change - the closure feature minus the syntax.

**Named sub-programs, no capture.** A `func name(params)` form compiled like a top-level program, capturing nothing, values passed as arguments only. Removes the frame-escape and aliasing costs entirely. Cons: capture is most of why closures are wanted - `next.ServeHTTP` inside a middleware body wants `next` from the environment - and a capture-free func is just a second program with a calling convention.

## In other imperative settings

Imperative Go leans on closures exactly where this language wants to go: `http.HandlerFunc`, middleware as `func(next Handler) Handler`, `sort.Slice`, `t.Cleanup`, range-over-func bodies. testify is the counterexample: assertion-heavy test code needs no closures, and the fixture suite runs without them today. Handlers stay imperative through the adapter pattern above. Middleware is the case that needs full capture: its shape is a function that receives a continuation, and guard bindings cover the abort half only. Holding `next` and deciding when to call it takes a closure; the branch was never the missing part.

## Verdict

Closures are the best-aligned of the four extensions: no new type surface, stdlib materialization on both tiers, and they answer the middleware question directly. They cost capture-as-aliasing, an escaping frame, per-run construction, block grammar - and they make userland control flow expressible. The capture-free alternatives (`BindProgram`-style adapters) deliver most of the value as host API rather than language, and are the recommended first step if the pressure becomes real.

That first step landed as `Runtime.FuncOf[F](src, params...)`: one generic method, `reflect.MakeFunc` over the cached compilation, no grammar change and no capture. It buys the handler case in full - `mux.HandleFunc("/health", h)` with `h` a program - and nothing else the research asked for: still one program per func value, still nothing captured, inputs still travelling by name rather than as typed capture. The costs the research named are all absent because the syntax is: no frame escapes that did not already, nothing is constructed per run, and the grammar is unchanged. What the bridge does cost is `reflect.MakeFunc`'s own argument marshalling, 2 allocations and 64 B per call, which is the price of materializing an arbitrary signature without the shape-table closure the direct tier could build for a signature it knows.

The syntax landed next, in its capture-free form: `func(w, r) { ... }` in argument position, parameter types from the target signature, and the "named sub-programs, no capture" alternative above as the rule rather than the fallback. Three of the four costs the research named stay absent with capture gone. Nothing is captured, so no slot is aliased and the write-once proof is not asked for. Nothing closes over a frame, so the body's frame pools like any program's and a handler registered forever pins nothing but its own compiled nodes. Nothing is constructed per run: the materialized value is a compile-time constant, one per compilation, on both tiers. Only the grammar cost is real and it was already paid by `if` and `for`, whose braced statement list the body reuses.

What it buys over the `FuncOf` bridge is the call path. The step JIT builds an ordinary Go closure of the signature's layout shape over the body's nodes and reinterprets its funcval as the target type, the way `castFn` reinterprets a binding, so a materialized handler costs 170.3 ns and zero allocations per call against 876.0 ns and two through `reflect.MakeFunc` - one bridge removed, not one added. Out-of-table signatures, a result or a `context.Context` parameter among them, keep the MakeFunc value and are named as bridged, exactly as out-of-table calls are.

What it does not buy is the middleware case the research called the one that needs full capture. Holding `next` and deciding when to call it is capture; this rung rejects it by name, and userland control flow along with it, since `iff(ok, func() { ... })` cannot read the `w` it would write to.
