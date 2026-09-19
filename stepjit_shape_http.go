package gozero

import (
	"context"
	"unsafe" // also required by go:linkname
)

// The shapes the func literal fixture's outer statements call
// through: registering a handler, serving a recorded request, and
// building it. Each is a plain layout shape; the names only say which
// call motivated the entry, the way IL_i64E is the io.Writer print
// family's.

// httpShapeCall matches the shapes this file defines; callNode tries
// it before its own table.
func httpShapeCall(key string, fptr unsafe.Pointer, a []node) (node, bool) {
	switch key {
	case "P_":
		return ptrOnlyE(fptr, a[0].P), true
	case "PSP_":
		return muxRegisterE(fptr, a[0].P, a[1].S, a[2].P), true
	case "PIP_":
		return muxServeE(fptr, a[0].P, a[1].I, a[2].P), true
	case "SSI_P":
		return newRequestP(fptr, a[0].S, a[1].S, a[2].I), true
	}
	return node{}, false
}

// ptrOnlyE is "P_": one pointer-shaped argument, no results. keep(h)
// in the func literal tests is this shape.
func ptrOnlyE(fptr unsafe.Pointer, a0 nodeP) node {
	f := castFn[func(unsafe.Pointer)](fptr)
	return node{class: lNone, E: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
		p0, err := a0(fr, ctx, st, d)
		if err != nil {
			return err
		}
		f(p0)
		return nil
	}}
}

// muxRegisterE is "PSP_": (*http.ServeMux).HandleFunc, the receiver,
// the pattern and the handler funcval.
func muxRegisterE(fptr unsafe.Pointer, a0 nodeP, a1 nodeS, a2 nodeP) node {
	f := castFn[func(unsafe.Pointer, string, unsafe.Pointer)](fptr)
	return node{class: lNone, E: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
		p0, err := a0(fr, ctx, st, d)
		if err != nil {
			return err
		}
		s1, err := a1(fr, ctx, st, d)
		if err != nil {
			return err
		}
		p2, err := a2(fr, ctx, st, d)
		if err != nil {
			return err
		}
		f(p0, s1, p2)
		return nil
	}}
}

// muxServeE is "PIP_": (*http.ServeMux).ServeHTTP, the receiver, the
// writer and the request.
func muxServeE(fptr unsafe.Pointer, a0 nodeP, a1 nodeI, a2 nodeP) node {
	f := castFn[func(unsafe.Pointer, ifacePair, unsafe.Pointer)](fptr)
	return node{class: lNone, E: func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
		p0, err := a0(fr, ctx, st, d)
		if err != nil {
			return err
		}
		i1, err := a1(fr, ctx, st, d)
		if err != nil {
			return err
		}
		p2, err := a2(fr, ctx, st, d)
		if err != nil {
			return err
		}
		f(p0, i1, p2)
		return nil
	}}
}

// newRequestP is "SSI_P": httptest.NewRequest, which panics rather
// than returning an error, so the pointer is the only result.
func newRequestP(fptr unsafe.Pointer, a0, a1 nodeS, a2 nodeI) node {
	f := castFn[func(string, string, ifacePair) unsafe.Pointer](fptr)
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
		return f(s0, s1, i2), nil
	}}
}
