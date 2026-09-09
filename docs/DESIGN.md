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

The language has statements and nothing else: a call, a name bound to
a call's results, a var declaration, a field read or write, a return.
There are no operators, no conditionals, no loops and no standard
library. Everything a program can do, it does by calling a Go function
the host bound:

```go
rt := gozero.NewRuntime()
rt.BindScope("http", map[string]any{"NewRequest": http.NewRequest})
rt.BindScope("json", map[string]any{"NewEncoder": json.NewEncoder})
```

```gozero
req := http.NewRequest("GET", "/")
json.NewEncoder(dest).Encode(req.Cookies())
```

The consequence is that the language never grows a surface of its own
to maintain, and the host controls exactly what a program can reach:
the binding set is the sandbox boundary. A program cannot open a file
unless a function that opens files was bound.

Methods extend the reach without extending the bindings. `Cookies` and
`Encode` are not bound; they are resolved on the static result types
of the two calls when the program compiles, so an unknown method is a
compile error rather than an execution one.

## The pipeline

`Runtime.Compile` runs three stages and caches the result per source
string:

1. The parser turns the text into a flat list of statements.
2. The program compiler checks every statement against the bindings'
   `reflect.Type`s and emits a `vmProgram`, which is both the checked
   form and the reflect evaluator.
3. The step JIT tries to lower the whole `vmProgram` to a tree of
   typed closures making direct calls. When it declines, the
   `vmProgram` runs as it is.

A one-statement program that is a single flat call short-circuits to
the original single-statement path (`jit.go`), whose smaller shape
table predates the program compiler.

## The parser

Single-pass recursive descent over the source bytes, with the scanner
helpers in `lexer.go`; there is no token stream. String literals
without escapes are substrings of the source, zero-copy. Ambiguity is
handled by saving the position and rewinding: an assignment list that
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

There is no code generation and no go/types: the signatures the host
already has are the type system. `compileProgram` walks the
statements once, tracking a slot and a static type per name, and
checks everything checkable before the program ever runs:

- A literal must be representable in the parameter it fills:
  `takesU32(-1)` and `takesI8(300)` are compile errors.
- A declared type is not overridden by use, and a name's static type
  is what methods and fields resolve against.
- A name that would shadow a binding or keyword is rejected, because
  path resolution prefers the binding and the name could never be
  read back.
- A name assigned a literal with no declared type takes its type from
  the first binding parameter the program passes it to, falling back
  to the literal's own width.

Two implicit rules shape the emitted form: a trailing error result is
stripped from every call's result list and checked after the call,
and a `context.Context` parameter the program does not pass is filled
from the execution context. Both are language semantics and are
described in [syntax.md](syntax.md); here they mean a `vmCall` knows
its error index and its context slots at compile time.

The `vmProgram` is also the reflect evaluator: slots are
`reflect.Value`s allocated per run alongside one shared argument
frame, every call gets a disjoint window into that frame, and
invocation is `reflect.Value.Call`. Two measured costs are removed at
this tier: assignability of a stack value against an interface
parameter is cached per argument (it was 29% of the profile), and a
non-empty interface argument is pre-converted through a type
assertion so reflect receives a value already typed as the parameter
and skips its method-table walk. Only names read from the caller's
stack are checked at execution, because only then is their value
known.

## Discovery closes the binding gap

Binding a function registers more than a callable. The runtime walks
the type graph reachable from the signature - parameters, results,
pointees, elements, exported struct fields, interface methods - and
every type it finds becomes nameable in a `var` statement. Binding
`url.Parse` makes this compile, with no registration of
`url.URL` anywhere:

```gozero
var u url.URL
assert.Equal(tb, "", u.Path)
```

Ten standard library constructors contribute 118 types between them.
The practical effect on testing is that the binding set ages well: a
new fixture usually needs no new bindings, because the types it wants
to declare and the methods it wants to call are already reachable from
the functions bound on day one. `BindType` covers the exception, a
type no binding mentions. [types.md](types.md) is the full account.

## The JIT

A compiled call lands on one of three levels, cheapest first, and the
whole program is built from whichever each call reaches:

1. The direct-call tier. A shape table keyed by layout classes -
   pointer, string, interface, slice, error, and every scalar width -
   casts the binding's funcval to its concrete signature and calls it
   with no reflection. This is where allocation parity comes from: an
   interface argument taken from a once-written slot aliases the frame
   instead of copying, constants box once at compile time, and small
   scalars box into static cells.
2. The reflect bridge. A call whose signature is outside the table
   compiles to a per-call `reflect.Value.Call` with pre-typed
   arguments, while its neighbours stay direct. The bridge is a floor,
   not a cliff: one slow call does not send the program to the
   evaluator.
3. The reflect evaluator. The general implementation of the same
   semantics, used when the program as a whole cannot build a closure
   tree, and the reference the JIT is tested against: the equivalence
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
whose single result is read exactly once by the statement after it,
splicing the producer into the reader's argument tree. Only the names
that survive get slots, laid out as fields of a `reflect.StructOf`
frame struct allocated per run - typed storage the collector scans -
and a program with no such name allocates nothing beyond what its
bindings allocate. Interface arguments carry itabs computed at
compile time; the itab for a (concrete, interface) pair can only be
reached through a type assertion, so a small table of converters does
exactly that.

`Runtime.Supports(src) error` is the observable gate: it reports which
call keeps a program off the direct tier and why, so a benchmark or a
test asserts its tier instead of discovering a fallback in a slow
number.

One piece of plumbing is shared by every tier: a stack name read more
than once is loaded once when the program starts - one map lookup,
then offset reads - with the documented consequence that a binding
mutating the stack mid-run is not seen by later uses of the same
name.

## Tests without recompiling

The fixture suite under `testdata/` is the working proof of the
principle. Each `.txt` file is a program compiled and executed inside
a running Go test, against live bindings, with the test's own
`testing.TB` handed in on the stack; the program makes its own
assertions. Adding a test is adding a file and rerunning the binary:
nothing is generated and nothing is linked, and a new test needs a
recompile only when it calls an API nothing has bound yet.
[syntax.md](syntax.md) shows the fixtures next to the Go they mirror.

## API

```go
rt := gozero.NewRuntime()
err := rt.Bind("NewRequest", http.NewRequest)
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
suite runs at 1.1x-1.6x of handwritten mirrors with inlining disabled and
closer with it on ([fixtures.md](fixtures.md),
[inlining.md](inlining.md)). Parse and compile cost about 2us and are
paid once per source string.

## Open edges

- A call returned directly, `return f(x)`, does not reach the direct
  tier: a returned value needs a slot. The named form, `v := f(x); return v`, does. The planner could give a trailing returned call a
  slot of its own.
- `jit.go` holds the original single-statement tier, still used for a
  flat non-variadic call. Some of its shapes are unreachable from any
  test; whether the tier still earns its place against the program
  compiler is an open question.
- Pooling the frame remains foreclosed by slot aliasing; the unexplored
  approaches are recorded at the end of
  [overheads.md](overheads.md).
- Extending the syntax with conditionals, loops, closures or operator
  expressions is researched and declined, feature by feature, in
  [design/](design/).
