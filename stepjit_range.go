package gozero

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"sync"
	"unsafe" // also required by go:linkname
)

// The range loop on the direct tier. Ranging a slice is a length
// check and a stride walk over the sliceHdr the closure tree already
// produces; an integer range is a counted loop over its bits; a
// string walks by rune; a map iterates through a pooled
// reflect.MapIter writing each entry straight into the frame slots; a
// channel repeats the armed receive until close; an iterator func is
// cast to its shape and driven from a yield closure (stepjit_seq.go).
// The loop variable is a frame slot written per iteration through the
// same typed stores every other statement uses, the body is its own
// []nodeE, and every iteration checks the execution context, which is
// what bounds a loop the way the statement count bounds a straight
// line. An element type with no layout class stays on the reflect
// evaluator, and so does an array, which has no slice header.

// stepFn runs one iteration's body: more is false when a break or the
// end of an errored run stops the loop. It folds the loop signals the
// way the reflect tier's step does: continue ends the iteration,
// break ends the loop, any other error ends the program.
type stepFn func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (more bool, err error)

// raiseSignal is the compiled form of break and continue: the signal
// travels the error return and the innermost loop's step consumes it.
func raiseSignal(sig error) nodeE {
	return func(unsafe.Pointer, context.Context, map[string]any, any) error {
		return sig
	}
}

// rangeNode compiles one range loop.
func (c *jitCompiler) rangeNode(r *plannedRange, jp *jitProgram) (nodeE, error) {
	body := make([]nodeE, 0, len(r.body))
	for _, bs := range r.body {
		n, err := c.stmtNode(bs, jp)
		if err != nil {
			return nil, err
		}
		body = append(body, n)
	}
	src := r.src

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

	switch src.kind {
	case rangeInt:
		return c.intRangeNode(src, step)
	case rangeSlice:
		return c.sliceRangeNode(src, step)
	case rangeString:
		return c.stringRangeNode(src, step)
	case rangeMap:
		return c.mapRangeNode(src, step)
	case rangeChan:
		return c.chanRangeNode(src, step)
	case rangeFunc, rangeFunc2:
		return c.seqNode(src, step)
	}
	return nil, fmt.Errorf("range kind %d is not in the table", src.kind)
}

// scalarKey resolves the loop key's frame offset and scalar class, or
// reports that the key is blank. The numeric-key forms require a
// scalar; the map, channel and func forms place their own types.
func (c *jitCompiler) scalarKey(src *vmRange) (uintptr, layout, bool, error) {
	if src.keySlot < 0 {
		return 0, lBad, false, nil
	}
	field, ok := c.slotOf[src.keySlot]
	if !ok {
		return 0, lBad, false, fmt.Errorf("a range key has no slot")
	}
	off, cl := c.offs[field], layoutOf(c.types[field])
	if !cl.scalar() {
		return 0, lBad, false, fmt.Errorf("a range key of type %s has no scalar class", c.types[field])
	}
	return off, cl, true, nil
}

// slotField resolves a bound slot to its frame offset and static
// type; has is false for a blank name.
func (c *jitCompiler) slotField(slot int, what string) (uintptr, reflect.Type, bool, error) {
	if slot < 0 {
		return 0, nil, false, nil
	}
	field, ok := c.slotOf[slot]
	if !ok {
		return 0, nil, false, fmt.Errorf("a range %s has no slot", what)
	}
	return c.offs[field], c.types[field], true, nil
}

// intRangeNode is the go1.22 integer range: a counted loop over the
// bound's bits.
func (c *jitCompiler) intRangeNode(src *vmRange, step stepFn) (nodeE, error) {
	keyOff, keyCl, hasKey, err := c.scalarKey(src)
	if err != nil {
		return nil, err
	}
	cl := layoutOf(src.over.typ)
	if !cl.scalar() || cl.float() {
		return nil, fmt.Errorf("ranging over class %s is not in the table", cl)
	}
	over, err := c.argNode(src.over, src.over.typ, cl)
	if err != nil {
		return nil, err
	}
	xf, signed := over.N, cl == lI8 || cl == lI16 || cl == lI32 || cl == lI64
	return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
		bits, err := xf(fr, ctx, st, d)
		if err != nil {
			return err
		}
		// loadN zero-extends, so a signed bound is decoded at its
		// own width; a negative one runs zero times, as in Go.
		count := bits
		if signed {
			if v := signedOf(cl, bits); v > 0 {
				count = uint64(v)
			} else {
				count = 0
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
func (c *jitCompiler) sliceRangeNode(src *vmRange, step stepFn) (nodeE, error) {
	keyOff, keyCl, hasKey, err := c.scalarKey(src)
	if err != nil {
		return nil, err
	}
	if layoutOf(src.over.typ) != lSlice {
		return nil, fmt.Errorf("ranging over %s is not in the table", src.over.typ)
	}
	over, err := c.argNode(src.over, src.over.typ, lSlice)
	if err != nil {
		return nil, err
	}

	valOff, valSize := uintptr(0), uintptr(0)
	valCl := lBad
	if src.valSlot >= 0 {
		off, vt, _, err := c.slotField(src.valSlot, "value")
		if err != nil {
			return nil, err
		}
		valOff, valCl, valSize = off, layoutOf(vt), vt.Size()
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

// stringRangeNode ranges a string by rune, Go's semantics: the key is
// the byte index of the rune, the value the rune itself.
func (c *jitCompiler) stringRangeNode(src *vmRange, step stepFn) (nodeE, error) {
	keyOff, keyCl, hasKey, err := c.scalarKey(src)
	if err != nil {
		return nil, err
	}
	over, err := c.argNode(src.over, src.over.typ, lStr)
	if err != nil {
		return nil, err
	}
	valOff, valCl, hasVal := uintptr(0), lBad, false
	if src.valSlot >= 0 {
		off, vt, _, err := c.slotField(src.valSlot, "value")
		if err != nil {
			return nil, err
		}
		valOff, valCl, hasVal = off, layoutOf(vt), true
		if !valCl.scalar() || valCl.float() {
			return nil, fmt.Errorf("a rune value of type %s has no scalar class", vt)
		}
	}
	xf := over.S
	return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
		s, err := xf(fr, ctx, st, d)
		if err != nil {
			return err
		}
		for i, rn := range s {
			if err := ctx.Err(); err != nil {
				return err
			}
			if hasKey {
				storeN(keyCl, unsafe.Add(fr, keyOff), uint64(i))
			}
			if hasVal {
				storeN(valCl, unsafe.Add(fr, valOff), uint64(uint32(rn)))
			}
			more, err := step(fr, ctx, st, d)
			if err != nil || !more {
				return err
			}
		}
		return nil
	}, nil
}

// mapRangeNode iterates a map. The iterator is reflect's, pooled so a
// run allocates nothing steady-state, and SetIterKey and SetIterValue
// write each entry straight into the typed frame slots, so any key
// and value type iterates: the frame is typed storage and holds what
// a slot holds.
func (c *jitCompiler) mapRangeNode(src *vmRange, step stepFn) (nodeE, error) {
	mvGet, err := c.bridgeArg(src.over)
	if err != nil {
		return nil, err
	}
	keyOff, kt, hasKey, err := c.slotField(src.keySlot, "key")
	if err != nil {
		return nil, err
	}
	valOff, vt, hasVal, err := c.slotField(src.valSlot, "value")
	if err != nil {
		return nil, err
	}
	iters := &sync.Pool{}
	return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
		mv, err := mvGet(fr, ctx, st, d)
		if err != nil {
			return err
		}
		it, _ := iters.Get().(*reflect.MapIter)
		if it == nil {
			it = &reflect.MapIter{}
		}
		it.Reset(mv)
		var kc, vc reflect.Value
		if hasKey {
			kc = reflect.NewAt(kt, unsafe.Add(fr, keyOff)).Elem()
		}
		if hasVal {
			vc = reflect.NewAt(vt, unsafe.Add(fr, valOff)).Elem()
		}
		var out error
		for it.Next() {
			if err := ctx.Err(); err != nil {
				out = err
				break
			}
			if hasKey {
				kc.SetIterKey(it)
			}
			if hasVal {
				vc.SetIterValue(it)
			}
			more, err := step(fr, ctx, st, d)
			if err != nil {
				out = err
				break
			}
			if !more {
				break
			}
		}
		// Reset drops the map reference so the pooled iterator pins
		// nothing between runs.
		it.Reset(reflect.Value{})
		iters.Put(it)
		return out
	}, nil
}

// chanRangeNode receives until the channel closes: the same armed
// receive a receive statement runs, with its io.EOF, the implicit ok,
// ending the loop instead of the program.
func (c *jitCompiler) chanRangeNode(src *vmRange, step stepFn) (nodeE, error) {
	chGet, err := c.bridgeArg(src.over)
	if err != nil {
		return nil, err
	}
	keyOff, kt, hasKey, err := c.slotField(src.keySlot, "key")
	if err != nil {
		return nil, err
	}
	return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
		chv, err := chGet(fr, ctx, st, d)
		if err != nil {
			return err
		}
		var cell reflect.Value
		if hasKey {
			cell = reflect.NewAt(kt, unsafe.Add(fr, keyOff)).Elem()
		}
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			v, err := chanRecv(ctx, chv)
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			if hasKey {
				cell.Set(v)
			}
			more, err := step(fr, ctx, st, d)
			if err != nil || !more {
				return err
			}
		}
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
