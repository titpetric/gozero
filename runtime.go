package gozero

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
)

// Runtime holds the bindings and the expression -> func cache. Create
// one with NewRuntime, register functions with Bind, then use Eval, or
// Compile once and Exec/Scan many times.
type Runtime struct {
	mu       sync.RWMutex
	compiler Compiler
	cache    map[string]cacheEntry
	// gen counts the binding surface's revisions. Every Bind, BindType,
	// BindPackage and Import bumps it, and a cache entry compiled under
	// an older generation is a miss, so a rebind is visible to the next
	// Compile of the same source.
	gen uint64
	// packages holds the BindPackage registrations, keyed by import
	// path; a program's import block resolves against it.
	packages map[string]*boundPackage
	// types is the registry a var statement resolves against. It is
	// filled by walking every binding, so most types never need
	// registering by hand.
	types map[string]reflect.Type
	// log records what discovery found. Nil until SetLogger, and
	// checked rather than defaulted so an unlogged runtime pays
	// nothing.
	log *slog.Logger
	// origin names the binding the current discovery walk started from,
	// so the log says where a type came from.
	origin string
}

// SetLogger attaches a logger. Discovery reports every type it
// registers through it, at debug level, which is how a caller finds out
// what a var statement is allowed to name and where each name came
// from.
func (r *Runtime) SetLogger(l *slog.Logger) {
	r.mu.Lock()
	r.log = l
	r.mu.Unlock()
}

// cacheEntry is one compiled program and the binding generation it
// compiled under.
type cacheEntry struct {
	fn  CompiledFunc
	gen uint64
}

// NewRuntime returns an empty Runtime.
func NewRuntime() *Runtime {
	types := predeclared()
	packages := map[string]*boundPackage{}
	return &Runtime{
		compiler: Compiler{bindings: map[string]binding{}, types: types, packages: packages},
		cache:    map[string]cacheEntry{},
		packages: packages,
		types:    types,
	}
}

// Bind registers a Go function under a name, e.g.
// Bind("NewRequest", http.NewRequest). Arguments are borrowed: they
// are valid for the duration of the call, because the runtime pools
// the memory behind packs, boxes and literal arguments and reuses it
// after the call returns. A binding that keeps a received any, slice
// or literal pointer copies it first, the way a type assertion
// copies a value out of its box; plain string and scalar parameters
// need no copy.
func (r *Runtime) Bind(name string, fn any) error {
	v := reflect.ValueOf(fn)
	if v.Kind() != reflect.Func {
		return fmt.Errorf("bind: %s is %s, want func", name, v.Kind())
	}
	r.mu.Lock()
	r.compiler.bindings[name] = binding{rv: v, raw: fn}
	if r.log != nil {
		r.log.Debug("bind", "name", name, "signature", v.Type().String())
	}
	// Everything the signature mentions becomes nameable in a var
	// statement, along with what its methods reach.
	r.origin = name
	r.discover(v.Type(), 1)
	r.origin = ""
	r.gen++
	r.mu.Unlock()
	return nil
}

// BindScope registers a group of functions under a dotted prefix, so
// BindScope("json", map[string]any{"NewEncoder": json.NewEncoder})
// binds json.NewEncoder. It is Bind in a loop and needs no support in
// the compiler: a dotted name is one key in the binding map, and a path
// in the source resolves to the longest prefix that names a binding.
//
// Names are sorted before binding so a failure reports the same entry
// on every run. The first failure stops the loop; entries already bound
// stay bound.
func (r *Runtime) BindScope(prefix string, fns map[string]any) error {
	names := make([]string, 0, len(fns))
	for name := range fns {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := r.Bind(prefix+"."+name, fns[name]); err != nil {
			return err
		}
	}
	return nil
}

// BindPackage registers symbols under a Go import path, e.g.
// BindPackage("net/http", map[string]any{"NewRequest": http.NewRequest}).
// The funcs become callable once a program imports the path, under
// the path's base name or the import's alias; discovery walks the
// signatures into the package's own type table. Registering the same
// path again replaces the package, which is the hot-reload path. The
// borrowed-arguments contract of Bind applies unchanged.
func (r *Runtime) BindPackage(path string, symbols map[string]any) error {
	if path == "" {
		return fmt.Errorf("bindpackage: empty import path")
	}
	names := make([]string, 0, len(symbols))
	for name := range symbols {
		names = append(names, name)
	}
	sort.Strings(names)

	pkg := &boundPackage{
		path:  path,
		base:  pkgBase(path),
		fns:   map[string]binding{},
		types: map[string]reflect.Type{},
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, name := range names {
		fn := symbols[name]
		v := reflect.ValueOf(fn)
		if v.Kind() != reflect.Func {
			return fmt.Errorf("bindpackage: %s.%s is %s, want func", path, name, v.Kind())
		}
		pkg.fns[name] = binding{rv: v, raw: fn}
		r.origin = path + "." + name
		r.discoverInto(pkg.types, v.Type(), 1)
		r.origin = ""
	}
	r.packages[path] = pkg
	r.gen++
	return nil
}

// BindPackageType registers a type into a package, for the rare type
// no bound signature reaches. The contract matches BindType: pass a
// value, or a nil pointer to an interface.
func (r *Runtime) BindPackageType(path, name string, v any) error {
	t := reflect.TypeOf(v)
	if t == nil {
		return fmt.Errorf("bindpackage: %s.%s: cannot take the type of a nil value", path, name)
	}
	if t.Kind() == reflect.Pointer && t.Elem().Kind() == reflect.Interface {
		t = t.Elem()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	pkg, ok := r.packages[path]
	if !ok {
		return fmt.Errorf("bindpackage: %q is not registered, call BindPackage first", path)
	}
	pkg.types[name] = t
	r.origin = path + "." + name
	r.discoverInto(pkg.types, t, 1)
	r.origin = ""
	r.gen++
	return nil
}

// Import activates a registered package into the flat namespace under
// its base name, so a headerless snippet calls http.NewRequest after
// Import("net/http") the way it would after a BindScope. The package's
// type table merges into the registry without overwriting names the
// registry already holds.
func (r *Runtime) Import(path string) error {
	return r.ImportAs("", path)
}

// ImportAs is Import with an explicit prefix in place of the path's
// base name.
func (r *Runtime) ImportAs(alias, path string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	pkg, ok := r.packages[path]
	if !ok {
		return fmt.Errorf("import: %q is not registered, call BindPackage first", path)
	}
	name := alias
	if name == "" {
		name = pkg.base
	}
	for fname, b := range pkg.fns {
		r.compiler.bindings[name+"."+fname] = b
	}
	for tname, t := range pkg.types {
		if _, seen := r.types[tname]; !seen {
			r.types[tname] = t
		}
	}
	r.gen++
	return nil
}

// Invalidate drops the compile cache. The generation check already
// keeps stale entries from being served after a rebind; Invalidate is
// for reclaiming the memory of a cache grown under repeated reloads.
func (r *Runtime) Invalidate() {
	r.mu.Lock()
	r.cache = map[string]cacheEntry{}
	r.mu.Unlock()
}

// Load compiles a source file into a Program: its type and function
// declarations against this Runtime's registered packages, each
// func init() run once in declaration order before Load returns. A
// snippet loads too; a file with a package clause is the intended
// form.
func (r *Runtime) Load(src string) (*Program, error) {
	prog, err := (&Parser{}).Parse(src)
	if err != nil {
		return nil, err
	}
	return r.load(prog)
}

// LoadFile is Load over one file on disk.
func (r *Runtime) LoadFile(path string) (*Program, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load: %w", err)
	}
	p, err := r.Load(string(src))
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", path, err)
	}
	return p, nil
}

// LoadDir loads a package folder: every *.go file in dir, one
// package, merged in filename order, inits running in that order.
func (r *Runtime) LoadDir(dir string) (*Program, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, fmt.Errorf("load: %w", err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("load %s: no .go files", dir)
	}
	sort.Strings(files)
	var merged *program
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("load: %w", err)
		}
		prog, err := (&Parser{}).Parse(string(src))
		if err != nil {
			return nil, fmt.Errorf("load %s: %w", file, err)
		}
		if prog.pkg == "" {
			return nil, fmt.Errorf("load %s: a package file needs a package clause", file)
		}
		if merged == nil {
			merged = prog
			continue
		}
		if prog.pkg != merged.pkg {
			return nil, fmt.Errorf("load %s: package %s, want %s", file, prog.pkg, merged.pkg)
		}
		merged.imports = mergeImports(merged.imports, prog.imports)
		merged.types = append(merged.types, prog.types...)
		merged.funcs = append(merged.funcs, prog.funcs...)
	}
	p, err := r.load(merged)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", dir, err)
	}
	return p, nil
}

// load compiles a parsed program and runs its init hooks.
func (r *Runtime) load(prog *program) (*Program, error) {
	r.mu.RLock()
	vm, err := r.compiler.compileProgram(prog)
	r.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	for _, init := range vm.scriptInits {
		if _, err := init.call(context.Background(), nil, nil); err != nil {
			return nil, fmt.Errorf("load: init: %w", err)
		}
	}
	return &Program{rt: r, pkg: prog.pkg, vm: vm}, nil
}

// mergeImports concatenates import lists, deduplicating an identical
// alias and path pair; the same path under two names stays two
// entries, as it would across Go files.
func mergeImports(a, b []importSpec) []importSpec {
	out := a
	for _, imp := range b {
		dup := false
		for _, have := range out {
			if have == imp {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, imp)
		}
	}
	return out
}

// guard installs the panic boundary. It covers both tiers, because it
// wraps what Compile returns rather than either implementation.
func guard(fn CompiledFunc) CompiledFunc {
	return func(ctx context.Context, stack map[string]any, dest any) (res any, err error) {
		if ctx == nil {
			ctx = context.Background()
		}
		defer func() {
			if r := recover(); r != nil {
				res, err = nil, &PanicError{Value: r, Stack: debug.Stack()}
			}
		}()
		return fn(ctx, stack, dest)
	}
}

// Supports reports whether src compiles to the direct-call tier, and
// why it does not when it does not. Call it in a benchmark, or in a
// test that means to measure the JIT, so an accidental fall back to the
// reflect evaluator is a failure rather than a slow number.
func (r *Runtime) Supports(src string) error {
	prog, err := (&Parser{}).Parse(src)
	if err != nil {
		return err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if call, ok := prog.flatCall(); ok {
		s, err := r.compiler.compileStatement(call)
		if err != nil {
			return err
		}
		if s.fast == nil {
			return fmt.Errorf("%s: signature is outside the shape table", call.path[0])
		}
		return nil
	}
	p, err := r.compiler.compileProgram(prog)
	if err != nil {
		return err
	}
	jp, err := jitCompileProgram(p)
	if err != nil {
		return err
	}
	if len(jp.bridged) > 0 {
		return fmt.Errorf("%d calls bridge through reflect: %s", len(jp.bridged), strings.Join(jp.bridged, "; "))
	}
	return nil
}

// Compile parses and compiles a program into its constructed func.
// Results are cached per program string: the second Compile of the
// same source is a map lookup.
func (r *Runtime) Compile(stmt string) (CompiledFunc, error) {
	r.mu.RLock()
	e, ok := r.cache[stmt]
	gen := r.gen
	r.mu.RUnlock()
	if ok && e.gen == gen {
		return e.fn, nil
	}

	fn, err := r.compileUncached(stmt)
	if err != nil {
		return nil, err
	}
	fn = guard(fn)
	// The entry records the generation read before compiling, so a
	// bind landing mid-compile marks it stale rather than fresh.
	r.mu.Lock()
	r.cache[stmt] = cacheEntry{fn: fn, gen: gen}
	r.mu.Unlock()
	return fn, nil
}

// compileUncached parses and compiles a statement without touching the
// expression cache. This is the full first-run cost of a statement.
func (r *Runtime) compileUncached(stmt string) (CompiledFunc, error) {
	call, err := (&Parser{}).Parse(stmt)
	if err != nil {
		return nil, err
	}
	// The unlock is deferred so a panic inside the compiler cannot leak
	// the read lock; a leaked one deadlocks the next cache store.
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.compiler.Compile(call)
}

// Eval compiles (or reuses the cached compilation of) stmt and executes
// it against the stack. T is the expected result type and cannot be
// inferred, so it is always instantiated explicitly:
// rt.Eval[*http.Request](stmt, stack).
func (r *Runtime) Eval[T any](stmt string, stack map[string]any) (T, error) {
	fn, err := r.Compile(stmt)
	if err != nil {
		var zero T
		return zero, err
	}
	return fn.Exec[T](stack)
}

// EvalContext is Eval with an execution context, which auto-fills any
// context.Context parameter of the bindings the program calls.
func (r *Runtime) EvalContext[T any](ctx context.Context, stmt string, stack map[string]any) (T, error) {
	fn, err := r.Compile(stmt)
	if err != nil {
		var zero T
		return zero, err
	}
	return fn.ExecContext[T](ctx, stack)
}
