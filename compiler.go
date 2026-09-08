package gozero

import (
	"fmt"
	"reflect"
)

var errType = reflect.TypeOf((*error)(nil)).Elem()

// binding is one bound function: its reflect.Value for the general
// call path and the raw func value the JIT takes its funcval pointer
// from.
type binding struct {
	rv  reflect.Value
	raw any
}

// Compiler turns a parsed program into an executable form against a set
// of bound functions. All argument validation happens here, at compile
// time: literal types must be assignable to the parameter types of the
// binding, and a method must exist on the static type of the name it is
// called on. Names read from the caller's stack are only type-checked at
// execution time, when their value is known.
type Compiler struct {
	bindings map[string]binding
	// types is the Runtime's registry, shared by reference so a Bind
	// after a Compile is visible.
	types map[string]reflect.Type
}

// Compile validates a program and builds the constructed func.
//
// A program that is one flat call keeps the original single-statement
// path, so the shape table still applies to it and the JIT result
// stands. Anything longer, or anything with a chained or nested call,
// compiles to the multi-statement VM in vm.go, which is reflect only.
func (c *Compiler) Compile(prog *program) (CompiledFunc, error) {
	if call, ok := prog.flatCall(); ok && c.plainBinding(call.path[0]) {
		s, err := c.compileStatement(call)
		if err != nil {
			return nil, err
		}
		return s.Func(), nil
	}
	p, err := c.compileProgram(prog)
	if err != nil {
		return nil, err
	}
	// Every call in the shape table means the whole program runs
	// without reflect.Value.Call. One call outside it and the reflect
	// evaluator takes the program, so the two are never mixed.
	if jp, err := jitCompileProgram(p); err == nil {
		return CompiledFunc(jp.run), nil
	}
	return CompiledFunc(p.run), nil
}

// plainBinding reports whether name is a binding the single-statement
// path can call: not variadic, and with no context.Context parameter,
// both of which only the program compiler knows how to fill.
func (c *Compiler) plainBinding(name string) bool {
	b, ok := c.bindings[name]
	if !ok {
		return true // let compileStatement report the unknown name
	}
	ft := b.rv.Type()
	if ft.IsVariadic() {
		return false
	}
	for i := 0; i < ft.NumIn(); i++ {
		if ft.In(i) == ctxType {
			return false
		}
	}
	return true
}

// compileStatement builds the single-call Statement: a prebuilt
// argument slice, variable slots patched from the stack, and the JIT'd
// direct call when the binding fits a shape.
func (c *Compiler) compileStatement(call *callExpr) (*Statement, error) {
	name := call.path[0]
	b, ok := c.bindings[name]
	if !ok {
		return nil, fmt.Errorf("compile: unknown binding %q", name)
	}
	fn := b.rv
	ft := fn.Type()
	if ft.IsVariadic() {
		return nil, fmt.Errorf("compile: variadic binding %q not supported", name)
	}
	if len(call.args) > ft.NumIn() {
		return nil, fmt.Errorf("compile: %s takes %d arguments, got %d", name, ft.NumIn(), len(call.args))
	}

	s := &Statement{
		fn:       fn,
		args:     make([]reflect.Value, ft.NumIn()),
		outIndex: -1,
		errIndex: -1,
	}

	for i := 0; i < ft.NumIn(); i++ {
		pt := ft.In(i)
		// Missing arguments are filled with their zero value: "" for
		// string, nil for io.Reader and other pointer-shaped types.
		if i >= len(call.args) {
			s.args[i] = reflect.Zero(pt)
			continue
		}
		a := call.args[i]
		var v reflect.Value
		switch a.kind {
		case argString:
			v = reflect.ValueOf(a.str)
		case argInt:
			v = reflect.ValueOf(a.i)
		case argFloat:
			v = reflect.ValueOf(a.f)
		case argVar:
			s.vars = append(s.vars, varSlot{index: i, name: a.str, typ: pt, zero: reflect.Zero(pt)})
			s.args[i] = reflect.Zero(pt)
			continue
		}
		if !v.Type().AssignableTo(pt) {
			return nil, fmt.Errorf("compile: %s argument %d: cannot use %s as %s", name, i+1, v.Type(), pt)
		}
		s.args[i] = v
	}

	for i := 0; i < ft.NumOut(); i++ {
		if ft.Out(i) == errType {
			s.errIndex = i
		} else if s.outIndex < 0 {
			s.outIndex = i
		}
	}

	s.fast = jitCompile(b.raw, ft, call)
	return s, nil
}
