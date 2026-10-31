package gozero

import (
	"context"
	"fmt"
	"reflect"
)

// Slice literals. []string{"a", "b"} builds a slice per evaluation,
// the way a Go composite literal allocates each time the expression
// runs: a value shared between runs would share what a binding wrote
// into it.
//
// The element type comes from the type the literal names, not from
// the parameter, so []string{} filling an any parameter arrives as a
// []string. Each element is compiled against that type, which gives
// the same literal checks an argument gets.

// compileSliceLit compiles []T{...} to the argument that builds it.
func (c *Compiler) compileSliceLit(slots map[string]int, env map[string]reflect.Type, a arg) (*vmArg, reflect.Type, error) {
	st, ok := c.lookupType(a.typ)
	if !ok {
		return nil, nil, fmt.Errorf("unknown type %q, register it with BindType", a.typ)
	}
	if st.Kind() != reflect.Slice {
		return nil, nil, fmt.Errorf("%s is not a slice type", a.typ)
	}
	et := st.Elem()
	out := &vmArg{kind: vaSlice, styp: st, typ: st, iface: -1}
	for i := range a.elems {
		if a.elems[i].name != "" {
			return nil, nil, fmt.Errorf("a slice literal takes no keys, %s: is a struct form", a.elems[i].name)
		}
		v, err := c.compileArg(slots, env, a.typ, i, et, a.elems[i].val)
		if err != nil {
			return nil, nil, err
		}
		out.elems = append(out.elems, vmElem{val: v})
	}
	return out, st, nil
}

// buildSlice evaluates the elements into a fresh slice.
func (a *vmArg) buildSlice(ctx context.Context, slots, frame []reflect.Value, ifaces []ifacePair, stack map[string]any, dest any) (reflect.Value, error) {
	sv := reflect.MakeSlice(a.styp, len(a.elems), len(a.elems))
	for i := range a.elems {
		v, err := a.elems[i].val.get(ctx, slots, frame, ifaces, stack, dest)
		if err != nil {
			return reflect.Value{}, err
		}
		sv.Index(i).Set(v)
	}
	return sv, nil
}
