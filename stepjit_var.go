package gozero

import (
	"context"
	"fmt"
	"reflect"
	"unsafe" // also required by go:linkname
)

// Value bindings on the direct tier. A value the host registered with
// Bind lives at one address for the life of the runtime, so a read is
// one load from a compile-time constant address, with no frame slot,
// no boxing and no reflect.
//
// A rebind is not visible to a program already compiled: the node
// captures the binding as it stood when the program compiled, and a
// compiled program is cached per source string, so a later Bind under
// the same name reaches neither.

// bridgeRef resolves a value binding or a pointer read for the
// reflect bridge, which is where they land when the call around them
// is outside the shape table.
func (c *jitCompiler) bridgeRef(a *vmArg) (func(unsafe.Pointer, context.Context, map[string]any, any) (reflect.Value, error), error) {
	if a.kind == vaVar {
		// The Value is fixed at compile time, so it is resolved once
		// here and handed over per call.
		if a.addrOf {
			return nil, fmt.Errorf("an addressed value binding has no cell")
		}
		v := a.varv
		return func(unsafe.Pointer, context.Context, map[string]any, any) (reflect.Value, error) { return v, nil }, nil
	}

	inner, err := c.bridgeArg(a.src)
	if err != nil {
		return nil, err
	}
	name := a.name
	return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (reflect.Value, error) {
		p, err := inner(fr, ctx, st, d)
		if err != nil {
			return reflect.Value{}, err
		}
		if p.Kind() != reflect.Pointer {
			return reflect.Value{}, fmt.Errorf("exec: cannot dereference %s", p.Type())
		}
		if p.IsNil() {
			return reflect.Value{}, fmt.Errorf("exec: nil pointer dereference reading *%s", name)
		}
		return p.Elem(), nil
	}, nil
}

// varNode compiles a value-binding read to a load from the binding's
// fixed address. An addressed occurrence never reaches here: it
// became a frame slot when the program was assembled, which is what
// gives the program storage of its own to write.
func (c *jitCompiler) varNode(a *vmArg, pt reflect.Type, cl layout) (node, error) {
	if a.addrOf {
		return node{}, fmt.Errorf("an addressed value binding has no cell")
	}
	st := a.varv.Type()
	at := varAddr(a)
	scl := layoutOf(st)
	if scl == lBad {
		return node{}, fmt.Errorf("a value binding of type %s has no layout class", st)
	}

	// An interface parameter fed from a concrete binding needs the
	// itab, which is fixed by the pair of types the way it is for a
	// slot.
	if cl == lIface && st.Kind() != reflect.Interface {
		tab, ok := itabFor(st, pt)
		if !ok {
			return node{}, fmt.Errorf("%s does not implement %s", st, pt)
		}
		if scl == lPtr {
			return node{class: lIface, I: func(unsafe.Pointer, context.Context, map[string]any, any) (ifacePair, error) {
				return ifacePair{tab: tab, data: *(*unsafe.Pointer)(at)}, nil
			}}, nil
		}
		// Wider than a word, so the interface points at the
		// binding's own storage rather than a copy. That is sound
		// for the same reason the frame alias is, and more so: the
		// binding's storage outlives the runtime, and nothing writes
		// it, because a program that writes the name writes its cell.
		return node{class: lIface, I: func(unsafe.Pointer, context.Context, map[string]any, any) (ifacePair, error) {
			return ifacePair{tab: tab, data: at}, nil
		}}, nil
	}

	if scl != cl {
		return node{}, fmt.Errorf("a %s value binding cannot fill a %s parameter", scl, cl)
	}
	return varLoad(at, cl), nil
}

// varAddr is the fixed address of a binding's storage, copied into a
// cell once at compile time because reflect.ValueOf gives a value no
// address. Nothing writes it: the read is the only user.
func varAddr(a *vmArg) unsafe.Pointer {
	v := a.varv
	if v.CanAddr() {
		return v.Addr().UnsafePointer()
	}
	cell := reflect.New(v.Type())
	cell.Elem().Set(v)
	return cell.UnsafePointer()
}

// varLoad reads the class at a fixed address.
func varLoad(at unsafe.Pointer, cl layout) node {
	if cl.scalar() {
		if cl.float() {
			return node{class: cl, F: func(unsafe.Pointer, context.Context, map[string]any, any) (float64, error) {
				return loadF(at, cl), nil
			}}
		}
		return node{class: cl, N: func(unsafe.Pointer, context.Context, map[string]any, any) (uint64, error) {
			return loadN(at, cl), nil
		}}
	}
	switch cl {
	case lPtr:
		return node{class: lPtr, P: func(unsafe.Pointer, context.Context, map[string]any, any) (unsafe.Pointer, error) {
			return *(*unsafe.Pointer)(at), nil
		}}
	case lStr:
		return node{class: lStr, S: func(unsafe.Pointer, context.Context, map[string]any, any) (string, error) {
			return *(*string)(at), nil
		}}
	case lSlice:
		return node{class: lSlice, L: func(unsafe.Pointer, context.Context, map[string]any, any) (sliceHdr, error) {
			return *(*sliceHdr)(at), nil
		}}
	default:
		return node{class: lIface, I: func(unsafe.Pointer, context.Context, map[string]any, any) (ifacePair, error) {
			return *(*ifacePair)(at), nil
		}}
	}
}

// bridgeSlice builds a slice literal for the reflect bridge. The
// literal allocates through reflect either way, so the bridge is its
// whole implementation for now.
func (c *jitCompiler) bridgeSlice(a *vmArg) (func(unsafe.Pointer, context.Context, map[string]any, any) (reflect.Value, error), error) {
	// A slice literal builds through reflect either way, so the
	// bridge is the whole implementation of it for now.
	elems := make([]func(unsafe.Pointer, context.Context, map[string]any, any) (reflect.Value, error), len(a.elems))
	for i := range a.elems {
		g, err := c.bridgeArg(a.elems[i].val)
		if err != nil {
			return nil, err
		}
		elems[i] = g
	}
	styp := a.styp
	return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (reflect.Value, error) {
		sv := reflect.MakeSlice(styp, len(elems), len(elems))
		for i, g := range elems {
			v, err := g(fr, ctx, st, d)
			if err != nil {
				return reflect.Value{}, err
			}
			sv.Index(i).Set(v)
		}
		return sv, nil
	}, nil
}
