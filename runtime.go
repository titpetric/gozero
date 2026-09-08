package gozero

import (
	"context"
	"fmt"
	"log/slog"
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
	cache    map[string]CompiledFunc
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

// NewRuntime returns an empty Runtime.
func NewRuntime() *Runtime {
	types := predeclared()
	return &Runtime{
		compiler: Compiler{bindings: map[string]binding{}, types: types},
		cache:    map[string]CompiledFunc{},
		types:    types,
	}
}

// Bind registers a Go function under a name, e.g.
// Bind("NewRequest", http.NewRequest).
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
	fn, ok := r.cache[stmt]
	r.mu.RUnlock()
	if ok {
		return fn, nil
	}

	fn, err := r.compileUncached(stmt)
	if err != nil {
		return nil, err
	}
	fn = guard(fn)
	r.mu.Lock()
	r.cache[stmt] = fn
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
	r.mu.RLock()
	fn, err := r.compiler.Compile(call)
	r.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	return fn, nil
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
