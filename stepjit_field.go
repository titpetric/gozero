package gozero

import (
	"context"
	"fmt"
	"reflect"
	"unsafe" // also required by go:linkname
)

// The struct field read on the direct tier: an address computed from
// compile-time offsets, then one typed load.

// fieldNode reads one struct field out of a pointer. The offset is
// known when the program compiles, so the load is an add and a move
// rather than a call.
func (c *jitCompiler) fieldNode(a *vmArg, pt reflect.Type, cl layout) (node, error) {
	load, sf, err := c.fieldAddr(a)
	if err != nil {
		return node{}, err
	}
	// An addressed field is the address load itself: the receiver of a
	// pointer method on a field value. A field behind a pointer is not
	// frame memory, but the frame-resident case is, so both mark the
	// escape conservatively.
	if a.addrOf {
		if cl != lPtr {
			return node{}, fmt.Errorf("an address cannot fill a %s parameter", cl)
		}
		c.frameEscapes = true
		return node{class: lPtr, P: load}, nil
	}

	fcl := layoutOf(sf.Type)
	if fcl == lBad {
		return node{}, fmt.Errorf("field %s of type %s has no layout class", sf.Name, sf.Type)
	}

	var out node
	if fcl.scalar() {
		if fcl.float() {
			out = node{class: fcl, F: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (float64, error) {
				at, err := load(fr, ctx, st, d)
				if err != nil {
					return 0, err
				}
				return loadF(at, fcl), nil
			}}
		} else {
			out = node{class: fcl, N: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (uint64, error) {
				at, err := load(fr, ctx, st, d)
				if err != nil {
					return 0, err
				}
				return loadN(at, fcl), nil
			}}
		}
	}
	switch fcl {
	case lPtr:
		out = node{class: lPtr, P: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (unsafe.Pointer, error) {
			at, err := load(fr, ctx, st, d)
			if err != nil {
				return nil, err
			}
			return *(*unsafe.Pointer)(at), nil
		}}
	case lStr:
		out = node{class: lStr, S: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (string, error) {
			at, err := load(fr, ctx, st, d)
			if err != nil {
				return "", err
			}
			return *(*string)(at), nil
		}}
	case lSlice:
		out = node{class: lSlice, L: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (sliceHdr, error) {
			at, err := load(fr, ctx, st, d)
			if err != nil {
				return sliceHdr{}, err
			}
			return *(*sliceHdr)(at), nil
		}}
	case lIface:
		out = node{class: lIface, I: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (ifacePair, error) {
			at, err := load(fr, ctx, st, d)
			if err != nil {
				return ifacePair{}, err
			}
			return *(*ifacePair)(at), nil
		}}
	}

	if out.class == cl {
		return out, nil
	}
	if cl == lIface {
		c.armStrBox(a)
		return c.toIface(sf.Type, pt, out)
	}
	return node{}, fmt.Errorf("a %s field cannot fill a %s parameter", out.class, cl)
}

// fieldAddr compiles the address of the value a field read denotes.
//
// The source is a pointer that is loaded and nil-checked, a struct
// slot in the frame, or another field holding a struct by value, whose
// offset adds onto its source's address; the recursion bottoms out at
// a slot or a pointer load, so offsets only ever add within one
// allocation. A multi-step index is a promoted field; flatField sums
// it when every hop is a struct held by value.
func (c *jitCompiler) fieldAddr(a *vmArg) (func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (unsafe.Pointer, error), reflect.StructField, error) {
	srcType := a.src.typ
	switch {
	case a.deref && srcType != nil && srcType.Kind() == reflect.Pointer && srcType.Elem().Kind() == reflect.Struct:
		off, sf, err := flatField(srcType.Elem(), a.index)
		if err != nil {
			return nil, reflect.StructField{}, err
		}
		src, err := c.argNode(a.src, srcType, lPtr)
		if err != nil {
			return nil, reflect.StructField{}, err
		}
		sp, name := src.P, sf.Name
		return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (unsafe.Pointer, error) {
			p, err := sp(fr, ctx, st, d)
			if err != nil {
				return nil, err
			}
			if p == nil {
				return nil, fmt.Errorf("exec: field %s read on a nil %s", name, srcType)
			}
			return unsafe.Add(p, off), nil
		}, sf, nil
	case !a.deref && a.src.kind == vaSlot && srcType != nil && srcType.Kind() == reflect.Struct:
		// The struct lives in the frame, so the field is at a fixed
		// offset from the frame pointer: no load, no nil to check.
		field, ok := c.slotOf[a.src.slot]
		if !ok {
			return nil, reflect.StructField{}, fmt.Errorf("a field source has no slot")
		}
		off, sf, err := flatField(srcType, a.index)
		if err != nil {
			return nil, reflect.StructField{}, err
		}
		at := c.offs[field] + off
		return func(fr unsafe.Pointer, _ context.Context, _ map[string]any, _ any) (unsafe.Pointer, error) {
			return unsafe.Add(fr, at), nil
		}, sf, nil
	case !a.deref && a.src.kind == vaField && srcType != nil && srcType.Kind() == reflect.Struct:
		base, _, err := c.fieldAddr(a.src)
		if err != nil {
			return nil, reflect.StructField{}, err
		}
		off, sf, err := flatField(srcType, a.index)
		if err != nil {
			return nil, reflect.StructField{}, err
		}
		return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (unsafe.Pointer, error) {
			at, err := base(fr, ctx, st, d)
			if err != nil {
				return nil, err
			}
			return unsafe.Add(at, off), nil
		}, sf, nil
	}
	return nil, reflect.StructField{}, fmt.Errorf("this field source is not in the table")
}

// flatField sums the offsets of a field index path and returns the
// field it lands on. Only value structs compose: a field promoted
// through an embedded pointer needs a load and a nil check per hop,
// so it stays on the reflect evaluator, where reflect walks the chain.
func flatField(st reflect.Type, index []int) (uintptr, reflect.StructField, error) {
	var off uintptr
	var sf reflect.StructField
	for _, idx := range index {
		if st.Kind() != reflect.Struct {
			return 0, sf, fmt.Errorf("a field promoted through an embedded %s is not in the table", st)
		}
		sf = st.Field(idx)
		off += sf.Offset
		st = sf.Type
	}
	return off, sf, nil
}
