package gozero

import (
	"context"
	"fmt"
	"strings"
	"unsafe" // also required by go:linkname
)

// The expression tree on the direct tier. Every node is a combinator
// in its layout class: operands arrive as machine words and the
// result leaves as one, so an expression of any depth is a chain of
// native operations with no reflect anywhere. The class carries
// scalars zero-extended, so the signed operations sign-extend at the
// class width first and mask back after; division by zero and a
// negative shift count panic natively inside the closure, exactly
// where compiled Go panics.

// exprTree compiles one vmExpr node. The reflect compiler already
// typed the tree and admitted the operator per kind, so every case
// below is total over what reaches it.
func (c *jitCompiler) exprTree(e *vmExpr) (node, error) {
	if e.leaf != nil {
		cl := layoutOf(e.t)
		if cl == lBad {
			return node{}, fmt.Errorf("an operand of type %s has no layout class", e.t)
		}
		return c.argNode(e.leaf, e.t, cl)
	}
	x, err := c.exprTree(e.x)
	if err != nil {
		return node{}, err
	}
	if e.y == nil {
		return unaryExprNode(e.op, x)
	}
	if e.op == "&&" || e.op == "||" {
		y, err := c.exprTree(e.y)
		if err != nil {
			return node{}, err
		}
		return logicNode(e.op, x, y), nil
	}
	y, err := c.exprTree(e.y)
	if err != nil {
		return node{}, err
	}
	switch e.op {
	case "<<", ">>":
		return shiftNode(e.op, x, y)
	case "==", "!=", "<", "<=", ">", ">=":
		return cmpNode(e.op, x, y)
	}
	if e.op == "+" && x.class == lStr {
		return c.concatChainNode(e)
	}
	return binArithNode(e.op, x, y)
}

// concatChainNode flattens a left-leaning chain of string + into one
// build, the way the Go compiler lowers a + b + c to a single
// concatstrings call: the parts are leaves after flattening, so they
// evaluate once into a fixed buffer, and the result allocates once
// whatever the chain's length. Like the runtime, a chain with at most
// one non-empty part returns that part without copying. A chain
// longer than the buffer folds pairwise instead, one allocation per
// node past the first.
func (c *jitCompiler) concatChainNode(e *vmExpr) (node, error) {
	var flat []*vmExpr
	var walk func(*vmExpr)
	walk = func(n *vmExpr) {
		if n.op == "+" {
			walk(n.x)
			walk(n.y)
			return
		}
		flat = append(flat, n)
	}
	walk(e)
	if len(flat) > 8 {
		x, err := c.exprTree(e.x)
		if err != nil {
			return node{}, err
		}
		y, err := c.exprTree(e.y)
		if err != nil {
			return node{}, err
		}
		return binArithNode("+", x, y)
	}
	parts := make([]nodeS, len(flat))
	for i, pe := range flat {
		pn, err := c.exprTree(pe)
		if err != nil {
			return node{}, err
		}
		if pn.class != lStr {
			return node{}, fmt.Errorf("a %s operand cannot join a string concat", pn.class)
		}
		parts[i] = pn.S
	}
	if len(parts) == 2 {
		xf, yf := parts[0], parts[1]
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
	return node{class: lStr, S: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (string, error) {
		var vals [8]string
		total, nonEmpty, last := 0, 0, 0
		for i, f := range parts {
			v, err := f(fr, ctx, st, d)
			if err != nil {
				return "", err
			}
			vals[i] = v
			total += len(v)
			if v != "" {
				nonEmpty++
				last = i
			}
		}
		if nonEmpty <= 1 {
			return vals[last], nil
		}
		var sb strings.Builder
		sb.Grow(total)
		for i := range parts {
			sb.WriteString(vals[i])
		}
		return sb.String(), nil
	}}, nil
}

// unaryExprNode compiles - ^ and ! over one operand.
func unaryExprNode(op string, x node) (node, error) {
	cl := x.class
	if op == "-" && cl.float() {
		f := x.F
		return node{class: cl, F: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (float64, error) {
			v, err := f(fr, ctx, st, d)
			return -v, err
		}}, nil
	}
	mask := classMask(cl)
	var g func(uint64) uint64
	switch op {
	case "-":
		g = func(a uint64) uint64 { return (0 - a) & mask }
	case "^":
		g = func(a uint64) uint64 { return (^a) & mask }
	case "!":
		g = func(a uint64) uint64 { return a ^ 1 }
	default:
		return node{}, fmt.Errorf("unary %s has no node", op)
	}
	f := x.N
	return node{class: cl, N: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (uint64, error) {
		v, err := f(fr, ctx, st, d)
		if err != nil {
			return 0, err
		}
		return g(v), nil
	}}, nil
}

// logicNode compiles && and ||. The right side is a closure, so the
// laziness is free: it only runs when the left does not decide.
func logicNode(op string, x, y node) node {
	xf, yf := x.N, y.N
	and := op == "&&"
	return node{class: lBool, N: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (uint64, error) {
		a, err := xf(fr, ctx, st, d)
		if err != nil {
			return 0, err
		}
		if (a != 0) != and {
			return a, nil
		}
		return yf(fr, ctx, st, d)
	}}
}

// cmpNode compiles the six comparisons, dispatching on the operand
// class: strings compare as strings, floats as float64, where a NaN
// still compares unequal to itself, and integers at their width,
// sign-extended when the class is signed. Equality on the canonical
// zero-extended bits is value equality, because zero-extension is
// injective.
func cmpNode(op string, x, y node) (node, error) {
	switch {
	case x.class == lStr:
		var p func(a, b string) bool
		switch op {
		case "==":
			p = func(a, b string) bool { return a == b }
		case "!=":
			p = func(a, b string) bool { return a != b }
		case "<":
			p = func(a, b string) bool { return a < b }
		case "<=":
			p = func(a, b string) bool { return a <= b }
		case ">":
			p = func(a, b string) bool { return a > b }
		case ">=":
			p = func(a, b string) bool { return a >= b }
		}
		xf, yf := x.S, y.S
		return node{class: lBool, N: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (uint64, error) {
			a, err := xf(fr, ctx, st, d)
			if err != nil {
				return 0, err
			}
			b, err := yf(fr, ctx, st, d)
			if err != nil {
				return 0, err
			}
			return boolBits(p(a, b)), nil
		}}, nil
	case x.class.float():
		var p func(a, b float64) bool
		switch op {
		case "==":
			p = func(a, b float64) bool { return a == b }
		case "!=":
			p = func(a, b float64) bool { return a != b }
		case "<":
			p = func(a, b float64) bool { return a < b }
		case "<=":
			p = func(a, b float64) bool { return a <= b }
		case ">":
			p = func(a, b float64) bool { return a > b }
		case ">=":
			p = func(a, b float64) bool { return a >= b }
		}
		xf, yf := x.F, y.F
		return node{class: lBool, N: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (uint64, error) {
			a, err := xf(fr, ctx, st, d)
			if err != nil {
				return 0, err
			}
			b, err := yf(fr, ctx, st, d)
			if err != nil {
				return 0, err
			}
			return boolBits(p(a, b)), nil
		}}, nil
	}
	var p func(a, b uint64) bool
	if classSigned(x.class) {
		sx := sxFn(x.class)
		switch op {
		case "==":
			p = func(a, b uint64) bool { return a == b }
		case "!=":
			p = func(a, b uint64) bool { return a != b }
		case "<":
			p = func(a, b uint64) bool { return sx(a) < sx(b) }
		case "<=":
			p = func(a, b uint64) bool { return sx(a) <= sx(b) }
		case ">":
			p = func(a, b uint64) bool { return sx(a) > sx(b) }
		case ">=":
			p = func(a, b uint64) bool { return sx(a) >= sx(b) }
		}
	} else {
		switch op {
		case "==":
			p = func(a, b uint64) bool { return a == b }
		case "!=":
			p = func(a, b uint64) bool { return a != b }
		case "<":
			p = func(a, b uint64) bool { return a < b }
		case "<=":
			p = func(a, b uint64) bool { return a <= b }
		case ">":
			p = func(a, b uint64) bool { return a > b }
		case ">=":
			p = func(a, b uint64) bool { return a >= b }
		}
	}
	xf, yf := x.N, y.N
	return node{class: lBool, N: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (uint64, error) {
		a, err := xf(fr, ctx, st, d)
		if err != nil {
			return 0, err
		}
		b, err := yf(fr, ctx, st, d)
		if err != nil {
			return 0, err
		}
		return boolBits(p(a, b)), nil
	}}, nil
}

func boolBits(b bool) uint64 {
	if b {
		return 1
	}
	return 0
}
