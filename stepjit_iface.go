package gozero

import (
	"context"
	"fmt"
	"reflect"
	"unsafe" // also required by go:linkname
)

// itabFor returns the interface table for holding a value of concrete
// type ct in interface type it. The pair of types fixes the table, so
// this runs once per argument at compile time and only the data word
// varies per call. For an empty interface the word is the concrete
// type's rtype, which is the same position in the same two words.
func itabFor(ct, it reflect.Type) (unsafe.Pointer, bool) {
	if ct.Kind() == reflect.Interface || !ct.Implements(it) {
		return nil, false
	}
	cell := reflect.New(it).Elem()
	cell.Set(reflect.Zero(ct))
	pair := *(*ifacePair)(cell.Addr().UnsafePointer())
	if pair.tab == nil {
		return nil, false
	}
	return pair.tab, true
}

// toIface wraps a computed value as an interface. A pointer-shaped
// value is the data word itself; anything wider is stored indirectly,
// which is the allocation the Go compiler makes at the same place.
func (c *jitCompiler) toIface(st, pt reflect.Type, sub node) (node, error) {
	if st == nil {
		return node{}, fmt.Errorf("a call with no result cannot become an interface")
	}
	tab, ok := itabFor(st, pt)
	if !ok {
		return node{}, fmt.Errorf("%s does not implement %s", st, pt)
	}
	if sub.class.scalar() {
		return scalarIface(tab, sub)
	}
	switch sub.class {
	case lPtr:
		f := sub.P
		return node{class: lIface, I: func(fr unsafe.Pointer, ctx context.Context, s map[string]any, d any) (ifacePair, error) {
			v, err := f(fr, ctx, s, d)
			if err != nil {
				return ifacePair{}, err
			}
			return ifacePair{tab: tab, data: v}, nil
		}}, nil
	case lSlice:
		f := sub.L
		return node{class: lIface, I: func(fr unsafe.Pointer, ctx context.Context, s map[string]any, d any) (ifacePair, error) {
			v, err := f(fr, ctx, s, d)
			if err != nil {
				return ifacePair{}, err
			}
			return ifacePair{tab: tab, data: unsafe.Pointer(&v)}, nil
		}}, nil
	case lStr:
		f := sub.S
		return node{class: lIface, I: func(fr unsafe.Pointer, ctx context.Context, s map[string]any, d any) (ifacePair, error) {
			v, err := f(fr, ctx, s, d)
			if err != nil {
				return ifacePair{}, err
			}
			return ifacePair{tab: tab, data: unsafe.Pointer(&v)}, nil
		}}, nil
	}
	return node{}, fmt.Errorf("a %s result cannot become an interface", sub.class)
}

// staticBoxes is the immutable cell a small scalar aliases when it is
// boxed, the trick runtime.staticuint64s plays for convT64 and
// friends: bits under 256 point into this table instead of escaping a
// fresh cell to the heap. The data word of an interface has to point
// at a value of the concrete width; the low bytes of a uint64 cell
// read correctly at every narrower width on a little-endian machine,
// and smallBox offsets to the high bytes on a big-endian one.
var staticBoxes [256]uint64

var endianProbe uint16 = 1

var bigEndian = *(*byte)(unsafe.Pointer(&endianProbe)) == 0

func init() {
	for i := range staticBoxes {
		staticBoxes[i] = uint64(i)
	}
}

// smallBox returns the static cell for bits, or false when the value
// is too large to have one and needs a real allocation.
func smallBox(bits uint64, width uintptr) (unsafe.Pointer, bool) {
	if bits >= uint64(len(staticBoxes)) {
		return nil, false
	}
	p := unsafe.Pointer(&staticBoxes[bits])
	if bigEndian {
		p = unsafe.Add(p, 8-width)
	}
	return p, true
}

// scalarIface boxes a scalar into an interface. The data word has to
// point at a value of the concrete width, so each class materialises
// one of its own type; that escape is the allocation the Go compiler
// makes at the same place, except for the small values staticBoxes
// already holds.
func scalarIface(tab unsafe.Pointer, sub node) (node, error) {
	mk := func(width uintptr, box func(uint64) unsafe.Pointer) (node, error) {
		f := sub.N
		return node{class: lIface, I: func(fr unsafe.Pointer, ctx context.Context, s map[string]any, d any) (ifacePair, error) {
			n, err := f(fr, ctx, s, d)
			if err != nil {
				return ifacePair{}, err
			}
			if p, ok := smallBox(n, width); ok {
				return ifacePair{tab: tab, data: p}, nil
			}
			return ifacePair{tab: tab, data: box(n)}, nil
		}}, nil
	}
	switch sub.class {
	case lBool:
		return mk(1, func(n uint64) unsafe.Pointer { v := n != 0; return unsafe.Pointer(&v) })
	case lI8:
		return mk(1, func(n uint64) unsafe.Pointer { v := int8(uint8(n)); return unsafe.Pointer(&v) })
	case lI16:
		return mk(2, func(n uint64) unsafe.Pointer { v := int16(uint16(n)); return unsafe.Pointer(&v) })
	case lI32:
		return mk(4, func(n uint64) unsafe.Pointer { v := int32(uint32(n)); return unsafe.Pointer(&v) })
	case lI64:
		return mk(8, func(n uint64) unsafe.Pointer { v := int64(n); return unsafe.Pointer(&v) })
	case lU8:
		return mk(1, func(n uint64) unsafe.Pointer { v := uint8(n); return unsafe.Pointer(&v) })
	case lU16:
		return mk(2, func(n uint64) unsafe.Pointer { v := uint16(n); return unsafe.Pointer(&v) })
	case lU32:
		return mk(4, func(n uint64) unsafe.Pointer { v := uint32(n); return unsafe.Pointer(&v) })
	case lU64:
		return mk(8, func(n uint64) unsafe.Pointer { v := n; return unsafe.Pointer(&v) })
	case lF32:
		f := sub.F
		return node{class: lIface, I: func(fr unsafe.Pointer, ctx context.Context, s map[string]any, d any) (ifacePair, error) {
			x, err := f(fr, ctx, s, d)
			if err != nil {
				return ifacePair{}, err
			}
			v := float32(x)
			return ifacePair{tab: tab, data: unsafe.Pointer(&v)}, nil
		}}, nil
	case lF64:
		f := sub.F
		return node{class: lIface, I: func(fr unsafe.Pointer, ctx context.Context, s map[string]any, d any) (ifacePair, error) {
			v, err := f(fr, ctx, s, d)
			if err != nil {
				return ifacePair{}, err
			}
			return ifacePair{tab: tab, data: unsafe.Pointer(&v)}, nil
		}}, nil
	}
	return node{}, fmt.Errorf("a %s cannot become an interface", sub.class)
}

func constNode(v reflect.Value, cl layout) (node, error) {
	if cl.scalar() {
		n, f := scalarBits(v, cl)
		if cl.float() {
			return node{class: cl, F: func(unsafe.Pointer, context.Context, map[string]any, any) (float64, error) { return f, nil }}, nil
		}
		return node{class: cl, N: func(unsafe.Pointer, context.Context, map[string]any, any) (uint64, error) { return n, nil }}, nil
	}
	switch cl {
	case lStr:
		s := v.String()
		return node{class: lStr, S: func(unsafe.Pointer, context.Context, map[string]any, any) (string, error) { return s, nil }}, nil
	case lPtr:
		if !v.IsZero() {
			return node{}, fmt.Errorf("only a zero pointer can be a constant")
		}
		return node{class: lPtr, P: func(unsafe.Pointer, context.Context, map[string]any, any) (unsafe.Pointer, error) { return nil, nil }}, nil
	case lIface:
		if v.IsZero() && v.Kind() == reflect.Interface {
			return node{class: lIface, I: func(unsafe.Pointer, context.Context, map[string]any, any) (ifacePair, error) { return ifacePair{}, nil }}, nil
		}
		// A non-nil constant boxes once at compile time. The cell is a
		// heap value reflect allocated; the closure keeps it reachable,
		// and both words of the pair are pointers the collector traces.
		// Sharing one box across calls is safe because a literal is
		// immutable.
		cell := reflect.New(reflect.TypeFor[any]()).Elem()
		cell.Set(v)
		pair := *(*ifacePair)(cell.Addr().UnsafePointer())
		keep := cell
		return node{class: lIface, I: func(unsafe.Pointer, context.Context, map[string]any, any) (ifacePair, error) {
			_ = keep
			return pair, nil
		}}, nil
	case lSlice:
		if !v.IsZero() {
			return node{}, fmt.Errorf("only a nil slice can be a constant")
		}
		return node{class: lSlice, L: func(unsafe.Pointer, context.Context, map[string]any, any) (sliceHdr, error) { return sliceHdr{}, nil }}, nil
	}
	return node{}, fmt.Errorf("a constant of class %s is not supported", cl)
}
