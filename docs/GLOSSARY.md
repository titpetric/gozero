---
title: Glossary
date: "2026-09-10T16:00:00+02:00"
---

Definitions for the virtual machine and JIT terms this project's
documentation uses. Each entry defines one term on its own; entries
reference each other by name.

## allocation

One request for heap memory. Benchmarks report allocations per
operation as `allocs/op` and bytes per operation as `B/op`. An
allocation costs time when the program makes it and costs time again
when the garbage collector later scans and frees it.

## aliasing

Passing a pointer to existing storage in place of a copy of the
value. In gozero, an interface argument can point directly at a
frame slot and skip the copied cell, which saves the copy's
allocation. Because the alias is only safe while the storage
outlives every holder of the pointer, the frame pool cannot take an
aliased frame and no statement can rewrite an aliased slot.

## binding

A Go function the host registers in a Runtime under a name, for
example `rt.Bind("url.Parse", url.Parse)`. The set of bindings is
the complete surface a program can call: gozero programs have no
standard library of their own. `Bind` also takes data, which is a
value binding.

## boxed value

A value copied into a heap cell so that an interface can point at
it. Go's `any` and every other interface is two words: a type or
itab word, and a data word that holds a pointer. A pointer-sized
value stores its pointer in the data word directly. A wider value,
for example a string (two words) or a struct, does not fit. The
runtime allocates a cell, copies the value into the cell, and points
the data word at the cell. That allocation plus copy is boxing.

```
s := "GET"        // a string: two words (pointer, length), no heap cell
var v any = s     // boxing: cell := new(string); *cell = s
                  // v = {type: string, data: cell}

p := &Request{}   // a pointer: one word
var w any = p     // no boxing: w = {type: *Request, data: p}
```

Boxing appears in benchmark output as one allocation per boxed
value. gozero avoids boxing where it can: constants box once at
compile time, small integers point into a static table and need no
fresh cell, and an argument aliases an eligible frame slot and skips the copy.

## borrowed argument

The binding contract: a call's arguments are valid for the duration
of the call, and a binding that keeps one copies it first. The
runtime relies on the contract to recycle the memory behind
arguments - the pack slice, the boxed values, the literal blocks -
once the call returns. A type assertion copies a value out of its
box; a plain interface assignment copies only the box's address and
is not a copy in this sense. String and scalar parameters carry
their values directly and need no copy.

## value binding

Data the host registers with `Bind`, as opposed to the func a
`binding` holds: `Bind` takes either, and the kind of argument
decides what the name does. A program reads a value binding in
argument position. The name carries the static type the host bound
it with and can be the receiver of a field read or a method call.

## per-run cell

The storage a program gets when it writes or addresses a value
binding. Because `Bind` copies the value into the runtime, the name
is the program's own for the run: `name = v` and `&name` reach the
cell, and the host's variable keeps what it had. The cell starts
every run from the bound value. Each program gets one cell per name.
Two `&name` address the same storage the way two `&x` do in Go. A
host that binds an address instead lets the program write the
host's own storage through `*name = v`.

## direct-call tier

The fastest of the three execution tiers. A bound function whose
signature matches an entry in the shape table runs as a plain
Go call, with no reflection. The other two tiers are the reflect
bridge and the reflect evaluator.

## escape

A pointer escapes when it outlives the scope that created it, which
forces the pointed-to memory onto the heap and blocks reuse. The
frame pool depends on an escape proof: the compiler marks every node
that passes a frame pointer to a callee, and the pool recycles only
a frame with no such node.

## fixture

A gozero program stored as a file under `testdata/`, for example
`testdata/http.txt`. The test suite compiles and runs every fixture;
each fixture makes its own assertions through a bound assert
function.

## frame

One per-run block of memory holding a program's named values. The
compiler lays the frame out as a Go struct with one field per
surviving slot, using `reflect.StructOf`. The garbage collector
scans that struct correctly. A program with no multi-use names has
no frame.

## frame pool

A `sync.Pool` that recycles a program's frames between runs, saving
the per-run frame allocation. Only a program whose frame provably
never escapes gets a pool; the pool clears a reused frame back to
zero before the next run.

## itab

The runtime table that backs a non-empty interface value: it pairs a
concrete type with an interface type and lists the method addresses.
The Go runtime builds itabs on demand during type assertions, and
gozero obtains them the same way at compile time. No itab work
happens per call.

## JIT

Just-in-time compilation: translating a program into a faster
executable form at run time. gozero's JIT produces a tree of typed
Go closures, no machine code; the speed comes from replacing
per-call reflection with direct calls through the shape table.

## layout class

The ABI category of a type: pointer-shaped (one word), string (two
words), interface (two words), slice (three words), or one scalar
class per width. Because two types in the same layout class pass
through registers identically, one shape-table entry serves every
function with the same class signature.

## mirror

The handwritten Go function that performs the same work as one
fixture, used as the native baseline in benchmarks. Every fixture
has one mirror in `fixture_bench_test.go`; `BenchmarkFixtures` runs
the compiled fixture and its mirror under the same measurement and
reports both.

## reflect bridge

The middle execution tier. A call whose signature has no shape-table
entry compiles to one `reflect.Value.Call`, while the calls around
it stay on the direct-call tier. The bridge costs reflection for
that one call only.

## reflect evaluator

The slowest and most general execution tier, and the reference
implementation the JIT tests check against. Every value travels as a
`reflect.Value`, and every call runs through `reflect.Value.Call`.
A program falls back to this tier as a whole when the JIT cannot
compile it.

## shape table

The table of function signatures, keyed by layout class, that the
direct-call tier supports. One entry covers every bound function
with the same class signature: one entry calls every function
taking two strings and returning a pointer and an error. A signature
outside the table sends that call to the reflect bridge.

## slot

The storage for one named value in a program, held as a field of the
frame. A name that one statement produces and one statement consumes
travels as a Go return value and needs no slot; the planner removes
such slots before the compiler lays out the frame.

## splice

The planner optimization that removes a slot: when the next statement
reads a statement's single result exactly once, the planner moves
the producing call into the consuming call's argument tree, and
the value travels as a closure return value.

## stack map

The `map[string]any` a host passes to `Exec`. Names a program never
binds resolve against the stack map at execution time; the runtime checks
their types on read, because the map's values carry no static
type. The stack map has no relation to the call stack.

## tier

One of the three execution paths a compiled call lands on: the
direct-call tier, the reflect bridge, or the reflect evaluator.
`Runtime.Supports` reports which calls of a program leave the
direct-call tier and why.

## zero value

The value every Go type holds before assignment: `0`, `""`, `nil`,
or a struct of zero fields. gozero fills omitted trailing arguments
with the parameter's zero value, and the pool clears a pooled frame back
to zero before reuse so declarations start from it.
