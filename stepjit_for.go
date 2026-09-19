package gozero

import (
	"context"
	"fmt"
	"reflect"
	"unsafe" // also required by go:linkname
)

// The condition and three-clause loops on the direct tier. The loop
// variable is a frame slot: the init stores it, the comparison loads
// the operands as their own scalar classes and compares in the 64-bit
// domain, and the post clause is a load, an add and a truncating
// store, which is the wrap Go's ++ has. A bool condition is one load,
// one field read, or one call per iteration. Neither form carries a
// data bound, so the per-iteration ctx.Err() check is the bound, the
// same check every range form runs.

var boolType = reflect.TypeFor[bool]()

// testFn evaluates a loop's condition or comparison once.
type testFn func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (bool, error)

// foldSignals compiles a loop body into its step: continue ends the
// iteration, break ends the loop, any other error ends the program.
// Shared by every loop form on this tier.
func foldSignals(body []nodeE) stepFn {
	return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (bool, error) {
		for _, n := range body {
			if err := n(fr, ctx, st, d); err != nil {
				switch err {
				case errLoopContinue:
					return true, nil
				case errLoopBreak:
					return false, nil
				}
				return false, err
			}
		}
		return true, nil
	}
}

// forNode compiles one condition or three-clause loop.
func (c *jitCompiler) forNode(pf *plannedFor, jp *jitProgram) (nodeE, error) {
	src := pf.src
	body := make([]nodeE, 0, len(pf.body))
	for _, bs := range pf.body {
		n, err := c.stmtNode(bs, jp)
		if err != nil {
			return nil, err
		}
		body = append(body, n)
	}
	step := foldSignals(body)
	test, err := c.testNode(src)
	if err != nil {
		return nil, err
	}

	if src.initSlot < 0 {
		return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
			for {
				if err := ctx.Err(); err != nil {
					return err
				}
				ok, err := test(fr, ctx, st, d)
				if err != nil {
					return err
				}
				if !ok {
					return nil
				}
				more, err := step(fr, ctx, st, d)
				if err != nil || !more {
					return err
				}
			}
		}, nil
	}

	field, ok := c.slotOf[src.initSlot]
	if !ok {
		return nil, fmt.Errorf("a loop variable has no slot")
	}
	off, cl := c.offs[field], layoutOf(c.types[field])
	if !cl.scalar() || cl.float() || cl == lBool {
		return nil, fmt.Errorf("a loop variable of type %s has no integer class", c.types[field])
	}
	iv, err := c.argNode(src.initVal, src.varType, cl)
	if err != nil {
		return nil, err
	}
	ivN, dec := iv.N, src.postDec
	return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
		bits, err := ivN(fr, ctx, st, d)
		if err != nil {
			return err
		}
		at := unsafe.Add(fr, off)
		storeN(cl, at, bits)
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			ok, err := test(fr, ctx, st, d)
			if err != nil {
				return err
			}
			if !ok {
				return nil
			}
			more, err := step(fr, ctx, st, d)
			if err != nil || !more {
				return err
			}
			// The step in the bit domain with the truncating store is
			// the wrap the reflect tier's SetInt performs.
			b := loadN(at, cl)
			if dec {
				b--
			} else {
				b++
			}
			storeN(cl, at, b)
		}
	}, nil
}

// testNode compiles a loop's header test: the bool condition, or the
// comparison over two integer operands, each widened at its own
// signedness.
func (c *jitCompiler) testNode(f *vmFor) (testFn, error) {
	if f.cond != nil {
		n, err := c.argNode(f.cond, boolType, lBool)
		if err != nil {
			return nil, err
		}
		nf := n.N
		return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (bool, error) {
			bits, err := nf(fr, ctx, st, d)
			return bits != 0, err
		}, nil
	}
	xn, err := c.cmpOperandNode(f.cmp.x)
	if err != nil {
		return nil, err
	}
	yn, err := c.cmpOperandNode(f.cmp.y)
	if err != nil {
		return nil, err
	}
	xf, yf, op := xn.N, yn.N, f.cmp.op
	if f.cmp.unsigned {
		// loadN zero-extends, so unsigned bits compare directly.
		return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (bool, error) {
			xb, err := xf(fr, ctx, st, d)
			if err != nil {
				return false, err
			}
			yb, err := yf(fr, ctx, st, d)
			if err != nil {
				return false, err
			}
			return cmpUnsigned(op, xb, yb), nil
		}, nil
	}
	xc, yc := xn.class, yn.class
	return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (bool, error) {
		xb, err := xf(fr, ctx, st, d)
		if err != nil {
			return false, err
		}
		yb, err := yf(fr, ctx, st, d)
		if err != nil {
			return false, err
		}
		return cmpSigned(op, signedOf(xc, xb), signedOf(yc, yb)), nil
	}, nil
}

// cmpOperandNode compiles one comparison operand as its own scalar
// class.
func (c *jitCompiler) cmpOperandNode(a *vmArg) (node, error) {
	cl := layoutOf(a.typ)
	if !cl.scalar() || cl.float() || cl == lBool {
		return node{}, fmt.Errorf("a comparison operand of type %s has no integer class", a.typ)
	}
	return c.argNode(a, a.typ, cl)
}
