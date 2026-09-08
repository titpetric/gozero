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

The design as it stands is in [docs/DESIGN.md](docs/DESIGN.md). The
chapters below are the investigations that got it here, in the order
they happened; each ends with what it taught.

| Content | Date |
|---------|------|
| [Go, call overheads and JIT](docs/overheads.md) | 2026-09-03 |
| [What flatstack solved first](docs/flatstack.md) | 2026-09-05 |
| [Type binding, hydration and discovery](docs/types.md) | 2026-09-05 |
| [Fixture tests, programs evaluated hot](docs/fixtures.md) | 2026-09-06 |
| [Inlining and the vm/native gap](docs/inlining.md) | 2026-09-07 |
| [Design](docs/DESIGN.md) | 2026-09-07 |
