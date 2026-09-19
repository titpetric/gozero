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
         | ifstmt
         | path "<-" arg term
         | [ name { "," name } ( ":=" | "=" ) ] rhs term
ifstmt  := "if" header block [ "else" ( ifstmt | block ) ] term
header  := a Go expression, read by go/parser and narrowed to
           operand [ cmpop operand ]
operand := path | expr | string | number
cmpop   := "==" | "!=" | "<" | "<=" | ">" | ">="
block   := "{" { stmt } "}"
term    := ";" | EOL | EOF
rhs     := expr | string | number | "true" | "false" | "nil" | composite | recv
typeref := { "*" | "[]" | "chan" | "chan<-" | "<-chan" } path
expr    := path "(" [ args ] ")" { "." ident "(" [ args ] ")" }
path    := ident { "." ident }
args    := arg { "," arg }
arg     := string | number | path | expr | composite | recv | path "..."
recv    := "<-" ( path | expr )
composite := [ "&" ] path "{" [ elem { "," elem } [ "," ] ] "}"
elem    := [ ident ":" ] arg
```

The channel arrow and the six comparison operators are the only
operators, and a comparison exists only in an `if` header. A value
is a literal, a name, a field read, a composite literal, a receive,
or the result of a call; a loop or an arithmetic expression is a Go
function the host binds ([design/](design/) records why). The end of
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
parameter gives it another type. `dest`, `true`, `false`, `nil`,
`var`, `return`, `if` and `else` are reserved, and a name cannot
shadow a binding: `url := ...` with `url.Parse` bound is a compile
error, because the name could never be read back.

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

A trailing error result is never a value. It is stripped from the
result list at compile time and checked after every call; a non-nil
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
pass is filled with the execution context, so cancellation and
deadlines flow from `ExecContext` into the bindings without the
program mentioning them. A written argument that is statically a
context overrides the fill, which is how a program passes one it
made on purpose.

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
how `http.NewRequest` is called without its body, and how a message
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
calls as elements and a trailing comma allowed, as in Go. The value
is built fresh on every run and is addressable, so its fields assign
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

## Channels

A channel is a value like any other: made by a binding, held by a
name, passed to calls, read off a field, declared with `var` in the
`chan T`, `<-chan T` and `chan<- T` spellings. Receive and send are
the two operations Go spells with `<-`, and both carry the
language's implicit rules. The `ok` of Go's two-value receive is
implicit the way the trailing error of a call is: a receive from a
closed channel ends the program with `io.EOF`, so the host loops
`Exec` until `errors.Is(err, io.EOF)` and never writes the check.
Both operations are armed with the execution context: a blocked
receive or send ends the program with `ctx.Err()` when the
`ExecContext` deadline passes, which also bounds a nil channel.

<table>
<tr>
<th>go</th>
</tr>
<tr>
<td>

```go
line, ok := <-lines
if !ok {
	return io.EOF
}
out := fmt.Sprintf("got %s", line)
results <- out
```

</td>
</tr>
<tr>
<th>gozero</th>
</tr>
<tr>
<td>

```go
line := <-lines
out := fmt.Sprintf("got %s", line)
results <- out
```

</td>
</tr>
</table>

The peer is the host's: there is no `go` statement, so goroutines,
channel construction and topology stay in Go, and a compiled program
is safe to run from as many goroutines as the host starts. A worker
is the same program run per message:

<table>
<tr>
<th>go</th>
</tr>
<tr>
<td>

```go
for {
	job, ok := <-jobs
	if !ok {
		return
	}
	u, err := url.Parse(job)
	if err != nil {
		return err
	}
	done <- u.Host
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
// the host loops Exec until io.EOF
job := <-jobs
u := url.Parse(job)
done <- u.Host
```

</td>
</tr>
</table>

`select`, `range` over a channel and `go` remain outside the
language; [design/channels.md](design/channels.md) records why they
decompose onto conditions, loops and closures.

## Conditions

`if`, `else if` and `else` run braced statement lists. The condition
is a declared bool name, a bool field path, a call returning bool,
or exactly one comparison: `==`, `!=`, `<`, `<=`, `>` or `>=`
between two operands, each a name, a field path, a call, or a
literal. There is no init clause, no `&&` and no nested comparison
in the header, and the operators do not exist outside it: any other
position rejects them at parse time with the rule named (comparison
placement). [design/conditions.md](design/conditions.md) records
what the fuller forms cost.

The header is read by Go's own expression parser: the bytes between
`if` and the `{` that opens the block go to `go/parser.ParseExpr`,
and the tree it returns is narrowed to the forms above, each
rejection naming its rule (composition for `&&`, `||` and `!`,
arithmetic for the operator expressions). Because Go reads the
header, a header accepts Go spellings the line grammar does not:
parenthesized conditions, hex and binary and underscore-grouped
numbers, raw and fully escaped strings. A rune literal is rejected -
the language's only character data is a string - and the header and
its `{` share a line, though a parenthesized argument list may break
lines the way Go allows.

<table>
<tr>
<th>go</th>
</tr>
<tr>
<td>

```go
ok := strings.HasPrefix(req.URL.Path, "/api")
route := ""
if ok {
	route = "api"
} else if req.Close {
	route = "closed"
} else {
	route = "static"
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
ok := strings.HasPrefix(req.URL.Path, "/api")
route := ""
if ok {
	route = "api"
} else if req.Close {
	route = "closed"
} else {
	route = "static"
}
```

</td>
</tr>
</table>

A comparison types in one line: both sides carry identical static
types, and a literal side adopts the other side's type. The
comparison itself runs on the underlying kind - integers, floats and
strings compare and order, bool compares with `==` and `!=` only -
so a named scalar type works the way its kind does, which is what
lets a `time.Duration` order against `time.Hour`:

<table>
<tr>
<th>go</th>
</tr>
<tr>
<td>

```go
t := time.Now()
age := "stale"
if time.Since(t) < time.Hour {
	age = "fresh"
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
t := time.Now()
age := "stale"
if time.Since(t) < time.Hour {
	age = "fresh"
}
```

</td>
</tr>
</table>

`time.Hour` reaches the program through a value binding:
`BindValue("time.Hour", time.Hour)` registers a typed value under a
dotted name, which then reads as a comparison operand or as an
argument the way a Go program reads a package constant. Everything
outside the scalar kinds stays out of the rung and rejects with the
rule named (comparable scalar): pointers, interfaces, structs and
`nil` do not compare, mixed static types do not compare (identical
types), and two literal sides are a constant condition (constant
comparison).

Two rules keep the arms inside the language's contracts, and both
reject at compile time with the rule named. Flat scope: `var` and
`:=` cannot appear inside an arm, because the slot model has no
block scope and a name declared there would stay visible past the
brace; declare the name before the `if` and assign with `=`. Single
exit: `return` cannot appear inside an arm, because a compiled
program runs front to back and its only early exit is an error;
return after the join. `else` binds on the closing brace's line, as
gofmt shapes it, and `if` nests inside arms.

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
the signature and hands the writer and request in on the stack:

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

What the syntax deliberately leaves out - loops, closures, operator
expressions and struct type declarations - and what each would cost
the design is researched feature by feature in [design/](design/):
[conditions](design/conditions.md), [loops](design/loops.md),
[closures](design/closures.md),
[expressions](design/expressions.md),
[structs](design/structs.md). Channel receive and send started
there and moved into the syntax, and the restricted `if` above did
the same; [channels](design/channels.md) and
[conditions](design/conditions.md) record what landed and what
stayed out.
