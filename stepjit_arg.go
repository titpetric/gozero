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
	}
	// Every kind is named above. Reaching here means a new one was
	// added without a case, and the previous shape of this switch sent
	// it to dynamicNode, which looked up the empty name, found nothing
	// and produced a nil interface: a wrong answer with no error. That
	// is what adding vaField did.
	return node{}, fmt.Errorf("argument kind %d has no node", a.kind)
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
			bits, fl, ok := get(v)
			if !ok {
				return 0, fmt.Errorf("exec: variable %q: cannot use %T as %s", name, v, pt)
			}
			_ = bits
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
	reflect.TypeFor[int](): func(v any) (uint64, float64, bool) {
		n, ok := v.(int)
		return uint64(int64(n)), 0, ok
	},
	reflect.TypeFor[int8](): func(v any) (uint64, float64, bool) {
		n, ok := v.(int8)
		return uint64(uint8(n)), 0, ok
	},
	reflect.TypeFor[int16](): func(v any) (uint64, float64, bool) {
		n, ok := v.(int16)
		return uint64(uint16(n)), 0, ok
	},
	reflect.TypeFor[int32](): func(v any) (uint64, float64, bool) {
		n, ok := v.(int32)
		return uint64(uint32(n)), 0, ok
	},
	reflect.TypeFor[int64](): func(v any) (uint64, float64, bool) {
		n, ok := v.(int64)
		return uint64(n), 0, ok
	},
	reflect.TypeFor[uint](): func(v any) (uint64, float64, bool) {
		n, ok := v.(uint)
		return uint64(n), 0, ok
	},
	reflect.TypeFor[uint8](): func(v any) (uint64, float64, bool) {
		n, ok := v.(uint8)
		return uint64(n), 0, ok
	},
	reflect.TypeFor[uint16](): func(v any) (uint64, float64, bool) {
		n, ok := v.(uint16)
		return uint64(n), 0, ok
	},
	reflect.TypeFor[uint32](): func(v any) (uint64, float64, bool) {
		n, ok := v.(uint32)
		return uint64(n), 0, ok
	},
	reflect.TypeFor[uint64](): func(v any) (uint64, float64, bool) {
		n, ok := v.(uint64)
		return n, 0, ok
	},
	reflect.TypeFor[float32](): func(v any) (uint64, float64, bool) {
		f, ok := v.(float32)
		return 0, float64(f), ok
	},
	reflect.TypeFor[float64](): func(v any) (uint64, float64, bool) {
		f, ok := v.(float64)
		return 0, f, ok
	},
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
