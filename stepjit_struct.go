package gozero

import (
	"context"
	"fmt"
	"reflect"
	"unsafe" // also required by go:linkname
)

// The composite literal on the direct tier. A literal compiles to
// stores at field offsets, the technique the frame already uses: each
// store goes through a typed pointer, so a pointer store keeps its
// write barrier, and the block behind &T{} comes from unsafeNew with
// the struct's own rtype, so the collector scans it correctly.
//
// A value literal assigned to a name builds in place in the frame slot
// and allocates nothing. Everything else - &T{}, a literal in argument
// position, a literal filling an interface - allocates one fresh T per
// evaluation, which is the allocation the Go compiler makes for a
// literal that escapes.

// fillFunc writes one element of a composite literal at an offset from
// the struct's base pointer. The base is a parameter rather than a
// capture because the same fills serve a frame slot and a fresh
// allocation.
type fillFunc func(base, fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error

// structFills compiles the elements of a composite literal into typed
// stores. base accumulates the offset of a nested value literal, whose
// fields land directly in the outer struct.
func (c *jitCompiler) structFills(a *vmArg, base uintptr) ([]fillFunc, error) {
	var fills []fillFunc
	for i := range a.elems {
		e := &a.elems[i]
		if len(e.index) != 1 {
			return nil, fmt.Errorf("a promoted field is not in the table")
		}
		sf := a.styp.Field(e.index[0])
		off := base + sf.Offset
		cl := layoutOf(sf.Type)
		if cl == lBad {
			// A field of struct type has no transport class, but a
			// nested value literal needs none: its fields write in
			// place at their summed offsets.
			if sub := e.val; sub.kind == vaStruct && !sub.addr && sf.Type.Kind() == reflect.Struct {
				nested, err := c.structFills(sub, off)
				if err != nil {
					return nil, err
				}
				fills = append(fills, nested...)
				continue
			}
			return nil, fmt.Errorf("field %s of type %s has no layout class", sf.Name, sf.Type)
		}
		n, err := c.argNode(e.val, sf.Type, cl)
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", sf.Name, err)
		}
		fill := fillNode(n, off)
		if fill == nil {
			return nil, fmt.Errorf("field %s: a %s element cannot be stored", sf.Name, n.class)
		}
		fills = append(fills, fill)
	}
	return fills, nil
}

// fillNode adapts a compiled node into a store at base+off. It returns
// nil for a class it cannot store, which the caller reports.
func fillNode(n node, off uintptr) fillFunc {
	if n.class.scalar() {
		if n.class.float() {
			cl, f := n.class, n.F
			return func(base, fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
				v, err := f(fr, ctx, st, d)
				if err != nil {
					return err
				}
				storeF(cl, unsafe.Add(base, off), v)
				return nil
			}
		}
		cl, f := n.class, n.N
		return func(base, fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
			v, err := f(fr, ctx, st, d)
			if err != nil {
				return err
			}
			storeN(cl, unsafe.Add(base, off), v)
			return nil
		}
	}
	switch n.class {
	case lPtr:
		f := n.P
		return func(base, fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
			v, err := f(fr, ctx, st, d)
			if err != nil {
				return err
			}
			*(*unsafe.Pointer)(unsafe.Add(base, off)) = v
			return nil
		}
	case lStr:
		f := n.S
		return func(base, fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
			v, err := f(fr, ctx, st, d)
			if err != nil {
				return err
			}
			*(*string)(unsafe.Add(base, off)) = v
			return nil
		}
	case lSlice:
		f := n.L
		return func(base, fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
			v, err := f(fr, ctx, st, d)
			if err != nil {
				return err
			}
			*(*sliceHdr)(unsafe.Add(base, off)) = v
			return nil
		}
	case lIface:
		f := n.I
		return func(base, fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
			v, err := f(fr, ctx, st, d)
			if err != nil {
				return err
			}
			*(*ifacePair)(unsafe.Add(base, off)) = v
			return nil
		}
	}
	return nil
}

// structNode compiles a literal that leaves the frame: one fresh T per
// evaluation, filled at offsets, carried as the pointer to it. Two runs
// of the same program must not share the struct, so the allocation is
// per evaluation, never a prototype.
func (c *jitCompiler) structNode(a *vmArg) (node, error) {
	fills, err := c.structFills(a, 0)
	if err != nil {
		return node{}, err
	}
	rt := rtypePtr(a.styp)
	return node{class: lPtr, P: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (unsafe.Pointer, error) {
		base := unsafeNew(rt)
		for _, fill := range fills {
			if err := fill(base, fr, ctx, st, d); err != nil {
				return nil, err
			}
		}
		return base, nil
	}}, nil
}

// structIfaceNode wraps a value literal as an interface. The fresh T
// the literal builds is aliased as the data word rather than copied,
// which is sound because nothing else can reach it.
func (c *jitCompiler) structIfaceNode(pt reflect.Type, a *vmArg) (node, error) {
	n, err := c.structNode(a)
	if err != nil {
		return node{}, err
	}
	if a.addr {
		return c.toIface(reflect.PointerTo(a.styp), pt, n)
	}
	tab, ok := itabFor(a.styp, pt)
	if !ok {
		return node{}, fmt.Errorf("%s does not implement %s", a.styp, pt)
	}
	f := n.P
	return node{class: lIface, I: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (ifacePair, error) {
		v, err := f(fr, ctx, st, d)
		if err != nil {
			return ifacePair{}, err
		}
		return ifacePair{tab: tab, data: v}, nil
	}}, nil
}

// structArgNode compiles a literal in argument position to the class
// its parameter wants. A value literal can only travel as an interface,
// because a struct wider than a word has no transport class of its own.
func (c *jitCompiler) structArgNode(a *vmArg, pt reflect.Type, cl layout) (node, error) {
	switch {
	case cl == lPtr && a.addr:
		return c.structNode(a)
	case cl == lIface:
		return c.structIfaceNode(pt, a)
	}
	return node{}, fmt.Errorf("a %s literal cannot fill a %s parameter", a.styp, cl)
}

// structAssignNode compiles u := url.URL{...} and p := &T{...}. The
// value form whose slot holds the struct builds in place in the frame;
// the rest go through structNode's allocation and store like any other
// result of their class.
func (c *jitCompiler) structAssignNode(s plannedStmt) (nodeE, error) {
	a := s.assign
	field, ok := c.slotOf[s.out]
	if !ok {
		// Nothing reads the name, but the element calls still run and
		// their errors still end the program.
		n, err := c.structNode(a)
		if err != nil {
			return nil, err
		}
		return c.dropped(n)
	}
	off, st := c.offs[field], c.types[field]

	if !a.addr && st.Kind() == reflect.Struct {
		fills, err := c.structFills(a, 0)
		if err != nil {
			return nil, err
		}
		// The frame comes back zeroed, so the first write into the slot
		// starts from the zero value the literal promises. A slot
		// written more than once still holds the previous value, which
		// a fresh literal must not see, so it is cleared first; SetZero
		// is a typed clear, so the pointer fields keep their barriers.
		var zero func(unsafe.Pointer)
		if c.writes[s.out] > 1 {
			zt := st
			zero = func(at unsafe.Pointer) { reflect.NewAt(zt, at).Elem().SetZero() }
		}
		return func(fr unsafe.Pointer, ctx context.Context, stk map[string]any, d any) error {
			base := unsafe.Add(fr, off)
			if zero != nil {
				zero(base)
			}
			for _, fill := range fills {
				if err := fill(base, fr, ctx, stk, d); err != nil {
					return err
				}
			}
			return nil
		}, nil
	}

	switch {
	case a.addr && st.Kind() == reflect.Pointer:
		n, err := c.structNode(a)
		if err != nil {
			return nil, err
		}
		f := n.P
		return func(fr unsafe.Pointer, ctx context.Context, stk map[string]any, d any) error {
			v, err := f(fr, ctx, stk, d)
			if err != nil {
				return err
			}
			*(*unsafe.Pointer)(unsafe.Add(fr, off)) = v
			return nil
		}, nil
	case st.Kind() == reflect.Interface:
		n, err := c.structIfaceNode(st, a)
		if err != nil {
			return nil, err
		}
		f := n.I
		return func(fr unsafe.Pointer, ctx context.Context, stk map[string]any, d any) error {
			v, err := f(fr, ctx, stk, d)
			if err != nil {
				return err
			}
			*(*ifacePair)(unsafe.Add(fr, off)) = v
			return nil
		}, nil
	}
	return nil, fmt.Errorf("a %s literal cannot be stored in a %s slot", a.styp, st)
}
