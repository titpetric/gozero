package gozero

import (
	"context"
	"fmt"
	"unsafe" // also required by go:linkname
)

// The range loop on the direct tier. Ranging a slice is a length
// check and a stride walk over the sliceHdr the closure tree already
// produces; ranging an integer is a counted loop over its bits. The
// loop variable is a frame slot written per iteration through the
// same typed stores every other statement uses, the body is the
// []nodeE blockNodes builds for an if arm, and every iteration checks
// the execution context, which is what bounds a loop the way the
// statement count bounds a straight line. An element type with no
// layout class stays on the reflect evaluator, and so does an array,
// which has no slice header.

// stepFn runs one iteration's body: more is false when a break, or
// the error that ends the run, stops the loop. It folds the loop
// signals the way the reflect tier's step does: continue ends the
// iteration, break ends the loop, any other error ends the program.
type stepFn func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (more bool, err error)

// raiseSignal is the compiled form of break and continue: the signal
// travels the error return and the innermost loop's step consumes it.
func raiseSignal(sig error) nodeE {
	return func(unsafe.Pointer, context.Context, map[string]any, any) error {
		return sig
	}
}

// rangeNode compiles one range loop.
func (c *jitCompiler) rangeNode(r *vmRange, jp *jitProgram) (nodeE, error) {
	body, err := c.blockNodes(r.body, jp)
	if err != nil {
		return nil, err
	}
	step := stepFn(func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (bool, error) {
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
	})
	if r.overInt {
		return c.intRangeNode(r, step)
	}
	return c.sliceRangeNode(r, step)
}

// scalarKey resolves the loop key's frame offset and scalar class, or
// reports that the key is blank. Both shipped forms count, so both
// need a scalar there.
func (c *jitCompiler) scalarKey(r *vmRange) (uintptr, layout, bool, error) {
	if r.keySlot < 0 {
		return 0, lBad, false, nil
	}
	field, ok := c.slotOf[r.keySlot]
	if !ok {
		return 0, lBad, false, fmt.Errorf("a range key has no slot")
	}
	off, cl := c.offs[field], layoutOf(c.types[field])
	if !cl.scalar() {
		return 0, lBad, false, fmt.Errorf("a range key of type %s has no scalar class", c.types[field])
	}
	return off, cl, true, nil
}

// intRangeNode is the go1.22 integer range: a counted loop over the
// bound's bits.
func (c *jitCompiler) intRangeNode(r *vmRange, step stepFn) (nodeE, error) {
	keyOff, keyCl, hasKey, err := c.scalarKey(r)
	if err != nil {
		return nil, err
	}
	cl := layoutOf(r.over.typ)
	if !cl.scalar() || cl.float() {
		return nil, fmt.Errorf("ranging over class %s is not in the table", cl)
	}
	over, err := c.argNode(r.over, r.over.typ, cl)
	if err != nil {
		return nil, err
	}
	xf, signed := over.N, cl == lI8 || cl == lI16 || cl == lI32 || cl == lI64
	return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
		bits, err := xf(fr, ctx, st, d)
		if err != nil {
			return err
		}
		// loadN zero-extends, so a signed bound is decoded at its own
		// width; a negative one runs zero times, as in Go.
		count := bits
		if signed {
			count = 0
			if v := signedOf(cl, bits); v > 0 {
				count = uint64(v)
			}
		}
		for i := uint64(0); i < count; i++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			if hasKey {
				storeN(keyCl, unsafe.Add(fr, keyOff), i)
			}
			more, err := step(fr, ctx, st, d)
			if err != nil || !more {
				return err
			}
		}
		return nil
	}, nil
}

// sliceRangeNode walks a sliceHdr by stride, copying each element
// into the value slot.
func (c *jitCompiler) sliceRangeNode(r *vmRange, step stepFn) (nodeE, error) {
	keyOff, keyCl, hasKey, err := c.scalarKey(r)
	if err != nil {
		return nil, err
	}
	if layoutOf(r.over.typ) != lSlice {
		return nil, fmt.Errorf("ranging over %s is not in the table", r.over.typ)
	}
	over, err := c.argNode(r.over, r.over.typ, lSlice)
	if err != nil {
		return nil, err
	}

	valOff, valSize := uintptr(0), uintptr(0)
	valCl := lBad
	if r.valSlot >= 0 {
		field, ok := c.slotOf[r.valSlot]
		if !ok {
			return nil, fmt.Errorf("a range value has no slot")
		}
		vt := c.types[field]
		valOff, valCl, valSize = c.offs[field], layoutOf(vt), vt.Size()
		if valCl == lBad {
			return nil, fmt.Errorf("a range value of type %s has no layout class", vt)
		}
	}
	// copyVal moves one element into the value slot: Go's range
	// variable is a copy, not a view into the slice. The typed store
	// keeps the write barrier on pointer-carrying classes.
	copyVal := func(fr unsafe.Pointer, h sliceHdr, i int) {
		if valCl == lBad {
			return
		}
		at := unsafe.Add(h.ptr, uintptr(i)*valSize)
		dst := unsafe.Add(fr, valOff)
		switch valCl {
		case lPtr:
			*(*unsafe.Pointer)(dst) = *(*unsafe.Pointer)(at)
		case lStr:
			*(*string)(dst) = *(*string)(at)
		case lIface:
			*(*ifacePair)(dst) = *(*ifacePair)(at)
		case lSlice:
			*(*sliceHdr)(dst) = *(*sliceHdr)(at)
		default:
			if valCl.float() {
				storeF(valCl, dst, loadF(at, valCl))
			} else {
				storeN(valCl, dst, loadN(at, valCl))
			}
		}
	}

	xf := over.L
	return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
		h, err := xf(fr, ctx, st, d)
		if err != nil {
			return err
		}
		for i := 0; i < h.len; i++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			if hasKey {
				storeN(keyCl, unsafe.Add(fr, keyOff), uint64(i))
			}
			copyVal(fr, h, i)
			more, err := step(fr, ctx, st, d)
			if err != nil || !more {
				return err
			}
		}
		return nil
	}, nil
}

// signedOf decodes the zero-extended bits of a signed scalar class
// back to its value.
func signedOf(cl layout, bits uint64) int64 {
	switch cl {
	case lI8:
		return int64(int8(uint8(bits)))
	case lI16:
		return int64(int16(uint16(bits)))
	case lI32:
		return int64(int32(uint32(bits)))
	}
	return int64(bits) // lI64
}
