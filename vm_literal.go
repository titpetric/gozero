package gozero

import (
	"fmt"
	"reflect"
)

// The multi-statement VM. A program is a list of calls that bind their
// results to names. Because there are no operators, every value is a
// literal, a name, or the result of another call.
//
// Two rules shape the compiled form:
//
// A trailing error result is never a value. Because the compiler strips
// it from the result list and checks it after every call,
// "req := http.NewRequest(...)" binds one name to a two-result call and
// a failure returns from Exec or Scan at that statement. No program text
// mentions an error.
//
// Every argument is optional. A parameter the statement does not supply
// takes the zero value of its type; http.NewRequest with two arguments
// receives a nil body that way.
//
// Because names the program binds have a static type, the compiler
// resolves methods on them against that type when the statement
// compiles.
// json.NewEncoder is a binding; Encode is not. The program reaches it
// through the *json.Encoder the binding returns. Names coming from the
// caller's stack, including dest, are opaque and the VM checks them
// when it reads them.
// nilAs is the value the nil literal takes for a parameter type:
// the zero value when the type can hold nil, an error when it cannot.
func nilAs(pt reflect.Type) (reflect.Value, error) {
	switch pt.Kind() {
	case reflect.Pointer, reflect.UnsafePointer, reflect.Interface, reflect.Slice,
		reflect.Map, reflect.Chan, reflect.Func:
		return reflect.Zero(pt), nil
	}
	return reflect.Value{}, fmt.Errorf("cannot use nil as %s", pt)
}

// literalValue converts a parsed literal to type t.
func literalValue(t reflect.Type, a arg) (reflect.Value, error) {
	if a.kind == argBool {
		v := reflect.ValueOf(a.b)
		if t.Kind() == reflect.Interface && t.NumMethod() == 0 {
			return v, nil
		}
		if !v.Type().AssignableTo(t) {
			return reflect.Value{}, fmt.Errorf("cannot use %v as %s", a.b, t)
		}
		return v, nil
	}
	if a.kind == argNil {
		return nilAs(t)
	}
	if a.kind == argString {
		v := reflect.ValueOf(a.str)
		if !v.Type().AssignableTo(t) {
			if t.Kind() == reflect.Interface && t.NumMethod() == 0 {
				return v, nil
			}
			return reflect.Value{}, fmt.Errorf("cannot use a string as %s", t)
		}
		return v, nil
	}
	return literalAs(t, a)
}

// literalAs converts a numeric literal to the parameter type it fills.
// The parser only produces int64 and float64. Without this, a binding
// taking int, int32 or float32 could not receive a literal at all. The
// value must be representable: a literal that does not fit is a
// compile error, and never wraps.
//
// An empty interface parameter takes the literal at its written width,
// int64 or float64, as the parser produced it.
func literalAs(pt reflect.Type, a arg) (reflect.Value, error) {
	if pt.Kind() == reflect.Interface {
		if pt.NumMethod() != 0 {
			return reflect.Value{}, fmt.Errorf("cannot use a number as %s", pt)
		}
		if a.kind == argInt {
			return reflect.ValueOf(a.i), nil
		}
		return reflect.ValueOf(a.f), nil
	}
	v := reflect.New(pt).Elem()
	switch pt.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if a.kind == argFloat {
			return reflect.Value{}, fmt.Errorf("cannot use %v as %s, it has a decimal point", a.f, pt)
		}
		if v.OverflowInt(a.i) {
			return reflect.Value{}, fmt.Errorf("%d overflows %s", a.i, pt)
		}
		v.SetInt(a.i)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		if a.kind == argFloat {
			return reflect.Value{}, fmt.Errorf("cannot use %v as %s, it has a decimal point", a.f, pt)
		}
		if a.i < 0 {
			return reflect.Value{}, fmt.Errorf("cannot use %d as %s, it is negative", a.i, pt)
		}
		if v.OverflowUint(uint64(a.i)) {
			return reflect.Value{}, fmt.Errorf("%d overflows %s", a.i, pt)
		}
		v.SetUint(uint64(a.i))
	case reflect.Float32, reflect.Float64:
		f := a.f
		if a.kind == argInt {
			f = float64(a.i)
		}
		if v.OverflowFloat(f) {
			return reflect.Value{}, fmt.Errorf("%v overflows %s", f, pt)
		}
		v.SetFloat(f)
	default:
		return reflect.Value{}, fmt.Errorf("cannot use a number as %s", pt)
	}
	return v, nil
}

// fieldOf resolves name as an exported struct field on t,
// dereferencing a pointer to a struct. deref reports whether reading
// the field first needs a dereference of the value.
func fieldOf(t reflect.Type, name string) (f reflect.StructField, deref bool, ok bool) {
	st := t
	if st.Kind() == reflect.Pointer {
		st, deref = st.Elem(), true
	}
	if st.Kind() != reflect.Struct {
		return reflect.StructField{}, false, false
	}
	f, ok = st.FieldByName(name)
	if !ok || f.PkgPath != "" {
		return reflect.StructField{}, false, false
	}
	return f, deref, true
}

// unspread strips the spread flag for compiling the slice argument
// itself.
func unspread(a arg) arg {
	a.spread = false
	return a
}
