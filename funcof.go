package gozero

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// FuncOf materializes src as a func value of type F via
// reflect.MakeFunc, built once over the cached compilation, so a
// compiled program passes anywhere a func does, mux.HandleFunc included.
//
// Parameters enter the program as named stack values: params names
// them in order, defaulting to arg0..argN-1, and the first
// context.Context parameter is also the run's execution context. F may
// have one result besides a trailing error, filled from the value
// return produced; the error result carries the program's error, and
// without one a failing run panics with it.
func (r *Runtime) FuncOf[F any](src string, params ...string) (F, error) {
	var zero F
	fv, err := r.funcOf(reflect.TypeFor[F](), src, params)
	if err != nil {
		return zero, err
	}
	return fv.Interface().(F), nil
}

// funcOf is FuncOf with the func type as a value: it validates the
// signature, compiles src through the cache, and hands both to
// materialize.
func (r *Runtime) funcOf(ft reflect.Type, src string, params []string) (reflect.Value, error) {
	names, err := r.funcOfNames(ft, params)
	if err != nil {
		return reflect.Value{}, err
	}
	fn, err := r.Compile(src)
	if err != nil {
		return reflect.Value{}, err
	}
	return materialize(ft, fn, names)
}

// funcOfNames validates F's parameter side and resolves the stack
// names its arguments travel under. A stack value stays opaque to the
// program compiler: it is read in argument position, and calling a
// method on one takes a binding that accepts the value.
func (r *Runtime) funcOfNames(ft reflect.Type, params []string) ([]string, error) {
	if ft == nil || ft.Kind() != reflect.Func {
		return nil, fmt.Errorf("funcof: F is %s, want a func type", ft)
	}
	if ft.IsVariadic() {
		return nil, fmt.Errorf("funcof: %s is variadic; a program has no parameter to pack the tail into", ft)
	}
	n := ft.NumIn()
	names := params
	if len(names) == 0 && n > 0 {
		names = make([]string, n)
		for i := range names {
			names[i] = "arg" + strconv.Itoa(i)
		}
	}
	if len(names) != n {
		return nil, fmt.Errorf("funcof: %d names for the %d parameters of %s", len(params), n, ft)
	}

	// A name the program cannot read back is a silent zero at run
	// time, so the collisions compileProgram rejects for assignments
	// are rejected here for parameters: keywords, dest, and any
	// binding's first path segment.
	reserved := map[string]bool{"dest": true, "true": true, "false": true, "nil": true, "var": true, "return": true}
	r.mu.RLock()
	for name := range r.compiler.bindings {
		if i := strings.IndexByte(name, '.'); i > 0 {
			reserved[name[:i]] = true
		} else {
			reserved[name] = true
		}
	}
	r.mu.RUnlock()
	seen := map[string]bool{}
	for _, name := range names {
		switch {
		case name == "":
			return nil, fmt.Errorf("funcof: a parameter name is empty")
		case seen[name]:
			return nil, fmt.Errorf("funcof: parameter name %q repeats", name)
		case reserved[name]:
			return nil, fmt.Errorf("funcof: parameter name %q shadows a binding or keyword and could not be read", name)
		}
		seen[name] = true
	}
	return names, nil
}

// materialize wraps a compiled program as a func value of type ft via
// reflect.MakeFunc. It is tier-blind: fn is whatever Compile
// constructed, the direct-call form when the program JITs and the
// reflect evaluator otherwise, and the equivalence tests run both
// through here.
//
// The per-call stack map is pooled: the program only reads it during
// the run, so once fn returns the map is cleared and reused, and a
// call allocates no stack. What remains per call is reflect.MakeFunc's
// own dispatch - the argument Values and the result slice - which is
// the measured cost of this bridge.
func materialize(ft reflect.Type, fn CompiledFunc, names []string) (reflect.Value, error) {
	errIdx, resIdx := -1, -1
	for i := 0; i < ft.NumOut(); i++ {
		if ft.Out(i) == errType {
			if i != ft.NumOut()-1 {
				return reflect.Value{}, fmt.Errorf("funcof: the error result of %s must be last", ft)
			}
			errIdx = i
			continue
		}
		if resIdx >= 0 {
			return reflect.Value{}, fmt.Errorf("funcof: %s has %d results besides error; a program returns one value", ft, ft.NumOut()-boolToInt(errIdx >= 0))
		}
		resIdx = i
	}

	n := ft.NumIn()
	ctxIdx := -1
	for i := 0; i < n; i++ {
		if ft.In(i) == ctxType {
			ctxIdx = i
			break
		}
	}

	nout := ft.NumOut()
	zeros := make([]reflect.Value, nout)
	for i := range zeros {
		zeros[i] = reflect.Zero(ft.Out(i))
	}
	var resT reflect.Type
	if resIdx >= 0 {
		resT = ft.Out(resIdx)
	}
	var rcache atomic.Pointer[assignCache]
	pool := &sync.Pool{New: func() any { return make(map[string]any, n) }}

	return reflect.MakeFunc(ft, func(args []reflect.Value) []reflect.Value {
		ctx := context.Background()
		if ctxIdx >= 0 {
			if c, ok := args[ctxIdx].Interface().(context.Context); ok && c != nil {
				ctx = c
			}
		}
		var stack map[string]any
		if n > 0 {
			stack = pool.Get().(map[string]any)
			for i := range args {
				stack[names[i]] = args[i].Interface()
			}
		}
		res, err := fn(ctx, stack, nil)
		if stack != nil {
			clear(stack)
			pool.Put(stack)
		}
		fail := func(err error) []reflect.Value {
			if errIdx < 0 {
				panic(err)
			}
			out := make([]reflect.Value, nout)
			copy(out, zeros)
			out[errIdx] = reflect.ValueOf(&err).Elem()
			return out
		}
		if err != nil {
			return fail(err)
		}
		if nout == 0 {
			return nil
		}
		out := make([]reflect.Value, nout)
		copy(out, zeros)
		if resIdx >= 0 && res != nil {
			rv := reflect.ValueOf(res)
			c := rcache.Load()
			if c == nil || c.typ != rv.Type() {
				c = &assignCache{typ: rv.Type(), ok: rv.Type().AssignableTo(resT)}
				rcache.Store(c)
			}
			if !c.ok {
				return fail(fmt.Errorf("funcof: the program returned %T, want %s", res, resT))
			}
			out[resIdx] = rv
		}
		return out
	}), nil
}
