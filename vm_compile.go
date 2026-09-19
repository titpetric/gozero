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
	pc := &progCompiler{
		c:     c,
		p:     &vmProgram{addrTaken: map[int]bool{}},
		prog:  prog,
		slots: map[string]int{},
		env:   map[string]reflect.Type{},
	}

	// A name declared with var fixes its type before anything else is
	// compiled, so a literal assigned to it converts to that type. A
	// var inside an if arm is rejected when the arm compiles, so the
	// walk stays over the top-level list. The map is lazy: most
	// programs declare nothing and skip the allocation.
	for si := range prog.stmts {
		if s := prog.stmts[si]; s.varType != "" {
			if err := pc.checkName(s.varName); err != nil {
				return nil, err
			}
			t, ok := c.lookupType(s.varType)
			if !ok {
				return nil, fmt.Errorf("compile: unknown type %q, register it with BindType", s.varType)
			}
			if pc.declared == nil {
				pc.declared = map[string]reflect.Type{}
			}
			pc.declared[s.varName] = t
		}
	}

	p := pc.p
	if err := pc.compileStmts(prog.stmts, &p.stmts); err != nil {
		return nil, err
	}
	p.assignStmts(p.stmts)
	return p, nil
}

// progCompiler carries one compileProgram run: the program being
// built, the name-to-slot map and the static types, so an if arm
// compiles through exactly the code the top level does.
type progCompiler struct {
	c        *Compiler
	p        *vmProgram
	prog     *program
	slots    map[string]int
	env      map[string]reflect.Type
	declared map[string]reflect.Type
	// branch counts enclosing if arms. Declarations and returns are
	// rejected inside one: the slot model is flat, so a := would leak
	// its name past the closing brace with non-Go visibility, and a
	// return would need a second exit signal on both tiers. See
	// docs/design/conditions.md.
	branch int
}

// checkName rejects a name that collides with a keyword or with a
// binding's root, which path resolution would prefer, so the name
// could never be read back.
func (pc *progCompiler) checkName(name string) error {
	switch name {
	case "dest", "true", "false", "nil", "var", "return", "if", "else":
		return fmt.Errorf("compile: %s shadows a binding or keyword and cannot be assigned", name)
	}
	if pc.c.roots[name] {
		return fmt.Errorf("compile: %s shadows a binding or keyword and cannot be assigned", name)
	}
	return nil
}

// checkDecl enforces Go's declaration rule: := or var declares, a
// plain = assigns to a name that already exists. The rule holds for
// every statement kind: the initial prototype let a literal define
// its name with =, which left a typo one silent new name away from a
// stale read.
func (pc *progCompiler) checkDecl(name string, define bool) error {
	_, ok := pc.slots[name]
	if !ok && !define {
		return fmt.Errorf("compile: %s is not defined, use := or var", name)
	}
	return nil
}

// checkNew is the other half of the rule: := must declare something.
func (pc *progCompiler) checkNew(lhs []string) error {
	for _, name := range lhs {
		if _, ok := pc.slots[name]; !ok {
			return nil
		}
	}
	return fmt.Errorf("compile: no new variables on left side of :=")
}

func (pc *progCompiler) newSlot(name string, t reflect.Type) int {
	p := pc.p
	slot, ok := pc.slots[name]
	if !ok {
		slot = p.nslots
		p.nslots++
		p.slotTypes = append(p.slotTypes, nil)
		pc.slots[name] = slot
	}
	if prev := p.slotTypes[slot]; prev != nil && prev != t {
		p.polymorphic = true
	}
	p.slotTypes[slot] = t
	pc.env[name] = t
	return slot
}

// compileStmts compiles one statement list into dst: the program's
// own, or an if arm, which the branch counter restricts.
func (pc *progCompiler) compileStmts(list []stmt, dst *[]vmStmt) error {
	c, p, slots, env := pc.c, pc.p, pc.slots, pc.env
	checkName, checkDecl, checkNew, newSlot := pc.checkName, pc.checkDecl, pc.checkNew, pc.newSlot
	prog := pc.prog
	for si := range list {
		s := list[si]

		if pc.branch > 0 {
			// The arm restrictions, each a named rule of the rung.
			// Flat scope: a declaration inside an arm would leak its
			// name past the brace, so every name an arm writes is
			// declared before the if.
			if s.varType != "" {
				return fmt.Errorf("compile: a var inside an if arm is not allowed (flat scope), declare %s before the if", s.varName)
			}
			if s.define {
				return fmt.Errorf("compile: := inside an if arm would leak %s past the brace (flat scope), declare it before the if and assign with =", strings.Join(s.lhs, ", "))
			}
			// Single exit: the compiled form runs front to back and
			// the only early exit is an error.
			if s.ret || s.retVal != nil {
				return fmt.Errorf("compile: a return inside an if arm is not allowed (single exit), return after the if")
			}
		}

		if s.ifs != nil {
			if err := pc.compileIf(s.ifs, dst); err != nil {
				return err
			}
			continue
		}

		if s.varType != "" {
			t := pc.declared[s.varName]
			slot := newSlot(s.varName, t)
			p.inits = append(p.inits, slotInit{slot: slot, zero: reflect.Zero(t)})
			continue
		}

		if s.fieldLhs != nil {
			fs, err := c.compileFieldSet(slots, env, s)
			if err != nil {
				return err
			}
			*dst = append(*dst, vmStmt{fieldSet: fs})
			continue
		}
		if s.sendCh != nil {
			sn, err := c.compileSend(slots, env, s)
			if err != nil {
				return err
			}
			*dst = append(*dst, vmStmt{send: sn})
			continue
		}
		if s.lit != nil && s.lit.kind == argRecv {
			// The ok of Go's two-value receive is implicit, like the
			// trailing error of a call: a closed channel ends the
			// program with io.EOF, so there is no second name to bind.
			if len(s.lhs) > 1 {
				return fmt.Errorf("compile: a receive binds one name; ok is implicit, a closed channel ends the program with io.EOF")
			}
			for _, name := range s.lhs {
				if err := checkName(name); err != nil {
					return err
				}
				if err := checkDecl(name, s.define); err != nil {
					return err
				}
			}
			if s.define {
				if err := checkNew(s.lhs); err != nil {
					return err
				}
			}
			rv, err := c.compileRecv(slots, env, *s.lit)
			if err != nil {
				return err
			}
			var out []int
			if len(s.lhs) == 1 {
				out = []int{newSlot(s.lhs[0], rv.elem)}
			}
			*dst = append(*dst, vmStmt{recv: rv, out: out})
			continue
		}
		if s.lit != nil {
			if len(s.lhs) != 1 {
				return fmt.Errorf("compile: a literal assigns to exactly one name")
			}
			name := s.lhs[0]
			if err := checkName(name); err != nil {
				return err
			}
			if err := checkDecl(name, s.define); err != nil {
				return err
			}
			if s.define {
				if err := checkNew(s.lhs); err != nil {
					return err
				}
			}
			// u = url.URL{...} binds the name to the literal's own type,
			// built fresh on every run.
			if s.lit.kind == argStruct {
				sa, st, err := c.compileStructLit(slots, env, *s.lit)
				if err != nil {
					return fmt.Errorf("compile: %s: %w", name, err)
				}
				if prev, ok := env[name]; ok && prev != st {
					if !st.AssignableTo(prev) {
						return fmt.Errorf("compile: %s: cannot use %s as %s", name, st, prev)
					}
					st = prev
				}
				slot := newSlot(name, st)
				*dst = append(*dst, vmStmt{assign: sa, out: []int{slot}})
				continue
			}
			t, ok := env[name]
			if !ok {
				t = c.inferLiteralType(prog, name, *s.lit)
				if t == nil {
					return fmt.Errorf("compile: %s = nil needs a var declaration or a use to take a type from", name)
				}
			}
			v, err := literalValue(t, *s.lit)
			if err != nil {
				return fmt.Errorf("compile: %s: %w", name, err)
			}
			slot := newSlot(name, t)
			*dst = append(*dst, vmStmt{lit: v, out: []int{slot}})
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
					return fmt.Errorf("compile: a conversion assigns to exactly one name")
				}
				name := s.lhs[0]
				if err := checkName(name); err != nil {
					return err
				}
				if err := checkDecl(name, s.define); err != nil {
					return err
				}
				if s.define {
					if err := checkNew(s.lhs); err != nil {
						return err
					}
				}
				a, err := conversionArg(s.call)
				if err != nil {
					return fmt.Errorf("compile: %s = %s(...): %w", name, joinPath(s.call.path), err)
				}
				v, err := literalValue(t, a)
				if err != nil {
					return fmt.Errorf("compile: %s: %w", name, err)
				}
				// A declared type is not overridden by a hint. A hint
				// converting into an interface slot keeps the slot's
				// type, like any literal assigned to one.
				st := t
				if prev, ok := env[name]; ok && prev != t {
					if !t.AssignableTo(prev) {
						return fmt.Errorf("compile: %s: cannot use %s as %s", name, t, prev)
					}
					st = prev
				}
				slot := newSlot(name, st)
				*dst = append(*dst, vmStmt{lit: v, out: []int{slot}})
				continue
			}
		}

		if s.retVal != nil {
			ra, err := c.compileRetVal(slots, env, *s.retVal)
			if err != nil {
				return err
			}
			*dst = append(*dst, vmStmt{ret: true, retArg: ra})
			continue
		}
		if s.call == nil {
			// A bare "return;".
			*dst = append(*dst, vmStmt{ret: true})
			continue
		}
		call, _, err := c.compileExpr(slots, env, s.call)
		if err != nil {
			return err
		}
		if len(s.lhs) > call.nres {
			return fmt.Errorf("compile: %s returns %d values, cannot assign %d", call.name, call.nres, len(s.lhs))
		}

		if s.define && len(s.lhs) > 0 {
			if err := checkNew(s.lhs); err != nil {
				return err
			}
		}
		out := make([]int, 0, len(s.lhs))
		for i, name := range s.lhs {
			if err := checkName(name); err != nil {
				return err
			}
			if err := checkDecl(name, s.define); err != nil {
				return err
			}
			out = append(out, newSlot(name, c.resultType(call, i)))
		}
		*dst = append(*dst, vmStmt{call: call, out: out, ret: s.ret})
	}
	return nil
}

// assignStmts walks a statement list for the frame post-pass. An if
// statement descends into its condition and both arms, so a call
// anywhere in the tree gets its window.
func (p *vmProgram) assignStmts(stmts []vmStmt) {
	for i := range stmts {
		s := &stmts[i]
		if s.call != nil {
			p.assignFrame(s.call)
		}
		if s.assign != nil {
			p.assignArg(s.assign)
		}
		if s.fieldSet != nil {
			p.assignArg(s.fieldSet.val)
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
		if s.ifs != nil {
			p.assignArg(s.ifs.cond)
			p.assignStmts(s.ifs.then)
			p.assignStmts(s.ifs.els)
		}
	}
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
	for a.kind == vaField {
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
