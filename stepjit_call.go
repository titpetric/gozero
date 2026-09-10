package gozero

import (
	"context"
	"fmt"
	"reflect"
	"unsafe" // also required by go:linkname
)

// exprNode compiles one call and its arguments. A call the shape table
// cannot express compiles to a reflect bridge instead of failing the
// program: one call pays reflect.Value.Call, its neighbours stay
// direct. The vmCall tree was already type-checked by the program
// compiler, so a miss here is a capability gap, never a type error.
func (c *jitCompiler) exprNode(call *vmCall) (node, error) {
	if call.script != nil || call.dyn != nil {
		return c.scriptCallNode(call)
	}
	n, err := c.directNode(call)
	if err == nil {
		return n, nil
	}
	c.bridged = append(c.bridged, fmt.Sprintf("%s (%v)", call.name, err))
	return c.bridgeNode(call)
}

// directNode is the in-table compilation.
func (c *jitCompiler) directNode(call *vmCall) (node, error) {
	ft := call.fn.Type()
	// A variadic function is ABI-identical to the same signature with
	// the variadic parameter as a plain slice: the callee receives the
	// slice header either way, verified in TestVariadicSliceABI. A
	// spread call passes the slice it was given; a packed call builds
	// one from its element nodes, which is the allocation the Go
	// compiler makes at an escaping call site.
	fixed := ft.NumIn()
	if ft.IsVariadic() && !call.spread {
		fixed--
	}
	args := make([]node, 0, ft.NumIn())
	key := ""
	for i := 0; i < fixed; i++ {
		pt := ft.In(i)
		cl := layoutOf(pt)
		if cl == lBad {
			return node{}, fmt.Errorf("%s: parameter %d of type %s has no layout class", call.name, i+1, pt)
		}
		key += cl.String()
		a, err := c.argNode(call.args[i], pt, cl)
		if err != nil {
			return node{}, fmt.Errorf("%s: argument %d: %w", call.name, i+1, err)
		}
		args = append(args, a)
	}
	if ft.IsVariadic() && !call.spread {
		st := ft.In(ft.NumIn() - 1)
		pack, err := c.packNode(call, st, call.args[fixed:])
		if err != nil {
			return node{}, fmt.Errorf("%s: %w", call.name, err)
		}
		key += lSlice.String()
		args = append(args, pack)
	}
	key += "_"
	for j := 0; j < ft.NumOut(); j++ {
		if j == call.errIdx {
			key += lErr.String()
			continue
		}
		cl := layoutOf(ft.Out(j))
		if cl == lBad {
			return node{}, fmt.Errorf("%s: result %d of type %s has no layout class", call.name, j+1, ft.Out(j))
		}
		key += cl.String()
	}

	n, ok := callNode(key, funcPtr(call.fn.Interface()), args)
	if !ok {
		return node{}, fmt.Errorf("%s: shape %q is not in the table", call.name, key)
	}
	return n, nil
}

// packNode builds the variadic slice from the element nodes, for the
// element types worth special-casing: []any and []string cover the
// printf and assert families. Anything else bridges. The slice is
// allocated per call because the callee may keep it, which is the same
// escape the Go compiler assumes at a call site whose arguments escape.
func (c *jitCompiler) packNode(call *vmCall, st reflect.Type, elems []*vmArg) (node, error) {
	et := st.Elem()
	cl := layoutOf(et)
	if cl == lBad {
		return node{}, fmt.Errorf("a variadic element of type %s has no layout class", et)
	}
	nodes := make([]node, len(elems))
	for i, a := range elems {
		n, err := c.argNode(a, et, cl)
		if err != nil {
			return node{}, err
		}
		nodes[i] = n
	}
	// The binding contract lets the pack reuse a pooled backing
	// array instead of allocating one per call; the slice the callee
	// receives is dead when the call returns, and finish repools it.
	// takeBacking is nil for an ordinary call, and the pack allocates
	// fresh.
	var takeBacking func(fr unsafe.Pointer) unsafe.Pointer
	if site, ok := c.packOf[call]; ok && len(nodes) > 0 {
		off := c.offs[site.field]
		takeBacking = func(fr unsafe.Pointer) unsafe.Pointer {
			block := site.take()
			*(*unsafe.Pointer)(unsafe.Add(fr, off)) = block
			return block
		}
	}
	switch {
	case et.Kind() == reflect.Interface && et.NumMethod() == 0:
		getters := make([]nodeI, len(nodes))
		for i, n := range nodes {
			getters[i] = n.I
		}
		take := takeBacking
		return node{class: lSlice, L: func(fr unsafe.Pointer, ctx context.Context, stk map[string]any, d any) (sliceHdr, error) {
			var out []any
			if take != nil {
				h := sliceHdr{ptr: take(fr), len: len(getters), cap: len(getters)}
				out = *(*[]any)(unsafe.Pointer(&h))
			} else {
				out = make([]any, len(getters))
			}
			for i, g := range getters {
				pair, err := g(fr, ctx, stk, d)
				if err != nil {
					return sliceHdr{}, err
				}
				// A typed pointer store into the slice element keeps
				// the write barrier.
				*(*ifacePair)(unsafe.Pointer(&out[i])) = pair
			}
			return *(*sliceHdr)(unsafe.Pointer(&out)), nil
		}}, nil
	case et.Kind() == reflect.String:
		getters := make([]nodeS, len(nodes))
		for i, n := range nodes {
			getters[i] = n.S
		}
		take := takeBacking
		return node{class: lSlice, L: func(fr unsafe.Pointer, ctx context.Context, stk map[string]any, d any) (sliceHdr, error) {
			var out []string
			if take != nil {
				h := sliceHdr{ptr: take(fr), len: len(getters), cap: len(getters)}
				out = *(*[]string)(unsafe.Pointer(&h))
			} else {
				out = make([]string, len(getters))
			}
			for i, g := range getters {
				v, err := g(fr, ctx, stk, d)
				if err != nil {
					return sliceHdr{}, err
				}
				out[i] = v
			}
			return *(*sliceHdr)(unsafe.Pointer(&out)), nil
		}}, nil
	}
	return node{}, fmt.Errorf("packing []%s is not in the table", et)
}

// bridgeNode compiles a call to one reflect.Value.Call, its arguments
// resolved against the same frame the direct calls use. A slot argument
// is read in place through reflect.NewAt, so the bridge shares state
// with its JIT'd neighbours rather than needing the reflect
// evaluator's slot array.
func (c *jitCompiler) bridgeNode(call *vmCall) (node, error) {
	getters := make([]func(unsafe.Pointer, context.Context, map[string]any, any) (reflect.Value, error), len(call.args))
	for i, a := range call.args {
		g, err := c.bridgeArg(a)
		if err != nil {
			return node{}, err
		}
		getters[i] = g
	}

	fn, name, errIdx, spread := call.fn, call.name, call.errIdx, call.spread
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
		if spread {
			out = fn.CallSlice(args)
		} else {
			out = fn.Call(args)
		}
		if errIdx >= 0 {
			if e := out[errIdx]; !e.IsNil() {
				return nil, e.Interface().(error)
			}
		}
		return out, nil
	}
	_ = name

	if call.nres == 0 {
		return node{class: lNone, E: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
			_, err := invoke(fr, ctx, st, d)
			return err
		}}, nil
	}
	rt := callResultType(call, 0)
	cl := layoutOf(rt)
	if cl == lBad {
		return node{}, fmt.Errorf("%s: a bridged result of type %s has no layout class", call.name, rt)
	}
	resIdx := 0
	if call.errIdx == 0 {
		resIdx = 1
	}
	// The result value's words are read out of a typed cell, which is
	// the boxing reflect did anyway.
	pick := func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (unsafe.Pointer, error) {
		out, err := invoke(fr, ctx, st, d)
		if err != nil {
			return nil, err
		}
		cell := reflect.New(rt)
		cell.Elem().Set(out[resIdx])
		return cell.UnsafePointer(), nil
	}
	switch {
	case cl == lPtr:
		return node{class: cl, P: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (unsafe.Pointer, error) {
			at, err := pick(fr, ctx, st, d)
			if err != nil {
				return nil, err
			}
			return *(*unsafe.Pointer)(at), nil
		}}, nil
	case cl == lStr:
		return node{class: cl, S: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (string, error) {
			at, err := pick(fr, ctx, st, d)
			if err != nil {
				return "", err
			}
			return *(*string)(at), nil
		}}, nil
	case cl == lSlice:
		return node{class: cl, L: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (sliceHdr, error) {
			at, err := pick(fr, ctx, st, d)
			if err != nil {
				return sliceHdr{}, err
			}
			return *(*sliceHdr)(at), nil
		}}, nil
	case cl == lIface:
		return node{class: cl, I: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (ifacePair, error) {
			at, err := pick(fr, ctx, st, d)
			if err != nil {
				return ifacePair{}, err
			}
			return *(*ifacePair)(at), nil
		}}, nil
	case cl.float():
		return node{class: cl, F: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (float64, error) {
			at, err := pick(fr, ctx, st, d)
			if err != nil {
				return 0, err
			}
			return loadF(at, cl), nil
		}}, nil
	default:
		return node{class: cl, N: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (uint64, error) {
			at, err := pick(fr, ctx, st, d)
			if err != nil {
				return 0, err
			}
			return loadN(at, cl), nil
		}}, nil
	}
}
