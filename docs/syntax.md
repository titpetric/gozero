---
title: Language syntax
date: "2026-09-09T00:00:00+02:00"
---

gozero is a subset of Go's statement syntax: every gozero snippet in
this document is legal Go, minus the declarations Go would need
around it. What the language removes it replaces with an implicit
rule, so the same behaviour is usually a line shorter than the Go it
mirrors. Each section below shows the two side by side, Go first.
How the compiled form runs is [DESIGN.md](DESIGN.md); this document
is the surface.

The snippets run against the fixture bindings: `http.NewRequest`,
`url.Parse`, `json.NewEncoder`, `fmt.Sprintf`, `assert.Equal` and
friends, with the test's `testing.TB` on the stack as `tb`.

## The grammar

```
program := { stmt }
stmt    := "var" name typeref term
         | "return" [ arg ] term
         | [ name { "," name } ( ":=" | "=" ) ] rhs term
term    := ";" | EOL | EOF
rhs     := expr | string | number | "true" | "false" | "nil" | composite
typeref := { "*" | "[]" } path
expr    := path "(" [ args ] ")" { "." ident "(" [ args ] ")" }
path    := ident { "." ident }
args    := arg { "," arg }
arg     := string | number | path | expr | composite | path "..."
composite := [ "&" ] path "{" [ elem { "," elem } [ "," ] ] "}"
elem    := [ ident ":" ] arg
```

There are no operators. A value is a literal, a name, a field read,
a composite literal, or the result of a call; a condition, a loop or
an arithmetic expression is a Go function the host binds
([design/](design/) records why). The end of a line closes a
statement; the semicolon is a delimiter between statements sharing
one, so both spellings below are the same program:

```gozero
u := url.Parse("https://example.com"); assert.Equal(tb, "https", u.Scheme)
```

```gozero
u := url.Parse("https://example.com")
assert.Equal(tb, "https", u.Scheme)
```

Strings are single- or double-quoted. A number without a decimal
point is an int64 and one with is a float64, until an assignment or a
parameter gives it another type. `dest`, `true`, `false`, `nil`,
`var` and `return` are reserved, and a name cannot shadow a binding:
`url := ...` with `url.Parse` bound is a compile error, because the
name could never be read back.

## Implicit error checks

A trailing error result is never a value. It is stripped from the
result list at compile time and checked after every call; a non-nil
error ends the program and comes back from `Exec` or `Scan`. No
program text mentions an error, which removes the three lines Go
spends on each one:

```go
req, err := http.NewRequest("GET", "https://example.com/a/b", nil)
if err != nil {
	return err
}
u, err := url.Parse("https://example.com/p/q?x=1")
if err != nil {
	return err
}
```

```gozero
req := http.NewRequest("GET", "https://example.com/a/b")
u := url.Parse("https://example.com/p/q?x=1")
```

The program still checks: it stops at the first failing call, the
way a Go function returns at its first `if err != nil`. This is the
language's only conditional.

## Implicit context passing

A binding parameter of type `context.Context` the program does not
pass is filled with the execution context, so cancellation and
deadlines flow from `ExecContext` into the bindings without the
program mentioning them:

```go
req, err := http.NewRequestWithContext(ctx, "GET", "https://example.com/a/b", nil)
```

```gozero
req := http.NewRequestWithContext("GET", "https://example.com/a/b")
```

The fixture proves the context arrives: `req.Context()` inside the
program observes the value the test attached before calling
`ExecContext` (`testdata/http.txt`).

## Optional trailing arguments

Every trailing argument is optional and zero-fills: `""` for a
string, `nil` for a pointer or interface, zero for a scalar. That is
how `http.NewRequest` is called without its body, and how a message
parameter on an assert binding is optional without being variadic:

```go
assert.Equal(t, "GET", req.Method, "")
assert.Equal(t, "a/b/c", joined, "path.Join over spread fields")
```

```gozero
assert.Equal(tb, "GET", req.Method)
assert.Equal(tb, "a/b/c", joined, "path.Join over spread fields")
```

## Declarations, inference and conversion hints

`var` puts a named type's zero value in scope; the type spelling is
`reflect.Type.String()`, with `*` and `[]` prefixes. `:=` defines,
`=` assigns, and a plain `=` also defines when the name is new. A
literal assigned to an undeclared name takes its type from the first
binding parameter the program passes it to; failing that it keeps its
parsed width. A conversion hint fixes a literal's type at the
assignment, the way a var declaration would:

```go
var count int32
count = 41
var u url.URL
y := int32(7)
```

```gozero
var count int32
count = 41
var u url.URL
y = int32(7)
```

The hinted type sticks: a later `y = 9` stays an int32. A name
reassigned at a different type is legal on the reflect tier and
rejected by the JIT, so straight programs keep one type per name
(`testdata/types.txt`).

## Field reads and writes

A dotted path after a name is a field read or a field write,
resolved against the name's static type at compile time; an unknown
field is a compile error. Reads chain through pointers, and writes
require an addressable base - a pointer, or a value declared with
`var` or built by a composite literal:

```go
assert.Equal(t, "/a/b", req.URL.Path)
req.Method = "POST"
u.Path = "/rewritten"
```

```gozero
assert.Equal(tb, "/a/b", req.URL.Path)
req.Method = "POST"
u.Path = "/rewritten"
```

## Composite literals

`T{}` and `&T{}` build structs, keyed or positional, nested, with
calls as elements and a trailing comma allowed, as in Go. The value
is built fresh on every run and is addressable, so its fields assign
afterwards. The type name is any type discovery registered
(`testdata/structs.txt`):

```go
u := url.URL{Scheme: "https", Host: "example.com", Path: "/a"}
p := &url.URL{Scheme: "https", Host: "h", Path: "/p"}
r := &http.Request{Method: "POST", URL: &url.URL{Path: "/n"}, ProtoMajor: 1}
w := url.URL{}
w.Path = "/w"
```

```gozero
u := url.URL{Scheme: "https", Host: "example.com", Path: "/a"}
p := &url.URL{Scheme: "https", Host: "h", Path: "/p"}
r := &http.Request{Method: "POST", URL: &url.URL{Path: "/n"}, ProtoMajor: 1}
w := url.URL{}
w.Path = "/w"
```

The two blocks are identical: composite literals are the part of Go
the language keeps whole. A numeric element converts to its field's
width, so `ProtoMajor: 1` is an int without a hint.

## Variadic calls

A variadic parameter packs its arguments (`f(a, b, c)`) or takes a
spread slice (`f(xs...)`), both as in Go (`testdata/variadic.txt`,
`testdata/fmt.txt`):

```go
s := fmt.Sprintf("n=%d ok=%v", n, true)
parts := strings.Fields("a b c")
joined := path.Join(parts...)
```

```gozero
s := fmt.Sprintf("n=%d ok=%v", n, true)
parts := strings.Fields("a b c")
joined := path.Join(parts...)
```

## The stack and dest

Names the program never binds resolve against the stack map the host
passes to `Exec`; an unset or nil entry reads as the parameter's zero
value, and a set entry is type-checked when it is read, because only
then is its type known. `dest` is reserved for the pointer `Scan`
was handed, so a program writes its output into a variable the host
owns:

```go
var buf bytes.Buffer
if err := json.NewEncoder(&buf).Encode(req.Cookies()); err != nil {
	return err
}
```

```gozero
json.NewEncoder(dest).Encode(req.Cookies())
```

```go
err := fn.Scan(&buf, map[string]any{"req": req})
```

## Imperative programs in practice

The fixture suite is the reference for the style. A Go test and the
fixture that replaces it:

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

The same shape carries to an `http.Handler` body, which is a straight
line of build, encode, write; the host adapts the compiled program to
the signature and hands the writer and request in on the stack:

```go
mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(status())
})
```

```gozero
json.NewEncoder(w).Encode(status())
```

Middleware-style guarding works through the error contract: a bound
`auth.Require(w, r)` that writes the 401 and returns an error ends
the program before the next statement, the way `set -e` ends a shell
script.

What the syntax deliberately leaves out - conditionals, loops,
closures and operator expressions - and what each would cost the
design is researched feature by feature in [design/](design/):
[conditions](design/conditions.md), [loops](design/loops.md),
[closures](design/closures.md),
[expressions](design/expressions.md).
