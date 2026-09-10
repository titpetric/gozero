---
title: The plugin package
date: "2026-09-10T20:30:00+02:00"
---

`github.com/titpetric/gozero/plugin` mirrors the standard library's
plugin API over hot-compiled Go source: no build step, no cgo, no
process restart.

```go
rt := gozero.NewRuntime()
rt.BindPackage("strings", map[string]any{"CutPrefix": strings.CutPrefix})

l := plugin.NewLoader(rt)
p, err := l.Open("plugins/auth")   // a .go file or a package folder
sym, err := p.Lookup("Middleware") // stdlib-shaped
mw, err := plugin.Func[func(http.Handler) http.Handler](p, "Middleware")
```

The package-level Open uses Default, a loader over its own Runtime;
a host registers packages on Default.Runtime before opening.

## Deltas from the standard library

- Open takes Go source, a file or a one-package folder, and compiles
  it against the loader Runtime's registered packages; errors a
  build step would report surface from Open.
- Plugin is an interface, so loaders over other sources can
  implement it.
- A symbol is a func value of its declared signature. Package
  variables are not symbols; a plugin's state lives behind its
  functions.
- Reloadable exists. The standard library can never unload or
  reload; here `p.(plugin.Reloadable).Reload()` recompiles the
  source in place.
- The binding set is the sandbox: a plugin imports only what the
  host registered with BindPackage.

Same as the standard library: Open canonicalizes the path and
returns the cached handle for a repeat Open; `Symbol = any`; error
messages carry the `plugin:` prefix.

## Reload

A symbol is a trampoline that reads the live version at call entry.
Reload compiles the source off to the side and checks every symbol
already handed out still exists with an identical signature; a
missing or retyped symbol refuses the reload naming it, and the old
version stays live. On success one atomic store swaps the
implementation under every held symbol. Calls in flight finish on
the version they entered with; superseded versions are garbage once
no call references them. The suite races Reload against concurrent
calls under -race.

Freshness is explicit: Open never rereads a cached path, Reload
does. A watch loop is the caller's five lines over os.Stat and
Reloadable.
