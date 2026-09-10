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
			if cl != lPtr {
				return node{}, fmt.Errorf("an address cannot fill a %s parameter", cl)
			}
			c.frameEscapes = true
			off := c.offs[field]
			return node{class: lPtr, P: func(fr unsafe.Pointer, _ context.Context, _ map[string]any, _ any) (unsafe.Pointer, error) {
				return unsafe.Add(fr, off), nil
			}}, nil
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
			// rather than a copy saves the allocation, but is only
			// legal while the slot is written once: the frame outlives
			// the call and the callee may keep the interface. A slot
			// written more than once, one whose address the program
			// takes (a pointer method may write through it), and any
			// scalar, is copied instead, which is what the Go compiler
			// does anyway.
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

	case vaBinary, vaUnary, vaIndex, vaLen, vaFuncLit:
		n, err := c.valueNode(a)
		if err != nil {
			return node{}, err
		}
		if n.class == cl {
			return n, nil
		}
		if cl == lIface {
			return c.toIface(a.typ, pt, n)
		}
		return node{}, fmt.Errorf("a %s expression cannot fill a %s parameter", n.class, cl)
	}
	// Every kind is named above. Reaching here means a new one was
	// added without a case, and the previous shape of this switch sent
	// it to dynamicNode, which looked up the empty name, found nothing
	// and produced a nil interface: a wrong answer with no error. That
	// is what adding vaField did.
	return node{}, fmt.Errorf("argument kind %d has no node", a.kind)
}

// fieldNode reads one struct field out of a pointer. The offset is
// known when the program compiles, so the load is an add and a move
// rather than a call.
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

// dynamicNode compiles a value whose type is only known when it is
// read: from the caller's stack, or the pointer Scan was handed.
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

// callResultType is the static type of a call's i'th non-error result.
func callResultType(c *vmCall, i int) reflect.Type {
	ft := c.fn.Type()
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
