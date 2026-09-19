package gozero

import (
	"context"
	"fmt"
	"reflect"
	"unsafe" // also required by go:linkname
)

// Captured names on the direct tier. A capturing func literal's body
// compiles with jitCompileWith: each captured slot maps to an offset
// in the ENCLOSING frame, and a hidden capBase field in the body
// frame holds the enclosing frame pointer, installed by the closure
// on every call. A captured access is then one extra load - frame,
// enclosing frame, cell - against the plain slot's single load, and
// both sides address the same cell, which is what makes the capture
// write-through: the enclosing program's stores to the slot are the
// body's reads, and the body's stores are the enclosing program's.
//
// The closure itself is built per run, because it closes over that
// run's frame. One ordinary Go closure over the capBody and the frame
// pointer: one allocation per closure per run, the same construction
// the Go compiler emits for an escaping func literal. The enclosing
// frame escapes into the closure, so the program's frame pool is off,
// which planInline's caller records via frameEscapes.

// capBody is the compile-time half of a capturing closure: the body's
// program and the shape key its signature selected. The per-run half
// is the enclosing frame pointer capClosure closes over.
type capBody struct {
	jp  *jitProgram
	key string
}

// capClosure builds the per-run closure for a capturing body: one
// case per signature layout, like bodyClosure, plus the capBase store
// that hands the body this run's enclosing frame. Returns nil for a
// shape outside the table, which keeps the literal on the MakeFunc
// bridge.
func capClosure(outerF unsafe.Pointer, cb *capBody) any {
	switch cb.key {
	case "_":
		return func() {
			jp := cb.jp
			f := jp.getFrame()
			*(*unsafe.Pointer)(unsafe.Add(f, jp.capBase)) = outerF
			jp.runBody(f)
		}
	case "P_":
		return func(a0 unsafe.Pointer) {
			jp := cb.jp
			f := jp.getFrame()
			*(*unsafe.Pointer)(unsafe.Add(f, jp.capBase)) = outerF
			*(*unsafe.Pointer)(unsafe.Add(f, jp.paramOffs[0])) = a0
			jp.runBody(f)
		}
	case "S_":
		return func(a0 string) {
			jp := cb.jp
			f := jp.getFrame()
			*(*unsafe.Pointer)(unsafe.Add(f, jp.capBase)) = outerF
			*(*string)(unsafe.Add(f, jp.paramOffs[0])) = a0
			jp.runBody(f)
		}
	case "I_":
		return func(a0 ifacePair) {
			jp := cb.jp
			f := jp.getFrame()
			*(*unsafe.Pointer)(unsafe.Add(f, jp.capBase)) = outerF
			*(*ifacePair)(unsafe.Add(f, jp.paramOffs[0])) = a0
			jp.runBody(f)
		}
	case "II_":
		return func(a0, a1 ifacePair) {
			jp := cb.jp
			f := jp.getFrame()
			*(*unsafe.Pointer)(unsafe.Add(f, jp.capBase)) = outerF
			*(*ifacePair)(unsafe.Add(f, jp.paramOffs[0])) = a0
			*(*ifacePair)(unsafe.Add(f, jp.paramOffs[1])) = a1
			jp.runBody(f)
		}
	case "IP_":
		// func(http.ResponseWriter, *http.Request), the handler shape.
		return func(a0 ifacePair, a1 unsafe.Pointer) {
			jp := cb.jp
			f := jp.getFrame()
			*(*unsafe.Pointer)(unsafe.Add(f, jp.capBase)) = outerF
			*(*ifacePair)(unsafe.Add(f, jp.paramOffs[0])) = a0
			*(*unsafe.Pointer)(unsafe.Add(f, jp.paramOffs[1])) = a1
			jp.runBody(f)
		}
	}
	return nil
}

// capCell returns the loader of a captured slot's cell address: the
// enclosing frame pointer out of capBase, plus the slot's enclosing
// offset.
func (c *jitCompiler) capCell(slot int) (func(fr unsafe.Pointer) unsafe.Pointer, bool) {
	off, ok := c.capOffs[slot]
	if !ok {
		return nil, false
	}
	base := c.capBase
	return func(fr unsafe.Pointer) unsafe.Pointer {
		return unsafe.Add(*(*unsafe.Pointer)(unsafe.Add(fr, base)), off)
	}, true
}

// slotType is the static type of a slot, whether it owns a frame
// field or reads through a captured cell.
func (c *jitCompiler) slotType(slot int) (reflect.Type, bool) {
	if t, ok := c.capType[slot]; ok {
		return t, true
	}
	if field, ok := c.slotOf[slot]; ok {
		return c.types[field], true
	}
	return nil, false
}

// capSlotArg compiles a vaSlot argument whose slot is captured, or
// reports handled=false for an ordinary slot. A captured name reads
// and addresses through its cell in the enclosing frame; it is
// address-taken by construction, so an interface parameter always
// copies, never aliases, and the box is the same allocation the Go
// compiler makes converting a captured value. The cell's address does
// not escape THIS body's frame: it points into the enclosing frame,
// which already escaped when the closure was built.
func (c *jitCompiler) capSlotArg(a *vmArg, pt reflect.Type, cl layout) (node, bool, error) {
	cell, ok := c.capCell(a.slot)
	if !ok {
		return node{}, false, nil
	}
	if a.addrOf {
		if cl != lPtr {
			return node{}, true, fmt.Errorf("an address cannot fill a %s parameter", cl)
		}
		return node{class: lPtr, P: func(fr unsafe.Pointer, _ context.Context, _ map[string]any, _ any) (unsafe.Pointer, error) {
			return cell(fr), nil
		}}, true, nil
	}
	st := c.capType[a.slot]
	if cl == lIface && st.Kind() == reflect.Interface && st != pt {
		n, err := capIfaceToIface(a.name, cell, st, pt)
		return n, true, err
	}
	if cl == lIface && st.Kind() != reflect.Interface {
		n, err := c.toIface(st, pt, capSlotNode(cell, layoutOf(st)))
		return n, true, err
	}
	if layoutOf(st) != cl {
		return node{}, true, fmt.Errorf("a %s name cannot fill a %s parameter", layoutOf(st), cl)
	}
	return capSlotNode(cell, cl), true, nil
}

// capBridgeArg resolves a bridged call's argument whose slot is
// captured, so a reflect call inside the body sees the enclosing
// program's latest write; handled=false for an ordinary slot.
func (c *jitCompiler) capBridgeArg(a *vmArg) (func(unsafe.Pointer, context.Context, map[string]any, any) (reflect.Value, error), bool, error) {
	cell, ok := c.capCell(a.slot)
	if !ok {
		return nil, false, nil
	}
	st := c.capType[a.slot]
	if a.addrOf {
		if reflect.PointerTo(st) != a.typ {
			return nil, true, fmt.Errorf("cannot use *%s as %s", st, a.typ)
		}
		return func(fr unsafe.Pointer, _ context.Context, _ map[string]any, _ any) (reflect.Value, error) {
			return reflect.NewAt(st, cell(fr)), nil
		}, true, nil
	}
	if !st.AssignableTo(a.typ) {
		return nil, true, fmt.Errorf("cannot use %s as %s", st, a.typ)
	}
	return func(fr unsafe.Pointer, _ context.Context, _ map[string]any, _ any) (reflect.Value, error) {
		return reflect.NewAt(st, cell(fr)).Elem(), nil
	}, true, nil
}

// capFieldLoad is fieldNode's loader for a struct held by value in a
// captured slot: the struct lives in the enclosing frame, so the
// field is one hop behind the cell.
func (c *jitCompiler) capFieldLoad(slot int, fieldOff uintptr) (func(unsafe.Pointer, context.Context, map[string]any, any) (unsafe.Pointer, error), bool) {
	cell, ok := c.capCell(slot)
	if !ok {
		return nil, false
	}
	return func(fr unsafe.Pointer, _ context.Context, _ map[string]any, _ any) (unsafe.Pointer, error) {
		return unsafe.Add(cell(fr), fieldOff), nil
	}, true
}

// capSlotNode reads a captured slot through its cell, the indirected
// twin of slotNode.
func capSlotNode(cell func(unsafe.Pointer) unsafe.Pointer, cl layout) node {
	if cl.scalar() {
		if cl.float() {
			return node{class: cl, F: func(fr unsafe.Pointer, _ context.Context, _ map[string]any, _ any) (float64, error) {
				return loadF(cell(fr), cl), nil
			}}
		}
		return node{class: cl, N: func(fr unsafe.Pointer, _ context.Context, _ map[string]any, _ any) (uint64, error) {
			return loadN(cell(fr), cl), nil
		}}
	}
	switch cl {
	case lPtr:
		return node{class: lPtr, P: func(fr unsafe.Pointer, _ context.Context, _ map[string]any, _ any) (unsafe.Pointer, error) {
			return *(*unsafe.Pointer)(cell(fr)), nil
		}}
	case lSlice:
		return node{class: lSlice, L: func(fr unsafe.Pointer, _ context.Context, _ map[string]any, _ any) (sliceHdr, error) {
			return *(*sliceHdr)(cell(fr)), nil
		}}
	case lStr:
		return node{class: lStr, S: func(fr unsafe.Pointer, _ context.Context, _ map[string]any, _ any) (string, error) {
			return *(*string)(cell(fr)), nil
		}}
	default:
		return node{class: lIface, I: func(fr unsafe.Pointer, _ context.Context, _ map[string]any, _ any) (ifacePair, error) {
			return *(*ifacePair)(cell(fr)), nil
		}}
	}
}

// capIfaceToIface is ifaceToIface reading the source pair out of a
// captured cell instead of a frame offset.
func capIfaceToIface(name string, cell func(unsafe.Pointer) unsafe.Pointer, st, pt reflect.Type) (node, error) {
	conv, ok := ifaceConvs[pt]
	if !ok {
		return node{}, fmt.Errorf("%s is not in ifaceConvs", pt)
	}
	eface := st.NumMethod() == 0
	return node{class: lIface, I: func(fr unsafe.Pointer, _ context.Context, _ map[string]any, _ any) (ifacePair, error) {
		pair := *(*ifacePair)(cell(fr))
		if pair.tab == nil {
			return ifacePair{}, nil
		}
		if !eface {
			pair.tab = itabType(pair.tab)
		}
		var v any
		*(*ifacePair)(unsafe.Pointer(&v)) = pair
		out, ok := conv(v)
		if !ok {
			return ifacePair{}, fmt.Errorf("exec: variable %q: cannot use %T as %s", name, v, pt)
		}
		return out, nil
	}}, nil
}

// capStore stores a node's value through a captured cell, the
// indirected twin of stmtNode's frame store.
func capStore(cell func(unsafe.Pointer) unsafe.Pointer, n node) (nodeE, error) {
	if n.class.scalar() {
		cl := n.class
		if cl.float() {
			f := n.F
			return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
				v, err := f(fr, ctx, st, d)
				if err != nil {
					return err
				}
				storeF(cl, cell(fr), v)
				return nil
			}, nil
		}
		f := n.N
		return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
			v, err := f(fr, ctx, st, d)
			if err != nil {
				return err
			}
			storeN(cl, cell(fr), v)
			return nil
		}, nil
	}
	switch n.class {
	case lPtr:
		f := n.P
		return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
			v, err := f(fr, ctx, st, d)
			if err != nil {
				return err
			}
			*(*unsafe.Pointer)(cell(fr)) = v
			return nil
		}, nil
	case lSlice:
		f := n.L
		return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
			v, err := f(fr, ctx, st, d)
			if err != nil {
				return err
			}
			*(*sliceHdr)(cell(fr)) = v
			return nil
		}, nil
	case lStr:
		f := n.S
		return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
			v, err := f(fr, ctx, st, d)
			if err != nil {
				return err
			}
			*(*string)(cell(fr)) = v
			return nil
		}, nil
	case lIface:
		f := n.I
		return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
			v, err := f(fr, ctx, st, d)
			if err != nil {
				return err
			}
			*(*ifacePair)(cell(fr)) = v
			return nil
		}, nil
	}
	return nil, fmt.Errorf("a result of class %s cannot be stored through a captured cell", n.class)
}
