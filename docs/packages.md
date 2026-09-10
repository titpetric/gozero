---
title: Packages, functions and hot loading
date: "2026-09-10T20:00:00+02:00"
---

The Go-subset surface adopted on 2026-09-10, extending the statement language with declarations, expressions and control flow. The five research chapters in [design/](design/) declined each feature for the statement-only language; their verdicts are superseded by owner decision and each carries a dated status note. This chapter is the decision record for the adopted form. The statement language is unchanged: every headerless snippet compiles as it did.

## Files and snippets

A source starting with a package clause is a file; anything else is a snippet. A file holds declarations only: imports, type declarations, and functions, with one `func init()` per file run at load. There is no `const` and no top-level `var`. A snippet holds statements, and may declare types and functions before them.

## Packages and imports

Bindings register under Go import paths and programs reach them the way Go does:

```go
rt.BindPackage("net/http", map[string]any{"Error": http.Error})
rt.BindPackageType("net/http", "http.Handler", (*http.Handler)(nil))
rt.Import("net/http") // activates http.* for headerless snippets
```

A file resolves calls and types only through its `import ( ... )` block, aliases honoured. An import of an unregistered path fails the load naming the path; a selector the import does not export fails compile as Go spells it: `compile: undefined: http.NoSuchFunc`. Type resolution in a file sees the imports, the program's own declarations and the predeclared names; the shared registry serves snippets only.

The compile cache is generation-checked: every Bind, BindType, BindPackage, BindAdapter and Import advances a counter, and a cached program compiled under an older generation recompiles on its next Compile. Invalidate() reclaims the cache's memory under repeated reloads.

## Type declarations

`type X struct{...}` builds once per compilation with reflect.StructOf: field tags pass through verbatim so `json:"..."` shapes encoders, embedded fields carry data, and declarations resolve in dependency order, so order in the source never matters. The reflect ceilings are compile errors: a struct cannot contain itself even through a pointer, fields must be exported, and a type with methods cannot be embedded. `type I interface{...}` is a compiler-nominal method set; reflect cannot mint interface types, so values of it travel as `any` and satisfaction is checked structurally.

## Expressions and control flow

All of Go's operators at Go's precedence, short-circuit `&&` and `||`, unary `+ - ! ^`, indexing over slices, arrays, strings and maps, and `len`. Operands of a binary operator need identical static types; a literal side adopts the other's when representable; an all-literal expression folds at compile time, and constant division by zero is the compile error it is in Go. The evaluator closures use Go's own operators, so wraparound, the most-negative-over-minus-one edge, NaN and float32 per-operation rounding are native by construction; expr_oracle_test.go pins them against the same expressions compiled in Go.

`if`/`else if`/`else` with an optional init clause, the three `for` forms, `range` over slices, arrays, strings (by rune) and maps, `break`, `continue`, `defer`, and `i++`/`i--`, which desugar in the parser to the assignments the compiler already knows. Blocks scope names as Go does: `:=` shadows with a fresh slot, `=` assigns through the chain. Loops are bounded by the execution context: a cancelled run does not spin.

## The hybrid error rule

Naming one more value than a call returns binds the trailing error as an ordinary value with static type `error`:

```go
req, err := http.NewRequest("bad method", "/")
if err != nil {
	return -1
}
```

Eliding it keeps the implicit check-and-abort the statement language always had. Both spellings are legal Go. The blank identifier discards either side, and a call whose result fills an error-typed position keeps its trailing error as the value, which is how errors.New flows through a return list.

## Functions, methods and closures

`func` declarations with value and pointer receivers on the program's own types, func literals, multi-value returns, and calls of func-typed values. Each function compiles to its own unit with its own slot space, so calls and recursion are re-entrant and a bridged function is safe to call concurrently. Capture is by cell: a captured variable's storage becomes a write-through cell shared by the closure and its encloser, which is Go's one-variable rule, per-iteration range values included. `defer` inside a body runs at that function's exit, arguments evaluated at the defer site, and also during panic unwinding. A body's statements see parameters and captures only; the stack map does not cross a function boundary.

A conversion to a named func type, `http.HandlerFunc(func(w, r) { ... })`, materializes the literal as that type, method set included.

## Loading and bridging

```go
p, err := rt.LoadFile("plugins/auth.go") // or rt.Load(src)
p, err = rt.LoadDir("plugins/auth")      // *.go, one package, filename order
v, err := p.Call[int64]("Answer")        // (T, error), as Eval and Exec
mw, err := p.FuncOf[func(http.Handler) http.Handler]("Middleware")
```

Load compiles and then runs each `func init()` once in order. LoadDir merges a folder the way the Go build merges a package. A Program is owned by the caller and never cached, so a reload is a new Load while the old Program stays valid for its holders. FuncOf materializes a declared function as any matching Go func type through reflect.MakeFunc; Signature, FuncValue and Funcs are the dynamic surface the [plugin loader](plugin.md) dispatches through.

## Host-interface adapters

reflect cannot attach methods to a runtime-built type, so a script type satisfies a Go interface through one struct the host declares per interface and registers once:

```go
type HandlerAdapter struct {
	ServeHTTPFunc func(http.ResponseWriter, *http.Request)
}

func (a *HandlerAdapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.ServeHTTPFunc(w, r)
}

rt.BindAdapter[http.Handler]((*HandlerAdapter)(nil))
```

Where a script value fills an interface parameter, the compiler checks the script method set covers the interface, honours the pointer-receiver rule, and passes a fresh adapter whose func fields trampoline into the script methods. An unregistered interface is a compile error naming BindAdapter.

## Tiers

The new constructs run on the reflect evaluator. The step JIT declines each with a named reason Supports reports: operator expressions, control flow, script calls and error-binding calls are not in the shape table yet. Lowering them to direct nodes is the open perf edge; the semantics above are the contract that lowering must match.
