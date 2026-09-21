---
title: Glossary
date: "2026-09-10T16:00:00+02:00"
---

Definitions for the virtual machine and JIT terms this project's documentation uses. Each entry defines one term on its own; entries reference each other by name.

## allocation

One request for heap memory. Benchmarks report allocations per operation as `allocs/op` and bytes per operation as `B/op`. An allocation costs time when it is made and costs time again when the garbage collector later scans and frees it.

## aliasing

Passing a pointer to existing storage instead of a copy of the value. In gozero, an interface argument can point directly at a frame slot instead of at a copied cell, which saves the copy's allocation. The alias is only safe while the storage outlives every holder of the pointer, so an aliased frame cannot be pooled and an aliased slot cannot be rewritten.

## binding

A Go function the host registers in a Runtime under a name, for example `rt.Bind("url.Parse", url.Parse)`. The set of bindings is the complete surface a program can call: gozero programs have no standard library of their own.

## boxed value

A value copied into a heap cell so that an interface can point at it. Go's `any` and every other interface is two words: a type or itab word, and a data word that holds a pointer. A pointer-sized value stores its pointer in the data word directly. A wider value, for example a string (two words) or a struct, does not fit, so the runtime allocates a cell, copies the value into the cell, and points the data word at the cell. That allocation plus copy is boxing.

```
s := "GET"        // a string: two words (pointer, length), no heap cell
var v any = s     // boxing: cell := new(string); *cell = s
                  // v = {type: string, data: cell}

p := &Request{}   // a pointer: one word
var w any = p     // no boxing: w = {type: *Request, data: p}
```

Boxing appears in benchmark output as one allocation per boxed value. gozero avoids boxing where it can: constants box once at compile time, small integers point into a static table instead of a fresh cell, and an eligible frame slot is aliased instead of copied.

## borrowed argument

The binding contract: a call's arguments are valid for the duration of the call, and a binding that keeps one copies it first. The runtime relies on the contract to recycle the memory behind arguments - the pack slice, the boxed values, the literal blocks - once the call returns. A type assertion copies a value out of its box; a plain interface assignment copies only the box's address and is not a copy in this sense. String and scalar parameters carry their values directly and need no copy.

## control signal

An unexported sentinel error that carries control flow out of a nested statement list. Every statement on both tiers already returns an error, so a signal needs no second return value and no flag to check: a statement raises it, the construct that owns the exit compares against it and consumes it, and a run whose statements all return nil never makes the comparison. There are three: `errProgramReturn`, raised by a `return` inside an `if` arm and consumed at the top of the program, and `errLoopBreak` and `errLoopContinue`, raised by `break` and `continue` and consumed by the innermost range loop. A binding cannot produce a signal, because the values are unexported.

## direct-call tier

The fastest of the three execution tiers. A bound function whose signature matches an entry in the shape table is called as a plain Go call, with no reflection. The other two tiers are the reflect bridge and the reflect evaluator.

## escape

A pointer escapes when it outlives the scope that created it, which forces the pointed-to memory onto the heap and blocks reuse. The frame pool depends on an escape proof: the compiler marks every node that hands a frame pointer to a callee, and only a frame with no such node is recycled.

## fixture

A gozero program stored as a file under `testdata/`, for example `testdata/http.txt`. The test suite compiles and runs every fixture; each fixture makes its own assertions through a bound assert function.

## flat scope

The language's one scope rule: a program has a single namespace of slots, and a braced body does not open a new one. A name a range body declares outlives the loop, holding its last value, and the loop variable is one slot reused per iteration rather than a fresh variable per iteration. The declaration forms that would be misread under the rule are rejected where they are written: `var` inside any body, and `:=` inside an `if` arm.

## frame

One per-run block of memory holding a program's named values. The compiler lays the frame out as a Go struct with one field per surviving slot, using `reflect.StructOf`, so the garbage collector scans it correctly. A program with no multi-use names has no frame.

## frame pool

A `sync.Pool` that recycles a program's frames between runs, saving the per-run frame allocation. Only a program whose frame provably never escapes gets a pool; a reused frame is cleared back to zero before the next run.

## itab

The runtime table that backs a non-empty interface value: it pairs a concrete type with an interface type and lists the method addresses. The Go runtime builds itabs on demand during type assertions, and gozero obtains them the same way at compile time, so no itab work happens per call.

## JIT

Just-in-time compilation: translating a program into a faster executable form at run time. gozero's JIT produces a tree of typed Go closures rather than machine code; the speed comes from replacing per-call reflection with direct calls through the shape table.

## layout class

The ABI category of a type: pointer-shaped (one word), string (two words), interface (two words), slice (three words), or one scalar class per width. Two types in the same layout class pass through registers identically, which is what lets one shape-table entry serve every function with the same class signature.

## mirror

The handwritten Go function that performs the same work as one fixture, used as the native baseline in benchmarks. Every fixture has one mirror in `fixture_bench_test.go`; `BenchmarkFixtures` runs the compiled fixture and its mirror under the same measurement and reports both.

## reflect bridge

The middle execution tier. A call whose signature has no shape-table entry compiles to one `reflect.Value.Call`, while the calls around it stay on the direct-call tier. The bridge costs reflection for that one call only. A call whose result has no layout class bridges as a whole statement instead, writing the result into the frame slot's typed storage, because a node cannot carry that value between calls.

## reflect evaluator

The slowest and most general execution tier, and the reference implementation the JIT is tested against. Every value travels as a `reflect.Value`, and every call goes through `reflect.Value.Call`. A program falls back to this tier as a whole when the JIT cannot compile it.

## shape table

The table of function signatures, keyed by layout class, that the direct-call tier supports. One entry covers every bound function with the same class signature: one entry calls every function taking two strings and returning a pointer and an error. A signature outside the table sends that call to the reflect bridge.

## sign extension

Widening a signed integer while keeping its value, by copying the sign bit into the new high bits: `int8(-1)` is `0xFF` in eight bits and `0xFFFFFFFFFFFFFFFF` in sixty-four. The direct-call tier carries a narrow load zero-extended, so a comparison canonicalizes both operands through `signN` before comparing; without it `int8(-1)` would order above zero.

## slot

The storage for one named value in a program, held as a field of the frame. A name that is produced and consumed once travels as a Go return value instead and needs no slot; the planner removes such slots before the frame is laid out.

## splice

The planner optimization that removes a slot: when a statement's single result is read exactly once by the next statement, the producing call is moved into the consuming call's argument tree, and the value travels as a closure return value.

## stack map

The `map[string]any` a host passes to `Exec`. Names a program never binds resolve against the stack map at execution time; their types are checked when read, because the map's values carry no static type. The stack map is unrelated to the call stack.

## tier

One of the three execution paths a compiled call lands on: the direct-call tier, the reflect bridge, or the reflect evaluator. `Runtime.Supports` reports which calls of a program leave the direct-call tier and why.

## value binding

A Go value the host registers in a Runtime under a dotted name, for example `rt.BindValue("time.Hour", time.Hour)`, the way a program would read a package constant. The value is captured once, at bind time, keeps the static type it was bound with, and compiles to a constant wherever an argument or a comparison operand reads the name. A func belongs in a binding instead.

## zero value

The value Go gives every type before assignment: `0`, `""`, `nil`, or a struct of zero fields. gozero fills omitted trailing arguments with the parameter's zero value, and a pooled frame is cleared back to zero before reuse so declarations start from it.
