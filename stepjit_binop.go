package gozero

import (
	"context"
	"fmt"
	"unsafe" // also required by go:linkname
)

// The arithmetic combinators of the direct tier. An operator is two
// loads and a native operation between machine words: wraparound is
// the mask at the class width, the signed division and shifts
// sign-extend at the width first, a float32 operation rounds once at
// 32 bits, and a string + is Go's own concatenation, which allocates
// exactly where compiled Go allocates.

// classMask is the identity mask of an integer class's width. Summing
// inside the closure and masking keeps the value in the canonical
// zero-extended form nodeN carries everywhere.
func classMask(cl layout) uint64 {
	switch cl {
	case lI8, lU8, lBool:
		return 0xFF
	case lI16, lU16:
		return 0xFFFF
	case lI32, lU32:
		return 0xFFFFFFFF
	}
	return ^uint64(0)
}

// classSigned reports the signed integer classes.
func classSigned(cl layout) bool {
	return cl == lI8 || cl == lI16 || cl == lI32 || cl == lI64
}

// sxFn sign-extends a class's canonical zero-extended bits to int64.
func sxFn(cl layout) func(uint64) int64 {
	var sh uint
	switch cl {
	case lI8, lU8:
		sh = 56
	case lI16, lU16:
		sh = 48
	case lI32, lU32:
		sh = 32
	}
	return func(v uint64) int64 { return int64(v<<sh) >> sh }
}

// binArithNode compiles + - * / % and the bitwise operators over two
// operands of the same class.
func binArithNode(op string, x, y node) (node, error) {
	cl := x.class
	switch {
	case cl == lStr:
		if op != "+" {
			return node{}, fmt.Errorf("operator %s has no string node", op)
		}
		xf, yf := x.S, y.S
		return node{class: lStr, S: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (string, error) {
			a, err := xf(fr, ctx, st, d)
			if err != nil {
				return "", err
			}
			b, err := yf(fr, ctx, st, d)
			if err != nil {
				return "", err
			}
			return a + b, nil
		}}, nil
	case cl.float():
		var g func(a, b float64) float64
		if cl == lF32 {
			// One rounding at 32 bits per operation, as compiled Go
			// rounds; the operands arrive as exact float32 values.
			switch op {
			case "+":
				g = func(a, b float64) float64 { return float64(float32(a) + float32(b)) }
			case "-":
				g = func(a, b float64) float64 { return float64(float32(a) - float32(b)) }
			case "*":
				g = func(a, b float64) float64 { return float64(float32(a) * float32(b)) }
			case "/":
				g = func(a, b float64) float64 { return float64(float32(a) / float32(b)) }
			}
		} else {
			switch op {
			case "+":
				g = func(a, b float64) float64 { return a + b }
			case "-":
				g = func(a, b float64) float64 { return a - b }
			case "*":
				g = func(a, b float64) float64 { return a * b }
			case "/":
				g = func(a, b float64) float64 { return a / b }
			}
		}
		if g == nil {
			return node{}, fmt.Errorf("operator %s has no float node", op)
		}
		xf, yf := x.F, y.F
		return node{class: cl, F: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (float64, error) {
			a, err := xf(fr, ctx, st, d)
			if err != nil {
				return 0, err
			}
			b, err := yf(fr, ctx, st, d)
			if err != nil {
				return 0, err
			}
			return g(a, b), nil
		}}, nil
	}
	mask := classMask(cl)
	signed := classSigned(cl)
	sx := sxFn(cl)
	var g func(a, b uint64) uint64
	switch op {
	case "+":
		g = func(a, b uint64) uint64 { return (a + b) & mask }
	case "-":
		g = func(a, b uint64) uint64 { return (a - b) & mask }
	case "*":
		g = func(a, b uint64) uint64 { return (a * b) & mask }
	case "/":
		// The division panics on a zero divisor inside the closure,
		// natively; the signed edge case, the most negative value over
		// -1, wraps to itself the way the Go spec fixes it, because
		// the int64 quotient masks back to the width.
		if signed {
			g = func(a, b uint64) uint64 { return uint64(sx(a)/sx(b)) & mask }
		} else {
			g = func(a, b uint64) uint64 { return a / b }
		}
	case "%":
		if signed {
			g = func(a, b uint64) uint64 { return uint64(sx(a)%sx(b)) & mask }
		} else {
			g = func(a, b uint64) uint64 { return a % b }
		}
	case "&":
		g = func(a, b uint64) uint64 { return a & b }
	case "|":
		g = func(a, b uint64) uint64 { return a | b }
	case "^":
		g = func(a, b uint64) uint64 { return a ^ b }
	case "&^":
		g = func(a, b uint64) uint64 { return a &^ b }
	default:
		return node{}, fmt.Errorf("operator %s has no integer node", op)
	}
	xf, yf := x.N, y.N
	return node{class: cl, N: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (uint64, error) {
		a, err := xf(fr, ctx, st, d)
		if err != nil {
			return 0, err
		}
		b, err := yf(fr, ctx, st, d)
		if err != nil {
			return 0, err
		}
		return g(a, b), nil
	}}, nil
}

// shiftNode compiles << and >>. The count keeps its own class: a
// signed count sign-extends and a negative one panics natively inside
// the shift, a count past the width produces the zero or the sign
// fill Go defines, and the result masks back to the left operand's
// canonical form.
func shiftNode(op string, x, y node) (node, error) {
	cl, ycl := x.class, y.class
	mask := classMask(cl)
	signed := classSigned(cl)
	sx := sxFn(cl)
	countSigned := classSigned(ycl)
	csx := sxFn(ycl)
	var g func(a, b uint64) uint64
	switch {
	case op == "<<" && countSigned:
		g = func(a, b uint64) uint64 { return (a << csx(b)) & mask }
	case op == "<<":
		g = func(a, b uint64) uint64 { return (a << b) & mask }
	case signed && countSigned:
		g = func(a, b uint64) uint64 { return uint64(sx(a)>>csx(b)) & mask }
	case signed:
		g = func(a, b uint64) uint64 { return uint64(sx(a)>>b) & mask }
	case countSigned:
		g = func(a, b uint64) uint64 { return a >> csx(b) }
	default:
		g = func(a, b uint64) uint64 { return a >> b }
	}
	xf, yf := x.N, y.N
	return node{class: cl, N: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (uint64, error) {
		a, err := xf(fr, ctx, st, d)
		if err != nil {
			return 0, err
		}
		b, err := yf(fr, ctx, st, d)
		if err != nil {
			return 0, err
		}
		return g(a, b), nil
	}}, nil
}
