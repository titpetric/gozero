package gozero

import (
	"context"
	"fmt"
	"reflect"
	"unsafe" // also required by go:linkname
)

// Value bindings on the direct tier. A binding the host registered
// with BindVar lives at one address for the life of the runtime: a
// package variable, or the cell behind the pointer the host passed.
// The address is therefore a compile-time constant and a read is one
// load from it, with no frame slot, no boxing and no reflect.
//
// Reading the storage rather than folding the value into the node is
// what makes a mutable binding track the host's writes to the
// variable. It does not make a rebind visible: the node captures the
// binding as it stood when the program compiled, and a compiled
// program is cached per source string, so a later BindVar under the
// same name reaches neither. That is the rule Bind already has.

// bridgeRef resolves a value binding or a pointer read for the
// reflect bridge, which is where they land when the call around them
// is outside the shape table.
func (c *jitCompiler) bridgeRef(a *vmArg) (func(unsafe.Pointer, context.Context, map[string]any, any) (reflect.Value, error), error) {
	if a.kind == vaVar {
		// The Value is fixed at compile time; for a mutable binding it
		// reads through to the host's storage, so it is resolved once
		// here and read per call.
		v := a.varv
		if a.addrOf {
			if !v.CanAddr() {
				return nil, fmt.Errorf("%s is not addressable", a.name)
			}
			pv := v.Addr()
			return func(unsafe.Pointer, context.Context, map[string]any, any) (reflect.Value, error) { return pv, nil }, nil
		}
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
// fixed address.
func (c *jitCompiler) varNode(a *vmArg, pt reflect.Type, cl layout) (node, error) {
	st := a.varv.Type()
	if a.addrOf {
		if cl != lPtr {
			return node{}, fmt.Errorf("an address cannot fill a %s parameter", cl)
		}
		at, err := varAddr(a)
		if err != nil {
			return node{}, err
		}
		return node{class: lPtr, P: func(unsafe.Pointer, context.Context, map[string]any, any) (unsafe.Pointer, error) {
			return at, nil
		}}, nil
	}

	at, err := varAddr(a)
	if err != nil {
		return node{}, err
	}
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
		// Wider than a word, so the interface points at the binding's
		// own storage rather than a copy. That is sound for the same
		// reason the frame alias is: the storage outlives every run,
		// and here it outlives the runtime. A mutable binding is the
		// exception, because a later write would be visible through an
		// interface a callee kept, so it copies.
		if !a.mutable {
			return node{class: lIface, I: func(unsafe.Pointer, context.Context, map[string]any, any) (ifacePair, error) {
				return ifacePair{tab: tab, data: at}, nil
			}}, nil
		}
		return c.toIface(st, pt, varLoad(at, scl))
	}

	if scl != cl {
		return node{}, fmt.Errorf("a %s value binding cannot fill a %s parameter", scl, cl)
	}
	return varLoad(at, cl), nil
}

// varAddr is the fixed address of a binding's storage. The Value is
// addressable exactly when the host passed a pointer; an immutable
// binding is copied into a fresh addressable cell once, so both read
// through the same node shape and only the mutable one tracks later
// writes.
func varAddr(a *vmArg) (unsafe.Pointer, error) {
	v := a.varv
	if v.CanAddr() {
		return v.Addr().UnsafePointer(), nil
	}
	if a.addrOf {
		return nil, fmt.Errorf("%s is not addressable", a.name)
	}
	cell := reflect.New(v.Type())
	cell.Elem().Set(v)
	return cell.UnsafePointer(), nil
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
