---
title: Go, call overheads and JIT
date: "2026-09-03T15:34:51+02:00"
---

What does it cost to call Go functions from a small interpreted
language, compared with calling them from Go? The runtime parses a
statement once, compiles it once, caches it per source string, and then
executes it many times. This measures the execution, against the same
calls written by hand.

The costs over native:

- a single call costs 61.87ns, with the same number of allocations as
  the native code;
- a four-call program costs 196.9ns, also with the same number of
  allocations as the native code;
- parsing and compiling, on a cache miss, cost 2.136us.

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
other calls. The language has no arithmetic, no string concatenation, no field
access, no conditionals. Errors are never written down: the compiler
strips a trailing error result, the runtime checks it after every call,
and a non-nil one ends the program.

Two rules cover the gaps that leaves. Every argument is optional and a
missing one is the zero value of its parameter type. A three
parameter function accepts a call with two arguments. `dest` is a
reserved name holding the pointer passed to `Scan`; the program writes
its output through it.

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

- The call omits the third argument of `http.NewRequest`, and the body
  arrives as a nil `io.Reader`.
- Neither error has a name. When `http.NewRequest` fails, `Scan`
  returns that error, and nothing after it runs.
- The program binds neither `Cookies` nor `Encode`. The compiler finds
  them on the result types of the two bindings. An unknown method is a
  compile error and not an execution one.
- Chaining and nesting compile to the same four steps. The one line
  form is `json.NewEncoder(dest).Encode(http.NewRequest("GET", "/").Cookies());`.

## Bindings

The runtime binds only functions, and only some shapes reach the fast
path.

- `Bind` takes any func value. Method expressions such as
  `(*json.Encoder).Encode` are ordinary funcs and bind directly,
  although this program does not need them.
- The compiler rejects variadic functions.
- Names a program binds carry a static type, and the compiler resolves
  methods against it. Names read from the caller's stack are opaque,
  and the runtime checks them on each read.
- The JIT understands four layout classes: pointer-shaped, string,
  interface and slice. A parameter or result outside them, an `int`
  for example, keeps the whole program on the reflect tier.
- An interface argument fed from the stack needs its type in
  `ifaceConvs`, which holds `any`, `io.Writer` and `io.Reader`. Hand
  code cannot assemble the interface table for a pair of concrete and
  interface types. Each entry obtains it with a type assertion.
- A slot written more than once cannot back an interface argument,
  because the argument aliases the slot and does not copy it.

## API

```go
rt := NewRuntime()
rt.BindScope("http", map[string]any{"NewRequest": http.NewRequest})
rt.BindScope("json", map[string]any{"NewEncoder": json.NewEncoder})

fn, err := rt.Compile(src)  // cached per source string
err = fn.Scan(&dest, stack) // dest is bound to the name "dest"
req, err := fn.Exec[*http.Request](stack)
req, err := rt.Eval[*http.Request](src, stack)
```

`Bind` registers one name, `BindScope` a dotted group; a dotted name is
one key in the binding map, and a path resolves to the longest prefix
that names a binding. `Compile` returns a `CompiledFunc`, a defined
`func(map[string]any, any) (any, error)`.

`Eval`, `Exec` and `Scan` are generic methods, which need the go1.27
language version the `go` line in go.mod selects. `T` appears only in
the results of `Eval` and `Exec`. Callers instantiate both explicitly;
`Scan` infers `T` from `dest`.

## Overhead

Medians of five runs, each benchmark in its own process, pinned to one
core with inlining disabled, Intel N150, go1.27.0. cost-sec/op is the
measured loop minus a native baseline timed inline in the same
process. [`bench.txt`](../bench.txt) held this sweep at the time of
this chapter; the `atkins save` job now writes its combined count-3
sweep of every benchmark to that file, where the amortized cost figures
read in the same tens of nanoseconds.

| Benchmark          | sec/op  | B/op | allocs/op | cost-sec/op |
|--------------------|--------:|-----:|----------:|------------:|
| Native             |  651.7n |  512 |         3 |           - |
| CostWithoutCaching | 2.795us | 1256 |        18 |     2.136us |
| CostNaive          | 1.997us |  576 |         5 |     1.189us |
| CostAmortizedCache |  921.4n |  512 |         3 |      61.87n |
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
  two numbers around 650n or 1.4us. A few percent of drift in the
  baseline moves it by a large fraction of itself. The ordering of the
  tiers holds; the JIT figures land in the tens to low hundreds of
  nanoseconds, and the measurement resolves them no further.

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
913n between sweeps. The benchmarks require `-gcflags=all=-l`: without
it the compiler inlines the native baseline into the loop body, and the
baseline measures nothing.

## Optimisation

The attempts, in order, with the effect on the four call program's
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

- The assignability cache is the largest single win. `AssignableTo`
  against an interface walks method tables comparing names and took
  29% of the profile; because the runtime passes the same concrete
  type to the same program every time, one entry removes it.
- `reflect.Value.Call` runs its own `assignTo` per argument, which the
  cache above cannot reach. The argument needs pre-typing. A type
  assertion produces the interface table, and the value arrives
  already typed as the parameter.
- The step JIT is the shape table from `jit.go`, applied to every call
  and not to one statement alone. `reflect.StructOf` builds the slots
  as one allocation with a correct pointer map and a stable address per
  field. The JIT reads and writes raw words at offsets. Every store
  keeps its write barrier. A program JITs whole or not at all.
- Aliasing buys the allocation parity. An interface argument taken
  from a slot points at the slot and not at a copy, which is legal only
  because the frame outlives the call and the program writes the slot
  once.
- `reflect.New` took 12% of the JIT'd program; its pointer-type lookup
  accounts for the bulk of that, the allocation for the rest. The
  linkname goes to the allocator reflect itself calls. This is a pull
  linkname to an internal symbol, and a toolchain bump can break it.

Approaches tested and rejected:

- Pooling frames. The aliasing above means an interface passed to a
  callee can outlive the run. The runtime cannot reuse a frame.
- Boxing a slice result into an `any` through `reflect.New` per call.
  Correct, and one allocation more than native.
- Carrying slot values as `reflect.Value`. Every JIT'd step then pays
  the conversion at both ends.

The remaining 197n is the frame allocation and the loop over four
closures.

`TestStepJITMatchesReflect` runs the same program down both tiers and
compares output, errors, and what each callee received through the
interface it got. A toolchain that changes the runtime layouts the
JIT encodes fails the suite ahead of any benchmark.

## Is the frame allocation avoidable

The remaining cost is the frame allocation and the loop over four
closures. Running the same steps with the frame hoisted out of the loop
measures the split, one 48 byte allocation apart:

| Measurement                             | Cost    |
|-----------------------------------------|--------:|
| Program cost over native                | 196.9ns |
| Steps, frame hoisted out of the loop    | 1924ns  |
| Steps, frame allocated in the loop      | 2009ns  |
| Frame share of the 197ns                | 85ns    |

Preallocating is not possible, because a frame cannot outlive one run
of a concurrent program. Pooling measures well:

| Frame per run               | sec/op | allocs/op |
|-----------------------------|-------:|----------:|
| unsafeNew                   | 62.57n |         1 |
| sync.Pool, cleared on reuse | 22.85n |         0 |
| sync.Pool, not cleared      | 20.39n |         0 |

- Pooling saves 40ns and one allocation per run.

Pooling and the aliasing described above are mutually exclusive: an
interface argument taken from a slot passes a pointer into the frame to
the callee, and nothing proves the
callee does not keep it past the run. Dropping the alias to allow
pooling trades one allocation for another, 22.85n plus boxing the
slice against 62.57n and no box.

Pooling is a clean win for a program with no aliased slot, which is
every program whose interface arguments are pointer-shaped, since the
frame holds those pointers directly and they never point at the frame.
It landed on
2026-09-10 as a per-program pool gated on exactly that proof: the
compiler marks the nodes that hand out frame pointers (an indirectly
aliased interface argument, an addressed receiver), and a program
with none recycles its frame through a `sync.Pool`, cleared on reuse
with the typed clear reflect performs, while every other program
allocates fresh. The retention annotation this chapter lists below
under approaches not tried became the binding contract instead: a
binding borrows its arguments for the duration of the call. The pack
slices, string boxes and literal blocks behind every call repool
after the run, and a binding that keeps an argument copies it
([changelog](changelog.md)).

One measurement needs care. Under `-gcflags=all=-l` the numbers above
show 6 allocations for both the program and its native counterpart.
With inlining enabled, escape analysis stops boxing `cookies`, and
native drops:

| Flags             | Native allocations | Native B/op | Program B/op |
|-------------------|-------------------:|------------:|-------------:|
| `-gcflags=all=-l` |                  6 |         688 |          712 |
| inlining enabled  |                  5 |         560 |          712 |

The allocation parity in the overhead table holds for the flags the
benchmarks use and not for an ordinary build, where the frame is a real
extra allocation.
Cost over native with inlining is 104.7n to 164.0n on a 545n call.

Approaches not tried:

- Reuse slots whose live ranges do not overlap. In the four call
  program `req` dies before the program creates `enc`, and both are
  pointer-shaped, which would take the frame from 48 to 32 bytes.
- Fuse a step whose pointer result feeds the next step's pointer
  parameter, when the next step reads that slot once. The program
  becomes two steps and one slot, at the cost of a shape table that grows with the
  square of the shapes it already holds.
- A per-P free list in place of `sync.Pool`.
- Mark a binding as not retaining its arguments, at `Bind`. Aliasing
  and pooling could then coexist, at the cost of an annotation the
  compiler cannot check.

Stack allocation is out. Because opaque closures receive the frame
pointer, escape analysis moves it to the heap whatever its size.
Removing that means fusing a whole program into a single closure, which
is the same combinatorial problem as fusing pairs.

## Learnings

- A compiled and cached call costs tens of nanoseconds over native with
  the same allocation count; parsing and compiling dominate everything
  else at 2.136us, and the per-source cache removes that cost.
- The reflect tier's two big wins are caching assignability per
  argument and passing `reflect.Value.Call` a pre-typed interface
  value, because reflect re-checks both on every call.
- Aliasing a frame slot for an interface argument reaches allocation
  parity and forecloses frame pooling in the same move: the callee may
  keep the pointer into the frame past the run. The pool that landed
  later admits only programs without exactly that aliasing.
- Cost figures are differences of two noisy numbers. One benchmark per
  process, a pinned core and `-gcflags=all=-l` make them
  repeatable; the inlining flag also changes what "parity" means, which
  [inlining.md](inlining.md) later measured on its own.
- Later work supersedes four claims in this chapter: variadic functions bind now, as pack and spread
  ([fixtures.md](fixtures.md), 2026-09-06); the four layout classes
  grew to scalars at every width ([types.md](types.md), 2026-09-05);
  field access landed the same day; and the mandatory `;` became a
  statement delimiter, with the end of a line closing a statement
  (2026-09-07).
