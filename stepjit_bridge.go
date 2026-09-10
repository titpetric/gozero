package gozero

import (
	"context"
	"fmt"
	"reflect"
	"unsafe" // also required by go:linkname
)

// The bridge argument plumbing: every argument kind materialized as
// a reflect value for the per-call reflect bridge, the script call
// boundary and the deferred calls.

// bridgeArg resolves one argument of a bridged call to a reflect.Value.
func (c *jitCompiler) bridgeArg(a *vmArg) (func(unsafe.Pointer, context.Context, map[string]any, any) (reflect.Value, error), error) {
	switch a.kind {
	case vaConst:
		v := a.val
		return func(unsafe.Pointer, context.Context, map[string]any, any) (reflect.Value, error) { return v, nil }, nil

	case vaCtx:
		return func(_ unsafe.Pointer, ctx context.Context, _ map[string]any, _ any) (reflect.Value, error) {
			return reflect.ValueOf(ctx), nil
		}, nil

	case vaFuncLit:
		if len(a.caps) > 0 {
			return nil, fmt.Errorf("a capturing closure stays on the reflect tier")
		}
		fn, ft := a.fnLit, a.litType
		return func(_ unsafe.Pointer, ctx context.Context, _ map[string]any, _ any) (reflect.Value, error) {
			return fn.materialize(ctx, ft, nil), nil
		}, nil

	case vaBinary, vaUnary, vaIndex, vaLen:
		n, err := c.valueNode(a)
		if err != nil {
			return nil, err
		}
		return c.nodeToValue(a.typ, n)

	case vaSlot:
		if producer := c.splices[a]; producer != nil {
			sub, err := c.exprNode(producer)
			if err != nil {
				return nil, err
			}
			return c.nodeToValue(callResultType(producer, 0), sub)
		}
		field, ok := c.slotOf[a.slot]
		if !ok {
			return nil, fmt.Errorf("a bridged name has no slot")
		}
		off, st := c.offs[field], c.types[field]
		if a.addrOf {
			// NewAt of the frame slot is the address the receiver
			// wants, already typed *T.
			if reflect.PointerTo(st) != a.typ {
				return nil, fmt.Errorf("cannot use *%s as %s", st, a.typ)
			}
			c.frameEscapes = true
			return func(fr unsafe.Pointer, _ context.Context, _ map[string]any, _ any) (reflect.Value, error) {
				return reflect.NewAt(st, unsafe.Add(fr, off)), nil
			}, nil
		}
		if !st.AssignableTo(a.typ) {
			return nil, fmt.Errorf("cannot use %s as %s", st, a.typ)
		}
		// The frame is typed storage, so the value is read in place.
		return func(fr unsafe.Pointer, _ context.Context, _ map[string]any, _ any) (reflect.Value, error) {
			return reflect.NewAt(st, unsafe.Add(fr, off)).Elem(), nil
		}, nil

	case vaStack, vaDest:
		arg, typ := a, a.typ
		return func(_ unsafe.Pointer, _ context.Context, stack map[string]any, dest any) (reflect.Value, error) {
			v := dest
			name := "dest"
			if arg.kind == vaStack {
				var ok bool
				name = arg.name
				v, ok = stack[name]
				if !ok || v == nil {
					return reflect.Zero(typ), nil
				}
			} else if v == nil {
				return reflect.Value{}, fmt.Errorf("exec: dest is only set by Scan")
			}
			rv := reflect.ValueOf(v)
			if !arg.assignable(rv.Type()) {
				return reflect.Value{}, fmt.Errorf("exec: variable %q: cannot use %s as %s", name, rv.Type(), typ)
			}
			return rv, nil
		}, nil

	case vaCall:
		sub, err := c.exprNode(a.sub)
		if err != nil {
			return nil, err
		}
		rt := callResultType(a.sub, 0)
		return c.nodeToValue(rt, sub)

	case vaField:
		fieldNode, err := c.argNode(a, a.typ, layoutOf(a.typ))
		if err != nil {
			return nil, err
		}
		return c.nodeToValue(a.typ, fieldNode)

	case vaStruct:
		// The literal builds directly and the bridge reads the block in
		// place; reflect copies it into the callee's frame like any
		// argument.
		n, err := c.structNode(a)
		if err != nil {
			return nil, err
		}
		styp, addr, f := a.styp, a.addr, n.P
		return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (reflect.Value, error) {
			v, err := f(fr, ctx, st, d)
			if err != nil {
				return reflect.Value{}, err
			}
			pv := reflect.NewAt(styp, v)
			if addr {
				return pv, nil
			}
			return pv.Elem(), nil
		}, nil
	}
	return nil, fmt.Errorf("a bridged argument of kind %d is not supported", a.kind)
}

// nodeToValue adapts a compiled node into a reflect.Value producer, by
// writing the node's words into a typed cell.
func (c *jitCompiler) nodeToValue(rt reflect.Type, n node) (func(unsafe.Pointer, context.Context, map[string]any, any) (reflect.Value, error), error) {
	if rt == nil {
		return nil, fmt.Errorf("a bridged argument with no result type")
	}
	store := func(cell reflect.Value, fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
		at := cell.UnsafePointer()
		switch n.class {
		case lPtr:
			v, err := n.P(fr, ctx, st, d)
			if err != nil {
				return err
			}
			*(*unsafe.Pointer)(at) = v
		case lStr:
			v, err := n.S(fr, ctx, st, d)
			if err != nil {
				return err
			}
			*(*string)(at) = v
		case lSlice:
			v, err := n.L(fr, ctx, st, d)
			if err != nil {
				return err
			}
			*(*sliceHdr)(at) = v
		case lIface:
			v, err := n.I(fr, ctx, st, d)
			if err != nil {
				return err
			}
			*(*ifacePair)(at) = v
		default:
			if n.class.float() {
				v, err := n.F(fr, ctx, st, d)
				if err != nil {
					return err
				}
				storeF(n.class, at, v)
			} else {
				v, err := n.N(fr, ctx, st, d)
				if err != nil {
					return err
				}
				storeN(n.class, at, v)
			}
		}
		return nil
	}
	return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (reflect.Value, error) {
		cell := reflect.New(rt)
		if err := store(cell, fr, ctx, st, d); err != nil {
			return reflect.Value{}, err
		}
		return cell.Elem(), nil
	}, nil
}
