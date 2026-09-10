package gozero

import (
	"fmt"
	"reflect"
)

// The call forms beyond a binding: a declared function or method, a
// func-typed value, the conversion to a named func type, and the
// script method resolution behind them.

// compileScriptCall compiles a call of a declared function or method
// with Go's strict arity; the trailing error follows the hybrid
// rule at the statement level, exactly as a binding's does.
func (c *Compiler) compileScriptCall(sc *cscope, fn *scriptFn, name string, recv *vmArg, src []arg) (*vmCall, error) {
	call := &vmCall{script: fn, name: name, errIdx: fn.errIdx}
	sigOff := 0
	if recv != nil {
		if !recv.typ.AssignableTo(fn.sig.In(0)) {
			return nil, fmt.Errorf("compile: %s: receiver is %s, want %s", name, sc.typeName(recv.typ), sc.typeName(fn.sig.In(0)))
		}
		call.args = append(call.args, recv)
		sigOff = 1
	}
	fixed := fn.sig.NumIn() - sigOff
	if fn.variadic {
		fixed--
	}
	if len(src) < fixed || (!fn.variadic && len(src) != fixed) {
		return nil, fmt.Errorf("compile: %s takes %d arguments, got %d", name, fn.sig.NumIn()-sigOff, len(src))
	}
	for i := 0; i < fixed; i++ {
		if src[i].spread {
			return nil, fmt.Errorf("compile: %s: ... spreads only into a variadic parameter", name)
		}
		va, err := c.compileArg(sc, name, i, fn.sig.In(i+sigOff), src[i])
		if err != nil {
			return nil, err
		}
		call.args = append(call.args, va)
	}
	if fn.variadic {
		st := fn.sig.In(fn.sig.NumIn() - 1)
		rest := src[fixed:]
		switch {
		case len(rest) == 1 && rest[0].spread:
			va, err := c.compileArg(sc, name, fixed, st, unspread(rest[0]))
			if err != nil {
				return nil, err
			}
			call.args = append(call.args, va)
			call.spread = true
		default:
			et := st.Elem()
			for i, a := range rest {
				if a.spread {
					return nil, fmt.Errorf("compile: %s: ... must be the only variadic argument", name)
				}
				va, err := c.compileArg(sc, name, fixed+i, et, a)
				if err != nil {
					return nil, err
				}
				call.args = append(call.args, va)
			}
		}
	}
	call.nres = len(fn.results)
	if call.errIdx >= 0 {
		call.nres--
	}
	return call, nil
}

// underlyingFunc is the structural func type behind a possibly named
// one.
func underlyingFunc(t reflect.Type) reflect.Type {
	in := make([]reflect.Type, t.NumIn())
	for i := range in {
		in[i] = t.In(i)
	}
	out := make([]reflect.Type, t.NumOut())
	for i := range out {
		out[i] = t.Out(i)
	}
	return reflect.FuncOf(in, out, t.IsVariadic())
}

// funcConversion reports a call-shaped expression that is a
// conversion to a func type the program can name: one argument, no
// chain, a path that is a type rather than a binding. The conversion
// carries a func literal or a func value into a named func type,
// which is how a handler-func adapter pattern spells.
func (c *Compiler) funcConversion(sc *cscope, e *callExpr) (reflect.Type, bool) {
	if len(e.chain) != 0 || len(e.args) != 1 || e.args[0].spread {
		return nil, false
	}
	name := joinPath(e.path)
	if _, bound := sc.bindings[name]; bound {
		return nil, false
	}
	t, ok := c.resolveType(sc, name)
	if !ok || t.Kind() != reflect.Func {
		return nil, false
	}
	return t, true
}

// scriptMethodOf resolves a declared method on t or its pointee.
func scriptMethodOf(sc *cscope, t reflect.Type, name string) *scriptFn {
	if sc.methods == nil {
		return nil
	}
	if fn := sc.methods[t][name]; fn != nil {
		return fn
	}
	if t.Kind() == reflect.Pointer {
		if fn := sc.methods[t.Elem()][name]; fn != nil {
			return fn
		}
		return nil
	}
	return sc.methods[reflect.PointerTo(t)][name]
}

// compileDynCall compiles a call of a func-typed value with Go's
// strict arity; the trailing error follows the hybrid rule.
func (c *Compiler) compileDynCall(sc *cscope, fnVal *vmArg, ft reflect.Type, name string, src []arg) (*vmCall, error) {
	call := &vmCall{dyn: fnVal, name: name, errIdx: -1}
	fixed := ft.NumIn()
	if ft.IsVariadic() {
		fixed--
	}
	if len(src) < fixed || (!ft.IsVariadic() && len(src) != fixed) {
		return nil, fmt.Errorf("compile: %s takes %d arguments, got %d", name, ft.NumIn(), len(src))
	}
	for i := 0; i < fixed; i++ {
		if src[i].spread {
			return nil, fmt.Errorf("compile: %s: ... spreads only into a variadic parameter", name)
		}
		va, err := c.compileArg(sc, name, i, ft.In(i), src[i])
		if err != nil {
			return nil, err
		}
		call.args = append(call.args, va)
	}
	if ft.IsVariadic() {
		st := ft.In(ft.NumIn() - 1)
		rest := src[fixed:]
		switch {
		case len(rest) == 1 && rest[0].spread:
			va, err := c.compileArg(sc, name, fixed, st, unspread(rest[0]))
			if err != nil {
				return nil, err
			}
			call.args = append(call.args, va)
			call.spread = true
		default:
			et := st.Elem()
			for i, a := range rest {
				if a.spread {
					return nil, fmt.Errorf("compile: %s: ... must be the only variadic argument", name)
				}
				va, err := c.compileArg(sc, name, fixed+i, et, a)
				if err != nil {
					return nil, err
				}
				call.args = append(call.args, va)
			}
		}
	}
	call.nres = ft.NumOut()
	if n := ft.NumOut(); n > 0 && ft.Out(n-1) == errType {
		call.errIdx = n - 1
		call.nres--
	}
	return call, nil
}
