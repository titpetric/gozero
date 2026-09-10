package gozero

import (
	"context"
	"fmt"
	"unsafe" // also required by go:linkname
)

// The range loops of the direct tier: slices by element copy,
// strings by rune, both bounded by the execution context. Maps stay
// on the reflect evaluator.

func (c *jitCompiler) rangeFlowNode(r *vmRange, jp *jitProgram) (nodeE, error) {
	over, err := c.valueNode(r.over)
	if err != nil {
		return nil, err
	}
	body, err := c.blockNodes(r.body.stmts, jp)
	if err != nil {
		return nil, err
	}
	keyOff, valOff := uintptr(0), uintptr(0)
	keyCl, valCl := lBad, lBad
	var valSize uintptr
	if r.keySlot >= 0 {
		field, ok := c.slotOf[r.keySlot]
		if !ok {
			return nil, fmt.Errorf("a range key has no slot")
		}
		keyOff, keyCl = c.offs[field], layoutOf(c.types[field])
	}
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
	step := func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		switch err := runNodes(body, fr, ctx, st, d); err {
		case nil, errJITContinue:
			return true, nil
		case errJITBreak:
			return false, nil
		default:
			return false, err
		}
	}
	setKey := func(fr unsafe.Pointer, i int) {
		if keyCl != lBad {
			storeN(keyCl, unsafe.Add(fr, keyOff), uint64(i))
		}
	}
	switch over.class {
	case lSlice:
		xf := over.L
		copyVal := func(fr unsafe.Pointer, h sliceHdr, i int) {
			if valCl == lBad {
				return
			}
			src := unsafe.Add(h.ptr, uintptr(i)*valSize)
			dst := unsafe.Add(fr, valOff)
			switch valCl {
			case lPtr:
				*(*unsafe.Pointer)(dst) = *(*unsafe.Pointer)(src)
			case lStr:
				*(*string)(dst) = *(*string)(src)
			case lIface:
				*(*ifacePair)(dst) = *(*ifacePair)(src)
			case lSlice:
				*(*sliceHdr)(dst) = *(*sliceHdr)(src)
			default:
				if valCl.float() {
					storeF(valCl, dst, loadF(src, valCl))
				} else {
					storeN(valCl, dst, loadN(src, valCl))
				}
			}
		}
		return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
			h, err := xf(fr, ctx, st, d)
			if err != nil {
				return err
			}
			for i := 0; i < h.len; i++ {
				setKey(fr, i)
				copyVal(fr, h, i)
				more, err := step(fr, ctx, st, d)
				if err != nil {
					return err
				}
				if !more {
					return nil
				}
			}
			return nil
		}, nil
	case lStr:
		xf := over.S
		return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
			s, err := xf(fr, ctx, st, d)
			if err != nil {
				return err
			}
			for i, rn := range s {
				setKey(fr, i)
				if valCl != lBad {
					storeN(valCl, unsafe.Add(fr, valOff), uint64(uint32(rn)))
				}
				more, err := step(fr, ctx, st, d)
				if err != nil {
					return err
				}
				if !more {
					return nil
				}
			}
			return nil
		}, nil
	}
	return nil, fmt.Errorf("ranging over class %s is not in the table", over.class)
}
