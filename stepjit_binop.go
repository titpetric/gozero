package gozero

import (
	"context"
	"fmt"
	"unsafe" // also required by go:linkname
)

// The operator assignment on the direct tier. The operands arrive in
// their layout class and the result leaves in its own, so an operator
// is a combinator between two loads and a store: a native +, == or !=
// between machine words, no reflect anywhere. Wraparound is the mask
// at the class width, a float32 sum rounds once at 32 bits, and a
// string + is Go's own concatenation, which allocates exactly where
// compiled Go allocates.

// classMask is the identity mask of an integer class's width. Summing
// inside the closure and masking keeps the value in the canonical
// zero-extended form nodeN carries everywhere.
func classMask(cl layout) uint64 {
	switch cl {
	case lI8, lU8:
		return 0xFF
	case lI16, lU16:
		return 0xFFFF
	case lI32, lU32:
		return 0xFFFFFFFF
	}
	return ^uint64(0)
}

// binopNode compiles s := a + b. The operand class is the slot
// type's, and compileBinop admits only kinds that have one, so every
// case below is total over what reaches it.
func (c *jitCompiler) binopNode(b *vmBinop) (node, error) {
	cl := layoutOf(b.t)
	if cl == lBad {
		return node{}, fmt.Errorf("an operand of type %s has no layout class", b.t)
	}
	x, err := c.argNode(b.x, b.t, cl)
	if err != nil {
		return node{}, err
	}
	y, err := c.argNode(b.y, b.t, cl)
	if err != nil {
		return node{}, err
	}
	if b.op == "+" {
		switch {
		case cl == lStr:
			xf, yf := x.S, y.S
			return node{class: lStr, S: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (string, error) {
				a, err := xf(fr, ctx, st, d)
				if err != nil {
					return "", err
				}
				bb, err := yf(fr, ctx, st, d)
				if err != nil {
					return "", err
				}
				return a + bb, nil
			}}, nil
		case cl.float():
			xf, yf := x.F, y.F
			f32 := cl == lF32
			return node{class: cl, F: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (float64, error) {
				a, err := xf(fr, ctx, st, d)
				if err != nil {
					return 0, err
				}
				bb, err := yf(fr, ctx, st, d)
				if err != nil {
					return 0, err
				}
				sum := a + bb
				if f32 {
					// One rounding at 32 bits, as compiled Go rounds. The
					// float64 sum of two float32 values carries enough
					// precision that rounding it once cannot double-round.
					sum = float64(float32(sum))
				}
				return sum, nil
			}}, nil
		default:
			xf, yf := x.N, y.N
			mask := classMask(cl)
			return node{class: cl, N: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (uint64, error) {
				a, err := xf(fr, ctx, st, d)
				if err != nil {
					return 0, err
				}
				bb, err := yf(fr, ctx, st, d)
				if err != nil {
					return 0, err
				}
				return (a + bb) & mask, nil
			}}, nil
		}
	}
	// == and !=. The scalar classes compare their canonical bits:
	// zero-extension is injective, so bit equality is value equality,
	// and the float compare runs on the widened values, where a NaN
	// still compares unequal to itself.
	neq := b.op == "!="
	switch {
	case cl == lStr:
		xf, yf := x.S, y.S
		return node{class: lBool, N: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (uint64, error) {
			a, err := xf(fr, ctx, st, d)
			if err != nil {
				return 0, err
			}
			bb, err := yf(fr, ctx, st, d)
			if err != nil {
				return 0, err
			}
			if (a == bb) != neq {
				return 1, nil
			}
			return 0, nil
		}}, nil
	case cl.float():
		xf, yf := x.F, y.F
		return node{class: lBool, N: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (uint64, error) {
			a, err := xf(fr, ctx, st, d)
			if err != nil {
				return 0, err
			}
			bb, err := yf(fr, ctx, st, d)
			if err != nil {
				return 0, err
			}
			if (a == bb) != neq {
				return 1, nil
			}
			return 0, nil
		}}, nil
	default:
		xf, yf := x.N, y.N
		return node{class: lBool, N: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (uint64, error) {
			a, err := xf(fr, ctx, st, d)
			if err != nil {
				return 0, err
			}
			bb, err := yf(fr, ctx, st, d)
			if err != nil {
				return 0, err
			}
			if (a == bb) != neq {
				return 1, nil
			}
			return 0, nil
		}}, nil
	}
}
