package gozero

import (
	"context"
	"fmt"
	"unsafe" // also required by go:linkname
)

// Range over an iterator func on the direct tier. An iter.Seq[V] is
// func(func(V) bool) and an iter.Seq2[K, V] is func(func(K, V) bool);
// both are pointer-shaped values whose calling convention depends
// only on the layout classes of the yield parameters, so the seq is
// cast to the shape type the way every direct call is, and the yield
// handed to it is a real Go closure that stores the yielded values
// into the frame slots and drives the body.
//
// The signals map onto the iter contract: the body's continue is
// yield returning true, break is yield returning false, and an error
// stops the same way with the error carried out beside the call. The
// yield closure captures the run's frame, so it is built per run:
// that one allocation is the same closure Go allocates for a
// non-inlined range-over-func loop. Termination is the iterator
// author's problem, as the loops design records; the closure checks
// the execution context before every body run, so a cancelled run
// stops at the next yield.

// seqNode compiles a range over an iter.Seq or iter.Seq2 shaped func.
func (c *jitCompiler) seqNode(src *vmRange, step stepFn) (nodeE, error) {
	over, err := c.argNode(src.over, src.over.typ, lPtr)
	if err != nil {
		return nil, err
	}
	keyOff, _, hasKey, err := c.slotField(src.keySlot, "key")
	if err != nil {
		return nil, err
	}
	// The classes come from the yield signature, not the slots: the
	// shape cast needs them even when both names are blank.
	kCl := layoutOf(src.keyType)
	if kCl == lBad {
		return nil, fmt.Errorf("a range func yielding %s is not in the table", src.keyType)
	}
	if src.kind == rangeFunc {
		return seq1For(kCl, over.P, keyOff, hasKey, step)
	}
	valOff, _, hasVal, err := c.slotField(src.valSlot, "value")
	if err != nil {
		return nil, err
	}
	vCl := layoutOf(src.elem)
	if vCl == lBad {
		return nil, fmt.Errorf("a range func yielding %s is not in the table", src.elem)
	}
	return seq2For(kCl, vCl, over.P, keyOff, valOff, hasKey, hasVal, step)
}

// seqCtl is the loop state the yield closure mutates. One struct so
// the two reference captures cost one cell, not two, beside the
// closure itself.
type seqCtl struct {
	out  error
	done bool
}

// seq1 drives one iter.Seq shaped func. T is the shape type of the
// yield parameter's class; the typed store through *T keeps the write
// barrier where the class carries pointers.
func seq1[T any](over nodeP, off uintptr, has bool, step stepFn) nodeE {
	return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
		w, err := over(fr, ctx, st, d)
		if err != nil {
			return err
		}
		f := castFn[func(func(T) bool)](w)
		var c seqCtl
		f(func(v T) bool {
			if c.done {
				// Go's own rule for a misbehaving iterator: yielding
				// again after false is a panic, not a silent write into
				// a frame the loop has left.
				panic("gozero: a range func continued iteration after the loop ended")
			}
			if err := ctx.Err(); err != nil {
				c.out, c.done = err, true
				return false
			}
			if has {
				*(*T)(unsafe.Add(fr, off)) = v
			}
			more, err := step(fr, ctx, st, d)
			if err != nil {
				c.out, c.done = err, true
				return false
			}
			if !more {
				c.done = true
				return false
			}
			return true
		})
		c.done = true
		return c.out
	}
}

// seq2 drives one iter.Seq2 shaped func.
func seq2[K, V any](over nodeP, kOff, vOff uintptr, hasK, hasV bool, step stepFn) nodeE {
	return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
		w, err := over(fr, ctx, st, d)
		if err != nil {
			return err
		}
		f := castFn[func(func(K, V) bool)](w)
		var c seqCtl
		f(func(k K, v V) bool {
			if c.done {
				panic("gozero: a range func continued iteration after the loop ended")
			}
			if err := ctx.Err(); err != nil {
				c.out, c.done = err, true
				return false
			}
			if hasK {
				*(*K)(unsafe.Add(fr, kOff)) = k
			}
			if hasV {
				*(*V)(unsafe.Add(fr, vOff)) = v
			}
			more, err := step(fr, ctx, st, d)
			if err != nil {
				c.out, c.done = err, true
				return false
			}
			if !more {
				c.done = true
				return false
			}
			return true
		})
		c.done = true
		return c.out
	}
}

// seq1For instantiates seq1 at the shape type of one layout class.
func seq1For(cl layout, over nodeP, off uintptr, has bool, step stepFn) (nodeE, error) {
	switch cl {
	case lPtr:
		return seq1[unsafe.Pointer](over, off, has, step), nil
	case lStr:
		return seq1[string](over, off, has, step), nil
	case lIface:
		return seq1[ifacePair](over, off, has, step), nil
	case lSlice:
		return seq1[sliceHdr](over, off, has, step), nil
	case lBool:
		return seq1[bool](over, off, has, step), nil
	case lI8:
		return seq1[int8](over, off, has, step), nil
	case lI16:
		return seq1[int16](over, off, has, step), nil
	case lI32:
		return seq1[int32](over, off, has, step), nil
	case lI64:
		return seq1[int64](over, off, has, step), nil
	case lU8:
		return seq1[uint8](over, off, has, step), nil
	case lU16:
		return seq1[uint16](over, off, has, step), nil
	case lU32:
		return seq1[uint32](over, off, has, step), nil
	case lU64:
		return seq1[uint64](over, off, has, step), nil
	case lF32:
		return seq1[float32](over, off, has, step), nil
	case lF64:
		return seq1[float64](over, off, has, step), nil
	}
	return nil, fmt.Errorf("a range func yielding class %s is not in the table", cl)
}

// seq2For instantiates seq2 at the shape types of two layout classes:
// the key class picks K here, the value class picks V in seq2V.
func seq2For(kCl, vCl layout, over nodeP, kOff, vOff uintptr, hasK, hasV bool, step stepFn) (nodeE, error) {
	switch kCl {
	case lPtr:
		return seq2V[unsafe.Pointer](vCl, over, kOff, vOff, hasK, hasV, step)
	case lStr:
		return seq2V[string](vCl, over, kOff, vOff, hasK, hasV, step)
	case lIface:
		return seq2V[ifacePair](vCl, over, kOff, vOff, hasK, hasV, step)
	case lSlice:
		return seq2V[sliceHdr](vCl, over, kOff, vOff, hasK, hasV, step)
	case lBool:
		return seq2V[bool](vCl, over, kOff, vOff, hasK, hasV, step)
	case lI8:
		return seq2V[int8](vCl, over, kOff, vOff, hasK, hasV, step)
	case lI16:
		return seq2V[int16](vCl, over, kOff, vOff, hasK, hasV, step)
	case lI32:
		return seq2V[int32](vCl, over, kOff, vOff, hasK, hasV, step)
	case lI64:
		return seq2V[int64](vCl, over, kOff, vOff, hasK, hasV, step)
	case lU8:
		return seq2V[uint8](vCl, over, kOff, vOff, hasK, hasV, step)
	case lU16:
		return seq2V[uint16](vCl, over, kOff, vOff, hasK, hasV, step)
	case lU32:
		return seq2V[uint32](vCl, over, kOff, vOff, hasK, hasV, step)
	case lU64:
		return seq2V[uint64](vCl, over, kOff, vOff, hasK, hasV, step)
	case lF32:
		return seq2V[float32](vCl, over, kOff, vOff, hasK, hasV, step)
	case lF64:
		return seq2V[float64](vCl, over, kOff, vOff, hasK, hasV, step)
	}
	return nil, fmt.Errorf("a range func yielding class %s is not in the table", kCl)
}

// seq2V is seq2For's value half.
func seq2V[K any](vCl layout, over nodeP, kOff, vOff uintptr, hasK, hasV bool, step stepFn) (nodeE, error) {
	switch vCl {
	case lPtr:
		return seq2[K, unsafe.Pointer](over, kOff, vOff, hasK, hasV, step), nil
	case lStr:
		return seq2[K, string](over, kOff, vOff, hasK, hasV, step), nil
	case lIface:
		return seq2[K, ifacePair](over, kOff, vOff, hasK, hasV, step), nil
	case lSlice:
		return seq2[K, sliceHdr](over, kOff, vOff, hasK, hasV, step), nil
	case lBool:
		return seq2[K, bool](over, kOff, vOff, hasK, hasV, step), nil
	case lI8:
		return seq2[K, int8](over, kOff, vOff, hasK, hasV, step), nil
	case lI16:
		return seq2[K, int16](over, kOff, vOff, hasK, hasV, step), nil
	case lI32:
		return seq2[K, int32](over, kOff, vOff, hasK, hasV, step), nil
	case lI64:
		return seq2[K, int64](over, kOff, vOff, hasK, hasV, step), nil
	case lU8:
		return seq2[K, uint8](over, kOff, vOff, hasK, hasV, step), nil
	case lU16:
		return seq2[K, uint16](over, kOff, vOff, hasK, hasV, step), nil
	case lU32:
		return seq2[K, uint32](over, kOff, vOff, hasK, hasV, step), nil
	case lU64:
		return seq2[K, uint64](over, kOff, vOff, hasK, hasV, step), nil
	case lF32:
		return seq2[K, float32](over, kOff, vOff, hasK, hasV, step), nil
	case lF64:
		return seq2[K, float64](over, kOff, vOff, hasK, hasV, step), nil
	}
	return nil, fmt.Errorf("a range func yielding class %s is not in the table", vCl)
}
