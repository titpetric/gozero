package gozero

import (
	"fmt"
	"reflect"
)

// Literal typing. A literal has no type of its own beyond the width
// the parser produced, so the compiler works one out: a conversion
// hint fixes it at the assignment, and inference reads it off the
// first binding parameter the program passes the name to.

// conversionType reports whether a call is a conversion hint: no
// method chain, and a path that names a registered type. A binding
// wins over a type of the same name, which keeps every program that
// compiled before hints existed meaning what it meant.
func (c *Compiler) conversionType(e *callExpr) (reflect.Type, bool) {
	if len(e.chain) != 0 {
		return nil, false
	}
	name := joinPath(e.path)
	if _, bound := c.bindings[name]; bound {
		return nil, false
	}
	return c.lookupType(name)
}

// conversionArg is the single literal a conversion hint wraps. A name
// or a nested call is rejected: a value already has a type, and the
// hint exists to give one to a literal that has none.
func conversionArg(e *callExpr) (arg, error) {
	if len(e.args) != 1 {
		return arg{}, fmt.Errorf("a conversion takes exactly one argument, got %d", len(e.args))
	}
	a := e.args[0]
	if a.spread {
		return arg{}, fmt.Errorf("a conversion argument cannot be spread")
	}
	switch a.kind {
	case argString, argInt, argFloat, argBool, argNil:
		return a, nil
	}
	return arg{}, fmt.Errorf("a conversion takes a literal")
}

// inferLiteralType picks the type of a name a literal is assigned to,
// when no var statement fixed it. The first place the program passes
// the name to a binding decides, because that is the only type the
// value has to satisfy. Failing that the literal keeps the width the
// parser gave it.
func (c *Compiler) inferLiteralType(prog *program, name string, lit arg) reflect.Type {
	// A use inside a loop body types the name like a use outside one:
	// the walk descends into loop bodies.
	var walk func(stmts []stmt) reflect.Type
	walk = func(stmts []stmt) reflect.Type {
		for si := range stmts {
			if call := stmts[si].call; call != nil {
				if t := c.useType(call, name); t != nil {
					return t
				}
			}
			if rng := stmts[si].rng; rng != nil {
				if t := walk(rng.body); t != nil {
					return t
				}
			}
			if fors := stmts[si].fors; fors != nil {
				if t := walk(fors.body); t != nil {
					return t
				}
			}
		}
		return nil
	}
	if t := walk(prog.stmts); t != nil {
		return t
	}
	switch lit.kind {
	case argFloat:
		return reflect.TypeFor[float64]()
	case argString:
		return reflect.TypeFor[string]()
	case argBool:
		return reflect.TypeFor[bool]()
	case argNil:
		// nil alone names no type; the any it would infer to is never
		// what the program meant, so the caller reports it.
		return nil
	}
	return reflect.TypeFor[int64]()
}

// useType is the parameter type a call gives to name, looking through
// nested calls. Only a call whose whole path is a binding is
// considered: a method's receiver type may itself depend on a type not
// worked out yet.
func (c *Compiler) useType(e *callExpr, name string) reflect.Type {
	if b, ok := c.bindings[joinPath(e.path)]; ok && len(e.chain) == 0 {
		ft := b.rv.Type()
		fixed := ft.NumIn()
		if ft.IsVariadic() {
			fixed--
		}
		i := 0
		for _, a := range e.args {
			// Mirror compileCall: a context parameter consumes no
			// written argument.
			for i < fixed && ft.In(i) == ctxType {
				i++
			}
			var pt reflect.Type
			switch {
			case i < fixed:
				pt = ft.In(i)
			case ft.IsVariadic():
				pt = ft.In(fixed).Elem()
			default:
				return nil
			}
			if a.kind == argVar && a.str == name {
				// An empty interface accepts anything, so it says
				// nothing about what the name should be.
				if pt.Kind() == reflect.Interface && pt.NumMethod() == 0 {
					return nil
				}
				return pt
			}
			i++
		}
	}
	for _, a := range e.args {
		if a.kind == argCall {
			if t := c.useType(a.sub, name); t != nil {
				return t
			}
		}
	}
	for _, l := range e.chain {
		for _, a := range l.args {
			if a.kind == argCall {
				if t := c.useType(a.sub, name); t != nil {
					return t
				}
			}
		}
	}
	return nil
}

// staticType is the compile-time type of an argument when it has one:
// a program-bound name. Everything else answers nil, which for the
// context rule means auto-fill.
func (c *Compiler) staticType(env map[string]reflect.Type, a arg) reflect.Type {
	if a.kind == argVar {
		return env[a.str]
	}
	return nil
}
