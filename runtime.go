package gozero

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"reflect"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
)

// Runtime holds the bindings and the expression -> func cache. Create
// one with NewRuntime, register functions with Bind, then use Eval, or
// Compile once and Exec/Scan many times.
//
// A binding borrows its arguments. The runtime pools the memory behind
// packs, boxes and literal arguments and reuses it after the call returns,
// so a binding that keeps a received any, slice or literal pointer copies
// it first, the way a type assertion copies a value out of its box. Plain
// string and scalar parameters need no copy.
type Runtime struct {
	mu       sync.RWMutex
	compiler Compiler
	cache    map[string]CompiledFunc
	// types is the registry a var statement resolves against. A walk
	// over every binding fills it, so most types never need
	// registering by hand.
	types map[string]reflect.Type
	// log records what discovery found. Nil until SetLogger, and
	// checked with no default so an unlogged runtime pays
	// nothing.
	log *slog.Logger
	// origin names the binding the current discovery walk started from,
	// so the log says where a type came from.
	origin string
}

// SetLogger attaches the logger the runtime reports each registration to.
func (r *Runtime) SetLogger(l *slog.Logger) {
	r.mu.Lock()
	r.log = l
	r.mu.Unlock()
}

// NewRuntime returns an empty Runtime.
func NewRuntime() *Runtime {
	types := predeclared()
	return &Runtime{
		compiler: Compiler{
			bindings: map[string]binding{},
			vars:     map[string]varBinding{},
			types:    types,
		},
		cache: map[string]CompiledFunc{},
		types: types,
	}
}

// Bind registers a Go function or a value under a name, and refuses a nil
// value or a nil pointer: neither carries a type to take or a value to read.
//
//	rt.Bind("url.Parse", url.Parse)  // a func, called
//	rt.Bind("io.EOF", io.EOF)        // a value, read
//	rt.Bind("os.Args", &os.Args)     // a pointer, read and written through
//
// A value read through its name carries the static type Bind recorded.
func (r *Runtime) Bind(name string, fn any) error {
	if fn == nil {
		return fmt.Errorf("bind: %s: cannot bind a nil value, its type is unknown", name)
	}
	v := reflect.ValueOf(fn)
	if v.Kind() != reflect.Func {
		return r.bindValue(name, v)
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

// bindValue is the value half of Bind.
func (r *Runtime) bindValue(name string, v reflect.Value) error {
	if v.Kind() == reflect.Pointer && v.IsNil() {
		return fmt.Errorf("bind: %s: cannot bind a nil %s", name, v.Type())
	}
	r.mu.Lock()
	r.compiler.vars[name] = varBinding{val: v}
	if r.log != nil {
		r.log.Debug("bind value", "name", name, "type", v.Type().String())
	}
	r.origin = name
	r.discover(v.Type(), 1)
	r.origin = ""
	r.mu.Unlock()
	return nil
}

// BindScope is Bind in a loop over a map of functions under a dotted prefix,
// so {"NewEncoder": json.NewEncoder} under "json" binds json.NewEncoder.
func (r *Runtime) BindScope(prefix string, fns map[string]any) error {
	for _, name := range slices.Sorted(maps.Keys(fns)) {
		if err := r.Bind(prefix+"."+name, fns[name]); err != nil {
			return err
		}
	}
	return nil
}

// guard installs the panic boundary. It covers both tiers, because it
// wraps what Compile returns, outside either implementation.
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
// reflect evaluator fails the test and does not pass as a slow number.
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
// The runtime caches results per program string: the second Compile of
// the same source is a map lookup.
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
	// The deferred RUnlock keeps a panic inside the compiler from
	// leaking the read lock; a leaked one deadlocks the next cache store.
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.compiler.Compile(call)
}

// Eval compiles stmt, or reuses the cached compilation of it, and executes
// it against the stack, returning the result as T.
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
