package gozero

import (
	"context"
	"fmt"
	"reflect"
	"sync"
)

// varSlot is an argument filled from the stack on every call.
type varSlot struct {
	index int
	name  string
	typ   reflect.Type
	zero  reflect.Value
}

// Statement is the compiled form of one return statement. It
// internalizes the reflect.Value of the bound function and a prebuilt
// argument slice: literals are filled once at compile time, variable
// slots are patched from a per-call copy, and the invocation is a
// single fn.Call.
//
// Both paths are safe for concurrent use. The JIT'd statement (fast !=
// nil) keeps no per-call state; the reflect path pools its argument
// slice, because patching the prebuilt one in place raced between
// goroutines sharing the func Runtime.Compile caches.
type Statement struct {
	fn   reflect.Value
	fast CompiledFunc // JIT'd direct call, nil when out of shape
	args []reflect.Value
	vars []varSlot

	// pool holds *[]reflect.Value rather than the slice, so returning
	// one does not allocate a header to box.
	pool sync.Pool

	outIndex int // index of the result value, -1 if none
	errIndex int // index of a trailing error value, -1 if none
}

// borrow takes an argument slice with the literals already in place.
// Only the variable slots are cleared on release, so a fresh buffer is
// the only one that needs the full copy.
func (s *Statement) borrow() *[]reflect.Value {
	if p, ok := s.pool.Get().(*[]reflect.Value); ok {
		return p
	}
	buf := make([]reflect.Value, len(s.args))
	copy(buf, s.args)
	return &buf
}

// release drops the references the call took off the stack and returns
// the buffer. Literals stay: they are held by s.args regardless, so
// keeping them costs nothing and saves the copy.
func (s *Statement) release(p *[]reflect.Value) {
	buf := *p
	for i := range s.vars {
		buf[s.vars[i].index] = reflect.Value{}
	}
	s.pool.Put(p)
}

// Func returns the constructed closure over the Statement: the JIT'd
// direct call when the binding fits a shape, otherwise the reflect
// path. This is the value Runtime.Compile caches.
func (s *Statement) Func() CompiledFunc {
	if s.fast != nil {
		return s.fast
	}
	return s.call
}

func (s *Statement) call(ctx context.Context, stack map[string]any, dest any) (any, error) {
	p := s.borrow()
	args := *p
	for i := range s.vars {
		v, err := __get(stack, &s.vars[i])
		if err != nil {
			s.release(p)
			return nil, err
		}
		args[s.vars[i].index] = v
	}
	out := s.fn.Call(args)
	s.release(p)
	if s.errIndex >= 0 {
		if e := out[s.errIndex]; !e.IsNil() {
			return nil, e.Interface().(error)
		}
	}
	if s.outIndex < 0 {
		return nil, nil
	}
	return out[s.outIndex].Interface(), nil
}

// __get resolves a variable slot against the stack. Unset or nil
// entries produce the parameter's zero value; a set entry must hold a
// value assignable to the parameter type.
func __get(stack map[string]any, vs *varSlot) (reflect.Value, error) {
	v, ok := stack[vs.name]
	if !ok || v == nil {
		return vs.zero, nil
	}
	rv := reflect.ValueOf(v)
	if !rv.Type().AssignableTo(vs.typ) {
		return reflect.Value{}, fmt.Errorf("exec: variable %q: cannot use %s as %s", vs.name, rv.Type(), vs.typ)
	}
	return rv, nil
}
