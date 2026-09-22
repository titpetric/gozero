package gozero

import (
	"fmt"
	"reflect"
	"strings"
)

// The multi-statement VM. A program is a list of calls whose results
// are bound to names; there are no operators, so every value is a
// literal, a name, or the result of another call.
//
// Two rules shape the compiled form:
//
// A trailing error result is never a value. It is stripped from the
// result list at compile time and checked after every call, so
// "req := http.NewRequest(...)" binds one name to a two-result call and
// a failure returns from Exec or Scan on the spot. No program text
// mentions an error.
//
// Every argument is optional. A parameter the statement does not supply
// is filled with the zero value of its type, which is how
// http.NewRequest is called with two arguments and a nil body.
//
// Names bound by the program carry a static type, so methods on them
// are resolved against that type when the statement compiles.
// json.NewEncoder is bound; Encode is not, and is reached through the
// *json.Encoder the binding returns. Names coming from the caller's
// stack, including dest, are opaque and are checked when they are read.
// compileProgram builds a vmProgram. Slots and their static types are
// tracked as the statements are walked, so a name must be bound before
// it is used and a method must exist on the type of the name it is
// called on.
func (c *Compiler) compileProgram(prog *program) (*vmProgram, error) {
	p := &vmProgram{addrTaken: map[int]bool{}}
	slots := map[string]int{}
	env := map[string]reflect.Type{}

	// A name that collides with a binding can never be read back:
	// path resolution prefers the binding, so url := ... with url.Parse
	// bound compiles and then silently resolves the other way. Shadowing
	// is rejected instead.
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

	// A name declared with var fixes its type before anything else is
	// compiled, so a literal assigned to it converts to that type.
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
		if s.sendCh != nil {
			sn, err := c.compileSend(slots, env, s)
			if err != nil {
				return nil, err
			}
			p.stmts = append(p.stmts, vmStmt{send: sn})
			continue
		}
		if s.lit != nil && s.lit.kind == argRecv {
			// The ok of Go's two-value receive is implicit, like the
			// trailing error of a call: a closed channel ends the
			// program with io.EOF, so there is no second name to bind.
			if len(s.lhs) > 1 {
				return nil, fmt.Errorf("compile: a receive binds one name; ok is implicit, a closed channel ends the program with io.EOF")
			}
			for _, name := range s.lhs {
				if err := checkName(name); err != nil {
					return nil, err
				}
				if err := checkDecl(name, s.define); err != nil {
					return nil, err
				}
			}
			if s.define {
				if err := checkNew(s.lhs); err != nil {
					return nil, err
				}
			}
			rv, err := c.compileRecv(slots, env, *s.lit)
			if err != nil {
				return nil, err
			}
			var out []int
			if len(s.lhs) == 1 {
				out = []int{newSlot(s.lhs[0], rv.elem)}
			}
			p.stmts = append(p.stmts, vmStmt{recv: rv, out: out})
			continue
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
		// as a call; a path that names a registered type rather than a
		// binding cannot be called, so it fixes the literal's type
		// instead, the way a var declaration would. The var form stays:
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
			out = append(out, newSlot(name, c.resultType(call, i)))
		}
		p.stmts = append(p.stmts, vmStmt{call: call, out: out, ret: s.ret})
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
		if s.recv != nil {
			p.assignArg(s.recv.ch)
		}
		if s.send != nil {
			p.assignArg(s.send.ch)
			p.assignArg(s.send.val)
		}
	}
	return p, nil
}

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

// compileRetVal compiles the value of a "return x;" form. The
// parameter type it is compiled against is its own: a program-bound
// name uses its static type, a stack name has none and is returned as
// it is, a literal keeps its natural width.
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

// resultType is the static type of the i'th non-error result.
func (c *Compiler) resultType(call *vmCall, i int) reflect.Type {
	ft := call.fn.Type()
	n := 0
	for j := 0; j < ft.NumOut(); j++ {
		if j == call.errIdx {
			continue
		}
		if n == i {
			return ft.Out(j)
		}
		n++
	}
	return nil
}
