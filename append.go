package gozero

import (
	"fmt"
	"reflect"
)

// Append appends through a pointer to a slice, which the builtin
// cannot do once the slice has been through an interface: append
// returns a new header and a value parameter has nowhere to put it.
// &name on a value binding yields the pointer.
//
// The common element shapes are type-asserted, the rest goes through
// reflect, and a nil slice appends to an empty one as in Go.
func Append(slice any, v any) error {
	if slice == nil {
		return fmt.Errorf("append: the destination is nil")
	}
	// Before the type switch, because a typed nil pointer matches an
	// arm and the write through it would be a crash rather than an
	// error. An any holding (*[]string)(nil) is not == nil.
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
		*p = append(*p, v)
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
	sv.Set(reflect.Append(sv, ev))
	return nil
}
