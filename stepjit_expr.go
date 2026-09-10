package gozero

import (
	"context"
	"fmt"
	"reflect"
	"unsafe" // also required by go:linkname
)

// Expression nodes. Operators are combinators over the scalar
// plumbing the tier already has: integers and bools travel as nodeN
// bits zero-extended to their class, floats as nodeF, strings as
// nodeS. An operation loads its operands in the class's own domain,
// applies Go's operator, and normalizes the result back to the
// class's representation, so wraparound, division panics and shift
// behaviour are the language's own. No new layout classes and no
// shape-table growth.

// signedClass reports the classes nodeN carries sign-extended
// meaning for.
func signedClass(cl layout) bool {
	return cl == lI8 || cl == lI16 || cl == lI32 || cl == lI64
}

// asSigned reads nodeN bits as the class's signed value.
func asSigned(cl layout, u uint64) int64 {
	switch cl {
	case lI8:
		return int64(int8(uint8(u)))
	case lI16:
		return int64(int16(uint16(u)))
	case lI32:
		return int64(int32(uint32(u)))
	}
	return int64(u)
}

// normN truncates a computed value back to the class's width and
// zero-extends, the representation every nodeN carries.
func normN(cl layout, u uint64) uint64 {
	switch cl {
	case lBool:
		if u != 0 {
			return 1
		}
		return 0
	case lI8, lU8:
		return uint64(uint8(u))
	case lI16, lU16:
		return uint64(uint16(u))
	case lI32, lU32:
		return uint64(uint32(u))
	}
	return u
}

// valueNode compiles an argument as a value in its own class, the
// form expression operands travel in.
func (c *jitCompiler) valueNode(a *vmArg) (node, error) {
	switch a.kind {
	case vaConst:
		cl := layoutOf(a.val.Type())
		if cl == lBad {
			return node{}, fmt.Errorf("a constant of type %s has no layout class", a.val.Type())
		}
		return constNode(a.val, cl)
	case vaSlot, vaField:
		t := a.typ
		if t == nil {
			return node{}, fmt.Errorf("an operand has no static type")
		}
		cl := layoutOf(t)
		if cl == lBad {
			return node{}, fmt.Errorf("an operand of type %s has no layout class", t)
		}
		return c.argNode(a, t, cl)
	case vaCall:
		return c.exprNode(a.sub)
	case vaBinary:
		return c.binaryNode(a)
	case vaUnary:
		return c.unaryNode(a)
	case vaIndex:
		return c.indexNode(a)
	case vaLen:
		return c.lenNode(a)
	}
	return node{}, fmt.Errorf("operand kind %d is not in the table", a.kind)
}

// binaryNode compiles one operator application.
func (c *jitCompiler) binaryNode(a *vmArg) (node, error) {
	switch a.op {
	case "&&", "||":
		return c.shortCircuitNode(a)
	case "==nil", "!=nil":
		return c.nilCompareNode(a)
	}
	x, err := c.valueNode(a.x)
	if err != nil {
		return node{}, err
	}
	y, err := c.valueNode(a.y)
	if err != nil {
		return node{}, err
	}
	shift := a.op == "<<" || a.op == ">>"
	if !shift && x.class != y.class {
		return node{}, fmt.Errorf("operands of class %s and %s do not mix", x.class, y.class)
	}
	switch {
	case x.class.scalar() && !x.class.float():
		return intBinaryNode(a.op, x, y)
	case x.class.float():
		return floatBinaryNode(a.op, x, y)
	case x.class == lStr:
		return stringBinaryNode(a.op, x, y)
	case x.class == lPtr:
		return ptrBinaryNode(a.op, x, y)
	}
	return node{}, fmt.Errorf("operator %s over class %s is not in the table", a.op, x.class)
}

// shiftCountN reads a shift count from either integer family,
// panicking on a negative one the way the runtime does.
func shiftCountN(cl layout, u uint64) uint64 {
	if signedClass(cl) {
		n := asSigned(cl, u)
		if n < 0 {
			panic("runtime error: negative shift amount")
		}
		return uint64(n)
	}
	return u
}

// shortCircuitNode is && and || as control flow: the right side runs
// only when the left does not decide, as in Go.
func (c *jitCompiler) shortCircuitNode(a *vmArg) (node, error) {
	x, err := c.valueNode(a.x)
	if err != nil {
		return node{}, err
	}
	y, err := c.valueNode(a.y)
	if err != nil {
		return node{}, err
	}
	if x.class != lBool || y.class != lBool {
		return node{}, fmt.Errorf("%s wants bool operands", a.op)
	}
	xf, yf := x.N, y.N
	and := a.op == "&&"
	return node{class: lBool, N: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (uint64, error) {
		v, err := xf(fr, ctx, st, d)
		if err != nil {
			return 0, err
		}
		if and && v == 0 {
			return 0, nil
		}
		if !and && v != 0 {
			return 1, nil
		}
		return yf(fr, ctx, st, d)
	}}, nil
}

// nilCompareNode tests a nilable operand against nil: the tab word
// of an interface, the pointer word of a pointer, map, chan or func,
// the data pointer of a slice.
func (c *jitCompiler) nilCompareNode(a *vmArg) (node, error) {
	x, err := c.valueNode(a.x)
	if err != nil {
		return node{}, err
	}
	wantNil := a.op == "==nil"
	switch x.class {
	case lIface:
		xf := x.I
		return node{class: lBool, N: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (uint64, error) {
			v, err := xf(fr, ctx, st, d)
			if err != nil {
				return 0, err
			}
			if (v.tab == nil) == wantNil {
				return 1, nil
			}
			return 0, nil
		}}, nil
	case lPtr:
		xf := x.P
		return node{class: lBool, N: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (uint64, error) {
			v, err := xf(fr, ctx, st, d)
			if err != nil {
				return 0, err
			}
			if (v == nil) == wantNil {
				return 1, nil
			}
			return 0, nil
		}}, nil
	case lSlice:
		xf := x.L
		return node{class: lBool, N: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (uint64, error) {
			v, err := xf(fr, ctx, st, d)
			if err != nil {
				return 0, err
			}
			if (v.ptr == nil) == wantNil {
				return 1, nil
			}
			return 0, nil
		}}, nil
	}
	return node{}, fmt.Errorf("a nil comparison over class %s is not in the table", x.class)
}

func (c *jitCompiler) unaryNode(a *vmArg) (node, error) {
	x, err := c.valueNode(a.x)
	if err != nil {
		return node{}, err
	}
	cl := x.class
	switch {
	case a.op == "-" && cl.scalar() && !cl.float():
		xf := x.N
		return node{class: cl, N: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (uint64, error) {
			v, err := xf(fr, ctx, st, d)
			if err != nil {
				return 0, err
			}
			return normN(cl, -v), nil
		}}, nil
	case a.op == "-" && cl.float():
		xf := x.F
		return node{class: cl, F: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (float64, error) {
			v, err := xf(fr, ctx, st, d)
			if err != nil {
				return 0, err
			}
			return -v, nil
		}}, nil
	case a.op == "^" && cl.scalar() && !cl.float():
		xf := x.N
		return node{class: cl, N: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (uint64, error) {
			v, err := xf(fr, ctx, st, d)
			if err != nil {
				return 0, err
			}
			return normN(cl, ^v), nil
		}}, nil
	case a.op == "!" && cl == lBool:
		xf := x.N
		return node{class: lBool, N: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (uint64, error) {
			v, err := xf(fr, ctx, st, d)
			if err != nil {
				return 0, err
			}
			return v ^ 1, nil
		}}, nil
	}
	return node{}, fmt.Errorf("unary %s over class %s is not in the table", a.op, cl)
}

// indexNode covers slice and string indexing with the runtime's
// bounds panic; map indexing stays on the reflect tier.
func (c *jitCompiler) indexNode(a *vmArg) (node, error) {
	x, err := c.valueNode(a.x)
	if err != nil {
		return node{}, err
	}
	y, err := c.valueNode(a.y)
	if err != nil {
		return node{}, err
	}
	if !y.class.scalar() || y.class.float() {
		return node{}, fmt.Errorf("an index of class %s is not in the table", y.class)
	}
	ycl := y.class
	yf := y.N
	idx := func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any, n int) (int, error) {
		u, err := yf(fr, ctx, st, d)
		if err != nil {
			return 0, err
		}
		i := int(asSigned(ycl, u))
		if !signedClass(ycl) {
			i = int(u)
		}
		if i < 0 || i >= n {
			panic(fmt.Sprintf("runtime error: index out of range [%d] with length %d", i, n))
		}
		return i, nil
	}
	if x.class == lStr {
		xf := x.S
		return node{class: lU8, N: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (uint64, error) {
			s, err := xf(fr, ctx, st, d)
			if err != nil {
				return 0, err
			}
			i, err := idx(fr, ctx, st, d, len(s))
			if err != nil {
				return 0, err
			}
			return uint64(s[i]), nil
		}}, nil
	}
	if x.class != lSlice {
		return node{}, fmt.Errorf("indexing class %s is not in the table", x.class)
	}
	et := a.typ
	ecl := layoutOf(et)
	if ecl == lBad {
		return node{}, fmt.Errorf("an element of type %s has no layout class", et)
	}
	size := et.Size()
	xf := x.L
	elem := func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (unsafe.Pointer, error) {
		h, err := xf(fr, ctx, st, d)
		if err != nil {
			return nil, err
		}
		i, err := idx(fr, ctx, st, d, h.len)
		if err != nil {
			return nil, err
		}
		return unsafe.Add(h.ptr, uintptr(i)*size), nil
	}
	return loadClassNode(elem, ecl), nil
}

// loadClassNode wraps an address producer in the class's load.
func loadClassNode(at func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (unsafe.Pointer, error), cl layout) node {
	if cl.scalar() {
		if cl.float() {
			return node{class: cl, F: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (float64, error) {
				p, err := at(fr, ctx, st, d)
				if err != nil {
					return 0, err
				}
				return loadF(p, cl), nil
			}}
		}
		return node{class: cl, N: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (uint64, error) {
			p, err := at(fr, ctx, st, d)
			if err != nil {
				return 0, err
			}
			return loadN(p, cl), nil
		}}
	}
	switch cl {
	case lPtr:
		return node{class: lPtr, P: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (unsafe.Pointer, error) {
			p, err := at(fr, ctx, st, d)
			if err != nil {
				return nil, err
			}
			return *(*unsafe.Pointer)(p), nil
		}}
	case lStr:
		return node{class: lStr, S: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (string, error) {
			p, err := at(fr, ctx, st, d)
			if err != nil {
				return "", err
			}
			return *(*string)(p), nil
		}}
	case lSlice:
		return node{class: lSlice, L: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (sliceHdr, error) {
			p, err := at(fr, ctx, st, d)
			if err != nil {
				return sliceHdr{}, err
			}
			return *(*sliceHdr)(p), nil
		}}
	}
	return node{class: lIface, I: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (ifacePair, error) {
		p, err := at(fr, ctx, st, d)
		if err != nil {
			return ifacePair{}, err
		}
		return *(*ifacePair)(p), nil
	}}
}

// lenNode covers slices and strings; map and channel lengths stay on
// the reflect tier.
func (c *jitCompiler) lenNode(a *vmArg) (node, error) {
	x, err := c.valueNode(a.x)
	if err != nil {
		return node{}, err
	}
	// len returns int, whose class follows the platform word.
	cl := layoutOf(reflect.TypeFor[int]())
	switch x.class {
	case lSlice:
		xf := x.L
		return node{class: cl, N: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (uint64, error) {
			h, err := xf(fr, ctx, st, d)
			if err != nil {
				return 0, err
			}
			return normN(cl, uint64(h.len)), nil
		}}, nil
	case lStr:
		xf := x.S
		return node{class: cl, N: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (uint64, error) {
			s, err := xf(fr, ctx, st, d)
			if err != nil {
				return 0, err
			}
			return normN(cl, uint64(len(s))), nil
		}}, nil
	}
	return node{}, fmt.Errorf("len over class %s is not in the table", x.class)
}
