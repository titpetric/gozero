---
title: Language syntax
date: "2026-09-09T00:00:00+02:00"
---

gozero is a subset of Go's statement syntax: every gozero snippet in
this document is legal Go, minus the declarations Go would need
around it. What the language removes it replaces with an implicit
rule, so the same behaviour is usually a line shorter than the Go it
mirrors. Each section pairs the two side by side, Go on the left;
gozero is fenced as Go, because it is one. How the compiled form
runs is [DESIGN.md](DESIGN.md); this document is the surface.

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

```
u := url.Parse("https://example.com"); assert.Equal(tb, "https", u.Scheme)
```

```go
u := url.Parse("https://example.com")
assert.Equal(tb, "https", u.Scheme)
```

Strings are single- or double-quoted. A number without a decimal
point is an int64 and one with is a float64, until an assignment or a
parameter gives it another type. `dest`, `true`, `false`, `nil`,
`var` and `return` are reserved, and a name cannot shadow a binding:
`url := ...` with `url.Parse` bound is a compile error, because the
name could never be read back.

## Declarations and assignment

The rule is Go's. `:=` declares a new name, `var` declares one with
an explicit type, and `=` assigns to a name one of those declared. A
plain `=` on an undeclared name is a compile error
(`x is not defined, use := or var`), and a `:=` that declares
nothing new is too (`no new variables on left side of :=`). The
initial prototype let a literal define its name with `=`; that form
is gone, so a typo in an assignment target is an error instead of a
silent second name.

<table>
<tr>
<th>go</th>
<th>gozero</th>
</tr>
<tr>
<td>

```go
var count int32
count = 41

x := 2.5
y := int32(7)
y = 9 // still int32
```

</td>
<td>

```go
var count int32
count = 41

x := 2.5
y := int32(7)
y = 9 // still int32
```

</td>
</tr>
</table>

The two columns are identical: declaration syntax is a part of Go
the language keeps whole. `y := int32(7)` is a conversion hint, the
short form of `var y int32; y = 7`: the named type fixes the
literal, later assignments convert to it, and use does not override
it. A literal declared without a hint takes its type from the first
binding parameter the program passes it to, falling back to the
parsed width; that inference is the depth [types.md](types.md)
covers.

## Implicit error checks

A trailing error result is never a value. It is stripped from the
result list at compile time and checked after every call; a non-nil
error ends the program and comes back from `Exec` or `Scan`. No
program text mentions an error, which removes the three lines Go
spends on each one:

<table>
<tr>
<th>go</th>
<th>gozero</th>
</tr>
<tr>
<td>

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

</td>
<td>

```go
req := http.NewRequest("GET", "https://example.com/a/b")
u := url.Parse("https://example.com/p/q?x=1")
```

</td>
</tr>
</table>

The program still checks: it stops at the first failing call, the
way a Go function returns at its first `if err != nil`. This is the
language's only conditional.

## Implicit context passing

A binding parameter of type `context.Context` the program does not
pass is filled with the execution context, so cancellation and
deadlines flow from `ExecContext` into the bindings without the
program mentioning them. A written argument that is statically a
context overrides the fill, which is how a program passes one it
made on purpose.

<table>
<tr>
<th>go</th>
<th>gozero</th>
</tr>
<tr>
<td>

```go
req, err := http.NewRequestWithContext(ctx, "GET", "https://example.com/a/b", nil)
assert.NoError(tb, err)
```

</td>
<td>

```go
req := http.NewRequestWithContext("GET", "https://example.com/a/b")
```

</td>
</tr>
</table>

The fixture proves the context arrives: `req.Context()` inside the
program observes the value the test attached before calling
`ExecContext` (`testdata/http.txt`).

## Optional trailing arguments

Every trailing argument is optional and zero-fills: `""` for a
string, `nil` for a pointer or interface, zero for a scalar. That is
how `http.NewRequest` is called without its body, and how a message
parameter on an assert binding is optional without being variadic:

<table>
<tr>
<th>go</th>
<th>gozero</th>
</tr>
<tr>
<td>

```go
assert.Equal(tb, "GET", req.Method, "")
assert.Equal(tb, "a/b/c", joined, "path.Join over spread fields")
```

</td>
<td>

```go
assert.Equal(tb, "GET", req.Method)
assert.Equal(tb, "a/b/c", joined, "path.Join over spread fields")
```

</td>
</tr>
</table>

## Field reads and writes

A dotted path after a name is a field read or a field write,
resolved against the name's static type at compile time; an unknown
field is a compile error. Reads chain through pointers, and writes
require an addressable base - a pointer, or a value declared with
`var` or built by a composite literal:

<table>
<tr>
<th>go</th>
<th>gozero</th>
</tr>
<tr>
<td>

```go
assert.Equal(tb, "/a/b", req.URL.Path)
req.Method = "POST"
u.Path = "/rewritten"
```

</td>
<td>

```go
assert.Equal(tb, "/a/b", req.URL.Path)
req.Method = "POST"
u.Path = "/rewritten"
```

</td>
</tr>
</table>

## Composite literals

`T{}` and `&T{}` build structs, keyed or positional, nested, with
calls as elements and a trailing comma allowed, as in Go. The value
is built fresh on every run and is addressable, so its fields assign
afterwards. The type name is any type discovery registered
(`testdata/structs.txt`):

<table>
<tr>
<th>go</th>
<th>gozero</th>
</tr>
<tr>
<td>

```go
u := url.URL{Scheme: "https", Host: "example.com", Path: "/a"}
p := &url.URL{Scheme: "https", Host: "h", Path: "/p"}
r := &http.Request{Method: "POST", URL: &url.URL{Path: "/n"}, ProtoMajor: 1}
w := url.URL{}
w.Path = "/w"
```

</td>
<td>

```go
u := url.URL{Scheme: "https", Host: "example.com", Path: "/a"}
p := &url.URL{Scheme: "https", Host: "h", Path: "/p"}
r := &http.Request{Method: "POST", URL: &url.URL{Path: "/n"}, ProtoMajor: 1}
w := url.URL{}
w.Path = "/w"
```

</td>
</tr>
</table>

Identical columns again. A numeric element converts to its field's
width, so `ProtoMajor: 1` is an int without a hint.

## Variadic calls

A variadic parameter packs its arguments (`f(a, b, c)`) or takes a
spread slice (`f(xs...)`), both as in Go (`testdata/variadic.txt`,
`testdata/fmt.txt`):

<table>
<tr>
<th>go</th>
<th>gozero</th>
</tr>
<tr>
<td>

```go
s := fmt.Sprintf("n=%d ok=%v", n, true)
parts := strings.Fields("a b c")
joined := path.Join(parts...)
```

</td>
<td>

```go
s := fmt.Sprintf("n=%d ok=%v", n, true)
parts := strings.Fields("a b c")
joined := path.Join(parts...)
```

</td>
</tr>
</table>

## The stack and dest

Names the program never binds resolve against the stack map the host
passes to `Exec`; an unset or nil entry reads as the parameter's zero
value, and a set entry is type-checked when it is read, because only
then is its type known. `dest` is reserved for the pointer `Scan`
was handed, so a program writes its output into a variable the host
owns:

<table>
<tr>
<th>go</th>
<th>gozero</th>
</tr>
<tr>
<td>

```go
var buf bytes.Buffer
if err := json.NewEncoder(&buf).Encode(req.Cookies()); err != nil {
	return err
}
```

</td>
<td>

```go
json.NewEncoder(dest).Encode(req.Cookies())
```

</td>
</tr>
</table>

```go
err := fn.Scan(&buf, map[string]any{"req": req})
```

## Imperative programs in practice

The fixture suite is the reference for the style. A Go test and the
fixture that replaces it:

<table>
<tr>
<th>go</th>
<th>gozero</th>
</tr>
<tr>
<td>

```go
req, err := http.NewRequest("GET", "https://example.com/a/b", nil)
assert.NoError(tb, err)
assert.Equal(tb, "GET", req.Method)
req.Method = "POST"
assert.Equal(tb, "POST", req.Method)
```

</td>
<td>

```go
req := http.NewRequest("GET", "https://example.com/a/b")
assert.Equal(tb, "GET", req.Method)
req.Method = "POST"
assert.Equal(tb, "POST", req.Method)
```

</td>
</tr>
</table>

The same shape carries to an `http.Handler` body, which is a straight
line of build, encode, write; the host adapts the compiled program to
the signature and hands the writer and request in on the stack:

<table>
<tr>
<th>go</th>
<th>gozero</th>
</tr>
<tr>
<td>

```go
mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(status())
})
```

</td>
<td>

```go
json.NewEncoder(w).Encode(status())
```

</td>
</tr>
</table>

Middleware-style guarding works through the error contract: a bound
`auth.Require(w, r)` that writes the 401 and returns an error ends
the program before the next statement, the way `set -e` ends a shell
script.

What the syntax deliberately leaves out - conditionals, loops,
closures, operator expressions and channels - and what each would
cost the design is researched feature by feature in
[design/](design/): [conditions](design/conditions.md),
[loops](design/loops.md), [closures](design/closures.md),
[expressions](design/expressions.md),
[channels](design/channels.md).
