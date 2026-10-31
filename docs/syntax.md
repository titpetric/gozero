---
title: Language syntax
date: "2026-09-09T00:00:00+02:00"
---

gozero is a subset of Go's statement syntax: every gozero snippet in
this document is legal Go, minus the declarations Go would need
around it. What the language removes it replaces with an implicit
rule, so the same behaviour runs a line shorter than the Go it
mirrors in the common case. Each section pairs the two side by side,
Go on the left; the gozero fences carry the Go tag, because gozero is Go. How the compiled form
runs is [DESIGN.md](DESIGN.md); this document is the surface.

The snippets run against the fixture bindings: `http.NewRequest`,
`url.Parse`, `json.NewEncoder`, `fmt.Sprintf`, `assert.Equal` and
friends, with the test's `testing.TB` on the stack as `tb`.

## The grammar

```
program := { stmt }
stmt    := "var" name typeref term
         | "return" [ arg ] term
         | "*" path "=" rhs term
         | [ name { "," name } ( ":=" | "=" ) ] rhs term
term    := ";" | EOL | EOF
rhs     := expr | string | number | "true" | "false" | "nil" | composite | slicelit | addr | deref
typeref := { "*" | "[]" } path
expr    := path "(" [ args ] ")" { "." ident "(" [ args ] ")" }
path    := ident { "." ident }
args    := arg { "," arg }
arg     := string | number | path | expr | composite | slicelit | addr | deref | path "..."
addr    := "&" path
deref   := "*" path
composite := [ "&" ] path "{" [ elem { "," elem } [ "," ] ] "}"
slicelit  := "[]" typeref "{" [ arg { "," arg } [ "," ] ] "}"
elem    := [ ident ":" ] arg
```

The two reference operators are the only operators. A value is a
literal, a name, a field read, a composite literal, an address, a
pointer read, or the result of a call; a condition, a loop or an
arithmetic expression is a Go function the host binds
([design/](design/) records why). The end of
a line closes a statement; the semicolon is a delimiter between
statements sharing one, so both spellings below are the same
program:

```
u := url.Parse("https://example.com"); assert.Equal(tb, "https", u.Scheme)
```

```go
u := url.Parse("https://example.com")
assert.Equal(tb, "https", u.Scheme)
```

Strings are single- or double-quoted. A number without a decimal
point is an int64 and one with is a float64, until an assignment or a
parameter sets another type. The language reserves `dest`, `true`,
`false`, `nil`, `var` and `return`, and a name cannot shadow a binding:
`url := ...` with `url.Parse` bound is a compile error, because no
statement could read the name back.

## Declarations and assignment

The rule is Go's. `:=` declares a new name, `var` declares one with
an explicit type, and `=` assigns to a name one of those declared. A
plain `=` on an undeclared name is a compile error
(`x is not defined, use := or var`), and a `:=` that declares
nothing new is too (`no new variables on left side of :=`). The
initial prototype let a literal define its name with `=`; the
language drops that form, so a typo in an assignment target is an
error and never a silent second name.

<table>
<tr>
<th>go</th>
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
</tr>
<tr>
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

A trailing error result is never a value. The compiler strips it from
the result list and the program checks it after every call; a non-nil
error ends the program and comes back from `Exec` or `Scan`. No
program text mentions an error, which removes the three lines Go
spends on each one:

<table>
<tr>
<th>go</th>
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
</tr>
<tr>
<th>gozero</th>
</tr>
<tr>
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
pass receives the execution context, so cancellation and
deadlines flow from `ExecContext` into the bindings without the
program mentioning them. A written argument that is statically a
context overrides the fill, so a program can pass one it made itself.

<table>
<tr>
<th>go</th>
</tr>
<tr>
<td>

```go
req, err := http.NewRequestWithContext(ctx, "GET", "https://example.com/a/b", nil)
assert.NoError(tb, err)
```

</td>
</tr>
<tr>
<th>gozero</th>
</tr>
<tr>
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
how a program calls `http.NewRequest` without its body, and how a message
parameter on an assert binding is optional without being variadic:

<table>
<tr>
<th>go</th>
</tr>
<tr>
<td>

```go
assert.Equal(tb, "GET", req.Method, "")
assert.Equal(tb, "a/b/c", joined, "path.Join over spread fields")
```

</td>
</tr>
<tr>
<th>gozero</th>
</tr>
<tr>
<td>

```go
assert.Equal(tb, "GET", req.Method)
assert.Equal(tb, "a/b/c", joined, "path.Join over spread fields")
```

</td>
</tr>
</table>

## Fields and methods

A dotted path after a name is a field read, a field write, or a
method call, resolved against the name's static type at compile
time; an unknown selector is a compile error. Methods follow Go's
method sets in full: value receivers, pointer receivers, and methods
promoted from embedded types, on both value- and pointer-typed
names. A pointer-receiver method on a value needs the value's
address, and a name or a field of one has an address the way a Go
variable does; only the direct result of a call does not, so
`f().Bump()` is a compile error where `o := f(); o.Bump()` mutates
`o`. Reads chain through pointers, and writes require an addressable
base - a pointer, or a value declared with `var` or built by a
composite literal:

<table>
<tr>
<th>go</th>
</tr>
<tr>
<td>

```go
assert.Equal(tb, "/a/b", req.URL.Path)
req.Method = "POST"
u.Path = "/rewritten"
```

</td>
</tr>
<tr>
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
</tr>
</table>

## Composite literals

`T{}` and `&T{}` build structs, keyed or positional, nested, with
calls as elements and a trailing comma allowed, as in Go. Every run
builds the value fresh. The value is addressable, so its fields assign
afterwards. The type name is any type discovery registered
(`testdata/structs.txt`):

<table>
<tr>
<th>go</th>
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
</tr>
<tr>
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
</tr>
<tr>
<td>

```go
s := fmt.Sprintf("n=%d ok=%v", n, true)
parts := strings.Fields("a b c")
joined := path.Join(parts...)
```

</td>
</tr>
<tr>
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
</tr>
</table>

## Values and references

`Bind` takes a func or a value. A func is called; anything else is a
value, read in argument position like any other name and carrying
the static type it was bound with:

```go
rt.Bind("url.Parse", url.Parse)   // a func
rt.Bind("time.Hour", time.Hour)   // a value
rt.Bind("io.EOF", io.EOF)         // a value
rt.Bind("os.Args", &os.Args)      // an address
```

A value binding is also a receiver, with Go's method sets in full:
it owns the longest dotted prefix of a path the way a func binding
does, so `Bind("u", u)` with a `*url.URL` makes `u.Path` and
`u.String()` both compile, and a pointer-receiver method is callable
on a bound value because a bound name is addressable. The address is
the per-run cell, so the mutation is the program's.

A callee can be a value too: a func-typed name, a func-typed field,
or a func bound under a name.

```go
f := getFn()
s := f()
t := cfg.Fn()
```

Whether a program reaches the host is Go's rule, and the `&` at the
`Bind` call site is the whole of the opt-in. A value is copied in,
so the name is the program's own for the run; an address makes the
name a `*T`, and the program writes through it:

<table>
<tr>
<th>go</th>
</tr>
<tr>
<td>

```go
frozen := []string{"kept"}
frozen = []string{"replaced"}
frozen = append(frozen, "added")

os.Args = []string{"1", "2", "3"}
sort.Strings(os.Args)
```

</td>
</tr>
<tr>
<th>gozero</th>
</tr>
<tr>
<td>

```go
// Bind("frozen", []string{"kept"})
frozen = []string{"replaced"}
append(&frozen, "added")

// Bind("os.Args", &os.Args)
*os.Args = []string{"1", "2", "3"}
sort.Strings(*os.Args)
```

</td>
</tr>
</table>

The copy is per run: the next run starts from the bound value again,
and two concurrent `Exec`s cannot see each other. One cell per name
per program, so a write, an `&` and every read reach the same
storage. The shallow copy is Go's, so a slice or a map bound by
value still shares its elements with the host.

`&` and `*` are the only operators besides the channel arrow.
`&name` takes the address of a program name, a field of one, or a
value binding; `*p` reads through a pointer and `*p = v` writes
through one. A value never fills a `*T` parameter and a pointer
never fills a `T` one:

```go
p := ptrTo(7)
n := *p
*p = 8
```

Nothing auto-references an argument. The one address the compiler
takes implicitly is the receiver of a pointer-method call, which is
the only place Go takes one too: `o := f(); o.Bump()` addresses `o`,
and `Mutate(o)` against a `*T` parameter is a compile error until it
is written `Mutate(&o)`.

## Slice literals

`[]T{a, b}` builds a slice per evaluation, the way a Go composite
literal allocates each time the expression runs. It stands anywhere
a value stands - an argument, a name, the right of an assignment -
and each element is checked against `T` the way an argument of that
type is:

```go
xs := []string{"a", "b"}
assert.Equal(tb, "a/b", path.Join(xs...))
*os.Args = []string{"1", "2", "3"}
```

Elements are unkeyed: an index key would need the compiler to size
the slice, and nothing in the language needs one. The type is
spelled the way a `var` statement spells one, so it must name a type
discovery registered.

The program in [`testdata/vars.txt`](../testdata/vars.txt) runs the
whole surface.

## The stack and dest

Names the program never binds resolve against the stack map the host
passes to `Exec`; an unset or nil entry reads as the parameter's zero
value, and the program type-checks a set entry on read, because its
type exists only then. `dest` names the pointer passed to `Scan`, so
a program writes its output into a variable the host
owns:

<table>
<tr>
<th>go</th>
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
</tr>
<tr>
<th>gozero</th>
</tr>
<tr>
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
</tr>
<tr>
<th>gozero</th>
</tr>
<tr>
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
the signature and passes the writer and request in on the stack:

<table>
<tr>
<th>go</th>
</tr>
<tr>
<td>

```go
mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(status())
})
```

</td>
</tr>
<tr>
<th>gozero</th>
</tr>
<tr>
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

What the syntax leaves out - conditionals, loops, closures, operator
expressions and struct type declarations - and what each would cost
the design has its research, feature by feature, in [design/](design/): [conditions](design/conditions.md),
[loops](design/loops.md), [closures](design/closures.md),
[expressions](design/expressions.md),
[structs](design/structs.md). Channel receive and send started
there, spent a month in the syntax and came back out; a channel is
still a value a binding produces and a program passes on.
[channels](design/channels.md) records the research and the
[changelog](changelog.md) the removal.
