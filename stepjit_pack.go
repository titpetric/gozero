package gozero

import (
	"context"
	"fmt"
	"reflect"
	"unsafe" // also required by go:linkname
)

// The variadic pack. A call the source wrote with loose arguments
// gathers them into the slice the callee's last parameter wants,
// which is the allocation the Go compiler makes at the same place;
// the binding contract lets that allocation come from a pool.

// packNode builds the variadic slice from the element nodes, for the
// element types worth special-casing: []any and []string cover the
// printf and assert families. Anything else bridges. The slice is
// allocated per call because the callee may keep it, which is the same
// escape the Go compiler assumes at a call site whose arguments escape.
func (c *jitCompiler) packNode(call *vmCall, st reflect.Type, elems []*vmArg) (node, error) {
	et := st.Elem()
	cl := layoutOf(et)
	if cl == lBad {
		return node{}, fmt.Errorf("a variadic element of type %s has no layout class", et)
	}
	nodes := make([]node, len(elems))
	for i, a := range elems {
		n, err := c.argNode(a, et, cl)
		if err != nil {
			return node{}, err
		}
		nodes[i] = n
	}
	// The binding contract lets the pack reuse a pooled backing
	// array instead of allocating one per call; the slice the callee
	// receives is dead when the call returns, and finish repools it.
	// takeBacking is nil for an ordinary call, and the pack allocates
	// fresh.
	var takeBacking func(fr unsafe.Pointer) unsafe.Pointer
	if site, ok := c.packOf[call]; ok && len(nodes) > 0 {
		off := c.offs[site.field]
		takeBacking = func(fr unsafe.Pointer) unsafe.Pointer {
			block := site.take()
			*(*unsafe.Pointer)(unsafe.Add(fr, off)) = block
			return block
		}
	}
	switch {
	case et.Kind() == reflect.Interface && et.NumMethod() == 0:
		getters := make([]nodeI, len(nodes))
		for i, n := range nodes {
			getters[i] = n.I
		}
		take := takeBacking
		return node{class: lSlice, L: func(fr unsafe.Pointer, ctx context.Context, stk map[string]any, d any) (sliceHdr, error) {
			var out []any
			if take != nil {
				h := sliceHdr{ptr: take(fr), len: len(getters), cap: len(getters)}
				out = *(*[]any)(unsafe.Pointer(&h))
			} else {
				out = make([]any, len(getters))
			}
			for i, g := range getters {
				pair, err := g(fr, ctx, stk, d)
				if err != nil {
					return sliceHdr{}, err
				}
				// A typed pointer store into the slice element keeps
				// the write barrier.
				*(*ifacePair)(unsafe.Pointer(&out[i])) = pair
			}
			return *(*sliceHdr)(unsafe.Pointer(&out)), nil
		}}, nil
	case et.Kind() == reflect.String:
		getters := make([]nodeS, len(nodes))
		for i, n := range nodes {
			getters[i] = n.S
		}
		take := takeBacking
		return node{class: lSlice, L: func(fr unsafe.Pointer, ctx context.Context, stk map[string]any, d any) (sliceHdr, error) {
			var out []string
			if take != nil {
				h := sliceHdr{ptr: take(fr), len: len(getters), cap: len(getters)}
				out = *(*[]string)(unsafe.Pointer(&h))
			} else {
				out = make([]string, len(getters))
			}
			for i, g := range getters {
				v, err := g(fr, ctx, stk, d)
				if err != nil {
					return sliceHdr{}, err
				}
				out[i] = v
			}
			return *(*sliceHdr)(unsafe.Pointer(&out)), nil
		}}, nil
	}
	return node{}, fmt.Errorf("packing []%s is not in the table", et)
}
