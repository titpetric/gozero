---
title: What if we implemented expressions?
date: "2026-09-09T00:00:00+02:00"
---

Expressions are the extension the other three keep pointing at:
conditions want `==`, loops want `i < n`, and both documents defer
here. This one answers directly. The machinery is the cheap part;
the compromise is that operators give the language a semantic
surface of its own, and [DESIGN.md](../DESIGN.md) records that the
language must never grow one.

## How it would look

```go
total := price * qty
if status == 200 && attempts < 3 {
	...
}
```

On the JIT tier, operators are cheap. The node types
already carry every scalar as `uint64` bits (`nodeN`) or `float64`
(`nodeF`), so an operator is a combinator: two `nodeN` in, one
`nodeN` out, a native `+` in the middle. No shape table growth, no
reflect, no new layout classes - the scalar classes are closed under
arithmetic. String concatenation is `nodeS x nodeS -> nodeS`.
Comparisons produce `lBool`, which exists. The reflect evaluator
mirrors each with `reflect.Value` arithmetic. A few dozen small
closures per tier.

## What it costs the design

**The Go spec, reimplemented by hand.** The type system today is the
bindings' own `reflect.Type`s; nothing in the compiler defines what a
type means, only where one is required. Operators change that. The
compiler must now own:

- Untyped constants: `x := 5` currently types from context or
  defaults; `5 + u32Field` requires Go's untyped-constant conversion
  and representability rules, per operator, per width.
- Overflow and truncation across 12 scalar widths, signed and
  unsigned, matching what compiled Go does - the equivalence suite
  can only compare the two tiers against each other, not against the
  Go compiler, so a shared misunderstanding passes.
- Shifts (untyped left operand rules), integer division and its
  panics, float NaN ordering in comparisons, `==` on interfaces
  (panics on uncomparable dynamic types), `==` on structs
  (field-wise, only if comparable), string ordering.
- Precedence and associativity in the parser, which today has no
  expression grammar at all.

This is a re-derivation of go/types and go/constant semantics without
using them, and "no go/types" is a recorded gate. Every divergence is
a bug that looks like a wrong value, not an error.

**Each operator implies the next.** `==` alone is not a stopping
point: `!=`, `!`, `&&` and `||` follow, and short-circuit evaluation
is the first lazy construct in a language that otherwise evaluates
strictly left to right. Arithmetic brings `+` on strings, `%`, unary
minus, parentheses, conversions (`int64(x)`), and eventually
indexing and `len`, at which point the language has a standard
library after all. Each step is individually reasonable; the sum is
the surface the imperative principle refuses. The AST gains an
expression tree that every later feature must handle.

**Panics become language behaviour.** Today a program's only
non-call semantics is "an error ends it". Division by zero, shift
overflow and uncomparable `==` add panic sites that belong to the
language, not to a binding; `*PanicError` would start carrying
gozero's own arithmetic faults, and the claim that a program can
only do what a binding does no longer covers panics.

## Alternatives

**Runtime bindings for operations.** The recorded preference. The
host binds what programs may compute:

```go
rt.BindScope("op", map[string]any{
	"eq":  func(a, b int) bool { return a == b },
	"lt":  func(a, b int) bool { return a < b },
	"add": func(a, b int) int { return a + b },
})
```

```go
n := op.add(subtotal, tax)
ok := op.eq(status, 200)
```

Everything stays inside the existing design: the AST holds calls it
already compiles, signatures type-check the operands at compile time,
scalar shapes make the calls direct, and the semantics are the Go
compiler's because the host wrote Go. Overflow, NaN and comparability
behave exactly as compiled Go, for free. Cons:

- Not idiomatic Go at the use site: `op.add(op.mul(p, q), t)` is
  precedence by nesting. Legal Go, unpleasant arithmetic.
- One binding per type per operation, or a generic `any` version
  that boxes scalars and defers type errors to runtime. Host
  generics (`func Eq[T comparable](a, b T) bool`) do not help - a
  binding is one instantiation.
- A call costs tens of nanoseconds where a native add costs a
  fraction of one, a factor of about a thousand. That bounds the
  pattern to a few predicates per run; a program that computes in a
  loop is outside what the language targets.

**expr-lang, cel-go, and their family.** A ready expression grammar
and evaluator. Rejected:

- Outside the stdlib, which is a hard constraint here.
- Evaluation moves into userland: their own type systems, their own
  reflection paths, `any`-boxed values, no route onto the direct
  tier. The binding-signature type safety that defines this design
  does not survive the boundary crossing.
- Two languages in one source, two error models, two sandboxes.

**go/types and go/constant for compile-time folding only.** Stdlib,
and correct by construction for constant expressions (`x := 3 * 60`
folded at compile, no runtime operators). Cons: crosses the recorded
no-go/types gate; constant-only folding satisfies almost no real
condition, so the pressure for runtime operators remains; and it
adds a type checker to a compiler whose premise is that the bindings
are the type checker.

**Generate Go and compile.** yaegi-style interpretation or plugin
builds. Out of scope: the design's premise is no code generation and
no toolchain at runtime.

## In other imperative settings

testify is the proof that expression-as-call reads fine when the
domain is right: `assert.Equal(t, want, got)`,
`require.Less(t, a, b)` - Go tests are full of comparisons nobody
writes as operators, and the fixture suite runs on exactly this.
Shell went the same way for decades: `test $a -eq $b` and `expr 1 + 2` are commands, and `$(( ))` arithmetic arrived later, as shells
grew into general languages; this design stops before that step. For
handlers and middleware, the arithmetic that occurs is predicates:
status classes, path prefixes, header equality. Those are one
`op.eq` or one `strings.HasPrefix` per site, within the cost bound
above. A program that computes (accumulate, transform, score)
belongs in Go: write the computation as a binding and call it.

## Verdict

The JIT can run operators as class-closed combinators at native
cost. What the design cannot carry is the semantics: Go's expression
rules reimplemented without go/types, and the operator-by-operator
growth that follows the first `==`. Operation bindings keep the AST
call-only, keep the types coming from signatures, keep the semantics
in compiled Go, and cover the predicate-shaped uses that conditions
([conditions.md](conditions.md)) and middleware have. That is the
recorded position; math-heavy programs are host code.

## Status

Superseded 2026-09-10. The verdict above declined operator expressions for the
statement-only language; the package extension adopts them by owner
decision, and the adopted form is in [packages.md](../packages.md).
The cost analysis above still binds the implementation.
