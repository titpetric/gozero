package gozero

import (
	"context"
	"fmt"
	"reflect"
	"unsafe" // also required by go:linkname
)

// Comparisons on the direct tier: the header-comparison kit every
// condition header lowers through. A comparison compiles to one
// nodeN closed over the operands' shared layout class, two loads and
// a machine compare with no reflect between them; vm_cmp.go is the
// same kit on the reflect tier, and it has already fixed the one
// static type both sides carry.
//
// Narrow signed classes canonicalize through signN, which truncates
// to width and sign-extends, because the tier carries loads
// zero-extended while scalarBits sign-extends constants; without it
// int8(-1) compares bigger than zero.

// cmpNode compiles a header comparison to bool bits. Both sides
// carry one static type, so one layout class closes the whole node:
// the operand loads and the compare are machine operations, and the
// only reflect a comparison can pay sits inside an operand call the
// shape table cannot express.
func (c *jitCompiler) cmpNode(cm *vmCmp) (nodeN, error) {
	cl := layoutOf(cm.typ)
	if cl == lBad {
		return nil, fmt.Errorf("a comparison over %s is not in the table", cm.typ)
	}
	x, err := c.cmpOperandNode(cm.lhs, cm.typ, cl)
	if err != nil {
		return nil, err
	}
	y, err := c.cmpOperandNode(cm.rhs, cm.typ, cl)
	if err != nil {
		return nil, err
	}
	switch {
	case cl == lStr:
		return cmpNodeOf(cm.op, x.S, y.S)
	case cl.float():
		return cmpNodeOf(cm.op, x.F, y.F)
	case cl == lBool:
		// The vm compiler admits only == and != for bool, and the
		// bits a nodeN carries are 0 or 1, so the integer compare is
		// the bool compare.
		return cmpNodeN(cm.op, cl, x.N, y.N)
	case cl.scalar():
		return cmpNodeN(cm.op, cl, x.N, y.N)
	}
	return nil, fmt.Errorf("a comparison over class %s is not in the table", cl)
}

// cmpOperandNode compiles one side of a comparison to the shared
// class. A call goes through exprNode so a predicate operand
// compiles exactly like a condition call does.
func (c *jitCompiler) cmpOperandNode(a *vmArg, t reflect.Type, cl layout) (node, error) {
	var n node
	var err error
	if a.kind == vaCall {
		n, err = c.exprNode(a.sub)
	} else {
		n, err = c.argNode(a, t, cl)
	}
	if err != nil {
		return node{}, err
	}
	if n.class != cl {
		return node{}, fmt.Errorf("a %s operand cannot compare as %s", n.class, cl)
	}
	return n, nil
}

// signN sign-extends the zero-extended bits a nodeN carries, so a
// signed class orders by value: int8(-1) travels as 0xFF and must
// compare below 0.
func signN(cl layout, v uint64) int64 {
	switch cl {
	case lI8:
		return int64(int8(uint8(v)))
	case lI16:
		return int64(int16(uint16(v)))
	case lI32:
		return int64(int32(uint32(v)))
	}
	return int64(v) // lI64
}

// signedClass reports a class whose ordering sign-extends.
func signedClass(cl layout) bool {
	return cl == lI8 || cl == lI16 || cl == lI32 || cl == lI64
}

// cmpNodeN builds the comparison over integer and bool bits. A
// signed class runs every operator through signN, which truncates
// to the class width and sign-extends: that canonicalizes the two
// bit conventions in play, the zero-extended loads and the
// sign-extended constant bits scalarBits produces, before any
// compare. Unsigned and bool bits are already canonical.
func cmpNodeN(op string, cl layout, x, y nodeN) (nodeN, error) {
	if signedClass(cl) {
		return cmpNodeSigned(op, cl, x, y)
	}
	var f func(a, b uint64) bool
	switch op {
	case "==":
		f = func(a, b uint64) bool { return a == b }
	case "!=":
		f = func(a, b uint64) bool { return a != b }
	case "<":
		f = func(a, b uint64) bool { return a < b }
	case "<=":
		f = func(a, b uint64) bool { return a <= b }
	case ">":
		f = func(a, b uint64) bool { return a > b }
	case ">=":
		f = func(a, b uint64) bool { return a >= b }
	default:
		return nil, fmt.Errorf("operator %s is not in the table", op)
	}
	return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (uint64, error) {
		a, err := x(fr, ctx, st, d)
		if err != nil {
			return 0, err
		}
		b, err := y(fr, ctx, st, d)
		if err != nil {
			return 0, err
		}
		if f(a, b) {
			return 1, nil
		}
		return 0, nil
	}, nil
}

// cmpNodeSigned is cmpNodeN's signed half.
func cmpNodeSigned(op string, cl layout, x, y nodeN) (nodeN, error) {
	var f func(a, b int64) bool
	switch op {
	case "==":
		f = func(a, b int64) bool { return a == b }
	case "!=":
		f = func(a, b int64) bool { return a != b }
	case "<":
		f = func(a, b int64) bool { return a < b }
	case "<=":
		f = func(a, b int64) bool { return a <= b }
	case ">":
		f = func(a, b int64) bool { return a > b }
	case ">=":
		f = func(a, b int64) bool { return a >= b }
	default:
		return nil, fmt.Errorf("operator %s is not in the table", op)
	}
	return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (uint64, error) {
		a, err := x(fr, ctx, st, d)
		if err != nil {
			return 0, err
		}
		b, err := y(fr, ctx, st, d)
		if err != nil {
			return 0, err
		}
		if f(signN(cl, a), signN(cl, b)) {
			return 1, nil
		}
		return 0, nil
	}, nil
}

// cmpNodeOf builds the comparison over the classes whose nodes carry
// their value directly: strings as nodeS, floats as nodeF. A float32
// travels as float64, an exact conversion, so one compare covers
// both widths.
func cmpNodeOf[T string | float64](op string, x, y func(unsafe.Pointer, context.Context, map[string]any, any) (T, error)) (nodeN, error) {
	var f func(a, b T) bool
	switch op {
	case "==":
		f = func(a, b T) bool { return a == b }
	case "!=":
		f = func(a, b T) bool { return a != b }
	case "<":
		f = func(a, b T) bool { return a < b }
	case "<=":
		f = func(a, b T) bool { return a <= b }
	case ">":
		f = func(a, b T) bool { return a > b }
	case ">=":
		f = func(a, b T) bool { return a >= b }
	default:
		return nil, fmt.Errorf("operator %s is not in the table", op)
	}
	return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (uint64, error) {
		a, err := x(fr, ctx, st, d)
		if err != nil {
			return 0, err
		}
		b, err := y(fr, ctx, st, d)
		if err != nil {
			return 0, err
		}
		if f(a, b) {
			return 1, nil
		}
		return 0, nil
	}, nil
}
