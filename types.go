package gozero

import (
	"fmt"
	"reflect"
)

// The type registry answers "what does this name in a var statement
// mean". Almost every type a program needs is already reachable from
// the bindings: binding http.NewRequest names string, io.Reader,
// *http.Request and error, and walking those reaches http.Request, its
// methods, and the types those return. Only a type no binding mentions
// has to be registered by hand with BindType.
//
// Names are reflect.Type.String(), so "int64", "*http.Request",
// "http.Header". Two packages with the same base name would collide;
// the later Bind wins, and BindType is the way out.

// discoverDepth bounds the walk. A bound function is one level, its
// results the next, the struct behind a result pointer the next, and
// that struct's fields the one after: reaching http.Header from
// http.NewRequest takes five. Beyond that the walk starts pulling in
// the transitive closure of the standard library for no gain.
const discoverDepth = 5

// predeclared seeds the registry with the types a program can name
// without any binding mentioning them.
func predeclared() map[string]reflect.Type {
	out := map[string]reflect.Type{}
	for _, t := range []reflect.Type{
		reflect.TypeFor[bool](),
		reflect.TypeFor[string](),
		reflect.TypeFor[int](), reflect.TypeFor[int8](), reflect.TypeFor[int16](),
		reflect.TypeFor[int32](), reflect.TypeFor[int64](),
		reflect.TypeFor[uint](), reflect.TypeFor[uint8](), reflect.TypeFor[uint16](),
		reflect.TypeFor[uint32](), reflect.TypeFor[uint64](), reflect.TypeFor[uintptr](),
		reflect.TypeFor[float32](), reflect.TypeFor[float64](),
		reflect.TypeFor[[]byte](),
		reflect.TypeFor[error](),
	} {
		out[t.String()] = t
	}
	// any prints as "interface {}", which is not a name a program can
	// write, so it gets the spelling Go uses in source.
	out["any"] = reflect.TypeFor[any]()
	return out
}

// discover records t and the types reachable from it, so a var
// statement can name anything the bindings imply.
func (r *Runtime) discover(t reflect.Type, depth int) {
	if t == nil || depth > discoverDepth {
		return
	}
	name := t.String()
	if _, seen := r.types[name]; seen {
		return // also what stops a recursive type looping
	}
	r.types[name] = t
	if r.log != nil {
		r.log.Debug("type discovered",
			"name", name,
			"kind", t.Kind().String(),
			"via", r.origin,
			"depth", depth,
			"methods", t.NumMethod())
	}

	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Chan:
		r.discover(t.Elem(), depth+1)
	case reflect.Map:
		r.discover(t.Key(), depth+1)
		r.discover(t.Elem(), depth+1)
	case reflect.Struct:
		// Field types matter because a program can read a field, so
		// req.Header has to make http.Header nameable.
		for i := 0; i < t.NumField(); i++ {
			if f := t.Field(i); f.PkgPath == "" {
				r.discover(f.Type, depth+1)
			}
		}
	case reflect.Func:
		for i := 0; i < t.NumIn(); i++ {
			r.discover(t.In(i), depth+1)
		}
		for i := 0; i < t.NumOut(); i++ {
			r.discover(t.Out(i), depth+1)
		}
	}
	// A method's signature names types the program can reach by calling
	// it, so they belong in the registry too.
	for i := 0; i < t.NumMethod(); i++ {
		r.discover(t.Method(i).Type, depth+1)
	}
}

// BindType registers a type under a name, for the case a type is not
// reachable from any binding. v is a value of the type; for an
// interface, pass a nil pointer to it, BindType("io.Closer",
// (*io.Closer)(nil)).
func (r *Runtime) BindType(name string, v any) error {
	t := reflect.TypeOf(v)
	if t == nil {
		return fmt.Errorf("bindtype: %s: cannot take the type of a nil value", name)
	}
	if t.Kind() == reflect.Pointer && t.Elem().Kind() == reflect.Interface {
		t = t.Elem()
	}
	r.mu.Lock()
	r.types[name] = t
	if r.log != nil {
		r.log.Debug("type bound", "name", name, "kind", t.Kind().String())
	}
	r.origin = name
	r.discover(t, 1)
	r.origin = ""
	r.mu.Unlock()
	return nil
}

// Types lists the names a var statement can use, for working out why
// one was not found.
func (r *Runtime) Types() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.types))
	for name := range r.types {
		out = append(out, name)
	}
	return out
}

// lookupType resolves a type name written in a var statement. The
// caller holds the lock.
func (c *Compiler) lookupType(name string) (reflect.Type, bool) {
	t, ok := c.types[name]
	return t, ok
}
