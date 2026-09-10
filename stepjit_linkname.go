//go:build !gozero_purego

package gozero

import (
	"unsafe" // required by go:linkname
)

// The default allocator and clear go straight to reflect's own
// internals, which a toolchain bump can break; the gozero_purego
// build tag swaps in stepjit_purego.go, which reaches the same
// operations through reflect's public surface at a small cost per
// frame.

// unsafeNew is the allocator reflect.New itself calls. Going straight
// to it skips reflect.New's pointer-type lookup. The argument is the
// frame struct's rtype, so the block comes back with the right size and
// pointer map and the collector scans the slots correctly.
//
//go:linkname unsafeNew reflect.unsafe_New
func unsafeNew(rtype unsafe.Pointer) unsafe.Pointer

// typedmemclr is the typed clear reflect.Value.SetZero performs,
// without building the Value: the pointer fields keep their write
// barriers because the rtype carries the pointer map. Used to reset
// a pooled frame. Like unsafeNew, it is a pull linkname to an
// internal symbol and a toolchain bump can break it.
//
//go:linkname typedmemclr reflect.typedmemclr
func typedmemclr(rtype unsafe.Pointer, ptr unsafe.Pointer)
