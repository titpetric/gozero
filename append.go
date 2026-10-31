package gozero

import (
	"fmt"
	"reflect"
)

// Append appends through a pointer to a slice, which the builtin
// cannot do once the slice has been through an interface: append
// returns a new header and a value parameter has nowhere to put it.
// &name on a value binding names that pointer.
//
// The common element shapes are type-asserted, the rest takes the
// reflect path, and a nil slice appends to an empty one as in Go.
func Append(slice any, v any) error {
	if slice == nil {
		return fmt.Errorf("append: the destination is nil")
	}
	// Before the type switch: a typed nil pointer matches an arm, and
	// the write through it panics where Append owes an error. An any
	// holding (*[]string)(nil) is not == nil.
	if pv := reflect.ValueOf(slice); pv.Kind() == reflect.Pointer && pv.IsNil() {
		return fmt.Errorf("append: the destination pointer is nil")
	}
	switch p := slice.(type) {
	case *[]string:
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("append: cannot use %T as a []string element", v)
		}
		*p = append(*p, s)
		return nil
	case *[]int:
		n, ok := v.(int)
		if !ok {
			return fmt.Errorf("append: cannot use %T as a []int element", v)
		}
		*p = append(*p, n)
		return nil
	case *[]any:
		*p = append(*p, copyBoxed(v))
		return nil
	}

	pv := reflect.ValueOf(slice)
	if pv.Kind() != reflect.Pointer {
		return fmt.Errorf("append: %s is not a pointer to a slice, write &name", pv.Type())
	}
	if pv.IsNil() {
		return fmt.Errorf("append: the destination pointer is nil")
	}
	sv := pv.Elem()
	if sv.Kind() != reflect.Slice {
		return fmt.Errorf("append: %s is not a slice", sv.Type())
	}
	if !sv.CanSet() {
		return fmt.Errorf("append: %s is not settable", pv.Type())
	}
	et := sv.Type().Elem()
	ev := reflect.ValueOf(v)
	if !ev.IsValid() {
		ev = reflect.Zero(et)
	}
	if !ev.Type().AssignableTo(et) {
		if !ev.Type().ConvertibleTo(et) {
			return fmt.Errorf("append: cannot use %s as a %s element", ev.Type(), sv.Type())
		}
		ev = ev.Convert(et)
	}
	// The store copies an element of concrete type. An interface
	// element would keep the box the argument arrived in, which stays
	// valid only for the length of the call. The slice takes a box of
	// its own.
	if et.Kind() == reflect.Interface {
		ev = reflect.ValueOf(copyBoxed(ev.Interface()))
		if !ev.IsValid() {
			ev = reflect.Zero(et)
		}
	}
	sv.Set(reflect.Append(sv, ev))
	return nil
}

// copyBoxed moves a borrowed any into a box of its own: a fresh cell,
// a typed move into it, and a new interface over that cell. A plain
// reflect.ValueOf(v).Interface() is not a copy, since for a
// non-addressable value it returns an interface over the same box as
// the argument.
//
// The binding contract makes every argument borrowed for the length
// of the call, and the direct tier pools the cell a value boxes into
// for an interface parameter. A binding that keeps a box past the
// call has to copy it first, or the release clears the block
// underneath it.
func copyBoxed(v any) any {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return nil
	}

	nv := reflect.New(rv.Type()).Elem()
	nv.Set(rv)

	return nv.Interface()
}
