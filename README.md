# gozero

gozero runs imperative programs written in a minimal statement language
against bound Go functions, with type safety taken from the bindings'
own signatures and per-call overhead measured in tens of nanoseconds.
It began as an experiment in call overheads and grew a direct-call JIT,
a reflect fallback, type discovery and a hot fixture test suite.

```go
rt := gozero.NewRuntime()
rt.BindScope("http", map[string]any{"NewRequest": http.NewRequest})
rt.BindScope("json", map[string]any{"NewEncoder": json.NewEncoder})

fn, err := rt.Compile(`
	req := http.NewRequest("GET", "/")
	json.NewEncoder(dest).Encode(req.Cookies())
`)
err = fn.Scan(&dest, nil)
```

## Syntax

A script is the Go you would have written, minus what the runtime
does implicitly: a trailing error result ends the program instead of
being named, a `context.Context` parameter fills from the execution
context, and a trailing argument left out is the zero value. The
full surface is in [docs/syntax.md](docs/syntax.md).

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
<tr>
<td>

```go
u, err := url.Parse("https://example.com/p?x=1")
assert.NoError(tb, err)

u.Path = "/rewritten"
assert.Equal(tb, "https://example.com/rewritten?x=1", u.String())
```

</td>
<td>

```go
u := url.Parse("https://example.com/p?x=1")

u.Path = "/rewritten"
assert.Equal(tb, "https://example.com/rewritten?x=1", u.String())
```

</td>
</tr>
<tr>
<td>

```go
req, err := http.NewRequestWithContext(ctx, "GET", "https://example.com/", nil)
assert.NoError(tb, err)
assert.Equal(tb, "/", req.URL.Path)
```

</td>
<td>

```go
req := http.NewRequestWithContext("GET", "https://example.com/")
assert.Equal(tb, "/", req.URL.Path)
```

</td>
</tr>
</table>

The design as it stands is in [docs/DESIGN.md](docs/DESIGN.md). The
chapters below are the investigations that got it here, in the order
they happened; each ends with what it taught.

| Content                                                   | Date       |
|-----------------------------------------------------------|------------|
| [Go, call overheads and JIT](docs/overheads.md)           | 2026-09-03 |
| [What flatstack solved first](docs/flatstack.md)          | 2026-09-05 |
| [Type binding, hydration and discovery](docs/types.md)    | 2026-09-05 |
| [Fixture tests, programs evaluated hot](docs/fixtures.md) | 2026-09-06 |
| [Inlining and the vm/native gap](docs/inlining.md)        | 2026-09-07 |
| [Design](docs/DESIGN.md)                                  | 2026-09-07 |
| [Changelog](docs/changelog.md)                            | 2026-09-08 |
| [Language syntax](docs/syntax.md)                         | 2026-09-09 |
