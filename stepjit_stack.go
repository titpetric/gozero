package gozero

import (
	"context"
	"fmt"
	"reflect"
	"unsafe" // also required by go:linkname
)

// Scalar reads off the caller's stack map: one type assertion per
// predeclared type, no reflect on the read path.

// stackScalarNode reads a scalar off the caller's stack. The stack
// holds any, so the read is one type assertion against the exact
// parameter type; a named scalar type would need reflect to check and
// stays on the reflect evaluator. An unset or nil entry is the zero
// value, like every other stack read.
func stackScalarNode(name string, pt reflect.Type, cl layout) (node, error) {
	get := stackScalarConvs[pt]
	if get == nil {
		return node{}, fmt.Errorf("a stack value of named type %s stays on the reflect tier", pt)
	}
	if cl.float() {
		return node{class: cl, F: func(_ unsafe.Pointer, _ context.Context, st map[string]any, _ any) (float64, error) {
			v, ok := st[name]
			if !ok || v == nil {
				return 0, nil
			}
			bits, fl, ok := get(v)
			if !ok {
				return 0, fmt.Errorf("exec: variable %q: cannot use %T as %s", name, v, pt)
			}
			_ = bits
			return fl, nil
		}}, nil
	}
	return node{class: cl, N: func(_ unsafe.Pointer, _ context.Context, st map[string]any, _ any) (uint64, error) {
		v, ok := st[name]
		if !ok || v == nil {
			return 0, nil
		}
		bits, _, ok := get(v)
		if !ok {
			return 0, fmt.Errorf("exec: variable %q: cannot use %T as %s", name, v, pt)
		}
		return bits, nil
	}}, nil
}

// stackScalarConvs asserts a stack any to each predeclared scalar type
// and reports its bits. Keyed by exact type, so a named type misses.
var stackScalarConvs = map[reflect.Type]func(any) (uint64, float64, bool){
	reflect.TypeFor[bool](): func(v any) (uint64, float64, bool) {
		b, ok := v.(bool)
		if b {
			return 1, 0, ok
		}
		return 0, 0, ok
	},
	reflect.TypeFor[int](): func(v any) (uint64, float64, bool) {
		n, ok := v.(int)
		return uint64(int64(n)), 0, ok
	},
	reflect.TypeFor[int8](): func(v any) (uint64, float64, bool) {
		n, ok := v.(int8)
		return uint64(uint8(n)), 0, ok
	},
	reflect.TypeFor[int16](): func(v any) (uint64, float64, bool) {
		n, ok := v.(int16)
		return uint64(uint16(n)), 0, ok
	},
	reflect.TypeFor[int32](): func(v any) (uint64, float64, bool) {
		n, ok := v.(int32)
		return uint64(uint32(n)), 0, ok
	},
	reflect.TypeFor[int64](): func(v any) (uint64, float64, bool) {
		n, ok := v.(int64)
		return uint64(n), 0, ok
	},
	reflect.TypeFor[uint](): func(v any) (uint64, float64, bool) {
		n, ok := v.(uint)
		return uint64(n), 0, ok
	},
	reflect.TypeFor[uint8](): func(v any) (uint64, float64, bool) {
		n, ok := v.(uint8)
		return uint64(n), 0, ok
	},
	reflect.TypeFor[uint16](): func(v any) (uint64, float64, bool) {
		n, ok := v.(uint16)
		return uint64(n), 0, ok
	},
	reflect.TypeFor[uint32](): func(v any) (uint64, float64, bool) {
		n, ok := v.(uint32)
		return uint64(n), 0, ok
	},
	reflect.TypeFor[uint64](): func(v any) (uint64, float64, bool) {
		n, ok := v.(uint64)
		return n, 0, ok
	},
	reflect.TypeFor[float32](): func(v any) (uint64, float64, bool) {
		f, ok := v.(float32)
		return 0, float64(f), ok
	},
	reflect.TypeFor[float64](): func(v any) (uint64, float64, bool) {
		f, ok := v.(float64)
		return 0, f, ok
	},
}
