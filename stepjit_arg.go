package gozero

import (
	"context"
	"fmt"
	"reflect"
	"unsafe" // also required by go:linkname
)

// argNode compiles one argument to the class its parameter wants.
func (c *jitCompiler) argNode(a *vmArg, pt reflect.Type, cl layout) (node, error) {
	switch a.kind {
	case vaCall:
		sub, err := c.exprNode(a.sub)
		if err != nil {
			return node{}, err
		}
		if sub.class == cl {
			return sub, nil
		}
		if cl == lIface {
			c.armStrBox(a)
			return c.toIface(callResultType(a.sub, 0), pt, sub)
		}
		return node{}, fmt.Errorf("a %s result cannot fill a %s parameter", sub.class, cl)

	case vaSlot:
		if a.addrOf {
			// The frame is the variable's storage, so the address of a
			// name is an offset from the frame pointer: no load at all.
			field, ok := c.slotOf[a.slot]
			if !ok {
				return node{}, fmt.Errorf("an addressed name has no slot")
			}
			if cl != lPtr && cl != lIface {
				return node{}, fmt.Errorf("an address cannot fill a %s parameter", cl)
			}
			c.frameEscapes = true
			off := c.offs[field]
			at := node{class: lPtr, P: func(fr unsafe.Pointer, _ context.Context, _ map[string]any, _ any) (unsafe.Pointer, error) {
				return unsafe.Add(fr, off), nil
			}}
			if cl == lPtr {
				return at, nil
			}
			// An address filling an interface parameter is the same
			// pointer with an itab in front of it, and the itab for
			// (*T, I) is fixed at compile time like any other.
			return c.toIface(reflect.PointerTo(c.types[field]), pt, at)
		}
		if producer := c.splices[a]; producer != nil {
			sub, err := c.exprNode(producer)
			if err != nil {
				return node{}, err
			}
			if sub.class == cl {
				return sub, nil
			}
			if cl == lIface {
				c.armStrBox(a)
				return c.toIface(callResultType(producer, 0), pt, sub)
			}
			return node{}, fmt.Errorf("a %s result cannot fill a %s parameter", sub.class, cl)
		}
		field, ok := c.slotOf[a.slot]
		if !ok {
			return node{}, fmt.Errorf("a name has no slot")
		}
		off, st := c.offs[field], c.types[field]
		if cl == lIface && st.Kind() != reflect.Interface {
			tab, ok := itabFor(st, pt)
			if !ok {
				return node{}, fmt.Errorf("%s does not implement %s", st, pt)
			}
			if layoutOf(st) == lPtr {
				// Stored directly: the data word is the pointer itself,
				// read out of the slot, so nothing aliases the frame.
				return node{class: lIface, I: func(fr unsafe.Pointer, ctx context.Context, _ map[string]any, _ any) (ifacePair, error) {
					return ifacePair{tab: tab, data: *(*unsafe.Pointer)(unsafe.Add(fr, off))}, nil
				}}, nil
			}
			// Stored indirectly. Pointing the interface at the slot
			// and skipping a copy saves the allocation, but is only
			// legal while the slot takes one write: the frame outlives
			// the call and the callee may keep the interface. A slot
			// written more than once, one whose address the program
			// takes (a pointer method may write through it), and any
			// scalar, gets a copy, as the Go compiler does anyway.
			if c.writes[a.slot] == 1 && !c.addr[a.slot] && !layoutOf(st).scalar() {
				c.frameEscapes = true
				return node{class: lIface, I: func(fr unsafe.Pointer, ctx context.Context, _ map[string]any, _ any) (ifacePair, error) {
					return ifacePair{tab: tab, data: unsafe.Add(fr, off)}, nil
				}}, nil
			}
			return c.toIface(st, pt, slotNode(layoutOf(st), off))
		}
		if layoutOf(st) != cl {
			return node{}, fmt.Errorf("a %s name cannot fill a %s parameter", layoutOf(st), cl)
		}
		return slotNode(cl, off), nil

	case vaConst:
		return constNode(a.val, cl)

	case vaStruct:
		return c.structArgNode(a, pt, cl)

	case vaVar:
		return c.varNode(a, pt, cl)

	case vaCtx:
		// The execution context is already the exact interface type the
		// parameter wants, so the two words copy straight through.
		return node{class: lIface, I: func(_ unsafe.Pointer, ctx context.Context, _ map[string]any, _ any) (ifacePair, error) {
			return *(*ifacePair)(unsafe.Pointer(&ctx)), nil
		}}, nil

	case vaField:
		return c.fieldNode(a, pt, cl)

	case vaStack, vaDest:
		return c.dynamicNode(a, pt, cl)
	}
	// The switch above names every kind. Reaching here means a new kind
	// has no case. Sending it to dynamicNode would look up the empty
	// name, find nothing and produce a nil interface: a wrong value with
	// no error, the failure vaField hit when it went in.
	return node{}, fmt.Errorf("argument kind %d has no node", a.kind)
}

// fieldNode reads one struct field out of a pointer. The compiler
// computes the offset, so the load is an add and a move
// and needs no call.
//
// Only a single field of a pointer to a struct is in the table. A
// deeper index reaches through embedded types whose offsets do not
// simply add when one of them is itself a pointer, so those go to the
// reflect evaluator.
func (c *jitCompiler) fieldNode(a *vmArg, pt reflect.Type, cl layout) (node, error) {
	if len(a.index) != 1 {
		return node{}, fmt.Errorf("only a single field is in the table")
	}
	srcType := a.src.typ

	var load func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (unsafe.Pointer, error)
	var sf reflect.StructField
	switch {
	case a.deref && srcType != nil && srcType.Kind() == reflect.Pointer && srcType.Elem().Kind() == reflect.Struct:
		sf = srcType.Elem().Field(a.index[0])
		src, err := c.argNode(a.src, srcType, lPtr)
		if err != nil {
			return node{}, err
		}
		sp, off, name := src.P, sf.Offset, sf.Name
		load = func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (unsafe.Pointer, error) {
			p, err := sp(fr, ctx, st, d)
			if err != nil {
				return nil, err
			}
			if p == nil {
				return nil, fmt.Errorf("exec: field %s read on a nil %s", name, srcType)
			}
			return unsafe.Add(p, off), nil
		}
	case !a.deref && a.src.kind == vaSlot && srcType != nil && srcType.Kind() == reflect.Struct:
		// The struct lives in the frame, so the field is at a fixed
		// offset from the frame pointer: no load, no nil to check.
		field, ok := c.slotOf[a.src.slot]
		if !ok {
			return node{}, fmt.Errorf("a field source has no slot")
		}
		sf = srcType.Field(a.index[0])
		at := c.offs[field] + sf.Offset
		load = func(fr unsafe.Pointer, _ context.Context, _ map[string]any, _ any) (unsafe.Pointer, error) {
			return unsafe.Add(fr, at), nil
		}
	default:
		return node{}, fmt.Errorf("this field source is not in the table")
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

// dynamicNode compiles a value whose type only shows at the read:
// from the caller's stack, or the pointer passed to Scan.
func (c *jitCompiler) dynamicNode(a *vmArg, pt reflect.Type, cl layout) (node, error) {
	isDest := a.kind == vaDest
	name := a.name
	if isDest {
		name = "dest"
	}
	switch cl {
	case lStr:
		if isDest {
			return node{}, fmt.Errorf("dest cannot fill a string parameter")
		}
		if idx, ok := c.stackFields[name]; ok {
			off := c.offs[idx]
			return node{class: lStr, S: func(fr unsafe.Pointer, _ context.Context, _ map[string]any, _ any) (string, error) {
				v := *(*any)(unsafe.Add(fr, off))
				if v == nil {
					return "", nil
				}
				s, ok := v.(string)
				if !ok {
					return "", fmt.Errorf("exec: variable %q: cannot use %T as string", name, v)
				}
				return s, nil
			}}, nil
		}
		return node{class: lStr, S: func(_ unsafe.Pointer, _ context.Context, st map[string]any, _ any) (string, error) {
			v, ok := st[name]
			if !ok || v == nil {
				return "", nil
			}
			s, ok := v.(string)
			if !ok {
				return "", fmt.Errorf("exec: variable %q: cannot use %T as string", name, v)
			}
			return s, nil
		}}, nil
	case lIface:
		conv, ok := ifaceConvs[pt]
		if !ok {
			return node{}, fmt.Errorf("%s is not in ifaceConvs", pt)
		}
		if isDest {
			return node{class: lIface, I: func(_ unsafe.Pointer, _ context.Context, _ map[string]any, d any) (ifacePair, error) {
				if d == nil {
					return ifacePair{}, fmt.Errorf("exec: dest is only set by Scan")
				}
				pair, ok := conv(d)
				if !ok {
					return ifacePair{}, fmt.Errorf("exec: variable %q: cannot use %T as %s", name, d, pt)
				}
				return pair, nil
			}}, nil
		}
		if idx, ok := c.stackFields[name]; ok {
			off := c.offs[idx]
			return node{class: lIface, I: func(fr unsafe.Pointer, _ context.Context, _ map[string]any, _ any) (ifacePair, error) {
				v := *(*any)(unsafe.Add(fr, off))
				if v == nil {
					return ifacePair{}, nil
				}
				pair, ok := conv(v)
				if !ok {
					return ifacePair{}, fmt.Errorf("exec: variable %q: cannot use %T as %s", name, v, pt)
				}
				return pair, nil
			}}, nil
		}
		return node{class: lIface, I: func(_ unsafe.Pointer, _ context.Context, st map[string]any, _ any) (ifacePair, error) {
			v, ok := st[name]
			if !ok || v == nil {
				return ifacePair{}, nil
			}
			pair, ok := conv(v)
			if !ok {
				return ifacePair{}, fmt.Errorf("exec: variable %q: cannot use %T as %s", name, v, pt)
			}
			return pair, nil
		}}, nil
	}
	if cl.scalar() {
		if isDest {
			return node{}, fmt.Errorf("dest cannot fill a %s parameter", cl)
		}
		return stackScalarNode(name, pt, cl)
	}
	return node{}, fmt.Errorf("a stack value cannot fill a %s parameter", cl)
}

// stackScalarNode reads a scalar off the caller's stack. The stack
// holds any, so the read is one type assertion against the exact
// parameter type; a named scalar type would need reflect to check and
// stays on the reflect evaluator. An unset or nil entry is the zero
// value, like every other stack read.
func stackScalarNode(name string, pt reflect.Type, cl layout) (node, error) {
	get := stackScalarConvs[pt]
	if get == nil {
		return node{}, fmt.Errorf("a stack value of named type %s stays on the reflect tier", pt)
	}
	if cl.float() {
		return node{class: cl, F: func(_ unsafe.Pointer, _ context.Context, st map[string]any, _ any) (float64, error) {
			v, ok := st[name]
			if !ok || v == nil {
				return 0, nil
			}
			_, fl, ok := get(v)
			if !ok {
				return 0, fmt.Errorf("exec: variable %q: cannot use %T as %s", name, v, pt)
			}
			return fl, nil
		}}, nil
	}
	return node{class: cl, N: func(_ unsafe.Pointer, _ context.Context, st map[string]any, _ any) (uint64, error) {
		v, ok := st[name]
		if !ok || v == nil {
			return 0, nil
		}
		bits, _, ok := get(v)
		if !ok {
			return 0, fmt.Errorf("exec: variable %q: cannot use %T as %s", name, v, pt)
		}
		return bits, nil
	}}, nil
}

// intBits asserts a stack any to the signed type T and reports its
// bits. Sign-extension past T's width is invisible: storeN writes the
// parameter's width, so only the low bits are ever read back.
func intBits[T int | int8 | int16 | int32 | int64]() func(any) (uint64, float64, bool) {
	return func(v any) (uint64, float64, bool) {
		n, ok := v.(T)
		return uint64(int64(n)), 0, ok
	}
}

// uintBits asserts a stack any to the unsigned type T and reports its
// bits.
func uintBits[T uint | uint8 | uint16 | uint32 | uint64]() func(any) (uint64, float64, bool) {
	return func(v any) (uint64, float64, bool) {
		n, ok := v.(T)
		return uint64(n), 0, ok
	}
}

// floatBits asserts a stack any to the float type T and reports it on
// the float lane.
func floatBits[T float32 | float64]() func(any) (uint64, float64, bool) {
	return func(v any) (uint64, float64, bool) {
		f, ok := v.(T)
		return 0, float64(f), ok
	}
}

// stackScalarConvs asserts a stack any to each predeclared scalar type
// and reports its bits. Keyed by exact type, so a named type misses.
var stackScalarConvs = map[reflect.Type]func(any) (uint64, float64, bool){
	reflect.TypeFor[bool](): func(v any) (uint64, float64, bool) {
		b, ok := v.(bool)
		if b {
			return 1, 0, ok
		}
		return 0, 0, ok
	},
	reflect.TypeFor[int]():     intBits[int](),
	reflect.TypeFor[int8]():    intBits[int8](),
	reflect.TypeFor[int16]():   intBits[int16](),
	reflect.TypeFor[int32]():   intBits[int32](),
	reflect.TypeFor[int64]():   intBits[int64](),
	reflect.TypeFor[uint]():    uintBits[uint](),
	reflect.TypeFor[uint8]():   uintBits[uint8](),
	reflect.TypeFor[uint16]():  uintBits[uint16](),
	reflect.TypeFor[uint32]():  uintBits[uint32](),
	reflect.TypeFor[uint64]():  uintBits[uint64](),
	reflect.TypeFor[float32](): floatBits[float32](),
	reflect.TypeFor[float64](): floatBits[float64](),
}

// callResultType returns the static type for a call's i'th non-error result.
func callResultType(c *vmCall, i int) reflect.Type {
	ft := c.ft
	n := 0
	for j := 0; j < ft.NumOut(); j++ {
		if j == c.errIdx {
			continue
		}
		if n == i {
			return ft.Out(j)
		}
		n++
	}
	return nil
}
