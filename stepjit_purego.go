//go:build gozero_purego

package gozero

import (
	"reflect"
	"unsafe"
)

// The linkname-free allocator and clear: an rtype pointer rebuilt
// into a reflect.Type through the one itab every Type value shares,
// then the public New and SetZero. Costs one reflect call per frame
// operation; the interface layout it relies on is the same one the
// whole tier already assumes.

var typeTab = func() unsafe.Pointer {
	t := reflect.TypeOf(0)
	return (*ifacePair)(unsafe.Pointer(&t)).tab
}()

func typeFromRT(rt unsafe.Pointer) reflect.Type {
	pair := ifacePair{tab: typeTab, data: rt}
	return *(*reflect.Type)(unsafe.Pointer(&pair))
}

func unsafeNew(rt unsafe.Pointer) unsafe.Pointer {
	return reflect.New(typeFromRT(rt)).UnsafePointer()
}

func typedmemclr(rt unsafe.Pointer, ptr unsafe.Pointer) {
	reflect.NewAt(typeFromRT(rt), ptr).Elem().SetZero()
}
