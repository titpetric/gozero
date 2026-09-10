package gozero

import (
	"context"
	"fmt"
	"unsafe" // also required by go:linkname
)

// The binary operator families over the scalar, string and pointer
// classes; the dispatch and the operand plumbing are in
// stepjit_expr.go.

// intBinaryNode covers the integer and bool classes: nodeN in,
// nodeN out, results normalized to the operand class.
func intBinaryNode(op string, x, y node) (node, error) {
	cl := x.class
	xf, yf := x.N, y.N
	bin := func(f func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any, a, b uint64) uint64) node {
		return node{class: cl, N: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (uint64, error) {
			a, err := xf(fr, ctx, st, d)
			if err != nil {
				return 0, err
			}
			b, err := yf(fr, ctx, st, d)
			if err != nil {
				return 0, err
			}
			return f(fr, ctx, st, d, a, b), nil
		}}
	}
	cmp := func(f func(a, b uint64) bool) node {
		n := bin(func(_ unsafe.Pointer, _ context.Context, _ map[string]any, _ any, a, b uint64) uint64 {
			if f(a, b) {
				return 1
			}
			return 0
		})
		n.class = lBool
		return n
	}
	signed := signedClass(cl)
	ycl := y.class
	switch op {
	case "+":
		return bin(func(_ unsafe.Pointer, _ context.Context, _ map[string]any, _ any, a, b uint64) uint64 {
			return normN(cl, a+b)
		}), nil
	case "-":
		return bin(func(_ unsafe.Pointer, _ context.Context, _ map[string]any, _ any, a, b uint64) uint64 {
			return normN(cl, a-b)
		}), nil
	case "*":
		return bin(func(_ unsafe.Pointer, _ context.Context, _ map[string]any, _ any, a, b uint64) uint64 {
			return normN(cl, a*b)
		}), nil
	case "/":
		if signed {
			return bin(func(_ unsafe.Pointer, _ context.Context, _ map[string]any, _ any, a, b uint64) uint64 {
				return normN(cl, uint64(asSigned(cl, a)/asSigned(cl, b)))
			}), nil
		}
		return bin(func(_ unsafe.Pointer, _ context.Context, _ map[string]any, _ any, a, b uint64) uint64 {
			return normN(cl, a/b)
		}), nil
	case "%":
		if signed {
			return bin(func(_ unsafe.Pointer, _ context.Context, _ map[string]any, _ any, a, b uint64) uint64 {
				return normN(cl, uint64(asSigned(cl, a)%asSigned(cl, b)))
			}), nil
		}
		return bin(func(_ unsafe.Pointer, _ context.Context, _ map[string]any, _ any, a, b uint64) uint64 {
			return normN(cl, a%b)
		}), nil
	case "&":
		return bin(func(_ unsafe.Pointer, _ context.Context, _ map[string]any, _ any, a, b uint64) uint64 {
			return a & b
		}), nil
	case "|":
		return bin(func(_ unsafe.Pointer, _ context.Context, _ map[string]any, _ any, a, b uint64) uint64 {
			return a | b
		}), nil
	case "^":
		return bin(func(_ unsafe.Pointer, _ context.Context, _ map[string]any, _ any, a, b uint64) uint64 {
			return a ^ b
		}), nil
	case "&^":
		return bin(func(_ unsafe.Pointer, _ context.Context, _ map[string]any, _ any, a, b uint64) uint64 {
			return a &^ b
		}), nil
	case "<<":
		if signed {
			return bin(func(_ unsafe.Pointer, _ context.Context, _ map[string]any, _ any, a, b uint64) uint64 {
				return normN(cl, uint64(asSigned(cl, a)<<shiftCountN(ycl, b)))
			}), nil
		}
		return bin(func(_ unsafe.Pointer, _ context.Context, _ map[string]any, _ any, a, b uint64) uint64 {
			return normN(cl, a<<shiftCountN(ycl, b))
		}), nil
	case ">>":
		if signed {
			return bin(func(_ unsafe.Pointer, _ context.Context, _ map[string]any, _ any, a, b uint64) uint64 {
				return normN(cl, uint64(asSigned(cl, a)>>shiftCountN(ycl, b)))
			}), nil
		}
		return bin(func(_ unsafe.Pointer, _ context.Context, _ map[string]any, _ any, a, b uint64) uint64 {
			return normN(cl, a>>shiftCountN(ycl, b))
		}), nil
	case "==":
		return cmp(func(a, b uint64) bool { return a == b }), nil
	case "!=":
		return cmp(func(a, b uint64) bool { return a != b }), nil
	case "<":
		if signed {
			return cmp(func(a, b uint64) bool { return asSigned(cl, a) < asSigned(cl, b) }), nil
		}
		return cmp(func(a, b uint64) bool { return a < b }), nil
	case "<=":
		if signed {
			return cmp(func(a, b uint64) bool { return asSigned(cl, a) <= asSigned(cl, b) }), nil
		}
		return cmp(func(a, b uint64) bool { return a <= b }), nil
	case ">":
		if signed {
			return cmp(func(a, b uint64) bool { return asSigned(cl, a) > asSigned(cl, b) }), nil
		}
		return cmp(func(a, b uint64) bool { return a > b }), nil
	case ">=":
		if signed {
			return cmp(func(a, b uint64) bool { return asSigned(cl, a) >= asSigned(cl, b) }), nil
		}
		return cmp(func(a, b uint64) bool { return a >= b }), nil
	}
	return node{}, fmt.Errorf("operator %s over class %s is not in the table", op, cl)
}

// floatBinaryNode covers lF32 and lF64. A float32 operation rounds
// at 32 bits per step, as compiled Go does; running it in float64
// and truncating once would double-round.
func floatBinaryNode(op string, x, y node) (node, error) {
	cl := x.class
	xf, yf := x.F, y.F
	f32 := cl == lF32
	arith := func(f func(a, b float64) float64) node {
		return node{class: cl, F: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (float64, error) {
			a, err := xf(fr, ctx, st, d)
			if err != nil {
				return 0, err
			}
			b, err := yf(fr, ctx, st, d)
			if err != nil {
				return 0, err
			}
			if f32 {
				return float64(float32(f(a, b))), nil
			}
			return f(a, b), nil
		}}
	}
	cmp := func(f func(a, b float64) bool) node {
		return node{class: lBool, N: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (uint64, error) {
			a, err := xf(fr, ctx, st, d)
			if err != nil {
				return 0, err
			}
			b, err := yf(fr, ctx, st, d)
			if err != nil {
				return 0, err
			}
			if f32 {
				a, b = float64(float32(a)), float64(float32(b))
			}
			if f(a, b) {
				return 1, nil
			}
			return 0, nil
		}}
	}
	switch op {
	case "+":
		return arith(func(a, b float64) float64 { return a + b }), nil
	case "-":
		return arith(func(a, b float64) float64 { return a - b }), nil
	case "*":
		return arith(func(a, b float64) float64 { return a * b }), nil
	case "/":
		return arith(func(a, b float64) float64 { return a / b }), nil
	case "==":
		return cmp(func(a, b float64) bool { return a == b }), nil
	case "!=":
		return cmp(func(a, b float64) bool { return a != b }), nil
	case "<":
		return cmp(func(a, b float64) bool { return a < b }), nil
	case "<=":
		return cmp(func(a, b float64) bool { return a <= b }), nil
	case ">":
		return cmp(func(a, b float64) bool { return a > b }), nil
	case ">=":
		return cmp(func(a, b float64) bool { return a >= b }), nil
	}
	return node{}, fmt.Errorf("operator %s over class %s is not in the table", op, cl)
}

func stringBinaryNode(op string, x, y node) (node, error) {
	xf, yf := x.S, y.S
	if op == "+" {
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
	}
	cmp := func(f func(a, b string) bool) node {
		return node{class: lBool, N: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (uint64, error) {
			a, err := xf(fr, ctx, st, d)
			if err != nil {
				return 0, err
			}
			b, err := yf(fr, ctx, st, d)
			if err != nil {
				return 0, err
			}
			if f(a, b) {
				return 1, nil
			}
			return 0, nil
		}}
	}
	switch op {
	case "==":
		return cmp(func(a, b string) bool { return a == b }), nil
	case "!=":
		return cmp(func(a, b string) bool { return a != b }), nil
	case "<":
		return cmp(func(a, b string) bool { return a < b }), nil
	case "<=":
		return cmp(func(a, b string) bool { return a <= b }), nil
	case ">":
		return cmp(func(a, b string) bool { return a > b }), nil
	case ">=":
		return cmp(func(a, b string) bool { return a >= b }), nil
	}
	return node{}, fmt.Errorf("operator %s over strings is not in the table", op)
}

func ptrBinaryNode(op string, x, y node) (node, error) {
	xf, yf := x.P, y.P
	eq := op == "=="
	if op != "==" && op != "!=" {
		return node{}, fmt.Errorf("operator %s over pointers is not in the table", op)
	}
	return node{class: lBool, N: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (uint64, error) {
		a, err := xf(fr, ctx, st, d)
		if err != nil {
			return 0, err
		}
		b, err := yf(fr, ctx, st, d)
		if err != nil {
			return 0, err
		}
		if (a == b) == eq {
			return 1, nil
		}
		return 0, nil
	}}, nil
}
