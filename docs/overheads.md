---
title: Go, call overheads and JIT
date: 2026-09-03T15:34:51+02:00
---

What does it cost to call Go functions from a small interpreted
language, instead of calling them from Go? A statement is parsed once,
compiled once, cached per source string, and then executed many times.
This measures the execution, against the same calls written by hand.

The answer is 61.87ns for a single call and 196.9ns for a four-call
program, both with the same number of allocations as the native code.
Parsing and compiling, if they are not cached, cost 2.136us.

## Syntax

The language has no operators. A program is a list of statements, each
one a call whose results bind to names:

```
program := { stmt }
stmt    := "return" [ expr ] ";"
         | [ name { "," name } ( ":=" | "=" ) ] expr ";"
expr    := path "(" [ args ] ")" { "." ident "(" [ args ] ")" }
arg     := string | number | name | expr
```

Values are string literals, int64, float64, names, and the results of
other calls. There is no arithmetic, no string concatenation, no field
access, no conditionals. Errors are never written down: a trailing
error result is stripped at compile time and checked after every call,
and a non-nil one ends the program.

Two rules cover the gaps that leaves. Every argument is optional and a
missing one is the zero value of its parameter type, so a three
parameter function can be called with two arguments. `dest` is a
reserved name holding the pointer `Scan` was given, which is how a
program writes its output.

## Is it functional

Yes. This is the program the benchmarks run:

```
req := http.NewRequest("GET", "/");
cookies := req.Cookies();
enc := json.NewEncoder(dest);
enc.Encode(cookies);
```

Run with `var dest bytes.Buffer` and `fn.Scan(&dest, nil)`, it leaves
`"[]\n"` in the buffer. Observations:

- The third argument of `http.NewRequest` is omitted and the body
  arrives as a nil `io.Reader`.
- Neither error is named. `http.NewRequest` failing returns that error
  from `Scan`, and nothing after it runs.
- `Cookies` and `Encode` are not bound. They are found on the result
  types of the two bindings when the program compiles, so an unknown
  method is a compile error and not an execution one.
- Chaining and nesting compile to the same four steps. The one line
  form is `json.NewEncoder(dest).Encode(http.NewRequest("GET",
  "/").Cookies());`.

## Bindings

Only functions are bound, and only some shapes reach the fast path.

- `Bind` takes any func value. Method expressions such as
  `(*json.Encoder).Encode` are ordinary funcs and bind directly,
  although this program does not need them.
- Variadic functions are rejected at compile time.
- Names bound by a program carry a static type; methods are resolved
  against it. Names read from the caller's stack are opaque and are
  checked when they are read.
- The JIT understands four layout classes: pointer-shaped, string,
  interface and slice. A parameter or result outside them, an `int`
  for example, keeps the whole program on the reflect tier.
- An interface argument fed from the stack needs its type in
  `ifaceConvs`, currently `any`, `io.Writer` and `io.Reader`. The
  interface table for a pair of concrete and interface types cannot be
  assembled by hand, so each entry obtains it with a type assertion.
- A slot written more than once cannot back an interface argument,
  because the argument aliases the slot rather than copying it.

## API

```go
rt := NewRuntime()
rt.BindScope("http", map[string]any{"NewRequest": http.NewRequest})
rt.BindScope("json", map[string]any{"NewEncoder": json.NewEncoder})

fn, err := rt.Compile(src)          // cached per source string
err = fn.Scan(&dest, stack)         // dest is bound to the name "dest"
req, err := fn.Exec[*http.Request](stack)
req, err := rt.Eval[*http.Request](src, stack)
```

`Bind` registers one name, `BindScope` a dotted group; a dotted name is
one key in the binding map, and a path resolves to the longest prefix
that names a binding. `Compile` returns a `CompiledFunc`, a defined
`func(map[string]any, any) (any, error)`.

`Eval`, `Exec` and `Scan` are generic methods, which need the go1.27
language version the `go` line in go.mod selects. `T` appears only in
the results of `Eval` and `Exec`, so both are instantiated explicitly;
`Scan` infers `T` from `dest`.

## Overhead

Medians of five runs, each benchmark in its own process, pinned to one
core with inlining disabled, Intel N150, go1.27.0. The raw sweep is
[`bench.txt`](../bench.txt). cost-sec/op is the measured loop minus a
native baseline timed inline in the same process.

| Benchmark          | sec/op  | B/op | allocs/op | cost-sec/op |
|--------------------|--------:|-----:|----------:|------------:|
| Native             | 651.7n  |  512 |         3 |           - |
| CostWithoutCaching | 2.795us | 1256 |        18 |     2.136us |
| CostNaive          | 1.997us |  576 |         5 |     1.189us |
| CostAmortizedCache | 921.4n  |  512 |         3 |      61.87n |
| ProgramNative      | 1.437us |  688 |         6 |           - |
| ProgramCached      | 1.579us |  712 |         6 |      196.9n |

The first four run `return NewRequest("GET", url);`. The last two run
the four statement program above.

- Both JIT tiers reach allocation parity with native: 3 against 3 for
  the single call, 6 against 6 for the program. The program's extra 24
  bytes are its frame, which replaces the allocation native pays boxing
  `cookies` into an `any`.
- Parsing and compiling are the bulk of the cost at 2.136us, thirty
  times the whole per-call cost of the compiled form. That is the part
  the expression cache removes.
- Reflection is smaller than parsing and is not free: 1.189us and two
  allocations over native, the argument frame and results slice
  `reflect.Value.Call` builds per call.
- Read the spread before the medians. A cost figure is a difference of
  two numbers around 650n or 1.4us, so a few percent of drift in the
  baseline moves it by a large fraction of itself. The ordering of the
  tiers holds; the JIT figures are bounded to tens and low hundreds of
  nanoseconds rather than resolved.

Benchmarks must run one per process. In a single binary the program
benchmarks and the parser inflate the shared heap, and everything
measured after them pays the garbage collection: `CostAmortizedCache`
reads 921.4n isolated and 960n in a combined sweep.

```sh
go test -run XXX -bench . -benchtime 3s -gcflags=all=-l . > /dev/null
for b in Native CostWithoutCaching CostNaive CostAmortizedCache \
         ProgramNative ProgramCached; do
	go test -run XXX -bench "^Benchmark$b\$" -benchmem \
		-benchtime 5s -count 5 -gcflags=all=-l .
done
benchstat bench.txt
```

The first line is a discarded warmup. Prefix each `go test` with
`taskset -c 3 nice -n -20` to pin it, which needs root for the nice
level; unpinned on a shared machine the same native call read 509n to
913n between sweeps. `-gcflags=all=-l` is required: without it the
native baseline is inlined into the loop body and measures nothing.

## Optimisation

What was tried, in order, with the effect on the four call program's
cost over native.

```
vm:   reflect evaluator, arg slice and AssignableTo per call     8791n
vm:   cache assignability per argument, one entry, atomic        5475n
vm:   one per-run frame for slots and every call's arguments       "
vm:   precompute the itab, hand reflect a pre-typed argument     2980n
jit:  shape table per step, reflect.StructOf frame, raw stores    295n
jit:  alias the slot for an interface argument, drop the box       "
jit:  linkname reflect.unsafe_New, skip the pointer-type lookup   197n
```

Notes on each:

- The assignability cache was the largest single win. `AssignableTo`
  against an interface walks method tables comparing names and was 29%
  of the profile; the same program is handed the same concrete type
  every time, so one entry removes it.
- Pre-typing the argument was needed because `reflect.Value.Call` runs
  its own `assignTo` per argument, which the cache above cannot reach.
  A type assertion produces the interface table, and the value arrives
  already typed as the parameter.
- The step JIT is the shape table from `jit.go` applied to every call
  rather than to one statement. `reflect.StructOf` gives the slots one
  allocation with a correct pointer map and a stable address per field,
  so raw words are read and written at offsets and every store keeps
  its write barrier. A program JITs whole or not at all.
- Aliasing is what buys allocation parity. An interface argument taken
  from a slot points at the slot instead of a copy, which is only legal
  because the frame outlives the call and the slot is written once.
- `reflect.New` was 12% of the JIT'd program, mostly its pointer-type
  lookup rather than the allocation. The linkname goes to the allocator
  reflect itself calls. It is a pull linkname to an internal symbol and
  a toolchain bump can break it.

Approaches tested and rejected:

- Pooling frames. The aliasing above means an interface handed to a
  callee can outlive the run, so a frame cannot be reused.
- Boxing a slice result into an `any` through `reflect.New` per call.
  Correct, and one allocation more than native.
- Carrying slot values as `reflect.Value`. Every JIT'd step then pays
  the conversion at both ends.

The remaining 197n is the frame allocation and the loop over four
closures.

`TestStepJITMatchesReflect` runs the same program down both tiers and
compares output, errors, and what each callee received through the
interface it was handed, so a toolchain that changes the assumptions
fails the suite rather than the benchmark.

## Open question: is the frame allocation avoidable

The remaining 196.9ns is the frame allocation and the loop over four
closures. The split is measured: running the same steps with the frame
hoisted out of the loop costs 1924ns against 2009ns with it, one
48 byte allocation apart, so the frame is 85ns of the 197ns.

Preallocating is not possible, because a frame cannot outlive one run
of a concurrent program. Pooling measures well:

| Frame per run                | sec/op  | allocs/op |
|------------------------------|--------:|----------:|
| unsafeNew                    | 62.57n  |         1 |
| sync.Pool, cleared on reuse  | 22.85n  |         0 |
| sync.Pool, not cleared       | 20.39n  |         0 |

That is 40ns and one allocation, and it is not currently claimable.
Pooling and the aliasing described above are mutually exclusive: an
interface argument taken from a slot hands a pointer into the frame to
the callee, and nothing proves the callee does not keep it past the
run. Dropping the alias to allow pooling trades one allocation for
another, 22.85n plus boxing the slice against 62.57n and no box.

Pooling is a clean win for a program with no aliased slot, which is
every program whose interface arguments are pointer-shaped, since those
are stored directly and never point at the frame. It is not implemented.

One measurement to be careful with. Under `-gcflags=all=-l` the numbers
above show 6 allocations for both the program and its native
equivalent. With inlining enabled native drops to 5 allocations and 560
B/op against 712, because escape analysis stops boxing `cookies`. The
allocation parity in the table holds for the flags the benchmarks use,
not for an ordinary build, where the frame is a real extra allocation.
Cost over native with inlining is 104.7n to 164.0n on a 545n call.

Approaches not tried:

- Reuse slots whose live ranges do not overlap. In the four call
  program `req` dies before `enc` is created and both are
  pointer-shaped, which would take the frame from 48 to 32 bytes.
- Fuse a step whose pointer result feeds the next step's pointer
  parameter, when that slot is read once. The program becomes two
  steps and one slot, at the cost of a shape table that grows with the
  square of the shapes it already holds.
- A per-P free list instead of `sync.Pool`.
- Mark a binding as not retaining its arguments, at `Bind`. Aliasing
  and pooling could then coexist, at the cost of an annotation the
  compiler cannot check.

Stack allocation is out. The frame pointer is passed to opaque
closures, so escape analysis moves it to the heap whatever its size.
Removing that means fusing a whole program into a single closure, which
is the same combinatorial problem as fusing pairs.

## Learnings

- A compiled and cached call costs tens of nanoseconds over native with
  the same allocation count; parsing and compiling dominate everything
  else at 2.136us, which is what the per-source cache removes.
- The reflect tier's two big wins were caching assignability per
  argument and handing `reflect.Value.Call` a pre-typed interface
  value, because reflect re-checks both on every call.
- Aliasing a frame slot for an interface argument is what reaches
  allocation parity, and it is also what forecloses frame pooling: the
  callee may keep the pointer into the frame past the run.
- Cost figures are differences of two noisy numbers. One benchmark per
  process, a pinned core and `-gcflags=all=-l` are what make them
  repeatable; the inlining flag also changes what "parity" means, which
  [inlining.md](inlining.md) later measured on its own.
- Several claims in this chapter were superseded as the experiment
  grew: variadic functions bind now, as pack and spread
  ([fixtures.md](fixtures.md), 2026-09-06); the four layout classes
  grew to scalars at every width ([types.md](types.md), 2026-09-05);
  field access landed the same day; and the mandatory `;` became a
  statement delimiter, with the end of a line closing a statement
  (2026-09-07).
