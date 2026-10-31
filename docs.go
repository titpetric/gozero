// Package gozero runs imperative programs written in a minimal statement
// language against bound Go functions, with type safety taken from the
// bindings' own signatures and per-call overhead measured in tens of
// nanoseconds.
//
// A host binds Go functions and values on a Runtime, compiles a program
// against them, and runs what it compiled as often as it likes:
//
//	rt := gozero.NewRuntime()
//	rt.BindScope("http", map[string]any{"NewRequest": http.NewRequest})
//	rt.BindScope("json", map[string]any{"NewEncoder": json.NewEncoder})
//
//	var dest bytes.Buffer
//	fn, err := rt.Compile(`
//		req := http.NewRequest("GET", "/")
//		json.NewEncoder(dest).Encode(req.Cookies())
//	`)
//	err = fn.Scan(&dest, nil)
//
// # Bindings
//
// Every name a program can write is a binding the host registered. Bind
// takes one func or one value, BindScope takes a map of them under a dotted
// prefix, and bindvar.go has the reference rules a value binding follows.
//
// A dotted name is one key in the binding map and needs no support in the
// compiler, because a path in a program resolves to the longest prefix of it
// that names a binding. Binding "os.Args" makes os.Args one name, and
// whatever follows such a prefix is fields and methods.
//
// BindScope binds in sorted order. Which entry a failure reports does not
// move with a map iteration. The first failure stops the loop, and the
// entries bound before it stay bound.
//
// A rebind is a new binding and not an update. A program that has already
// compiled captured the binding as it stood then, and Runtime.Compile caches
// a compilation per source string. A later Bind under the same name reaches
// neither.
//
// # Results
//
// Eval and CompiledFunc.Exec return the program's result as T. Because T
// appears only in the result, where Go has nothing to infer it from, every
// call instantiates it explicitly:
//
//	req, err := rt.Eval[*http.Request](src, stack)
//
// CompiledFunc.Scan takes a pointer the caller allocated and infers T from
// it. Because it dereferences a *T result, a *http.Request scans into an
// http.Request the caller owns, without an interface in the way.
//
// dest travels in both directions. A program that ends in a value assigns it
// to dest; a program that only passes dest to a binding, as
// json.NewEncoder(dest) does, has already written through the pointer by the
// time Scan returns. A program with no value leaves dest exactly as the
// bindings left it, and Scan does not zero it.
//
// # Types
//
// A var statement names a type, and the names it may use are the registry's.
// Binding a function registers every type its signature mentions, along with
// what the methods of those types reach. Most types never need registering
// by hand. Runtime.BindType covers the rest: it takes a value of the type,
// and a nil pointer to it for an interface.
//
//	rt.BindType("io.Closer", (*io.Closer)(nil))
//
// Runtime.Types lists what the registry holds. A host reads it to diagnose a
// lookup that failed. Runtime.SetLogger is the other half of that: it
// reports each registration at debug level, with the binding the walk
// started from. The log shows which names a var statement may use and where
// each one came from.
package gozero
