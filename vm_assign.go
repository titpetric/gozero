package gozero

import (
	"fmt"
	"reflect"
)

// The argument-frame layout and the assignment forms: field writes,
// return values, and the walk that gives every call a disjoint
// window into the per-run frame.

// assignFrame gives every call in the program a disjoint window into
// the per-run argument frame, so one allocation covers them all and a
// nested call cannot overwrite the arguments its parent is still
// filling.
func (p *vmProgram) assignFrame(c *vmCall) {
	c.off = p.frame
	p.frame += len(c.args)
	for _, a := range c.args {
		p.assignArg(a)
	}
}

// assignArg walks one argument for assignFrame: a nested call needs
// its own frame window whether it sits in an argument list, a struct
// element, or a field assignment's value.
func (p *vmProgram) assignArg(a *vmArg) {
	// An addressed argument pins the slot at its root: run stores the
	// slot's value through an addressable cell, and the step JIT
	// refuses to splice its producer or alias it behind an interface.
	if a.addrOf {
		root := a
		for root.kind == vaField {
			root = root.src
		}
		if root.kind == vaSlot {
			p.addrTaken[root.slot] = true
		}
	}
	for a.kind == vaField {
		a = a.src
	}
	switch a.kind {
	case vaCall:
		p.assignFrame(a.sub)
	case vaBinary, vaIndex:
		p.assignArg(a.x)
		if a.y != nil {
			p.assignArg(a.y)
		}
	case vaUnary, vaLen:
		p.assignArg(a.x)
	case vaStruct:
		for i := range a.elems {
			p.assignArg(a.elems[i].val)
		}
	case vaStack, vaDest:
		// Only a non-empty interface is worth pre-converting: for
		// an empty one reflect packs an eface directly and never
		// reaches implements.
		if a.typ.Kind() == reflect.Interface && a.typ.NumMethod() > 0 {
			if conv, ok := ifaceConvs[a.typ]; ok {
				a.conv = conv
				a.iface = p.nifaces
				p.nifaces++
			}
		}
	}
}

// compileFieldSet compiles req.Method = value. The base is a
// program-bound name, every selector is an exported field, and the
// value is a literal or a call whose result is assignable to the field.
func (c *Compiler) compileFieldSet(sc *cscope, s stmt) (*vmFieldSet, error) {
	base := s.fieldLhs[0]
	slot, ok := sc.slots[base]
	if !ok {
		return nil, fmt.Errorf("compile: %s is not a name bound by the program, so its fields cannot be assigned", base)
	}
	t := sc.env[base]
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
		if isExprKind(s.lit.kind) {
			node, vt, err := c.compileValueExpr(sc, *s.lit, t)
			if err != nil {
				return nil, err
			}
			if !vt.AssignableTo(t) {
				return nil, fmt.Errorf("compile: %s: cannot assign %s to %s", fs.field, sc.typeName(vt), sc.typeName(t))
			}
			fs.val = node
			return fs, nil
		}
		if s.lit.kind == argStruct {
			sa, st, err := c.compileStructLit(sc, *s.lit)
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
	call, rt, err := c.compileExpr(sc, s.call)
	if err != nil {
		return nil, err
	}
	if rt == nil || !rt.AssignableTo(t) {
		return nil, fmt.Errorf("compile: %s: cannot assign %s to %s", fs.field, rt, t)
	}
	fs.val = &vmArg{kind: vaCall, sub: call, typ: t, iface: -1}
	return fs, nil
}

// compileRetVal compiles the value of a "return x;" form. The
// parameter type it is compiled against is its own: a program-bound
// name uses its static type, a stack name has none and is returned as
// it is, a literal keeps its natural width.
func (c *Compiler) compileRetVal(sc *cscope, a arg) (*vmArg, error) {
	if isExprKind(a.kind) {
		node, _, err := c.compileValueExpr(sc, a, nil)
		return node, err
	}
	pt := reflect.TypeFor[any]()
	switch a.kind {
	case argVar:
		if t, ok := sc.env[a.str]; ok && t != nil {
			pt = t
		}
	case argString:
		pt = reflect.TypeFor[string]()
	case argInt:
		pt = reflect.TypeFor[int64]()
	case argFloat:
		pt = reflect.TypeFor[float64]()
	case argBool:
		pt = reflect.TypeFor[bool]()
	case argNil:
		return nil, fmt.Errorf("compile: return nil returns no value, use return;")
	case argPath, argStruct:
		// compileArg resolves these and checks assignability against
		// pt, so any is what lets the value keep its own type.
	}
	return c.compileArg(sc, "return", 0, pt, a)
}
