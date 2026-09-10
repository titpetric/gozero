package gozero

import (
	"reflect"
	"testing"
	"unsafe"
)

// TestFrameAllocatorClear pins the allocator pair either build mode
// provides: a typed block comes back zeroed, and the clear re-zeroes
// pointerful memory.
func TestFrameAllocatorClear(t *testing.T) {
	type blk struct {
		S string
		P *int
		N int64
	}
	rt := rtypePtr(reflect.TypeFor[blk]())
	p := unsafeNew(rt)
	b := (*blk)(p)
	if b.S != "" || b.P != nil || b.N != 0 {
		t.Fatalf("fresh block not zero: %+v", *b)
	}
	n := 7
	b.S, b.P, b.N = "x", &n, 42
	typedmemclr(rt, p)
	if b.S != "" || b.P != nil || b.N != 0 {
		t.Fatalf("cleared block not zero: %+v", *b)
	}
	_ = unsafe.Pointer(nil)
}
