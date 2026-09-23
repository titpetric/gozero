package gozero

import (
	"fmt"
	"reflect"
	"strings"
)

// The multi-statement VM. A program is a list of calls that bind their
// results to names; there are no operators, so every value is a
// literal, a name, or the result of another call.
//
// Two rules shape the compiled form:
//
// A trailing error result is never a value. The compiler strips it from
// the result list and checks it after every call, so
// "req := http.NewRequest(...)" binds one name to a two-result call and
// a failure returns from Exec or Scan at that statement. No program text
// mentions an error.
//
// Every argument is optional. A parameter the statement does not supply
// takes the zero value of its type; http.NewRequest with two arguments
// receives a nil body that way.
//
// Names the program binds carry a static type, so the compiler resolves
// methods on them against that type when the statement compiles.
// json.NewEncoder is a binding; Encode is not, and the program reaches
// it through the *json.Encoder the binding returns. Names coming from
// the caller's stack, including dest, are opaque and the VM checks them
// when it reads them.
// compileProgram builds a vmProgram. It tracks slots and their static
// types while it walks the statements, so the program must bind a name
// before using it and a method must exist on the receiver name's
// static type.
func (c *Compiler) compileProgram(prog *program) (*vmProgram, error) {
	p := &vmProgram{addrTaken: map[int]bool{}}
	slots := map[string]int{}
	env := map[string]reflect.Type{}

	// A program can never read back a name that collides with a binding:
	// path resolution prefers the binding, so url := ... with url.Parse
	// bound compiles and then silently resolves the other way. The
	// compiler rejects shadowing.
	reserved := map[string]bool{"dest": true, "true": true, "false": true, "nil": true, "var": true, "return": true}
	reserve := func(name string) {
		if i := strings.IndexByte(name, '.'); i > 0 {
			reserved[name[:i]] = true
		} else {
			reserved[name] = true
		}
	}
	for name := range c.bindings {
		reserve(name)
	}
	// Value bindings share the namespace with funcs: a program name
	// that shadowed os.Args could never read it back, the same reason
	// a func binding reserves its root.
	for name := range c.vars {
		reserve(name)
	}
	checkName := func(name string) error {
		if reserved[name] {
			return fmt.Errorf("compile: %s shadows a binding or keyword and cannot be assigned", name)
		}
		return nil
	}

	// Declaration is := or var; a plain = assigns to a name that already
	// exists. The rule is Go's and holds for every statement kind: the
	// initial prototype let a literal define its name with =, which left
	// a typo one silent new name away from a stale read.
	checkDecl := func(name string, define bool) error {
		_, ok := slots[name]
		if !ok && !define {
			return fmt.Errorf("compile: %s is not defined, use := or var", name)
		}
		return nil
	}
	// The other half of the rule: := must declare something.
	checkNew := func(lhs []string) error {
		for _, name := range lhs {
			if _, ok := slots[name]; !ok {
				return nil
			}
		}
		return fmt.Errorf("compile: no new variables on left side of :=")
	}

	// A name declared with var fixes its type before the compiler
	// reaches anything else, so a literal assigned to it converts to
	// that type.
	declared := map[string]reflect.Type{}
	for si := range prog.stmts {
		if s := prog.stmts[si]; s.varType != "" {
			if err := checkName(s.varName); err != nil {
				return nil, err
			}
			t, ok := c.lookupType(s.varType)
			if !ok {
				return nil, fmt.Errorf("compile: unknown type %q, register it with BindType", s.varType)
			}
			declared[s.varName] = t
		}
	}

	newSlot := func(name string, t reflect.Type) int {
		slot, ok := slots[name]
		if !ok {
			slot = p.nslots
			p.nslots++
			p.slotTypes = append(p.slotTypes, nil)
			slots[name] = slot
		}
		if prev := p.slotTypes[slot]; prev != nil && prev != t {
			p.polymorphic = true
		}
		p.slotTypes[slot] = t
		env[name] = t
		return slot
	}

	for si := range prog.stmts {
		s := prog.stmts[si]

		if s.varType != "" {
			t := declared[s.varName]
			slot := newSlot(s.varName, t)
			p.inits = append(p.inits, slotInit{slot: slot, zero: reflect.Zero(t)})
			continue
		}

		if s.derefLhs != nil {
			vs, err := c.compileDerefSet(slots, env, s.derefLhs, s)
			if err != nil {
				return nil, err
			}
			p.stmts = append(p.stmts, vmStmt{varSet: vs})
			continue
		}

		if s.fieldLhs != nil {
			// A dotted target is a value binding when a prefix of it
			// names one, and a field of a program name otherwise. The
			// binding wins, the same precedence path resolution uses
			// everywhere else.
			if _, _, ok := c.varOf(s.fieldLhs); ok {
				vs, err := c.compileVarSet(slots, env, s.fieldLhs, s)
				if err != nil {
					return nil, err
				}
				p.stmts = append(p.stmts, vmStmt{varSet: vs})
				continue
			}
			fs, err := c.compileFieldSet(slots, env, s)
			if err != nil {
				return nil, err
			}
			p.stmts = append(p.stmts, vmStmt{fieldSet: fs})
			continue
		}

		// A bare name on the left of "=" is a value binding when one
		// is registered under it; every other bare name is a program
		// slot and falls through.
		if len(s.lhs) == 1 && !s.define {
			if _, ok := c.vars[s.lhs[0]]; ok {
				vs, err := c.compileVarSet(slots, env, s.lhs[:1], s)
				if err != nil {
					return nil, err
				}
				p.stmts = append(p.stmts, vmStmt{varSet: vs})
				continue
			}
		}
		if s.lit != nil {
			if len(s.lhs) != 1 {
				return nil, fmt.Errorf("compile: a literal assigns to exactly one name")
			}
			name := s.lhs[0]
			if err := checkName(name); err != nil {
				return nil, err
			}
			if err := checkDecl(name, s.define); err != nil {
				return nil, err
			}
			if s.define {
				if err := checkNew(s.lhs); err != nil {
					return nil, err
				}
			}
			// u = url.URL{...} binds the name to the literal's own type,
			// built fresh on every run.
			if s.lit.kind == argStruct {
				sa, st, err := c.compileStructLit(slots, env, *s.lit)
				if err != nil {
					return nil, fmt.Errorf("compile: %s: %w", name, err)
				}
				if prev, ok := env[name]; ok && prev != st {
					if !st.AssignableTo(prev) {
						return nil, fmt.Errorf("compile: %s: cannot use %s as %s", name, st, prev)
					}
					st = prev
				}
				slot := newSlot(name, st)
				p.stmts = append(p.stmts, vmStmt{assign: sa, out: []int{slot}})
				continue
			}
			t, ok := env[name]
			if !ok {
				t = c.inferLiteralType(prog, name, *s.lit)
				if t == nil {
					return nil, fmt.Errorf("compile: %s = nil needs a var declaration or a use to take a type from", name)
				}
			}
			v, err := literalValue(t, *s.lit)
			if err != nil {
				return nil, fmt.Errorf("compile: %s: %w", name, err)
			}
			slot := newSlot(name, t)
			p.stmts = append(p.stmts, vmStmt{lit: v, out: []int{slot}})
			continue
		}

		// x = int32(0) is a conversion hint. The right-hand side parses
		// as a call; a path that names a registered type, and no
		// binding, is not callable, so it fixes the literal's type
		// the way a var declaration would. The var form stays:
		// the hint only covers a name assigned a literal.
		if s.call != nil && !s.ret && len(s.lhs) > 0 {
			if t, ok := c.conversionType(s.call); ok {
				if len(s.lhs) != 1 {
					return nil, fmt.Errorf("compile: a conversion assigns to exactly one name")
				}
				name := s.lhs[0]
				if err := checkName(name); err != nil {
					return nil, err
				}
				if err := checkDecl(name, s.define); err != nil {
					return nil, err
				}
				if s.define {
					if err := checkNew(s.lhs); err != nil {
						return nil, err
					}
				}
				a, err := conversionArg(s.call)
				if err != nil {
					return nil, fmt.Errorf("compile: %s = %s(...): %w", name, joinPath(s.call.path), err)
				}
				v, err := literalValue(t, a)
				if err != nil {
					return nil, fmt.Errorf("compile: %s: %w", name, err)
				}
				// A declared type is not overridden by a hint. A hint
				// converting into an interface slot keeps the slot's
				// type, like any literal assigned to one.
				st := t
				if prev, ok := env[name]; ok && prev != t {
					if !t.AssignableTo(prev) {
						return nil, fmt.Errorf("compile: %s: cannot use %s as %s", name, t, prev)
					}
					st = prev
				}
				slot := newSlot(name, st)
				p.stmts = append(p.stmts, vmStmt{lit: v, out: []int{slot}})
				continue
			}
		}

		if s.retVal != nil {
			ra, err := c.compileRetVal(slots, env, *s.retVal)
			if err != nil {
				return nil, err
			}
			p.stmts = append(p.stmts, vmStmt{ret: true, retArg: ra})
			continue
		}
		if s.call == nil {
			// A bare "return;".
			p.stmts = append(p.stmts, vmStmt{ret: true})
			continue
		}
		call, _, err := c.compileExpr(slots, env, s.call)
		if err != nil {
			return nil, err
		}
		if len(s.lhs) > call.nres {
			return nil, fmt.Errorf("compile: %s returns %d values, cannot assign %d", call.name, call.nres, len(s.lhs))
		}

		if s.define && len(s.lhs) > 0 {
			if err := checkNew(s.lhs); err != nil {
				return nil, err
			}
		}
		out := make([]int, 0, len(s.lhs))
		for i, name := range s.lhs {
			if err := checkName(name); err != nil {
				return nil, err
			}
			if err := checkDecl(name, s.define); err != nil {
				return nil, err
			}
			out = append(out, newSlot(name, callResultType(call, i)))
		}
		p.stmts = append(p.stmts, vmStmt{call: call, out: out, ret: s.ret})
	}

	// Addressed immutable bindings get their per-run cells before the
	// frame is laid out, because a cell is a slot and slots are what
	// the frame is made of. A runtime with no value bindings cannot
	// have produced one, and the walk is skipped rather than allocated
	// for: parse and compile are on a measured allocation budget.
	if len(c.vars) > 0 {
		roots := p.argRoots()
		p.markVarCells(roots)
		p.materializeVarCells(roots)
	}

	for i := range p.stmts {
		s := &p.stmts[i]
		if s.call != nil {
			p.assignFrame(s.call)
		}
		if s.assign != nil {
			p.assignArg(s.assign)
		}
		if s.fieldSet != nil {
			p.assignArg(s.fieldSet.val)
		}
		if s.varSet != nil {
			p.assignArg(s.varSet.val)
			if s.varSet.via != nil {
				p.assignArg(s.varSet.via)
			}
		}
		if s.retArg != nil {
			p.assignArg(s.retArg)
		}
	}
	return p, nil
}

// assignFrame assigns every call in the program a disjoint window into
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
	// neither splices its producer nor aliases it behind an interface.
	if a.addrOf {
		root := a
		for root.kind == vaField {
			root = root.src
		}
		if root.kind == vaSlot {
			p.addrTaken[root.slot] = true
		}
	}
	for a.kind == vaField || a.kind == vaDeref {
		a = a.src
	}
	switch a.kind {
	case vaCall:
		p.assignFrame(a.sub)
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

	val, err := c.assignedValue(slots, env, fs.field, t, s)
	if err != nil {
		return nil, err
	}
	fs.val = val
	return fs, nil
}

// compileRetVal compiles the value of a "return x;" form. The value
// compiles against its own type: a program-bound name uses its static
// type, a stack name has none and returns unchanged, a literal keeps
// its natural width.
func (c *Compiler) compileRetVal(slots map[string]int, env map[string]reflect.Type, a arg) (*vmArg, error) {
	pt := reflect.TypeFor[any]()
	switch a.kind {
	case argVar:
		if t, ok := env[a.str]; ok && t != nil {
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
	return c.compileArg(slots, env, "return", 0, pt, a)
}
