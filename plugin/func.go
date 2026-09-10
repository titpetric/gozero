package plugin

import (
	"fmt"
	"reflect"
)

// Func looks name up in p and adapts the symbol to the Go func type
// T: the exact signature asserts directly, and a named func type
// with the same underlying converts. The raw Lookup plus a type
// assertion is the standard-library-compatible path; this is the
// ergonomic one.
func Func[T any](p Plugin, name string) (T, error) {
	var zero T
	sym, err := p.Lookup(name)
	if err != nil {
		return zero, err
	}
	if got, ok := sym.(T); ok {
		return got, nil
	}
	ft := reflect.TypeFor[T]()
	sv := reflect.ValueOf(sym)
	if ft.Kind() == reflect.Func && sv.Type().ConvertibleTo(ft) {
		return sv.Convert(ft).Interface().(T), nil
	}
	return zero, fmt.Errorf("plugin: symbol %s is %s, not %s", name, sv.Type(), ft)
}
