package gozero

import (
	"context"
	"fmt"
	"reflect"
)

// Value bindings in a compiled program. A name BindVar registered is
// read like any other argument and written only when the host bound a
// pointer; both decisions are made here, at compile time, so a
// program that writes an immutable binding never reaches execution.

// varArg resolves a dotted path against the value bindings and returns
// the argument that reads it, with the static type. ok is false when
// no prefix of the path names a value binding, which leaves the path
// to the other resolvers.
func (c *Compiler) varArg(path []string, addrOf bool) (a *vmArg, t reflect.Type, ok bool, err error) {
	vb, rest, ok := c.varOf(path)
	if !ok {
		return nil, nil, false, nil
	}
	name := joinPath(path)
	t = vb.val.Type()
	a = &vmArg{kind: vaVar, name: name, varv: vb.val, mutable: vb.mutable, typ: t, iface: -1}
	for _, seg := range rest {
		f, deref, found := fieldOf(t, seg)
		if !found {
			return nil, nil, true, fmt.Errorf("%s has no field %s", t, seg)
		}
		a = &vmArg{kind: vaField, src: a, index: f.Index, deref: deref, typ: f.Type, iface: -1}
		t = f.Type
	}
	if !addrOf {
		return a, t, true, nil
	}
	// Go's addressability rule. A value binding is a copy the runtime
	// holds; taking its address would hand out a pointer to that copy,
	// which reads like a handle on the host's variable and is not one.
	if a.kind == vaVar && !vb.mutable {
		return nil, nil, true, fmt.Errorf("cannot take the address of %s, it is bound by value; pass a pointer to BindVar to make it addressable", name)
	}
	if a.kind == vaField && !fieldAddressable(a) {
		return nil, nil, true, fmt.Errorf("cannot take the address of %s, its base is bound by value", name)
	}
	a.addrOf = true
	a.typ = reflect.PointerTo(t)
	return a, a.typ, true, nil
}

// fieldAddressable reports whether a field chain rooted in a value
// binding has an address: either the root is mutable, or a pointer in
// the chain makes what follows it addressable regardless.
func fieldAddressable(a *vmArg) bool {
	for a.kind == vaField {
		if a.deref {
			return true
		}
		a = a.src
	}
	if a.kind == vaVar {
		return a.mutable
	}
	return true
}

// derefArg wraps an argument in a pointer read, "*p". The operand's
// static type must be a pointer, which is Go's rule and is what keeps
// the nil check out of every other argument kind.
func derefArg(src *vmArg, t reflect.Type, name string) (*vmArg, reflect.Type, error) {
	if t == nil || t.Kind() != reflect.Pointer {
		return nil, nil, fmt.Errorf("cannot dereference %s, it is not a pointer", name)
	}
	et := t.Elem()
	return &vmArg{kind: vaDeref, src: src, name: name, typ: et, iface: -1}, et, nil
}

// vmVarSet is a compiled write to a value binding: "os.Args = xs", or
// "*p = v" where p is a pointer. target is the settable destination
// resolved at compile time, and val is the value to store.
type vmVarSet struct {
	target reflect.Value
	val    *vmArg
	name   string // for diagnostics
	// via is set when the destination is reached through a pointer
	// held in the program rather than fixed at compile time; target is
	// then invalid and via produces the pointer per run.
	via *vmArg
}

func (vs *vmVarSet) apply(ctx context.Context, slots, frame []reflect.Value, ifaces []ifacePair, stack map[string]any, dest any) error {
	v, err := vs.val.get(ctx, slots, frame, ifaces, stack, dest)
	if err != nil {
		return err
	}
	target := vs.target
	if vs.via != nil {
		p, err := vs.via.get(ctx, slots, frame, ifaces, stack, dest)
		if err != nil {
			return err
		}
		if p.Kind() != reflect.Pointer {
			return fmt.Errorf("exec: %s: cannot write through %s", vs.name, p.Type())
		}
		if p.IsNil() {
			return fmt.Errorf("exec: %s: nil pointer dereference", vs.name)
		}
		target = p.Elem()
	}
	if !target.CanSet() {
		return fmt.Errorf("exec: %s: the destination is not settable", vs.name)
	}
	if !v.IsValid() {
		target.Set(reflect.Zero(target.Type()))
		return nil
	}
	if !v.Type().AssignableTo(target.Type()) {
		return fmt.Errorf("exec: %s: cannot use %s as %s", vs.name, v.Type(), target.Type())
	}
	target.Set(v)
	return nil
}

// compileVarSet compiles an assignment whose target is a value
// binding. A binding the host passed by value has no writable
// destination, and saying so here is the whole point of the
// distinction BindVar draws.
func (c *Compiler) compileVarSet(slots map[string]int, env map[string]reflect.Type, path []string, s stmt) (*vmVarSet, error) {
	vb, rest, ok := c.varOf(path)
	if !ok {
		return nil, fmt.Errorf("compile: %s is not a value binding", joinPath(path))
	}
	name := joinPath(path)
	if !vb.mutable {
		return nil, fmt.Errorf("compile: cannot assign to %s, it is bound by value; pass a pointer to BindVar to make it writable", name)
	}
	target := vb.val
	t := target.Type()
	for _, seg := range rest {
		f, deref, found := fieldOf(t, seg)
		if !found {
			return nil, fmt.Errorf("compile: %s has no field %s", t, seg)
		}
		if deref {
			if target.IsNil() {
				return nil, fmt.Errorf("compile: %s: field write through a nil %s", name, t)
			}
			target = target.Elem()
		}
		target = target.FieldByIndex(f.Index)
		t = f.Type
	}
	if !target.CanSet() {
		return nil, fmt.Errorf("compile: %s is not settable", name)
	}
	val, err := c.assignedValue(slots, env, name, t, s)
	if err != nil {
		return nil, err
	}
	return &vmVarSet{target: target, val: val, name: name}, nil
}

// compileDerefSet compiles "*p = v". The pointer comes from a program
// name or a value binding, so the destination is only known per run
// and travels as via.
func (c *Compiler) compileDerefSet(slots map[string]int, env map[string]reflect.Type, path []string, s stmt) (*vmVarSet, error) {
	name := "*" + joinPath(path)
	ptr, pt, err := c.pathValue(slots, env, path)
	if err != nil {
		return nil, fmt.Errorf("compile: %s: %w", name, err)
	}
	if pt == nil || pt.Kind() != reflect.Pointer {
		return nil, fmt.Errorf("compile: %s: %s is not a pointer", name, joinPath(path))
	}
	val, err := c.assignedValue(slots, env, name, pt.Elem(), s)
	if err != nil {
		return nil, err
	}
	return &vmVarSet{via: ptr, val: val, name: name}, nil
}

// addrArg compiles "&x": the address of a program name, a field of
// one, or a mutable value binding. Go's rule decides what qualifies -
// a variable has an address and a copy does not - and the two cases
// differ only in where the storage lives.
func (c *Compiler) addrArg(slots map[string]int, env map[string]reflect.Type, path []string) (*vmArg, reflect.Type, error) {
	if _, ok := slots[path[0]]; ok {
		a, t, err := c.pathValue(slots, env, path)
		if err != nil {
			return nil, nil, err
		}
		pa, err := addrOf(t, a)
		if err != nil {
			return nil, nil, fmt.Errorf("&%s: %w", joinPath(path), err)
		}
		return pa, pa.typ, nil
	}
	a, t, ok, err := c.varArg(path, true)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, fmt.Errorf("&%s: %s is not a name bound by the program or a value binding", joinPath(path), joinPath(path))
	}
	return a, t, nil
}

// pathValue resolves a dotted path to the argument that reads it and
// its static type, trying program names first and value bindings
// after, which is the order every other resolver uses.
func (c *Compiler) pathValue(slots map[string]int, env map[string]reflect.Type, path []string) (*vmArg, reflect.Type, error) {
	if slot, ok := slots[path[0]]; ok {
		t := env[path[0]]
		a := &vmArg{kind: vaSlot, slot: slot, name: path[0], typ: t, iface: -1}
		for _, seg := range path[1:] {
			f, deref, found := fieldOf(t, seg)
			if !found {
				return nil, nil, fmt.Errorf("%s has no field %s", t, seg)
			}
			a = &vmArg{kind: vaField, src: a, index: f.Index, deref: deref, typ: f.Type, iface: -1}
			t = f.Type
		}
		return a, t, nil
	}
	a, t, ok, err := c.varArg(path, false)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, fmt.Errorf("%s is not a name bound by the program or a value binding", joinPath(path))
	}
	return a, t, nil
}

// assignedValue compiles the right-hand side of an assignment against
// the destination type, for the forms that share one: a literal, a
// composite literal, a call, or a name.
func (c *Compiler) assignedValue(slots map[string]int, env map[string]reflect.Type, name string, t reflect.Type, s stmt) (*vmArg, error) {
	if s.lit != nil {
		switch s.lit.kind {
		case argStruct:
			sa, st, err := c.compileStructLit(slots, env, *s.lit)
			if err != nil {
				return nil, fmt.Errorf("compile: %s: %w", name, err)
			}
			if !st.AssignableTo(t) {
				return nil, fmt.Errorf("compile: %s: cannot use %s as %s", name, st, t)
			}
			sa.typ = t
			return sa, nil
		case argVar, argPath, argAddr, argDeref, argRecv:
			return c.compileArg(slots, env, name, 0, t, *s.lit)
		}
		v, err := literalValue(t, *s.lit)
		if err != nil {
			return nil, fmt.Errorf("compile: %s: %w", name, err)
		}
		return &vmArg{kind: vaConst, val: v, typ: t, iface: -1}, nil
	}
	if s.call == nil {
		return nil, fmt.Errorf("compile: %s: nothing to assign", name)
	}
	call, rt, err := c.compileExpr(slots, env, s.call)
	if err != nil {
		return nil, err
	}
	if rt == nil || !rt.AssignableTo(t) {
		return nil, fmt.Errorf("compile: %s: cannot use %s as %s", name, rt, t)
	}
	return &vmArg{kind: vaCall, sub: call, typ: t, iface: -1}, nil
}
