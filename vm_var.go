package gozero

import (
	"context"
	"fmt"
	"reflect"
)

// Value bindings in a compiled program. A name Bind registered with a
// value is read like any other argument. A program that writes the
// name writes its own per-run copy; writing the host's variable is
// what "*name = v" on a bound pointer does, which is Go's rule and
// needs nothing from this file.

// varArg resolves a dotted path against the value bindings and returns
// the argument that reads it, with the static type. ok is false when
// no prefix of the path names a value binding, which leaves the path
// to the other resolvers.
func (c *Compiler) varArg(path []string, addrOf bool) (a *vmArg, t reflect.Type, ok bool, err error) {
	vb, rest, ok := c.varOf(path)
	if !ok {
		return nil, nil, false, nil
	}
	t = vb.val.Type()
	// The root is named by the binding's own prefix, not the whole
	// path: the name is the key the per-run cell is found under, so
	// "pv" and "pv.N" have to agree on it or a method call and a field
	// read end up on different storage.
	a = &vmArg{kind: vaVar, name: joinPath(path[:len(path)-len(rest)]), varv: vb.val, typ: t, iface: -1}
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
	// &name addresses the program's per-run copy, which
	// materializeVarCells creates once the program is assembled. A
	// program may append to that copy, sort it or write through it,
	// and the host's variable keeps the header it had. Reaching the
	// host is what binding an address and writing "*name = v" does.
	a.addrOf = true
	a.typ = reflect.PointerTo(t)
	return a, a.typ, true, nil
}

// argRoots lists every argument the program evaluates, as the entry
// points of a walk: the passes below reach nested calls, struct
// elements and field chains from these.
func (p *vmProgram) argRoots() []*vmArg {
	var roots []*vmArg
	add := func(a *vmArg) {
		if a != nil {
			roots = append(roots, a)
		}
	}
	for i := range p.stmts {
		s := &p.stmts[i]
		if s.call != nil {
			roots = append(roots, s.call.args...)
		}
		add(s.assign)
		add(s.retArg)
		if s.fieldSet != nil {
			add(s.fieldSet.val)
		}
		if s.varSet != nil {
			add(s.varSet.val)
			add(s.varSet.via)
			add(s.varSet.dst)
		}
		if s.recv != nil {
			add(s.recv.ch)
		}
		if s.send != nil {
			add(s.send.ch)
			add(s.send.val)
		}
	}
	return roots
}

// materializeVarCells rewrites every addressed or assigned value
// binding into a per-run slot seeded from the bound value.
//
// The copy is per run rather than one copy the binding holds. A
// compiled program keeps no per-run state, so two concurrent Execs
// must not see each other's writes and a second run must not start
// from what the first one appended. A host that wants storage
// outliving the run binds Mutable(&v), which is what that is for.
//
// One cell per binding name per program, so two &os.Args address the
// same storage the way two &x do in Go.
func (p *vmProgram) materializeVarCells(args []*vmArg) {
	var walk func(a *vmArg)
	walk = func(a *vmArg) {
		if a == nil {
			return
		}
		switch a.kind {
		case vaField, vaDeref:
			walk(a.src)
		case vaStruct:
			for i := range a.elems {
				walk(a.elems[i].val)
			}
		case vaCall:
			for _, sub := range a.sub.args {
				walk(sub)
			}
		case vaVar:
			// A name the program only reads needs no storage of its
			// own: it reads the binding.
			if !p.varCells[a.name] {
				return
			}
			slot, ok := p.varSlots[a.name]
			if !ok {
				if p.varSlots == nil {
					p.varSlots = map[string]int{}
				}
				slot = p.nslots
				p.nslots++
				p.slotTypes = append(p.slotTypes, a.varv.Type())
				p.inits = append(p.inits, slotInit{slot: slot, zero: a.varv, seed: true})
				p.varSlots[a.name] = slot
			}
			p.addrTaken[slot] = true
			// Reads of the name come from the cell too, so a program
			// that appends through &name sees the longer slice when it
			// reads name afterwards.
			a.kind = vaSlot
			a.slot = slot
			a.varv = reflect.Value{}
		}
	}
	for _, a := range args {
		walk(a)
	}
}

// markVarCells records which binding names the program addresses or
// assigns anywhere, so every occurrence of one shares the cell rather
// than only the addressed occurrences moving into it. A write is a
// reason on its own: "os.Args = xs" needs the cell even when the
// program never writes &os.Args.
func (p *vmProgram) markVarCells(args []*vmArg) {
	for i := range p.stmts {
		vs := p.stmts[i].varSet
		if vs == nil || vs.dst == nil {
			continue
		}
		root := vs.dst
		for root.kind == vaField {
			root = root.src
		}
		if root.kind == vaVar {
			if p.varCells == nil {
				p.varCells = map[string]bool{}
			}
			p.varCells[root.name] = true
		}
	}
	p.markAddressed(args)
}

// markAddressed is the &name half of markVarCells.
func (p *vmProgram) markAddressed(args []*vmArg) {
	var walk func(a *vmArg, addressed bool)
	walk = func(a *vmArg, addressed bool) {
		if a == nil {
			return
		}
		addressed = addressed || a.addrOf
		switch a.kind {
		case vaField, vaDeref:
			walk(a.src, addressed)
		case vaStruct:
			for i := range a.elems {
				walk(a.elems[i].val, false)
			}
		case vaCall:
			for _, sub := range a.sub.args {
				walk(sub, false)
			}
		case vaVar:
			if addressed {
				if p.varCells == nil {
					p.varCells = map[string]bool{}
				}
				p.varCells[a.name] = true
			}
		}
	}
	for _, a := range args {
		walk(a, false)
	}
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
// "*p = v" where p is a pointer. Exactly one of the three destinations
// is set, and which one says where the write lands:
//
//   - dst, the program's per-run cell, for "name = v"
//   - via, the pointee of a pointer the program holds, for "*p = v"
type vmVarSet struct {
	val  *vmArg
	name string // for diagnostics
	// via is the pointer whose pointee is written, produced per run.
	via *vmArg
	// dst is the per-run cell of a value binding. It is a vaVar when
	// the program compiles and materializeVarCells rewrites it to the
	// cell's slot, the same rewrite every read of the name gets.
	dst *vmArg
}

func (vs *vmVarSet) apply(ctx context.Context, slots, frame []reflect.Value, ifaces []ifacePair, stack map[string]any, dest any) error {
	v, err := vs.val.get(ctx, slots, frame, ifaces, stack, dest)
	if err != nil {
		return err
	}
	var target reflect.Value
	if vs.dst != nil {
		d, err := vs.dst.get(ctx, slots, frame, ifaces, stack, dest)
		if err != nil {
			return err
		}
		target = d
	}
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
// binding. It writes the program's per-run cell, the same storage
// &name addresses, so the name shadows the binding for the run the
// way a local shadows an outer name in Go. A program reaches the
// host by writing through a bound pointer, "*name = v".
func (c *Compiler) compileVarSet(slots map[string]int, env map[string]reflect.Type, path []string, s stmt) (*vmVarSet, error) {
	vb, rest, ok := c.varOf(path)
	if !ok {
		return nil, fmt.Errorf("compile: %s is not a value binding", joinPath(path))
	}
	name := joinPath(path)
	// The target is built as the same vaVar chain a read of the name
	// compiles to, so materializeVarCells rewrites both to the one
	// cell and a read after the write sees what was written.
	dst := &vmArg{kind: vaVar, name: joinPath(path[:len(path)-len(rest)]), varv: vb.val, typ: vb.val.Type(), iface: -1}
	t := dst.typ
	for _, seg := range rest {
		f, deref, found := fieldOf(t, seg)
		if !found {
			return nil, fmt.Errorf("compile: %s has no field %s", t, seg)
		}
		dst = &vmArg{kind: vaField, src: dst, index: f.Index, deref: deref, typ: f.Type, iface: -1}
		t = f.Type
	}
	val, err := c.assignedValue(slots, env, name, t, s)
	if err != nil {
		return nil, err
	}
	return &vmVarSet{dst: dst, val: val, name: name}, nil
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
		case argSlice:
			sa, st, err := c.compileSliceLit(slots, env, *s.lit)
			if err != nil {
				return nil, fmt.Errorf("compile: %s: %w", name, err)
			}
			if !st.AssignableTo(t) {
				return nil, fmt.Errorf("compile: %s: cannot use %s as %s", name, st, t)
			}
			sa.typ = t
			return sa, nil
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
