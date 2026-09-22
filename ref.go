package gozero

import (
	"reflect"
)

// Ref is a writable handle on a host variable, produced by Mutable
// and understood by BindVar.
//
// It exists so that intent is a type rather than a shape. BindVar
// takes an any, and an any erases the interface it came from:
// reflect.ValueOf(io.EOF) is a *errors.errorString, indistinguishable
// from a pointer the host passed on purpose. Reading pointerness as
// intent would bind io.EOF as a mutable errors.errorString, so intent
// is spelled instead.
type Ref struct {
	// val is the settable Elem of the pointer Mutable was given, so a
	// write through it reaches the host's variable.
	val reflect.Value
}

// Mutable marks a host variable as writable by a program:
//
//	rt.BindVar("os.Args", gozero.Mutable(&os.Args))
//
// It takes the address because an address is the only thing that can
// alias. Mutable(os.Args) would hand over a copy, and a write to a
// copy reaches nothing the host reads.
func Mutable[T any](p *T) Ref {
	return Ref{val: reflect.ValueOf(p).Elem()}
}

// Ptr returns a pointer to a copy of in, the generic spelling of
// taking an address without reflect. It makes a fresh cell to bind:
//
//	rt.BindVar("cursor", gozero.Mutable(gozero.Ptr(0)))
//
// The copy is Go's, and is only about the header: Ptr of a slice or a
// map still shares the elements behind it.
func Ptr[T any](in T) *T { return &in }
