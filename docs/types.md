---
title: Type binding, hydration and discovery
date: "2026-09-05T17:44:56+02:00"
---

The VM calls the Go functions the host binds. Those functions have
types. A program has to be able to name them: `var x int64` needs
`int64`, and `var r *http.Request` needs a type the host never
mentioned except as a return value. This is how those names get into
the runtime and what happens to them at compile and execution time.

Binding a function is the only step a
caller normally takes; everything the signature implies follows from it.

## Three stages

**Binding**: `rt.Bind("http.NewRequest", http.NewRequest)` puts one
callable in the runtime under one name.

**Discovery** is what the runtime does with that signature. It walks the
type graph reachable from the function and records every type it finds.
A later `var` statement can name any of them.

**Hydration** is what a program does with a discovered type: `var u url.URL` produces the zero value of `url.URL` in a slot, without the
host ever having registered `url.URL` or written a constructor for it.

## Discovery

`Bind` calls `discover` on the function's `reflect.Type`, which records
the type and recurses into everything reachable from it:

| Kind                        | Reached                          |
|-----------------------------|----------------------------------|
| func                        | every parameter and every result |
| pointer, slice, array, chan | the element type                 |
| map                         | the key and the element type     |
| struct                      | every exported field's type      |
| any                         | every method's signature         |

Struct fields are in that list because a program can read a field.
`req.Header` has to make `http.Header` nameable, and `http.Header` is
reachable from `*http.Request` only through the field.

Recursion stops on a type already in the registry, which also keeps a
recursive type from looping. The walk caps depth at five, the distance
from a binding to a field of the struct behind its result:

```
http.NewRequest            depth 1  the func
  *http.Request            depth 2  a result
    http.Request           depth 3  the pointee
      http.Header          depth 4  a field
        []string           depth 5  the map's element
```

Names are `reflect.Type.String()`: `int64`, `*http.Request`,
`http.Header`, `func(string) http.Handler`. Two packages with the same
base name collide, and the later `Bind` wins; `BindType` resolves
the collision.

`any` is the exception. It prints as `interface {}`, which is not a name
a program can write. The registry also holds it under the spelling Go
source uses.

### What one binding is worth

Binding ten standard library constructors, counting only what each
contributes beyond the predeclared seed:

| Binding                      | Types | Notable                                             |
|------------------------------|------:|-----------------------------------------------------|
| `http.NewRequestWithContext` |   111 | `*http.Request` `*url.URL` `url.Values` `io.Reader` |
| `http.NewRequest`            |    86 | `*http.Request` `*url.URL` `url.Values` `io.Reader` |
| `http.FileServer`            |    32 | `http.Handler` `http.FileSystem`                    |
| `http.NewServeMux`           |    31 | `*http.ServeMux` `http.Handler`                     |
| `pprof.Handler`              |    25 | `http.Handler`                                      |
| `url.Parse`                  |    25 | `*url.URL` `url.Values`                             |
| `http.StripPrefix`           |    25 | `http.Handler`                                      |
| `json.NewDecoder`            |    13 | `io.Reader`                                         |
| `json.NewEncoder`            |     9 | `*json.Encoder` `io.Writer`                         |
| `url.ParseQuery`             |     9 | `url.Values`                                        |
| all together                 |   118 |                                                     |

`http.NewRequestWithContext` reaches more than `http.NewRequest` for one
reason: `context.Context` is a parameter, and its methods pull in
`time.Time` and everything `time.Time`'s methods return.

The totals overlap heavily. Ten bindings contribute 118 types between
them where one of them contributes 111, because most of their walks
cover the same graph from different entry points.

`net/http` has no `NewClient`. Its only exported constructors are
`NewRequest` and `NewRequestWithContext`. The client side of the
package becomes reachable through the types those name, with no
constructor of its own.

### Watching it happen

`Runtime.SetLogger` attaches an `slog.Logger` that discovery reports
through at debug level. A runtime without one pays nothing, because the
runtime checks the field for nil and installs no default.

```go
rt := NewRuntime()
rt.SetLogger(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
	Level: slog.LevelDebug,
})))
rt.Bind("http.NewRequest", http.NewRequest)
```

```
level=DEBUG msg=bind name=http.NewRequest signature="func(string, string, io.Reader) (*http.Request, error)"
level=DEBUG msg="type discovered" name="func(string, string, io.Reader) (*http.Request, error)" kind=func via=http.NewRequest depth=1 methods=0
level=DEBUG msg="type discovered" name=io.Reader kind=interface via=http.NewRequest depth=2 methods=1
level=DEBUG msg="type discovered" name=*http.Request kind=ptr via=http.NewRequest depth=2 methods=0
```

`via` is the binding the walk started from and `depth` is how far it had
to go. A name that turns up unexpectedly leads back to the signature
that dragged it in.

`Runtime.Types` returns the registry as a list. `atkins test:discovery`
runs the test that prints this output, including a table per binding and
the full set of names.

### Registering a type by hand

Only for a type no binding mentions:

```go
rt.BindType("io.Closer", (*io.Closer)(nil))
```

The value is a value of the type. Because an interface has no value
to pass, a nil pointer to it works and `BindType` takes the element
type. The walk then covers the registered type like anything else.

## Hydration

A `var` statement resolves its type name against the registry and puts
that type's zero value in scope:

```
var x int64;
x = 123;

var b []uint8;
var r *http.Request;

var u url.URL;
json.NewEncoder(dest).Encode(u.Path);
```

The type name can take `*` and `[]` prefixes, matching how
`reflect.Type.String()` spells them. An unknown name is a compile error
naming the fix: `unknown type "nope.Thing", register it with BindType`.

The zero value costs nothing to produce. On the direct-call tier the
frame is one allocation that comes back zeroed. A declared name is
already its zero value with no instruction; the reflect tier uses a
`reflect.Zero` recorded at compile time and written once per run.

### Inference

A short declaration names no type. `x := 123` has to work out
what `x` is. Three rules, in order:

1. A `var` statement fixes it.
2. Otherwise the first binding the program passes the name to decides,
   including through a nested call.
3. Otherwise the literal keeps the width the parser produced: `int64`,
   or `float64` when it has a decimal point.

```
var x int32;
x = 7;          int32, from the declaration

x := 5;
takesInt(x);    int, from the use

x := 5;         int64, nothing to infer from
x := 5.5;       float64
```

Declaration is `:=` or `var`; a plain `=` assigns to a declared name
and is a compile error otherwise. This chapter originally read the
other way, because the prototype let `x = 123` declare `x`; the
change that adopted Go's declaration rule removed the form
([changelog](changelog.md)).

Use does not override a declared type. `var x int64; x = 5; takesInt(x);` reports `cannot use int64 as int` and does not
convert, because the declaration states the type.

### Literals at the call

A numeric literal takes its type from the parameter it fills. The parser
produces only `int64` and `float64`. Without this a program could
not pass a literal to a binding taking `int` at all. The value has to
be representable, and a literal that is not is a compile error, never
a wrap:

```
takesInt(42)      int(42)
takesU32(7)       uint32(7)
takesF32(7)       float32(7)
takesI8(300)      300 overflows int8
takesU32(-1)      cannot use -1 as uint32, it is negative
takesInt(1.5)     cannot use 1.5 as int, it has a decimal point
```

An empty interface parameter keeps the width the parser produced, since
there is no parameter type to convert towards.

## What this costs at execution

Discovery and hydration are compile-time. No step in this document runs
per call: the compiler consults the registry when a `var` statement
compiles, converts the literal when the argument compiles, and the zero
value is
either the zeroed frame or a `reflect.Value` recorded once.

Scalars reach the direct-call tier. Each width is its own layout class,
because the cast that makes a direct call needs the exact Go type: an
`int32` parameter is four bytes where an `int64` is eight; a float
travels in a different register file from an integer of the same width.
`int` and `uint` follow their size and not their kind. They share
a class with `int64` and `uint64` on a 64 bit platform.

Between them, these all run without `reflect.Value.Call`:

```
var x int64;
x = 1;
json.NewEncoder(dest).Encode(x);

var x float64;
x = 2.5;
json.NewEncoder(dest).Encode(x);

var b bool;
json.NewEncoder(dest).Encode(b);

x := 1;
json.NewEncoder(dest).Encode(x);

var x int64;
x = 3;
takesI64(x);

json.NewEncoder(dest).Encode(takesInt(42));

req := http.NewRequest("GET", "/");
json.NewEncoder(dest).Encode(req.ContentLength);
```

Boxing a scalar into an interface allocates, because the data word has
to point at a value of the concrete width. That is the allocation the Go
compiler makes at the same place.

The shape table is finite and a program JITs whole or not at all. A
call whose parameter and result classes are not in it sends the whole
program to the reflect evaluator. `Runtime.Supports` reports which call
and which shape. This shows up before a benchmark has to discover it.

## Learnings

- Binding a function is the only registration step a caller takes:
  discovery walks the signature's type graph and everything reachable
  from it becomes nameable, struct fields included. Ten stdlib
  constructors contribute 118 types between them, with heavy overlap.
- A type becomes nameable because some bound function mentions it,
  not because anyone declared it; `BindType` exists only for the type
  no binding mentions.
- Hydration is free at execution: a declared zero value is the zeroed
  frame on the direct tier and a `reflect.Zero` recorded once on the
  other.
- Literal typing needs three rules (declaration, first use, parser
  width), and use does not override a declared type. A mismatch stays
  a compile error.
- Scalars reach the direct-call tier because each width is its own
  layout class: the cast needs the exact Go type.
- Three claims aged out of this chapter: a scalar boxed into an
  interface does not always cost an allocation, since bits under 256 alias a
  static cell (2026-09-07); a call outside the shape table compiles to
  a per-call bridge while its neighbours stay direct, and the rest of
  the program stays off reflect (2026-09-06); and `x = 123` does not
  declare, since `:=` became the required short declaration
  (2026-09-09).
