---
title: Design
date: "2026-09-09T00:00:00+02:00"
---

gozero executes imperative programs against bound Go functions. A
program is text, compiled at run time into a tree of typed closures
over the live function values, and the compiled form runs at tens of
nanoseconds over the same calls written in Go. This document
describes how the parser, the compiler and the JIT achieve that; the
language as a user sees it, with Go and gozero side by side, is in
[syntax.md](syntax.md). The chapters in the [README](../README.md)
are the investigations that produced both.

## The imperative principle

Statements are the whole language: a call, a name bound to a call's
results, a var declaration, a field read or write, a return.
The language has no operators, no conditionals, no loops and no
standard library. Everything a program can do, it does by calling a
Go function the host bound:

```go
rt := gozero.NewRuntime()
rt.BindScope("http", map[string]any{"NewRequest": http.NewRequest})
rt.BindScope("json", map[string]any{"NewEncoder": json.NewEncoder})
```

```go
req := http.NewRequest("GET", "/")
json.NewEncoder(dest).Encode(req.Cookies())
```

The consequence is that the language never grows a surface of its own
to maintain, and the host controls exactly what a program can reach:
the binding set is the sandbox boundary. A program cannot open a file
unless the host binds a function that opens files.

`Bind` takes data as readily as funcs. The boundary holds there too.
Binding a value copies it in. A program writing the name writes its
own storage. An address is the host saying the variable is the
program's to write, which the program does through `*name = v`.
[syntax.md](syntax.md) has the rules.

Methods extend the reach without extending the bindings. `Cookies` and
`Encode` are not bindings; the compiler resolves them on the static
result types of the two calls. An unknown method is a compile error
and never an execution one. Resolution covers Go's
method sets in full - value and pointer receivers, methods promoted
from embedded types - with Go's addressability rule: a pointer
receiver method needs a named value or a field of one, never the
direct result of a call.

## The pipeline

`Runtime.Compile` runs three stages and caches the result per source
string:

1. The parser turns the text into a flat list of statements.
2. The program compiler checks every statement against the bindings'
   `reflect.Type`s and emits a `vmProgram`, which is both the checked
   form and the reflect evaluator.
3. The step JIT lowers the whole `vmProgram` to a tree of typed
   closures making direct calls when every call fits. When it
   declines, the `vmProgram` runs unchanged.

A one-statement program that is a single flat call short-circuits to
the original single-statement path (`jit.go`), whose smaller shape
table predates the program compiler.

## The parser

Single-pass recursive descent over the source bytes, with the scanner
helpers in `lexer.go`; there is no token stream. String literals
without escapes are substrings of the source, zero-copy. The parser
resolves ambiguity by saving the position and rewinding: an assignment list that
turns out to be a call, a path that turns out to be a field
assignment.

The parser resolves nothing. `http.NewRequest` is one bound name,
`req.Cookies()` is a method on the value held by `req`, and
`req.Header` is a struct field on it - all three parse as a dotted
path, and only the compiler, holding the bindings and the types, can
tell them apart. That keeps the grammar free of the type system and
the parser under a few hundred lines.

The end of a line closes a statement; the semicolon is a delimiter
between statements sharing one. The full grammar is in
[syntax.md](syntax.md).

## The compiler

The compiler uses no code generation and no go/types: the signatures the host
already has are the type system. `compileProgram` walks the
statements once, tracking a slot and a static type per name, and
checks everything checkable before the program ever runs:

- A literal must be representable in the parameter it fills:
  `takesU32(-1)` and `takesI8(300)` are compile errors.
- A declared type is not overridden by use, and a name's static type
  is what methods and fields resolve against.
- The compiler rejects a name that would shadow a binding or keyword,
  because path resolution prefers the binding and no statement could
  read the name back.
- Declaration is Go's rule: `:=` or `var` declares, `=` assigns to a
  declared name and errors otherwise, and a `:=` that declares no new
  name errors too.
- A name declared `:=` with a literal takes its type from the first
  binding parameter the program passes it to, falling back to the
  literal's own width.

Two implicit rules shape the emitted form: the compiler strips a
trailing error result from every call's result list and checks it
after the call, and fills a `context.Context` parameter the program
does not pass from the execution context. Both are language semantics
that [syntax.md](syntax.md) describes; here they mean a `vmCall` knows
its error index and its context slots at compile time.

The `vmProgram` is also the reflect evaluator: slots are
`reflect.Value`s allocated per run alongside one shared argument
frame, every call gets a disjoint window into that frame, and
invocation is `reflect.Value.Call`. This tier removes two measured
costs: it caches the assignability of a stack value against an
interface parameter per argument (29% of the profile before the
cache), and a
non-empty interface argument is pre-converted through a type
assertion so reflect receives a value already typed as the parameter
and skips its method-table walk. The evaluator checks only names read
from the caller's stack at execution, because their value exists only
then.

## Discovery closes the binding gap

Binding a function registers more than a callable. The runtime walks
the type graph reachable from the signature - parameters, results,
pointees, elements, exported struct fields, interface methods - and
every type it finds becomes nameable in a `var` statement. Binding
`url.Parse` makes this compile, with no registration of
`url.URL` anywhere:

```go
var u url.URL
assert.Equal(tb, "", u.Path)
```

Ten standard library constructors contribute 118 types between them.
The practical effect on testing is that the binding set ages well: a
new fixture needs no new bindings in the common case, because the
types it declares and the methods it calls are already reachable from
the functions bound on day one. `BindType` covers the exception, a
type no binding mentions. [types.md](types.md) is the full account.

## The JIT

A compiled call lands on one of three levels, cheapest first, and the
whole program combines whichever level each call reaches:

1. The direct-call tier. A shape table keyed by layout classes -
   pointer, string, interface, slice, error, and every scalar width -
   casts the binding's funcval to its concrete signature and calls it
   with no reflection. This is where allocation parity comes from: an
   interface argument taken from a once-written slot aliases the frame
   and skips the copy, constants box once at compile time, and small
   scalars box into static cells.
2. The reflect bridge. A call whose signature is outside the table
   compiles to a per-call `reflect.Value.Call` with pre-typed
   arguments, while its neighbours stay direct. The bridge is a floor,
   not a cliff: one slow call does not send the program to the
   evaluator.
3. The reflect evaluator. The general implementation of the same
   semantics, used when the program as a whole cannot build a closure
   tree, and the reference the JIT tests compare against: the equivalence
   suite runs every program down both tiers and compares results,
   errors and what each callee received.

The direct tier works because under Go's internal ABI, argument and
result passing depends only on the layout classes of the types, not
their names: `io.Reader` and `struct{ tab, data unsafe.Pointer }` are
the same two words, `*http.Request` and `unsafe.Pointer` the same
one. A compiled expression is one closure per class, and a value
flows from the call that produces it to the call that consumes it as
a closure's return value - a Go local, no storage of its own.

Before compiling, a planner (`planInline`) drops every statement
whose single result the statement after it reads exactly once,
splicing the producer into the reader's argument tree. Only the names
that survive get slots, laid out as fields of a `reflect.StructOf`
frame struct allocated per run - typed storage the collector scans -
and a program with no such name allocates nothing beyond its
bindings' own allocations. Interface arguments carry itabs computed
at compile time; only a type assertion reaches the itab for a
(concrete, interface) pair. A small table of converters performs
exactly that assertion.

`Runtime.Supports(src) error` is the observable gate: it reports which
call keeps a program off the direct tier and why. A benchmark or a
test asserts its tier and never discovers a fallback through a slow
number.

Every tier shares one piece of plumbing: the program loads a stack
name read more than once a single time when it starts - one map
lookup, then offset reads - with the documented consequence that
later uses of the same name do not see a binding mutating the stack
mid-run.

## Tests without recompiling

The fixture suite under `testdata/` is the working proof of the
principle. Each `.txt` file is a program compiled and executed inside
a running Go test, against live bindings, with the test's own
`testing.TB` handed in on the stack; the program makes its own
assertions. Adding a test is adding a file and rerunning the binary:
the suite generates nothing and links nothing, and a new test needs a
recompile only when it calls an API nothing has bound yet.
[syntax.md](syntax.md) shows the fixtures next to the Go they mirror.

## API

```go
rt := gozero.NewRuntime()
err := rt.Bind("NewRequest", http.NewRequest)   // a func, called
err = rt.Bind("time.Hour", time.Hour)           // a value, read
err = rt.Bind("os.Args", &os.Args)              // an address, written through
err = rt.BindScope("json", map[string]any{"NewEncoder": json.NewEncoder})
err = rt.BindType("io.Closer", (*io.Closer)(nil))
rt.SetLogger(logger) // discovery reports at debug level

fn, err := rt.Compile(src) // cached per source string
v, err := rt.Eval[*http.Request](src, stack)
v, err = fn.Exec[*http.Request](stack)
v, err = fn.ExecContext[*http.Request](ctx, stack)
err = fn.Scan(&dest, stack) // dest bound to the name "dest"
err = rt.Supports(src)      // nil when every call is direct
```

`Eval`, `Exec` and `Scan` are generic methods, which needs the go1.27
language version the go.mod selects. A panic inside a binding arrives
as `*PanicError` carrying the value and a stack, on every tier,
because `Compile` wraps what it returns.

## Costs

A cached single call costs tens of nanoseconds over native with the
same allocations ([overheads.md](overheads.md)); the seven-fixture
suite runs at 1.1x-1.5x of handwritten mirrors with and without
inlining, with every fixture at or below its mirror's allocation
count (tables 1 and 2 in [inlining.md](inlining.md); the analysis is
in [fixtures.md](fixtures.md)). A program pays parse and compile once
per source string:

| Stage             | Cost      | Paid                   |
|-------------------|-----------|------------------------|
| Parse and compile | about 2us | once per source string |

## Open edges

- A call returned directly, `return f(x)`, does not reach the direct
  tier: a returned value needs a slot. The named form, `v := f(x); return v`, does. The planner could assign a trailing returned call
  a slot of its own.
- `jit.go` holds the original single-statement tier, still used for a
  flat non-variadic call. A measurement rejects deleting it in favour
  of the program tiers: a returned value travels through a frame
  slot on the step JIT, and the pooled-frame round trip plus the boxing
  read put the amortized cost of the cached flat statement at about
  290ns against this tier's 35ns on the benchmark box, with the same
  allocations. A profile splits the delta: about 65ns in the frame
  round trip (the pool get, the typed clear, the put), about 70ns in
  the `reflect.NewAt(...).Elem().Interface()` read that boxes the
  returned slot, the rest in per-node closure hops the fused
  single-statement closure does not pay. The tier stays until a
  returned value can travel without the frame.
- Each program gates frame pooling for itself: a frame no pointer
  escapes recycles through a pool, an aliased or addressed frame
  allocates fresh. The end of [overheads.md](overheads.md) records the
  trade and the approaches not taken.
- Extending the syntax with conditionals, loops, closures, operator
  expressions or struct type declarations has research and a
  decline, feature by feature, in [design/](design/). Channel
  receive and send moved from the same research into the syntax and
  back out: a channel still passes as a value, but both operations
  paid a reflect select and an element box per operation, and the
  goroutines and topology stayed with the host either way.
  [design/channels.md](design/channels.md) records the research and
  [changelog.md](changelog.md) the removal.
