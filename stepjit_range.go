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
// same typed stores every other statement uses, the body is its own
// []nodeE, and every iteration checks the execution context, which is
// what bounds a loop the way the statement count bounds a straight
// line. An element type with no layout class stays on the reflect
// evaluator, and so does an array, which has no slice header.

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

	keyOff := uintptr(0)
	keyCl := lBad
	if src.keySlot >= 0 {
		field, ok := c.slotOf[src.keySlot]
		if !ok {
			return nil, fmt.Errorf("a range key has no slot")
		}
		keyOff, keyCl = c.offs[field], layoutOf(c.types[field])
		if !keyCl.scalar() {
			return nil, fmt.Errorf("a range key of type %s has no scalar class", c.types[field])
		}
	}

	step := func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
		for _, n := range body {
			if err := n(fr, ctx, st, d); err != nil {
				return err
			}
		}
		return nil
	}

	if src.overInt {
		cl := layoutOf(src.over.typ)
		if !cl.scalar() || cl.float() {
			return nil, fmt.Errorf("ranging over class %s is not in the table", cl)
		}
		over, err := c.argNode(src.over, src.over.typ, cl)
		if err != nil {
			return nil, err
		}
		xf, signed := over.N, cl == lI8 || cl == lI16 || cl == lI32 || cl == lI64
		hasKey := src.keySlot >= 0
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
				if err := step(fr, ctx, st, d); err != nil {
					return err
				}
			}
			return nil
		}, nil
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
		field, ok := c.slotOf[src.valSlot]
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
	hasKey := src.keySlot >= 0
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
			if err := step(fr, ctx, st, d); err != nil {
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
