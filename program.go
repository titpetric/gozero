package gozero

import (
	"context"
	"fmt"
	"reflect"
)

// Program is a loaded source file or package: its declared functions
// reachable by name, its init hooks already run. A Program is owned
// by the caller; the Runtime does not cache it, so a reload is a new
// Load and the old Program stays valid for whoever still holds it.
type Program struct {
	rt  *Runtime
	pkg string
	vm  *vmProgram
}

// Package is the loaded package's declared name.
func (p *Program) Package() string {
	return p.pkg
}

// fn resolves a declared function by name.
func (p *Program) fn(name string) (*scriptFn, error) {
	if fn := p.vm.funcs[name]; fn != nil {
		return fn, nil
	}
	return nil, fmt.Errorf("program: %s is not a declared function in package %s", name, p.pkg)
}

// Call invokes a declared function with positional arguments,
// matching the results the way the runtime's generic surface does:
// the first result converts to T and a trailing error travels out as
// the error.
func (p *Program) Call[T any](name string, args ...any) (T, error) {
	var zero T
	fn, err := p.fn(name)
	if err != nil {
		return zero, err
	}
	if len(args) != fn.sig.NumIn() {
		return zero, fmt.Errorf("program: %s takes %d arguments, got %d", name, fn.sig.NumIn(), len(args))
	}
	in := make([]reflect.Value, len(args))
	for i, a := range args {
		pt := fn.sig.In(i)
		if a == nil {
			in[i] = reflect.Zero(pt)
			continue
		}
		av := reflect.ValueOf(a)
		if !av.Type().AssignableTo(pt) {
			return zero, fmt.Errorf("program: %s argument %d: cannot use %s as %s", name, i+1, av.Type(), pt)
		}
		in[i] = av
	}
	out, err := fn.call(context.Background(), nil, in)
	if err != nil {
		return zero, err
	}
	if fn.errIdx >= 0 {
		if ev := out[fn.errIdx]; !ev.IsNil() {
			return zero, ev.Interface().(error)
		}
	}
	for i, v := range out {
		if i == fn.errIdx {
			continue
		}
		got, ok := v.Interface().(T)
		if !ok {
			return zero, fmt.Errorf("program: %s returns %s, not %T", name, v.Type(), zero)
		}
		return got, nil
	}
	return zero, nil
}

// FuncOf materializes a declared function as the Go func type F, so
// the host calls it natively: a handler, a middleware, a comparator.
// F's parameter and result types must match the declaration.
func (p *Program) FuncOf[F any](name string) (F, error) {
	var zero F
	ft := reflect.TypeFor[F]()
	if ft.Kind() != reflect.Func {
		return zero, fmt.Errorf("program: FuncOf wants a func type, got %s", ft)
	}
	fn, err := p.fn(name)
	if err != nil {
		return zero, err
	}
	if underlyingFunc(ft) != fn.sig {
		return zero, fmt.Errorf("program: %s is %s, not %s", name, fn.sig, ft)
	}
	return fn.materialize(context.Background(), ft, nil).Interface().(F), nil
}
