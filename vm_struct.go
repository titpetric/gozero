package gozero

import (
	"fmt"
	"reflect"
	"strings"
)

// compileStructLit compiles a composite literal, url.URL{Path: "/"}
// or &http.Request{}, returning the compiled value and its static
// type. The path must name a registered struct type. Elements are
// keyed or positional but not mixed, as in Go; a positional list
// fills the fields in declaration order and may stop early, the rest
// staying zero, matching how a call fills missing arguments. Each
// value compiles against its field's type, so a numeric literal
// converts the way it does at a call.
func (c *Compiler) compileStructLit(slots map[string]int, env map[string]reflect.Type, a arg) (*vmArg, reflect.Type, error) {
	name := joinPath(a.path)
	t, ok := c.lookupType(name)
	if !ok {
		return nil, nil, fmt.Errorf("unknown type %q, register it with BindType", name)
	}
	if t.Kind() != reflect.Struct {
		return nil, nil, fmt.Errorf("%s is not a struct type", name)
	}
	out := &vmArg{kind: vaStruct, styp: t, addr: a.addr, iface: -1}
	keyed := false
	seen := map[string]bool{}
	for i, e := range a.elems {
		if i == 0 {
			keyed = e.name != ""
		} else if (e.name != "") != keyed {
			return nil, nil, fmt.Errorf("%s: cannot mix keyed and positional elements", name)
		}
		var f reflect.StructField
		if keyed {
			f, ok = t.FieldByName(e.name)
			// A promoted field is not a field of the literal's type,
			// matching Go, which also keeps a nil embedded pointer from
			// being written through.
			if !ok || f.PkgPath != "" || len(f.Index) > 1 {
				return nil, nil, fmt.Errorf("%s has no field %s", name, e.name)
			}
			if seen[e.name] {
				return nil, nil, fmt.Errorf("%s: duplicate field %s", name, e.name)
			}
			seen[e.name] = true
		} else {
			if i >= t.NumField() {
				return nil, nil, fmt.Errorf("%s has %d fields, got %d values", name, t.NumField(), len(a.elems))
			}
			f = t.Field(i)
			if f.PkgPath != "" {
				return nil, nil, fmt.Errorf("%s: field %s is unexported, use keyed elements", name, f.Name)
			}
		}
		va, err := c.compileArg(slots, env, name, i, f.Type, e.val)
		if err != nil {
			// compileArg speaks in call terms and carries the compile:
			// prefix the caller adds again; reduce it to the mismatch.
			return nil, nil, fmt.Errorf("field %s: %s", f.Name, strings.TrimPrefix(err.Error(), "compile: "))
		}
		out.elems = append(out.elems, vmElem{index: f.Index, val: va})
	}
	rt := t
	if a.addr {
		rt = reflect.PointerTo(t)
	}
	out.typ = rt
	return out, rt, nil
}
