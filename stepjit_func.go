package gozero

import (
	"context"
	"fmt"
	"reflect"
	"unsafe" // also required by go:linkname
)

// Script functions on the direct tier: the call node dispatching
// into a unit, the deferred form, and the error-binding form. The
// units themselves compile through jitCompileUnit in stepjit.go.

// scriptCallNode calls a declared function or a func-typed value.
// Arguments travel through the bridge getters and results come back
// as reflect values: the function's own body runs its unit, direct
// where the unit lowered, so the boundary boxing is the remaining
// cost, paid per call rather than per operand.
func (c *jitCompiler) scriptCallNode(call *vmCall) (node, error) {
	if call.dispatch != nil {
		return node{}, fmt.Errorf("a script-interface dispatch stays on the reflect tier")
	}
	getters := make([]func(unsafe.Pointer, context.Context, map[string]any, any) (reflect.Value, error), len(call.args))
	for i, a := range call.args {
		g, err := c.bridgeArg(a)
		if err != nil {
			return node{}, err
		}
		getters[i] = g
	}
	var dynGet func(unsafe.Pointer, context.Context, map[string]any, any) (reflect.Value, error)
	if call.dyn != nil {
		g, err := c.bridgeArg(call.dyn)
		if err != nil {
			return node{}, err
		}
		dynGet = g
	}
	fn, name, errIdx, bindErr, spread := call.script, call.name, call.errIdx, call.bindErr, call.spread
	invoke := func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) ([]reflect.Value, error) {
		args := make([]reflect.Value, len(getters))
		for i, g := range getters {
			v, err := g(fr, ctx, st, d)
			if err != nil {
				return nil, err
			}
			args[i] = v
		}
		var out []reflect.Value
		var err error
		if fn != nil {
			out, err = fn.invoke(ctx, nil, fn.packVariadic(args, spread))
		} else {
			fv, ferr := dynGet(fr, ctx, st, d)
			if ferr != nil {
				return nil, ferr
			}
			if !fv.IsValid() || fv.IsNil() {
				return nil, fmt.Errorf("exec: %s is nil, not a function", name)
			}
			if spread {
				out = fv.CallSlice(args)
			} else {
				// Call packs a variadic tail itself.
				out = fv.Call(args)
			}
		}
		if err != nil {
			return nil, err
		}
		if errIdx >= 0 && !bindErr {
			if e := out[errIdx]; !e.IsNil() {
				return nil, e.Interface().(error)
			}
		}
		return out, nil
	}
	if call.nres == 0 {
		return node{class: lNone, E: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
			_, err := invoke(fr, ctx, st, d)
			return err
		}}, nil
	}
	rt := c.callResultTypeOf(call, 0)
	cl := layoutOf(rt)
	if cl == lBad {
		return node{}, fmt.Errorf("%s: a result of type %s has no layout class", name, rt)
	}
	resIdx := 0
	if errIdx == 0 && !bindErr {
		resIdx = 1
	}
	pick := func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (unsafe.Pointer, error) {
		out, err := invoke(fr, ctx, st, d)
		if err != nil {
			return nil, err
		}
		cell := reflect.New(rt)
		cell.Elem().Set(out[resIdx])
		return cell.UnsafePointer(), nil
	}
	return loadClassNode(pick, cl), nil
}

// callResultTypeOf is callResultType across the three call kinds.
func (c *jitCompiler) callResultTypeOf(call *vmCall, i int) reflect.Type {
	if call.script != nil {
		n := 0
		for j, rt := range call.script.results {
			if j == call.errIdx {
				continue
			}
			if n == i {
				return rt
			}
			n++
		}
		return nil
	}
	if call.dyn != nil {
		ft := call.dyn.typ
		n := 0
		for j := 0; j < ft.NumOut(); j++ {
			if j == call.errIdx {
				continue
			}
			if n == i {
				return ft.Out(j)
			}
			n++
		}
		return nil
	}
	return callResultType(call, i)
}

// deferFlowNode evaluates the deferred call's arguments now, copies
// them off the frame, and stacks the entry; the run's exit drains
// the stack LIFO with the reflect call the entries carry.
func (c *jitCompiler) deferFlowNode(s *vmStmt, jp *jitProgram) (nodeE, error) {
	if c.deferField < 0 {
		return nil, fmt.Errorf("a defer has no stack field")
	}
	call := s.deferCall
	getters := make([]func(unsafe.Pointer, context.Context, map[string]any, any) (reflect.Value, error), len(call.args))
	for i, a := range call.args {
		g, err := c.bridgeArg(a)
		if err != nil {
			return nil, err
		}
		getters[i] = g
	}
	var dynGet func(unsafe.Pointer, context.Context, map[string]any, any) (reflect.Value, error)
	if call.dyn != nil {
		g, err := c.bridgeArg(call.dyn)
		if err != nil {
			return nil, err
		}
		dynGet = g
	}
	off := c.offs[c.deferField]
	return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
		args := make([]reflect.Value, len(getters))
		for i, g := range getters {
			v, err := g(fr, ctx, st, d)
			if err != nil {
				return err
			}
			// A bridge value may point into the frame, and the frame
			// repools before the drain; the copy detaches it.
			cell := reflect.New(v.Type()).Elem()
			cell.Set(v)
			args[i] = cell
		}
		e := deferredEntry{call: call, args: args, ctx: ctx}
		if dynGet != nil {
			fv, err := dynGet(fr, ctx, st, d)
			if err != nil {
				return err
			}
			e.fv = fv
		}
		defers := *(**deferStack)(unsafe.Add(fr, off))
		defers.entries = append(defers.entries, e)
		return nil
	}, nil
}

// bindErrNode is a call whose trailing error the program named: the
// results, error included, store positionally into the named slots
// with no implicit check. The call goes through the bridge; naming
// the error is already the slow, once-per-failure path.
func (c *jitCompiler) bindErrNode(s *vmStmt) (nodeE, error) {
	call := s.call
	getters := make([]func(unsafe.Pointer, context.Context, map[string]any, any) (reflect.Value, error), len(call.args))
	for i, a := range call.args {
		g, err := c.bridgeArg(a)
		if err != nil {
			return nil, err
		}
		getters[i] = g
	}
	nout := 0
	switch {
	case call.script != nil:
		nout = len(call.script.results)
	case call.dyn != nil:
		nout = call.dyn.typ.NumOut()
	default:
		nout = call.fn.Type().NumOut()
	}
	if len(s.out) > nout {
		return nil, fmt.Errorf("%s: more names than results", call.name)
	}
	type store struct {
		off uintptr
		cl  layout
	}
	stores := make([]*store, len(s.out))
	for i, slot := range s.out {
		if slot < 0 {
			continue
		}
		field, ok := c.slotOf[slot]
		if !ok {
			return nil, fmt.Errorf("%s: a bound result has no slot", call.name)
		}
		cl := layoutOf(c.types[field])
		if cl == lBad {
			return nil, fmt.Errorf("%s: a bound result of type %s has no layout class", call.name, c.types[field])
		}
		stores[i] = &store{off: c.offs[field], cl: cl}
	}
	fn, spread, script := call.fn, call.spread, call.script
	var dynGet func(unsafe.Pointer, context.Context, map[string]any, any) (reflect.Value, error)
	if call.dyn != nil {
		g, err := c.bridgeArg(call.dyn)
		if err != nil {
			return nil, err
		}
		dynGet = g
	}
	c.bridged = append(c.bridged, fmt.Sprintf("%s (error-binding calls bridge)", call.name))
	name := call.name
	return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
		args := make([]reflect.Value, len(getters))
		for i, g := range getters {
			v, err := g(fr, ctx, st, d)
			if err != nil {
				return err
			}
			args[i] = v
		}
		var out []reflect.Value
		switch {
		case script != nil:
			var err error
			out, err = script.invoke(ctx, nil, args)
			if err != nil {
				return err
			}
		case dynGet != nil:
			fv, err := dynGet(fr, ctx, st, d)
			if err != nil {
				return err
			}
			if !fv.IsValid() || fv.IsNil() {
				return fmt.Errorf("exec: %s is nil, not a function", name)
			}
			out = fv.Call(args)
		case spread:
			out = fn.CallSlice(args)
		default:
			out = fn.Call(args)
		}
		for i, sp := range stores {
			if sp == nil || i >= len(out) {
				continue
			}
			v := out[i]
			cell := reflect.New(v.Type())
			cell.Elem().Set(v)
			p := cell.UnsafePointer()
			at := unsafe.Add(fr, sp.off)
			switch sp.cl {
			case lPtr:
				*(*unsafe.Pointer)(at) = *(*unsafe.Pointer)(p)
			case lStr:
				*(*string)(at) = *(*string)(p)
			case lIface:
				*(*ifacePair)(at) = *(*ifacePair)(p)
			case lSlice:
				*(*sliceHdr)(at) = *(*sliceHdr)(p)
			default:
				if sp.cl.float() {
					storeF(sp.cl, at, loadF(p, sp.cl))
				} else {
					storeN(sp.cl, at, loadN(p, sp.cl))
				}
			}
		}
		return nil
	}, nil
}
