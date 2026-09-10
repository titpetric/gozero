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
	sc := &cscope{
		slots: map[string]int{},
		env:   map[string]reflect.Type{},
	}

	if err := c.buildNamespace(sc, prog); err != nil {
		return nil, err
	}

	// Declared types build after the namespace, because a field type
	// may name an imported package.
	if err := c.buildScriptTypes(sc, prog); err != nil {
		return nil, err
	}

	// A name that collides with a binding can never be read back:
	// path resolution prefers the binding, so url := ... with url.Parse
	// bound compiles and then silently resolves the other way. Shadowing
	// is rejected instead.
	reserved := map[string]bool{"dest": true, "true": true, "false": true, "nil": true, "var": true, "return": true}
	for name := range sc.bindings {
		if i := strings.IndexByte(name, '.'); i > 0 {
			reserved[name[:i]] = true
		} else {
			reserved[name] = true
		}
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
		_, ok := sc.slots[name]
		if !ok && !define {
			return fmt.Errorf("compile: %s is not defined, use := or var", name)
		}
		return nil
	}
	// The other half of the rule: := must declare something.
	checkNew := func(lhs []string) error {
		for _, name := range lhs {
			if _, ok := sc.slots[name]; !ok {
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
			t, ok := c.resolveType(sc, s.varType)
			if !ok {
				return nil, fmt.Errorf("compile: unknown type %q, register it with BindType", s.varType)
			}
			declared[s.varName] = t
		}
	}

	newSlot := func(name string, t reflect.Type) int {
		slot, ok := sc.slots[name]
		if !ok {
			slot = p.nslots
			p.nslots++
			p.slotTypes = append(p.slotTypes, nil)
			sc.slots[name] = slot
		}
		if prev := p.slotTypes[slot]; prev != nil && prev != t {
			p.polymorphic = true
		}
		p.slotTypes[slot] = t
		sc.env[name] = t
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

		if s.fieldLhs != nil {
			fs, err := c.compileFieldSet(sc, s)
			if err != nil {
				return nil, err
			}
			p.stmts = append(p.stmts, vmStmt{fieldSet: fs})
			continue
		}
		if s.sendCh != nil {
			sn, err := c.compileSend(sc, s)
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
			rv, err := c.compileRecv(sc, *s.lit)
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
			// An operator expression compiles to a per-run evaluation;
			// its type is its own unless the name already has one.
			if isExprKind(s.lit.kind) {
				node, t, err := c.compileValueExpr(sc, *s.lit, sc.env[name])
				if err != nil {
					return nil, err
				}
				st := t
				if prev, ok := sc.env[name]; ok && prev != t {
					if !t.AssignableTo(prev) {
						return nil, fmt.Errorf("compile: %s: cannot use %s as %s", name, sc.typeName(t), sc.typeName(prev))
					}
					st = prev
				}
				slot := newSlot(name, st)
				p.stmts = append(p.stmts, vmStmt{assign: node, out: []int{slot}})
				continue
			}
			// u = url.URL{...} binds the name to the literal's own type,
			// built fresh on every run.
			if s.lit.kind == argStruct {
				sa, st, err := c.compileStructLit(sc, *s.lit)
				if err != nil {
					return nil, fmt.Errorf("compile: %s: %w", name, err)
				}
				if prev, ok := sc.env[name]; ok && prev != st {
					if !st.AssignableTo(prev) {
						return nil, fmt.Errorf("compile: %s: cannot use %s as %s", name, sc.typeName(st), sc.typeName(prev))
					}
					st = prev
				}
				slot := newSlot(name, st)
				p.stmts = append(p.stmts, vmStmt{assign: sa, out: []int{slot}})
				continue
			}
			t, ok := sc.env[name]
			if !ok {
				t = c.inferLiteralType(sc, prog, name, *s.lit)
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

		// len(xs) in call position is the builtin, not a binding.
		if s.call != nil && c.isLenCall(sc, s.call) {
			node, t, err := c.compileLen(sc, s.call)
			if err != nil {
				return nil, err
			}
			if s.ret {
				p.stmts = append(p.stmts, vmStmt{ret: true, retArg: node})
				continue
			}
			if len(s.lhs) != 1 {
				return nil, fmt.Errorf("compile: len returns one value")
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
			slot := newSlot(name, t)
			p.stmts = append(p.stmts, vmStmt{assign: node, out: []int{slot}})
			continue
		}

		// x = int32(0) is a conversion hint. The right-hand side parses
		// as a call; a path that names a registered type rather than a
		// binding cannot be called, so it fixes the literal's type
		// instead, the way a var declaration would. The var form stays:
		// the hint only covers a name assigned a literal.
		if s.call != nil && !s.ret && len(s.lhs) > 0 {
			if t, ok := c.conversionType(sc, s.call); ok {
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
				if prev, ok := sc.env[name]; ok && prev != t {
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
			ra, err := c.compileRetVal(sc, *s.retVal)
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
		call, _, err := c.compileExpr(sc, s.call)
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
