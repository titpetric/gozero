package gozero

import (
	"context"
	"fmt"
	"unsafe" // also required by go:linkname
)

// The condition and three-clause loops on the direct tier. The
// header lowers through the nodes that already exist: a bool
// condition is condNode, the comparison is cmpNode, and the post
// clause is the incNode a step statement compiles to, a load, an add
// and a truncating store at the loop variable's frame offset. The
// body is the []nodeE blockNodes builds for an if arm, folded by the
// same signal reader a range body uses.
//
// Neither form carries a data bound, so the per-iteration ctx.Err()
// check is the bound, the same check every range form runs.

// forNode compiles one condition or three-clause loop.
func (c *jitCompiler) forNode(f *vmFor, jp *jitProgram) (nodeE, error) {
	body, err := c.blockNodes(f.body, jp)
	if err != nil {
		return nil, err
	}
	step := foldSignals(body)
	test, err := c.testNode(f)
	if err != nil {
		return nil, err
	}

	var init, post nodeE
	if f.initSlot >= 0 {
		if init, err = c.forInitNode(f); err != nil {
			return nil, err
		}
		if post, err = c.incNode(f.post); err != nil {
			return nil, err
		}
	}
	return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
		if init != nil {
			if err := init(fr, ctx, st, d); err != nil {
				return err
			}
		}
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			bits, err := test(fr, ctx, st, d)
			if err != nil {
				return err
			}
			if bits == 0 {
				return nil
			}
			more, err := step(fr, ctx, st, d)
			if err != nil || !more {
				return err
			}
			if post != nil {
				// A break leaves through step above, so the post
				// clause runs after a completed body and after a
				// continue, as in Go.
				if err := post(fr, ctx, st, d); err != nil {
					return err
				}
			}
		}
	}, nil
}

// testNode compiles the header test to the bool bits an iteration
// turns on: the same two nodes an if header lowers its condition
// with.
func (c *jitCompiler) testNode(f *vmFor) (nodeN, error) {
	if f.cmp != nil {
		return c.cmpNode(f.cmp)
	}
	return c.condNode(f.cond)
}

// forInitNode stores the init clause into the loop variable's frame
// slot, once, before the first test. The operand compiles through
// the comparison kit's reader, so a call in init position lowers
// exactly like a call in operand position.
func (c *jitCompiler) forInitNode(f *vmFor) (nodeE, error) {
	field, ok := c.slotOf[f.initSlot]
	if !ok {
		return nil, fmt.Errorf("a loop variable has no slot")
	}
	cl := layoutOf(c.types[field])
	if !cl.scalar() || cl.float() || cl == lBool {
		return nil, fmt.Errorf("a loop variable of type %s has no integer class", c.types[field])
	}
	iv, err := c.cmpOperandNode(f.initVal, f.varType, cl)
	if err != nil {
		return nil, err
	}
	off, n := c.offs[field], iv.N
	return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
		bits, err := n(fr, ctx, st, d)
		if err != nil {
			return err
		}
		storeN(cl, unsafe.Add(fr, off), bits)
		return nil
	}, nil
}
