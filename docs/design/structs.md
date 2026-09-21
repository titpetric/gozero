---
title: Struct type declarations
date: "2026-09-21T11:20:00+02:00"
---

Struct declarations moved from research into the syntax on 2026-09-21. Every type a program could name came from the host: the bindings' signatures, the discovery walk behind them, or `BindType`. This document records what landed, what it cost the design, and what stayed out with the reasons the research established. The claims about `reflect.StructOf` are probed, not assumed, and two of them are now pinned by tests: structurally identical calls return the identical canonical type, the type prints as the unnamed struct literal, an unnamed struct is assignable to a named host type with the same fields, and field tags are supported.

## How it looks

```go
type Point struct {
	X int64
	Y int64
}

p := Point{X: 3, Y: 4}
p.X = 5
json.NewEncoder(dest).Encode(p)
```

The machinery was already in the stdlib. The compiler builds the type once per source string with `reflect.StructOf`, registers it in a table scoped to that one compilation, and everything downstream worked unchanged: composite literals, field reads and writes, frame storage for value slots, method-set resolution (there are no methods to find), and json encoding. Field types resolve through the same typeref grammar a `var` statement uses. Fields carry tags, so the json output is fully shaped:

```go
type Reply struct {
	Status string `json:"status"`
	Count  int64  `json:"count"`
}
```

The rules, each rejected at compile time with the rule named:

- Fields are exported, unique and named; an unexported or duplicate field is what `reflect.StructOf` cannot build, so both are errors rather than a misbuild.
- A declaration resolves in dependency order over as many passes as it takes, so a field can name a type declared further down. A type that contains itself, even through a pointer, is an error: `StructOf` cannot build one.
- A field holds a declared type by value only. `*Point`, `[]Point` and `chan Point` do not resolve, because building them needs the element type before the struct that holds it.
- A declared name cannot shadow a registered type or a binding, and a program name cannot shadow a declared type. `type` and `struct` join the reserved words.
- A tag is a raw or double-quoted string on the field's own line, kept as written and part of the type's identity. A single-quoted tag and an unterminated raw string are parse errors naming themselves, and a tag on an embedded field falls under the embedding rejection.
- Field name lists and embedded fields are parse errors: neither has a place in a one-field-per-line grammar yet.
- A declaration is a program-level form. It cannot stand inside an `if` arm or a loop body, which keeps the registry built once, before any statement compiles.

## What it cost the design

**The grammar gained a declaration form.** `type name struct { ... }` is the first block that is not a statement body: brace tracking, one field per line or per semicolon, no nesting beyond the braces. The cost is bounded in a way control-flow blocks are not, fields rather than statements, and the sniff at the top of the parse loop rewinds like every other statement form.

**Declared types have no name at the reflect layer.** `StructOf` returns an unnamed type: `%T` prints `struct { X int64; Y int64 }`, and so does every compile and execution error that mentions the type. The compiler carries the declared name in its own registry and error text, but the name never reaches reflect, a host-side type switch, or a stack trace. The type system is `reflect.Type` end to end; declarations split naming into a layer reflect cannot see.

**The type universe grows monotonically.** Go runtime types are never collected. `StructOf` canonicalizes - identical shapes return the identical type, which `TestTypeDeclCanonical` and `TestTypeDeclTagIdentity` pin - and recompiling the same program costs nothing, but every distinct shape a program mints stays in the process for its lifetime. Tags widen what counts as distinct: the tag is part of the identity, so two declarations differing in one character of one tag mint two permanent types. The binding set is no longer the whole sandbox boundary: before declarations a program could not allocate anything the host did not provide a constructor for, and now program text controls permanent process state. A host compiling untrusted sources needs a shape budget the runtime does not enforce for it.

**Declared types are records, not objects.** A method needs a func body, which is [closures.md](closures.md) territory, and `StructOf` does not generate wrapper methods for embedded fields, so embedding does not promote behaviour either. Declared structs carry data between bindings and into encoders; anything with behaviour stays a host type. This is a hard boundary, not a first-version gap.

**Structural assignability cuts both ways.** An unnamed struct with the same fields is assignable to a named host parameter, so a program can build a value for a binding without the host exporting a constructor. The capability is not new - the registry already lets a program write `T{}` for any discovered host type - but declarations widen it from types the host mentioned to any shape the program can spell.

## Mandatory overheads

- Compile time: one `StructOf` per declaration, paid once per source string and amortized by the compile cache; canonicalization makes recompiles free.
- Execution: none beyond what composite literals already pay. A declared struct is a value like any registry struct: one fresh allocation per literal evaluation, frame-resident when held by value, the interface alias or copy when passed. `layoutOf` and the shape table needed no changes; the field table gained one case, a read through a struct held by value inside another, which is the composition declarations make common and which was previously reflect-only. One shape does pay: a struct slot written more than once cannot alias the frame into an interface argument and a struct has no transport class to copy through, so encoding that slot whole bridges through reflect - 10 allocations and 352 B against 5 and 240 for the same encode of a write-once literal.
- Memory: the permanent type, its field metadata and GC program, per distinct shape per process lifetime. This is the one overhead that scales with program text rather than with execution.

## Alternatives

**Host-declared types, discovery, BindType.** The previous answer, and still the one for anything with behaviour: the host declares the shape, binds anything that mentions it, and the program uses it. Cons:

- A new shape means recompiling the host. The fixture story loses half its claim: a new test needs a rebuild not only for an unbound API but for an unshaped output.
- The host must anticipate every DTO its programs want to emit.

**Maps for ad-hoc shapes.** A `map[string]string` or `map[string]any` built by bindings covers unstructured output. Cons: no static field checking, no field order in json, every value boxed, and the map kinds have no composite-literal syntax in the grammar either.

**A type-construction binding.** `mkstruct("X", "int64", ...)` cannot work: the compiler needs the type while the program compiles, and a binding runs after.

## In other imperative settings

The realistic use is output shaping. A Go `http.Handler` declares its response struct next to the handler and hands it to `json.Encode`; that was the one imperative pattern the language could not mirror without host help, and it is what this feature bought. Tests do not need it: the fixture suite asserts against stdlib types and formatted strings, and has never declared a type. Shell, the language's closest precedent, has no type declarations at all and shapes its records as text; jq sits on the other side, shaping JSON with literals rather than declarations, which is closer to what a maps-based alternative would feel like.

## Verdict

Struct declarations were implementable with stdlib machinery and they add no per-run cost over the composite literals the language already had: the `typedecl` fixture matches its handwritten mirror allocation for allocation. What shipped is records for output shaping and nothing more, and tags finished that job - the `reply` fixture encodes a tag-shaped response byte for byte on the direct tier. Methods stay out, because a method needs a func body; field name lists and embedding stay out as grammar the parser does not have yet; and compound spellings of a declared type stay out until a build order exists for them. The costs the research named all landed as written: a block form in a line-oriented grammar, names reflect can never see, and a type universe that grows with program text for the life of the process, the first resource the binding set does not control.
