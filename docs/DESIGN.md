---
title: Design
date: 2026-09-07T16:48:32+02:00
---

gozero executes imperative programs against bound Go functions. A
program is text, compiled at run time into a tree of typed closures
over the live function values, and the compiled form runs at tens of
nanoseconds over the same calls written in Go. This document describes
the design as it stands; the chapters in the [README](../README.md)
are the investigations that produced it.

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

## Type safety from the bindings

Compilation checks the program against the real `reflect.Type` of
every binding. A literal must be representable in the parameter it
fills, a declared type is not overridden by use, and a name's static
type is what methods and fields resolve against:

```gozero
takesU32(-1)        // compile error: cannot use -1 as uint32
takesI8(300)        // compile error: 300 overflows int8
var x int64
x = 5
takesInt(x)         // compile error: cannot use int64 as int
```

There is no code generation and no go/types: the signatures the host
already has are the type system. Only names read from the caller's
stack are checked at execution, because only then is their value
known.

## Tests without recompiling

The fixture suite under `testdata/` is the working proof of the
principle. Each `.txt` file is a program compiled and executed inside
a running Go test, against live bindings, with the test's own
`testing.TB` handed in on the stack; the program makes its own
assertions. The same behaviour written both ways:

```go
req, err := http.NewRequest("GET", "https://example.com/a/b", nil)
if err != nil {
	t.Fatal(err)
}
assert.Equal(t, "GET", req.Method)
req.Method = "POST"
assert.Equal(t, "POST", req.Method)
```

```gozero
req := http.NewRequest("GET", "https://example.com/a/b")
assert.Equal(tb, "GET", req.Method)
req.Method = "POST"
assert.Equal(tb, "POST", req.Method)
```

Adding a test is adding a file and rerunning the binary. Nothing is
generated and nothing is linked: the boundary is the binding set, not
the build. A new test needs a recompile only when it calls an API
nothing has bound yet, and type discovery pushes that boundary out
further than the binding list suggests.

## Discovery closes the binding gap

Binding a function registers more than a callable. The runtime walks
the type graph reachable from the signature - parameters, results,
pointees, elements, exported struct fields, interface methods - and
every type it finds becomes nameable in a `var` statement. Binding
`url.Parse` is what makes this compile, with no registration of
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

## The grammar

```
program := { stmt }
stmt    := "var" name typeref term
         | "return" [ arg ] term
         | [ name { "," name } ( ":=" | "=" ) ] rhs term
term    := ";" | EOL | EOF
rhs     := expr | string | number | "true" | "false" | "nil"
typeref := { "*" | "[]" } path
expr    := path "(" [ args ] ")" { "." ident "(" [ args ] ")" }
path    := ident { "." ident }
args    := arg { "," arg }
arg     := string | number | path | expr | path "..."
```

The end of a line closes a statement; the semicolon is a delimiter
between statements sharing one. Errors are never written down: a
trailing error result is stripped at compile time, checked after every
call, and a non-nil one ends the program. Every trailing argument is
optional and zero-fills, which is also what makes a message parameter
on an assert binding optional. `dest` is a reserved name holding the
pointer `Scan` was given. Variadic calls pack (`f(a, b, c)`) or spread
(`f(xs...)`).

## Levels of integration

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
   arguments, while its neighbours stay direct. The floor, not a cliff:
   one slow call does not send the program to the evaluator.
3. The reflect evaluator. The general implementation of the same
   semantics, used when the program as a whole cannot build a closure
   tree, and the reference the JIT is tested against: the equivalence
   suite runs every program down both tiers and compares results,
   errors and what each callee received.

`Runtime.Supports(src) error` is the observable gate: it reports which
call keeps a program off the direct tier and why, so a benchmark or a
test asserts its tier instead of discovering a fallback in a slow
number.

Two pieces of plumbing round out the integration. A binding parameter
of type `context.Context` the program does not pass is auto-filled
with the execution context, so cancellation and deadlines flow from
`ExecContext` into the bindings without the program mentioning them.
And a stack name read more than once is loaded once when the program
starts: one map lookup, then offset reads, with the documented
consequence that a binding mutating the stack mid-run is not seen by
later uses of the same name.

## API

```go
rt := gozero.NewRuntime()
err := rt.Bind("NewRequest", http.NewRequest)
err = rt.BindScope("json", map[string]any{"NewEncoder": json.NewEncoder})
err = rt.BindType("io.Closer", (*io.Closer)(nil))
rt.SetLogger(logger)                  // discovery reports at debug level

fn, err := rt.Compile(src)            // cached per source string
v, err := rt.Eval[*http.Request](src, stack)
v, err = fn.Exec[*http.Request](stack)
v, err = fn.ExecContext[*http.Request](ctx, stack)
err = fn.Scan(&dest, stack)           // dest bound to the name "dest"
err = rt.Supports(src)                // nil when every call is direct
```

`Eval`, `Exec` and `Scan` are generic methods, which needs the go1.27
language version the go.mod selects. A panic inside a binding arrives
as `*PanicError` carrying the value and a stack, on every tier,
because `Compile` wraps what it returns.

## Costs

A cached single call costs tens of nanoseconds over native with the
same allocations ([overheads.md](overheads.md)); the six-fixture suite
runs at 1.0x-1.6x of handwritten mirrors with inlining disabled and
closer with it on ([fixtures.md](fixtures.md),
[inlining.md](inlining.md)). Parse and compile cost about 2us and are
paid once per source string.

## Open edges

- A call returned directly, `return f(x)`, does not reach the direct
  tier: a returned value needs a slot. The named form, `v := f(x);
  return v`, does. The planner could give a trailing returned call a
  slot of its own.
- `jit.go` holds the original single-statement tier, still used for a
  flat non-variadic call. Some of its shapes are unreachable from any
  test; whether the tier still earns its place against the program
  compiler is an open question.
- Pooling the frame remains foreclosed by slot aliasing; the unexplored
  approaches are recorded at the end of
  [overheads.md](overheads.md).
