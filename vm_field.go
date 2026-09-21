package gozero

import (
	"context"
	"fmt"
	"reflect"
)

// The field assignment, "req.Method = value;". The base is a
// program-bound name, every selector is an exported field, and the
// value is a literal or a call whose result is assignable to the
// field.

// fieldStep is one selector of a field-assignment target.
type fieldStep struct {
	index []int
	deref bool
}

// vmFieldSet is a compiled field assignment: the slot the base name
// lives in, the selectors to the field, and the value.
type vmFieldSet struct {
	base  int // slot of the base name
	steps []fieldStep
	val   *vmArg
	field string // for diagnostics
}

// compileFieldSet compiles req.Method = value.
func (c *Compiler) compileFieldSet(slots map[string]int, env map[string]reflect.Type, s stmt) (*vmFieldSet, error) {
	base := s.fieldLhs[0]
	slot, ok := slots[base]
	if !ok {
		return nil, fmt.Errorf("compile: %s is not a name bound by the program, so its fields cannot be assigned", base)
	}
	t := env[base]
	fs := &vmFieldSet{base: slot, field: joinPath(s.fieldLhs)}
	for _, seg := range s.fieldLhs[1:] {
		f, deref, ok := fieldOf(t, seg)
		if !ok {
			return nil, fmt.Errorf("compile: %s has no field %s", t, seg)
		}
		fs.steps = append(fs.steps, fieldStep{index: f.Index, deref: deref})
		t = f.Type
	}

	if s.lit != nil {
		if s.lit.kind == argFuncLit {
			// A func-typed field takes a literal through the same rules
			// an argument does: the field's type is the signature.
			return c.fieldSetFuncLit(slots, env, fs, t, *s.lit)
		}
		if s.lit.kind == argStruct {
			sa, st, err := c.compileStructLit(slots, env, *s.lit)
			if err != nil {
				return nil, fmt.Errorf("compile: %s: %w", fs.field, err)
			}
			if !st.AssignableTo(t) {
				return nil, fmt.Errorf("compile: %s: cannot assign %s to %s", fs.field, st, t)
			}
			fs.val = sa
			return fs, nil
		}
		v, err := literalValue(t, *s.lit)
		if err != nil {
			return nil, fmt.Errorf("compile: %s: %w", fs.field, err)
		}
		fs.val = &vmArg{kind: vaConst, val: v, typ: t, iface: -1}
		return fs, nil
	}
	call, rt, err := c.compileExpr(slots, env, s.call)
	if err != nil {
		return nil, err
	}
	if rt == nil || !rt.AssignableTo(t) {
		return nil, fmt.Errorf("compile: %s: cannot assign %s to %s", fs.field, rt, t)
	}
	fs.val = &vmArg{kind: vaCall, sub: call, typ: t, iface: -1}
	return fs, nil
}

// apply writes the value through the field chain. Addressability comes
// from a pointer in the chain; a struct held by value in a slot is only
// settable when the slot was created addressable by a var declaration.
func (fs *vmFieldSet) apply(ctx context.Context, slots, frame []reflect.Value, ifaces []ifacePair, stack map[string]any, dest any) error {
	v := slots[fs.base]
	if !v.IsValid() {
		return fmt.Errorf("exec: %s: the base is not set", fs.field)
	}
	for _, st := range fs.steps {
		if st.deref {
			if v.IsNil() {
				return fmt.Errorf("exec: %s: field write on a nil %s", fs.field, v.Type())
			}
			v = v.Elem()
		}
		v = v.FieldByIndex(st.index)
	}
	if !v.CanSet() {
		return fmt.Errorf("exec: %s: the value is not addressable, declare the base with var or hold it behind a pointer", fs.field)
	}
	val, err := fs.val.get(ctx, slots, frame, ifaces, stack, dest)
	if err != nil {
		return err
	}
	v.Set(val)
	return nil
}
