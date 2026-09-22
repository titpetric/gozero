package gozero

import (
	"fmt"
	"reflect"
)

// Value bindings. Bind carries funcs; BindVar carries data, and
// whether a program may write it is stated at the call site:
//
//	rt.BindVar("os.Args", os.Args)           // immutable: a snapshot
//	rt.BindVar("os.Args", Mutable(&os.Args)) // mutable: aliases the variable
//
// Mutable is a marker rather than a plain pointer because an any
// erases the interface it came from: reflect.ValueOf(io.EOF) is a
// *errors.errorString, indistinguishable from a pointer the host
// passed on purpose. Reading pointerness as intent would bind io.EOF
// as a mutable errors.errorString, so intent is spelled instead.
//
// An immutable binding is copied into the runtime the way passing a
// value to a function copies it, so a program that assigns to it
// would be writing a copy nobody reads, and the compiler rejects the
// statement. Taking its address is rejected for the same reason,
// which is Go's addressability rule.
//
// Shallow copying is Go's too. An immutable binding of a slice or a
// map copies the header, so the elements behind it stay shared and a
// binding that writes them writes host memory. Only rebinding the
// name itself is blocked.

// varBinding is one value binding: the value, and whether a program
// may assign to it.
type varBinding struct {
	// val is the bound value. For a mutable binding it is the
	// settable Elem of the pointer the host passed, so writes reach
	// the host's variable and Addr gives back the original pointer.
	val     reflect.Value
	mutable bool
}

// BindVar registers a value under a name, read in argument position
// like any other and carrying the static type it was bound with.
// Wrap the address in [Mutable] to make it writable; a value bound
// without it rejects assignment when the program compiles. Funcs
// belong to [Runtime.Bind] and are rejected here.
func (r *Runtime) BindVar(name string, v any) error {
	if v == nil {
		return fmt.Errorf("bindvar: %s: cannot bind a nil value, its type is unknown", name)
	}

	var vb varBinding
	switch ref := v.(type) {
	case Ref:
		if !ref.val.IsValid() {
			return fmt.Errorf("bindvar: %s: the Mutable handle is zero", name)
		}
		vb = varBinding{val: ref.val, mutable: true}
	default:
		rv := reflect.ValueOf(v)
		if rv.Kind() == reflect.Func {
			return fmt.Errorf("bindvar: %s is a func, register it with Bind", name)
		}
		if rv.Kind() == reflect.Pointer && rv.IsNil() {
			return fmt.Errorf("bindvar: %s: cannot bind a nil %s", name, rv.Type())
		}
		vb = varBinding{val: rv}
	}

	r.mu.Lock()
	r.compiler.vars[name] = vb
	if r.log != nil {
		r.log.Debug("bindvar", "name", name, "type", vb.val.Type().String(), "mutable", vb.mutable)
	}
	r.origin = name
	r.discover(vb.val.Type(), 1)
	r.origin = ""
	r.mu.Unlock()
	return nil
}

// Delete removes a key from a map held in an any, which is what the
// builtin delete cannot do once the map has been through an interface.
// The common key shapes are type-asserted and the rest goes through
// reflect, so a bound Delete costs a type switch on the paths a
// program actually takes.
//
// A missing key is not an error, as in Go. A nil map is not either:
// deleting from one is a no-op in Go and stays one here.
func Delete(m any, key any) error {
	if m == nil {
		return fmt.Errorf("delete: the map is nil")
	}
	switch t := m.(type) {
	case map[string]any:
		k, ok := key.(string)
		if !ok {
			return fmt.Errorf("delete: cannot use %T as a map[string]any key", key)
		}
		delete(t, k)
		return nil
	case map[string]string:
		k, ok := key.(string)
		if !ok {
			return fmt.Errorf("delete: cannot use %T as a map[string]string key", key)
		}
		delete(t, k)
		return nil
	case map[string]int:
		k, ok := key.(string)
		if !ok {
			return fmt.Errorf("delete: cannot use %T as a map[string]int key", key)
		}
		delete(t, k)
		return nil
	case map[string][]string:
		k, ok := key.(string)
		if !ok {
			return fmt.Errorf("delete: cannot use %T as a map[string][]string key", key)
		}
		delete(t, k)
		return nil
	}

	mv := reflect.ValueOf(m)
	if mv.Kind() == reflect.Pointer {
		if mv.IsNil() {
			return fmt.Errorf("delete: the map pointer is nil")
		}
		mv = mv.Elem()
	}
	if mv.Kind() != reflect.Map {
		return fmt.Errorf("delete: %s is not a map", mv.Type())
	}
	if mv.IsNil() {
		return nil
	}
	kt := mv.Type().Key()
	kv := reflect.ValueOf(key)
	if !kv.IsValid() {
		kv = reflect.Zero(kt)
	}
	if !kv.Type().AssignableTo(kt) {
		if !kv.Type().ConvertibleTo(kt) {
			return fmt.Errorf("delete: cannot use %s as a %s key", kv.Type(), mv.Type())
		}
		kv = kv.Convert(kt)
	}
	// The zero Value is delete's erase form.
	mv.SetMapIndex(kv, reflect.Value{})
	return nil
}
