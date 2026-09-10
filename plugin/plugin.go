// Package plugin loads Go source as plugins, mirroring the standard
// library's plugin API without a build step: Open compiles a .go
// file or a package folder against a Runtime's registered packages,
// Lookup hands out symbols, and a handle can reload its source in
// place while callers keep their symbols.
//
// The deltas from the standard library's package:
//
//   - Open takes Go source, a file or a one-package folder, and
//     compiles it hot; errors a build step would report surface from
//     Open.
//   - Plugin is an interface, so loaders over other sources can
//     implement it.
//   - A symbol is a func value; package variables are not symbols,
//     because a plugin's state belongs behind its functions.
//   - Reloadable exists: a successful Reload swaps the
//     implementation under every symbol already handed out, and a
//     reload that removes or retypes a held symbol is refused.
//   - The binding set is the sandbox: a plugin imports only what the
//     host registered with BindPackage.
package plugin

// Symbol is a loaded symbol: a func value of the declared signature.
type Symbol = any

// Plugin is the handle Open returns.
type Plugin interface {
	// Lookup resolves an exported function to its func value. The
	// value is a trampoline: after a successful Reload it runs the
	// new implementation.
	Lookup(symName string) (Symbol, error)
}

// Reloadable is the optional capability a reloading handle adds;
// callers reach it with a type assertion.
type Reloadable interface {
	Reload() error
}

// Open opens path, a .go file or a package folder, through Default.
func Open(path string) (Plugin, error) {
	return Default.Open(path)
}
