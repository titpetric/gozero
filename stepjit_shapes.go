package gozero

import (
	"context"
	"strings"
	"unsafe" // also required by go:linkname
)

// callNode builds the node for one call from the nodes of its
// arguments, or reports that the shape is outside the table.
func callNode(key string, fptr unsafe.Pointer, a []node) (node, bool) {
	if i := strings.IndexByte(key, '_'); i > 0 && len(a) == 2 {
		if (a[0].class == lStr || a[0].class == lPtr) && a[1].class.scalar() {
			if n, ok := mixedScalarCall(fptr, a[0].class, key[i+1:], a[0], a[1]); ok {
				return n, true
			}
		}
	}
	if i := strings.IndexByte(key, '_'); i > 0 && len(a) == 1 {
		if a[0].class.scalar() {
			if n, ok := scalarCall(fptr, a[0].class, key[i+1:], a[0]); ok {
				return n, true
			}
		}
		if a[0].class == lPtr {
			if out, ok := classOf(key[i+1:]); ok && out.scalar() {
				return ptrScalarCall(fptr, out, a[0])
			}
		}
	}
	switch key {
	case "SL_S":
		f, a0, a1 := castFn[func(string, sliceHdr) string](fptr), a[0].S, a[1].L
		return node{class: lStr, S: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (string, error) {
			s0, err := a0(fr, ctx, st, d)
			if err != nil {
				return "", err
			}
			h, err := a1(fr, ctx, st, d)
			if err != nil {
				return "", err
			}
			return f(s0, h), nil
		}}, true

	case "PS_S":
		f, a0, a1 := castFn[func(unsafe.Pointer, string) string](fptr), a[0].P, a[1].S
		return node{class: lStr, S: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (string, error) {
			p0, err := a0(fr, ctx, st, d)
			if err != nil {
				return "", err
			}
			s1, err := a1(fr, ctx, st, d)
			if err != nil {
				return "", err
			}
			return f(p0, s1), nil
		}}, true

	case "S_L":
		f, a0 := castFn[func(string) sliceHdr](fptr), a[0].S
		return node{class: lSlice, L: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (sliceHdr, error) {
			s0, err := a0(fr, ctx, st, d)
			if err != nil {
				return sliceHdr{}, err
			}
			return f(s0), nil
		}}, true

	case "I_S":
		f, a0 := castFn[func(ifacePair) string](fptr), a[0].I
		return node{class: lStr, S: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (string, error) {
			i0, err := a0(fr, ctx, st, d)
			if err != nil {
				return "", err
			}
			return f(i0), nil
		}}, true

	case "IbS_":
		f, a0, a1, a2 := castFn[func(ifacePair, bool, string)](fptr), a[0].I, a[1].N, a[2].S
		return node{class: lNone, E: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
			i0, err := a0(fr, ctx, st, d)
			if err != nil {
				return err
			}
			n1, err := a1(fr, ctx, st, d)
			if err != nil {
				return err
			}
			s2, err := a2(fr, ctx, st, d)
			if err != nil {
				return err
			}
			f(i0, n1 != 0, s2)
			return nil
		}}, true

	case "IIIS_":
		f, a0, a1, a2, a3 := castFn[func(ifacePair, ifacePair, ifacePair, string)](fptr), a[0].I, a[1].I, a[2].I, a[3].S
		return node{class: lNone, E: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
			i0, err := a0(fr, ctx, st, d)
			if err != nil {
				return err
			}
			i1, err := a1(fr, ctx, st, d)
			if err != nil {
				return err
			}
			i2, err := a2(fr, ctx, st, d)
			if err != nil {
				return err
			}
			s3, err := a3(fr, ctx, st, d)
			if err != nil {
				return err
			}
			f(i0, i1, i2, s3)
			return nil
		}}, true

	case "L_S":
		f, a0 := castFn[func(sliceHdr) string](fptr), a[0].L
		return node{class: lStr, S: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (string, error) {
			h, err := a0(fr, ctx, st, d)
			if err != nil {
				return "", err
			}
			return f(h), nil
		}}, true

	case "P_L":
		f, a0 := castFn[stP_L](fptr), a[0].P
		return node{class: lSlice, L: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (sliceHdr, error) {
			p, err := a0(fr, ctx, st, d)
			if err != nil {
				return sliceHdr{}, err
			}
			return f(p), nil
		}}, true

	case "I_P":
		f, a0 := castFn[stI_P](fptr), a[0].I
		return node{class: lPtr, P: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (unsafe.Pointer, error) {
			i0, err := a0(fr, ctx, st, d)
			if err != nil {
				return nil, err
			}
			return f(i0), nil
		}}, true

	case "PI_E":
		f, a0, a1 := castFn[stPI_E](fptr), a[0].P, a[1].I
		return node{class: lNone, E: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
			p0, err := a0(fr, ctx, st, d)
			if err != nil {
				return err
			}
			i1, err := a1(fr, ctx, st, d)
			if err != nil {
				return err
			}
			return asError(f(p0, i1))
		}}, true

	case "ISSI_PE":
		f, a0, a1, a2, a3 := castFn[stISSI_PE](fptr), a[0].I, a[1].S, a[2].S, a[3].I
		return node{class: lPtr, P: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (unsafe.Pointer, error) {
			i0, err := a0(fr, ctx, st, d)
			if err != nil {
				return nil, err
			}
			s1, err := a1(fr, ctx, st, d)
			if err != nil {
				return nil, err
			}
			s2, err := a2(fr, ctx, st, d)
			if err != nil {
				return nil, err
			}
			i3, err := a3(fr, ctx, st, d)
			if err != nil {
				return nil, err
			}
			p, e := f(i0, s1, s2, i3)
			if err := asError(e); err != nil {
				return nil, err
			}
			return p, nil
		}}, true

	case "SSI_PE":
		f, a0, a1, a2 := castFn[stSSI_PE](fptr), a[0].S, a[1].S, a[2].I
		return node{class: lPtr, P: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (unsafe.Pointer, error) {
			s0, err := a0(fr, ctx, st, d)
			if err != nil {
				return nil, err
			}
			s1, err := a1(fr, ctx, st, d)
			if err != nil {
				return nil, err
			}
			i2, err := a2(fr, ctx, st, d)
			if err != nil {
				return nil, err
			}
			p, e := f(s0, s1, i2)
			if err := asError(e); err != nil {
				return nil, err
			}
			return p, nil
		}}, true

	case "SS_PE":
		f, a0, a1 := castFn[stSS_PE](fptr), a[0].S, a[1].S
		return node{class: lPtr, P: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (unsafe.Pointer, error) {
			s0, err := a0(fr, ctx, st, d)
			if err != nil {
				return nil, err
			}
			s1, err := a1(fr, ctx, st, d)
			if err != nil {
				return nil, err
			}
			p, e := f(s0, s1)
			if err := asError(e); err != nil {
				return nil, err
			}
			return p, nil
		}}, true

	case "S_PE":
		f, a0 := castFn[stS_PE](fptr), a[0].S
		return node{class: lPtr, P: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (unsafe.Pointer, error) {
			s0, err := a0(fr, ctx, st, d)
			if err != nil {
				return nil, err
			}
			p, e := f(s0)
			if err := asError(e); err != nil {
				return nil, err
			}
			return p, nil
		}}, true

	case "I_PE":
		f, a0 := castFn[stI_PE](fptr), a[0].I
		return node{class: lPtr, P: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (unsafe.Pointer, error) {
			i0, err := a0(fr, ctx, st, d)
			if err != nil {
				return nil, err
			}
			p, e := f(i0)
			if err := asError(e); err != nil {
				return nil, err
			}
			return p, nil
		}}, true

	case "S_P":
		f, a0 := castFn[stS_P](fptr), a[0].S
		return node{class: lPtr, P: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (unsafe.Pointer, error) {
			s0, err := a0(fr, ctx, st, d)
			if err != nil {
				return nil, err
			}
			return f(s0), nil
		}}, true

	case "P_P":
		f, a0 := castFn[stP_P](fptr), a[0].P
		return node{class: lPtr, P: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (unsafe.Pointer, error) {
			p0, err := a0(fr, ctx, st, d)
			if err != nil {
				return nil, err
			}
			return f(p0), nil
		}}, true

	case "P_S":
		f, a0 := castFn[stP_S](fptr), a[0].P
		return node{class: lStr, S: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (string, error) {
			p0, err := a0(fr, ctx, st, d)
			if err != nil {
				return "", err
			}
			return f(p0), nil
		}}, true

	case "P_I":
		f, a0 := castFn[stP_I](fptr), a[0].P
		return node{class: lIface, I: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (ifacePair, error) {
			p0, err := a0(fr, ctx, st, d)
			if err != nil {
				return ifacePair{}, err
			}
			return f(p0), nil
		}}, true

	case "P_E":
		f, a0 := castFn[stP_E](fptr), a[0].P
		return node{class: lNone, E: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
			p0, err := a0(fr, ctx, st, d)
			if err != nil {
				return err
			}
			return asError(f(p0))
		}}, true

	case "PP_PE":
		f, a0, a1 := castFn[stPP_PE](fptr), a[0].P, a[1].P
		return node{class: lPtr, P: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (unsafe.Pointer, error) {
			p0, err := a0(fr, ctx, st, d)
			if err != nil {
				return nil, err
			}
			p1, err := a1(fr, ctx, st, d)
			if err != nil {
				return nil, err
			}
			p, e := f(p0, p1)
			if err := asError(e); err != nil {
				return nil, err
			}
			return p, nil
		}}, true

	case "PP_E":
		f, a0, a1 := castFn[stPP_E](fptr), a[0].P, a[1].P
		return node{class: lNone, E: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
			p0, err := a0(fr, ctx, st, d)
			if err != nil {
				return err
			}
			p1, err := a1(fr, ctx, st, d)
			if err != nil {
				return err
			}
			return asError(f(p0, p1))
		}}, true

	case "II_E":
		f, a0, a1 := castFn[stII_E](fptr), a[0].I, a[1].I
		return node{class: lNone, E: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
			i0, err := a0(fr, ctx, st, d)
			if err != nil {
				return err
			}
			i1, err := a1(fr, ctx, st, d)
			if err != nil {
				return err
			}
			return asError(f(i0, i1))
		}}, true

	case "SS_E":
		f, a0, a1 := castFn[stSS_E](fptr), a[0].S, a[1].S
		return node{class: lNone, E: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
			s0, err := a0(fr, ctx, st, d)
			if err != nil {
				return err
			}
			s1, err := a1(fr, ctx, st, d)
			if err != nil {
				return err
			}
			return asError(f(s0, s1))
		}}, true
	}
	return node{}, false
}

// classOf is the inverse of layout.String, for reading a shape key.
func classOf(s string) (layout, bool) {
	for l := lBool; l <= lF64; l++ {
		if l.String() == s {
			return l, true
		}
	}
	return lBad, false
}
