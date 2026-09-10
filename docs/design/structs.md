---
title: What if we implemented struct type declarations?
date: "2026-09-10T12:00:00+02:00"
---

Every type a program can name today comes from the host: the
bindings' signatures, the discovery walk behind them, or `BindType`.
This document asks what it would cost to let a program declare its
own struct types, under the standing constraints: Go syntax, stdlib
only, no code generation. The claims about `reflect.StructOf` below
are probed, not assumed: structurally identical calls return the
identical canonical type, the type prints as the unnamed struct
literal, an unnamed struct is assignable to a named host type with
the same fields, and field tags are supported.

## How it would look

```go
type Point struct {
	X int64
	Y int64
}

p := Point{X: 3, Y: 4}
p.X = 5
json.NewEncoder(dest).Encode(p)
```

The machinery exists in the stdlib. The compiler builds the type
once per source string with `reflect.StructOf`, registers it in a
program-local table under the declared name, and everything
downstream already works: composite literals, field reads and
writes, frame storage for value slots, method-set resolution (there
are no methods to find), and json encoding. Field types resolve
through the same typeref grammar a `var` statement uses, so a
declared struct can hold any type the registry knows, including
another declared struct and channels. With tag syntax admitted, the
json output is fully shaped:

```go
type Reply struct {
	Status string `json:"status"`
	Count  int64  `json:"count"`
}
```

## What it costs the design

**The grammar gains a block form.** `type name struct { ... }` is
the first multi-line declaration: brace tracking, one field per
line, optional tags (a raw-string token the lexer does not have).
The cost is bounded in a way control-flow blocks are not - fields,
not statements, with no nesting beyond the braces - but the
line-oriented parser still grows its first block.

**Declared types have no name at the reflect layer.** `StructOf`
returns an unnamed type: `%T` prints
`struct { X int64; Y int64 }`, and so would every compile and
execution error that mentions the type. The compiler can alias the
declared name in its own registry and error text, but the name
never reaches reflect, a host-side type switch, or a stack trace.
The type system today is `reflect.Type` end to end; declarations
would split naming into a layer reflect cannot see.

**The type universe grows monotonically.** Go runtime types are
never collected. `StructOf` canonicalizes - identical shapes return
the identical type - and recompiling the same program costs nothing,
but every distinct shape a program mints stays in the process for
its lifetime. The binding set stops being the whole sandbox
boundary: today a program cannot allocate anything the host did not
provide a constructor for; with declarations, program text controls
permanent process state. A host compiling untrusted sources would need a
shape budget the runtime cannot enforce for it.

**Declared types are records, not objects.** A method needs a func
body, which is [closures.md](closures.md) territory, and
`StructOf` does not generate wrapper methods for embedded fields,
so embedding does not promote behaviour either. Declared structs
carry data between bindings and into encoders; anything with
behaviour stays a host type. This is a hard boundary, not a
first-version gap.

**Structural assignability cuts both ways.** An unnamed struct with
the same fields is assignable to a named host parameter, so a
program can build a value for a binding without the host exporting
a constructor. The capability is not new - the registry already
lets a program write `T{}` for any discovered host type - but
declarations widen it from types the host mentioned to any shape
the program can spell.

## Mandatory overheads

- Compile time: one `StructOf` per declaration, paid once per
  source string and amortized by the compile cache; canonicalization
  makes recompiles free.
- Execution: none beyond what composite literals already pay. A
  declared struct is a value like any registry struct: one fresh
  allocation per literal evaluation, frame-resident when held by
  value, the existing single-field offset rule on the direct tier,
  the interface alias or copy when passed. `layoutOf` and the shape
  table need no changes.
- Memory: the permanent type, its field metadata and GC program,
  per distinct shape per process lifetime. This is the one overhead
  that scales with program text rather than with execution.

## Alternatives

**Host-declared types, discovery, BindType.** Today's answer: the
host declares the shape, binds anything that mentions it, and the
program uses it. Cons:

- A new shape means recompiling the host. The fixture story loses
  half its claim: a new test needs a rebuild not only for an
  unbound API but for an unshaped output.
- The host must anticipate every DTO its programs want to emit.

**Maps for ad-hoc shapes.** A `map[string]string` or
`map[string]any` built by bindings covers unstructured output.
Cons: no static field checking, no field order in json, every value
boxed, and the map kinds have no composite-literal syntax in the
grammar either.

**A type-construction binding.** `mkstruct("X", "int64", ...)`
cannot work: the compiler needs the type while the program compiles,
and a binding runs after.

## In other imperative settings

The realistic use is output shaping. A Go `http.Handler` declares its
response struct next to the handler and hands it to `json.Encode`;
that is the one imperative pattern the current language cannot
mirror without host help. Tests do not need it: the fixture suite
asserts against stdlib types and formatted strings, and has never
declared a type. Shell, the language's closest precedent, has no
type declarations at all and shapes its records as text; jq sits on
the other side, shaping JSON with literals rather than
declarations, which is closer to what a maps-based alternative
would feel like.

## Verdict

Struct declarations are implementable with stdlib machinery and add
no per-run cost over the composite literals the language already
has. The real costs are structural: a block form in a line-oriented
grammar, names reflect can never see, and a type universe that
grows with program text for the life of the process - the first
resource the binding set would not control. Methods stay out
regardless, so the feature is records for output shaping and
nothing more. Until that pressure outweighs a host-side type and a
`BindType` call, host-declared types remain the recorded position.

## Status

Superseded 2026-09-10. The verdict above declined struct type declarations for the
statement-only language; the package extension adopts them by owner
decision, and the adopted form is in [packages.md](../packages.md).
The cost analysis above still binds the implementation.
