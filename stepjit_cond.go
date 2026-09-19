package gozero

import (
	"context"
	"fmt"
	"unsafe" // also required by go:linkname
)

// Header expressions on the direct tier. Composition is three bool
// combinators over predicate nodes, and arithmetic operands are
// class-closed nodes over the scalar plumbing comparisons already
// use: integers travel as nodeN bits and normalize back to the
// operand type's width after every operation, so wraparound is
// two's complement at that width, division runs in the class's own
// domain and panics on a runtime zero exactly as compiled Go does,
// and a float32 rounds per operation. Nothing here allocates per
// run; the whole header is loads, machine arithmetic and branches.

// predNode compiles the predicate tree to bool bits. && and || are
// control flow: the right side's node runs only when the left does
// not decide, so a skipped operand's call and its error never
// happen, matching the reflect evaluator and Go.
func (c *jitCompiler) predNode(p *vmPred) (nodeN, error) {
	switch p.op {
	case "&&", "||":
		x, err := c.predNode(p.x)
		if err != nil {
			return nil, err
		}
		y, err := c.predNode(p.y)
		if err != nil {
			return nil, err
		}
		and := p.op == "&&"
		return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (uint64, error) {
			v, err := x(fr, ctx, st, d)
			if err != nil {
				return 0, err
			}
			if and && v == 0 {
				return 0, nil
			}
			if !and && v != 0 {
				return 1, nil
			}
			return y(fr, ctx, st, d)
		}, nil
	case "!":
		x, err := c.predNode(p.x)
		if err != nil {
			return nil, err
		}
		return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (uint64, error) {
			v, err := x(fr, ctx, st, d)
			if err != nil {
				return 0, err
			}
			return v ^ 1, nil
		}, nil
	}
	if p.cmp != nil {
		return c.cmpNode(p.cmp)
	}
	return c.condNode(p.cond)
}

// normN truncates computed bits back to the class's width and
// zero-extends, the canonical form loads produce. Add, subtract and
// multiply are width-agnostic in two's complement, so their inputs
// need no care and only the output normalizes.
func normN(cl layout, v uint64) uint64 {
	switch cl {
	case lI8, lU8:
		return uint64(uint8(v))
	case lI16, lU16:
		return uint64(uint16(v))
	case lI32, lU32:
		return uint64(uint32(v))
	}
	return v
}

// arithNode compiles one arithmetic node at the class of its static
// type; the vm compiler admitted numeric kinds only.
func (c *jitCompiler) arithNode(a *vmArg) (node, error) {
	cl := layoutOf(a.typ)
	if !cl.scalar() || cl == lBool {
		return node{}, fmt.Errorf("arithmetic over %s is not in the table", a.typ)
	}
	x, err := c.cmpOperandNode(a.x, a.typ, cl)
	if err != nil {
		return node{}, err
	}
	y, err := c.cmpOperandNode(a.y, a.typ, cl)
	if err != nil {
		return node{}, err
	}
	if cl.float() {
		return floatArithNode(a.op, cl, x.F, y.F)
	}
	return intArithNode(a.op, cl, x.N, y.N)
}

// intArithNode builds the operation over integer bits. Divide and
// remainder canonicalize their operands first - sign-extending the
// signed classes through signN, masking the unsigned ones - because
// constant bits arrive sign-extended where loads zero-extend; the
// division itself is Go's, so a zero divisor panics in the runtime
// and truncation is toward zero.
func intArithNode(op string, cl layout, x, y nodeN) (node, error) {
	var f func(a, b uint64) uint64
	signed := signedClass(cl)
	switch op {
	case "+":
		f = func(a, b uint64) uint64 { return normN(cl, a+b) }
	case "-":
		f = func(a, b uint64) uint64 { return normN(cl, a-b) }
	case "*":
		f = func(a, b uint64) uint64 { return normN(cl, a*b) }
	case "/":
		if signed {
			f = func(a, b uint64) uint64 { return normN(cl, uint64(signN(cl, a)/signN(cl, b))) }
		} else {
			f = func(a, b uint64) uint64 { return normN(cl, normN(cl, a)/normN(cl, b)) }
		}
	case "%":
		if signed {
			f = func(a, b uint64) uint64 { return normN(cl, uint64(signN(cl, a)%signN(cl, b))) }
		} else {
			f = func(a, b uint64) uint64 { return normN(cl, normN(cl, a)%normN(cl, b)) }
		}
	default:
		return node{}, fmt.Errorf("operator %s is not in the table", op)
	}
	return node{class: cl, N: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (uint64, error) {
		a, err := x(fr, ctx, st, d)
		if err != nil {
			return 0, err
		}
		b, err := y(fr, ctx, st, d)
		if err != nil {
			return 0, err
		}
		return f(a, b), nil
	}}, nil
}

// floatArithNode runs float arithmetic in float64, rounding to
// float32 per operation when the class is lF32, which is what
// compiled Go does; carrying the wide result and truncating once at
// the end would double-round. % over floats never compiles.
func floatArithNode(op string, cl layout, x, y nodeF) (node, error) {
	var f func(a, b float64) float64
	switch op {
	case "+":
		f = func(a, b float64) float64 { return a + b }
	case "-":
		f = func(a, b float64) float64 { return a - b }
	case "*":
		f = func(a, b float64) float64 { return a * b }
	case "/":
		f = func(a, b float64) float64 { return a / b }
	default:
		return node{}, fmt.Errorf("operator %s is not in the table", op)
	}
	f32 := cl == lF32
	return node{class: cl, F: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (float64, error) {
		a, err := x(fr, ctx, st, d)
		if err != nil {
			return 0, err
		}
		b, err := y(fr, ctx, st, d)
		if err != nil {
			return 0, err
		}
		r := f(a, b)
		if f32 {
			r = float64(float32(r))
		}
		return r, nil
	}}, nil
}
