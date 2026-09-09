---
title: What if we implemented expressions?
date: "2026-09-09T00:00:00+02:00"
---

Expressions are the extension the other three keep pointing at:
conditions want `==`, loops want `i < n`, and both documents defer
here. This one answers directly. The machinery is the cheap part; the
compromise is that operators give the language a semantic surface of
its own, which is the one thing [DESIGN.md](../DESIGN.md) says it
must never grow.

## How it would look

```gozero
total := price * qty
if status == 200 && attempts < 3 {
	...
}
```

On the JIT tier, operators are unusually cheap. The node types
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

**Surface creep has no stable stopping point.** `==` without `!=` is
absurd; `!=` invites `!`, `&&`, `||` (short-circuit: the first
lazy evaluation in a language that is otherwise strict left-to-right
calls); arithmetic invites `+` on strings, `%`, unary minus,
parentheses, conversions (`int64(x)`), and eventually indexing and
`len`, at which point the language has a stdlib after all. Each step
is individually reasonable; the sum is the surface the imperative
principle exists to refuse. The AST gains an expression tree that
every future feature must handle, which is precisely what the user
of this design does not want carried.

**The error contract gains competition.** Today a program's only
non-call semantics is "an error ends it". Division by zero, shift
overflow and uncomparable `==` add panic sites that belong to the
language, not to a binding; `*PanicError` would start carrying
gozero's own arithmetic faults, and the sandbox claim "a program can
only do what a binding does" quietly weakens.

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

```gozero
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
- The call overhead, tens of nanoseconds, prices arithmetic at
  roughly a thousand times a native add. Fine for a predicate per
  request; wrong for math-heavy programs, which are simply not this
  language's programs.

**expr-lang, cel-go, and their family.** A ready expression grammar
and evaluator. Rejected without ambiguity:

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
drags a type checker into a compiler whose entire identity is that
the bindings are the type checker.

**Generate Go and compile.** yaegi-style interpretation or plugin
builds. Out of scope: the design's premise is no code generation and
no toolchain at runtime.

## In other imperative settings

testify is the proof that expression-as-call reads fine when the
domain is right: `assert.Equal(t, want, got)`,
`require.Less(t, a, b)` - Go tests are full of comparisons nobody
writes as operators, and the fixture suite runs on exactly this.
Shell went the same way for decades: `test $a -eq $b` and `expr 1 + 2` are commands, and `$(( ))` syntax arrived only once shells decided
to become languages - the trajectory this design is choosing not to
start. For handlers and middleware, the arithmetic that occurs is
predicates: status classes, path prefixes, header equality. Those are
one `op.eq` or one `strings.HasPrefix` per site, which the binding
alternative prices correctly. Programs that compute - accumulate,
transform, score - are on the wrong side of this language's line, and
the honest answer is to write them in Go and bind the result.

## Verdict

Operators would be cheap to execute and expensive to mean. The JIT
absorbs them as class-closed combinators; the design does not absorb
owning Go's expression semantics without go/types, nor the surface
creep that follows the first `==`. Operation bindings keep the AST
call-only, keep the types coming from signatures, keep the semantics
in compiled Go, and cover the predicate-shaped uses that conditions
([conditions.md](conditions.md)) and middleware actually have. That
is the recorded position; math-heavy programs are host code.
