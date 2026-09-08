package gozero

import (
	"context"
	"unsafe" // also required by go:linkname
)

// Shape types. The name lists the parameter classes, an underscore,
// then the result classes, with E for a trailing error. Adding a shape
// is one type and one case in callNode.
type (
	stP_L     = func(unsafe.Pointer) sliceHdr
	stI_P     = func(ifacePair) unsafe.Pointer
	stPI_E    = func(unsafe.Pointer, ifacePair) ifacePair
	stSSI_PE  = func(string, string, ifacePair) (unsafe.Pointer, ifacePair)
	stISSI_PE = func(ifacePair, string, string, ifacePair) (unsafe.Pointer, ifacePair)
	stSS_PE   = func(string, string) (unsafe.Pointer, ifacePair)
	stS_PE    = func(string) (unsafe.Pointer, ifacePair)
	stI_PE    = func(ifacePair) (unsafe.Pointer, ifacePair)
	stS_P     = func(string) unsafe.Pointer
	stP_P     = func(unsafe.Pointer) unsafe.Pointer
	stP_S     = func(unsafe.Pointer) string
	stP_I     = func(unsafe.Pointer) ifacePair
	stP_E     = func(unsafe.Pointer) ifacePair
	stPP_E    = func(unsafe.Pointer, unsafe.Pointer) ifacePair
	stII_E    = func(ifacePair, ifacePair) ifacePair
	stSS_E    = func(string, string) ifacePair
	stPP_PE   = func(unsafe.Pointer, unsafe.Pointer) (unsafe.Pointer, ifacePair)
)

// nPE and the helpers beside it are the scalar call families. They are
// generic over the parameter's Go type so one body covers every width:
// the cast needs the exact type, but nothing else does.
func nPE[T any](fptr unsafe.Pointer, a0 nodeN, conv func(uint64) T) node {
	f := castFn[func(T) (unsafe.Pointer, ifacePair)](fptr)
	return node{class: lPtr, P: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (unsafe.Pointer, error) {
		n, err := a0(fr, ctx, st, d)
		if err != nil {
			return nil, err
		}
		p, e := f(conv(n))
		if err := asError(e); err != nil {
			return nil, err
		}
		return p, nil
	}}
}

func nE[T any](fptr unsafe.Pointer, a0 nodeN, conv func(uint64) T) node {
	f := castFn[func(T) ifacePair](fptr)
	return node{class: lNone, E: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
		n, err := a0(fr, ctx, st, d)
		if err != nil {
			return err
		}
		return asError(f(conv(n)))
	}}
}

func fPE[T any](fptr unsafe.Pointer, a0 nodeF, conv func(float64) T) node {
	f := castFn[func(T) (unsafe.Pointer, ifacePair)](fptr)
	return node{class: lPtr, P: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (unsafe.Pointer, error) {
		v, err := a0(fr, ctx, st, d)
		if err != nil {
			return nil, err
		}
		p, e := f(conv(v))
		if err := asError(e); err != nil {
			return nil, err
		}
		return p, nil
	}}
}

func fE[T any](fptr unsafe.Pointer, a0 nodeF, conv func(float64) T) node {
	f := castFn[func(T) ifacePair](fptr)
	return node{class: lNone, E: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
		v, err := a0(fr, ctx, st, d)
		if err != nil {
			return err
		}
		return asError(f(conv(v)))
	}}
}

// pN is a pointer parameter and a scalar result, the shape a getter
// method has.
func pN[T any](fptr unsafe.Pointer, a0 nodeP, cl layout, up func(T) uint64) node {
	f := castFn[func(unsafe.Pointer) T](fptr)
	return node{class: cl, N: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (uint64, error) {
		p, err := a0(fr, ctx, st, d)
		if err != nil {
			return 0, err
		}
		return up(f(p)), nil
	}}
}

func pF[T any](fptr unsafe.Pointer, a0 nodeP, cl layout, up func(T) float64) node {
	f := castFn[func(unsafe.Pointer) T](fptr)
	return node{class: cl, F: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (float64, error) {
		p, err := a0(fr, ctx, st, d)
		if err != nil {
			return 0, err
		}
		return up(f(p)), nil
	}}
}

// scalarCall covers a call whose only parameter is a scalar. res is the
// result half of the shape key.
func scalarCall(fptr unsafe.Pointer, in layout, res string, a node) (node, bool) {
	switch res {
	case "PE":
		switch in {
		case lBool:
			return nPE(fptr, a.N, func(n uint64) bool { return n != 0 }), true
		case lI8:
			return nPE(fptr, a.N, func(n uint64) int8 { return int8(uint8(n)) }), true
		case lI16:
			return nPE(fptr, a.N, func(n uint64) int16 { return int16(uint16(n)) }), true
		case lI32:
			return nPE(fptr, a.N, func(n uint64) int32 { return int32(uint32(n)) }), true
		case lI64:
			return nPE(fptr, a.N, func(n uint64) int64 { return int64(n) }), true
		case lU8:
			return nPE(fptr, a.N, func(n uint64) uint8 { return uint8(n) }), true
		case lU16:
			return nPE(fptr, a.N, func(n uint64) uint16 { return uint16(n) }), true
		case lU32:
			return nPE(fptr, a.N, func(n uint64) uint32 { return uint32(n) }), true
		case lU64:
			return nPE(fptr, a.N, func(n uint64) uint64 { return n }), true
		case lF32:
			return fPE(fptr, a.F, func(v float64) float32 { return float32(v) }), true
		case lF64:
			return fPE(fptr, a.F, func(v float64) float64 { return v }), true
		}
	case "E":
		switch in {
		case lBool:
			return nE(fptr, a.N, func(n uint64) bool { return n != 0 }), true
		case lI8:
			return nE(fptr, a.N, func(n uint64) int8 { return int8(uint8(n)) }), true
		case lI16:
			return nE(fptr, a.N, func(n uint64) int16 { return int16(uint16(n)) }), true
		case lI32:
			return nE(fptr, a.N, func(n uint64) int32 { return int32(uint32(n)) }), true
		case lI64:
			return nE(fptr, a.N, func(n uint64) int64 { return int64(n) }), true
		case lU8:
			return nE(fptr, a.N, func(n uint64) uint8 { return uint8(n) }), true
		case lU16:
			return nE(fptr, a.N, func(n uint64) uint16 { return uint16(n) }), true
		case lU32:
			return nE(fptr, a.N, func(n uint64) uint32 { return uint32(n) }), true
		case lU64:
			return nE(fptr, a.N, func(n uint64) uint64 { return n }), true
		case lF32:
			return fE(fptr, a.F, func(v float64) float32 { return float32(v) }), true
		case lF64:
			return fE(fptr, a.F, func(v float64) float64 { return v }), true
		}
	}
	return node{}, false
}

// ptrScalarCall covers a pointer parameter and a scalar result.
func ptrScalarCall(fptr unsafe.Pointer, out layout, a node) (node, bool) {
	switch out {
	case lBool:
		return pN(fptr, a.P, out, func(v bool) uint64 {
			if v {
				return 1
			}
			return 0
		}), true
	case lI8:
		return pN(fptr, a.P, out, func(v int8) uint64 { return uint64(uint8(v)) }), true
	case lI16:
		return pN(fptr, a.P, out, func(v int16) uint64 { return uint64(uint16(v)) }), true
	case lI32:
		return pN(fptr, a.P, out, func(v int32) uint64 { return uint64(uint32(v)) }), true
	case lI64:
		return pN(fptr, a.P, out, func(v int64) uint64 { return uint64(v) }), true
	case lU8:
		return pN(fptr, a.P, out, func(v uint8) uint64 { return uint64(v) }), true
	case lU16:
		return pN(fptr, a.P, out, func(v uint16) uint64 { return uint64(v) }), true
	case lU32:
		return pN(fptr, a.P, out, func(v uint32) uint64 { return uint64(v) }), true
	case lU64:
		return pN(fptr, a.P, out, func(v uint64) uint64 { return v }), true
	case lF32:
		return pF(fptr, a.P, out, func(v float32) float64 { return float64(v) }), true
	case lF64:
		return pF(fptr, a.P, out, func(v float64) float64 { return v }), true
	}
	return node{}, false
}

// getterFor adapts a scalar node into a typed getter, converting from
// the width-erased carriers back to the exact Go type T.
func getterFor[T any](a node, fromN func(uint64) T, fromF func(float64) T) func(unsafe.Pointer, context.Context, map[string]any, any) (T, error) {
	if a.class.float() {
		f := a.F
		return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (T, error) {
			v, err := f(fr, ctx, st, d)
			if err != nil {
				var zero T
				return zero, err
			}
			return fromF(v), nil
		}
	}
	f := a.N
	return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (T, error) {
		v, err := f(fr, ctx, st, d)
		if err != nil {
			var zero T
			return zero, err
		}
		return fromN(v), nil
	}
}

// mixed2 builds the node for a two-parameter call whose second
// parameter is a scalar of type T and whose first is a string or a
// pointer. res is the result half of the shape key. One generic body
// covers every width; the per-width dispatch below only bakes the
// conversion.
func mixed2[T any](fptr unsafe.Pointer, first layout, res string, a0 node, get func(unsafe.Pointer, context.Context, map[string]any, any) (T, error)) (node, bool) {
	switch {
	case first == lStr && res == "PE":
		f, s0 := castFn[func(string, T) (unsafe.Pointer, ifacePair)](fptr), a0.S
		return node{class: lPtr, P: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (unsafe.Pointer, error) {
			x0, err := s0(fr, ctx, st, d)
			if err != nil {
				return nil, err
			}
			x1, err := get(fr, ctx, st, d)
			if err != nil {
				return nil, err
			}
			p, e := f(x0, x1)
			if err := asError(e); err != nil {
				return nil, err
			}
			return p, nil
		}}, true
	case first == lStr && res == "E":
		f, s0 := castFn[func(string, T) ifacePair](fptr), a0.S
		return node{class: lNone, E: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
			x0, err := s0(fr, ctx, st, d)
			if err != nil {
				return err
			}
			x1, err := get(fr, ctx, st, d)
			if err != nil {
				return err
			}
			return asError(f(x0, x1))
		}}, true
	case first == lPtr && res == "PE":
		f, p0 := castFn[func(unsafe.Pointer, T) (unsafe.Pointer, ifacePair)](fptr), a0.P
		return node{class: lPtr, P: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (unsafe.Pointer, error) {
			x0, err := p0(fr, ctx, st, d)
			if err != nil {
				return nil, err
			}
			x1, err := get(fr, ctx, st, d)
			if err != nil {
				return nil, err
			}
			p, e := f(x0, x1)
			if err := asError(e); err != nil {
				return nil, err
			}
			return p, nil
		}}, true
	case first == lPtr && res == "E":
		f, p0 := castFn[func(unsafe.Pointer, T) ifacePair](fptr), a0.P
		return node{class: lNone, E: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
			x0, err := p0(fr, ctx, st, d)
			if err != nil {
				return err
			}
			x1, err := get(fr, ctx, st, d)
			if err != nil {
				return err
			}
			return asError(f(x0, x1))
		}}, true
	}
	return node{}, false
}

// mixedScalarCall dispatches a (fixed, scalar) call to mixed2 with the
// width baked into the getter.
func mixedScalarCall(fptr unsafe.Pointer, first layout, res string, a0, a1 node) (node, bool) {
	switch a1.class {
	case lBool:
		return mixed2(fptr, first, res, a0, getterFor(a1, func(n uint64) bool { return n != 0 }, nil))
	case lI8:
		return mixed2(fptr, first, res, a0, getterFor(a1, func(n uint64) int8 { return int8(uint8(n)) }, nil))
	case lI16:
		return mixed2(fptr, first, res, a0, getterFor(a1, func(n uint64) int16 { return int16(uint16(n)) }, nil))
	case lI32:
		return mixed2(fptr, first, res, a0, getterFor(a1, func(n uint64) int32 { return int32(uint32(n)) }, nil))
	case lI64:
		return mixed2(fptr, first, res, a0, getterFor(a1, func(n uint64) int64 { return int64(n) }, nil))
	case lU8:
		return mixed2(fptr, first, res, a0, getterFor(a1, func(n uint64) uint8 { return uint8(n) }, nil))
	case lU16:
		return mixed2(fptr, first, res, a0, getterFor(a1, func(n uint64) uint16 { return uint16(n) }, nil))
	case lU32:
		return mixed2(fptr, first, res, a0, getterFor(a1, func(n uint64) uint32 { return uint32(n) }, nil))
	case lU64:
		return mixed2(fptr, first, res, a0, getterFor(a1, func(n uint64) uint64 { return n }, nil))
	case lF32:
		return mixed2(fptr, first, res, a0, getterFor(a1, nil, func(v float64) float32 { return float32(v) }))
	case lF64:
		return mixed2(fptr, first, res, a0, getterFor(a1, nil, func(v float64) float64 { return v }))
	}
	return node{}, false
}
