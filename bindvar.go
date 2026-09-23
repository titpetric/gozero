package gozero

import (
	"fmt"
	"reflect"
)

// Value bindings. Bind takes a func or a value: a func is callable, a
// value is readable, and Go's own reference rules decide the rest.
//
//	rt.Bind("url.Parse", url.Parse)  // a func, called
//	rt.Bind("io.EOF", io.EOF)        // a value, read
//	rt.Bind("os.Args", &os.Args)     // a pointer, read and written through
//
// A value is copied into the runtime the way passing a value to a
// function copies it, so the name is the program's own for the run:
// "label = x" writes the copy and the host's variable is untouched.
// Binding an address opts into mutation without any API for it. The
// name is then a *T, so "*os.Args" reads the host's slice and
// "*os.Args = xs" writes it, exactly as the same spellings do in Go.
//
// Shallow copying is Go's too. A value binding of a slice or a map
// copies the header, so the elements behind it stay shared and a
// binding that writes them writes host memory.

// varBinding is one value binding: the value the name reads.
type varBinding struct {
	val reflect.Value
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
