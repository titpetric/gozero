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
	pc := &progCompiler{c: c, p: p, prog: prog, reserved: reserved}

	for si := range prog.stmts {
		if err := pc.compileOne(sc, prog.stmts[si], &p.stmts, true); err != nil {
			return nil, err
		}
	}
	pc.walkFrames(p.stmts)
	return p, nil
}

// compileBlock compiles a statement list into dst under sc.
func (pc *progCompiler) compileBlock(sc *cscope, in []stmt, dst *[]vmStmt) error {
	for i := range in {
		if err := pc.compileOne(sc, in[i], dst, false); err != nil {
			return err
		}
	}
	return nil
}

// compileOne compiles one statement into dst. top marks the
// program's own statement list, whose var declarations become the
// pre-run inits.
func (pc *progCompiler) compileOne(sc *cscope, s stmt, dst *[]vmStmt, top bool) error {
	c, p, prog := pc.c, pc.p, pc.prog
	_ = prog
	if s.ifs != nil || s.fors != nil {
		return pc.compileFlow(sc, s, dst)
	}
	if s.deferred {
		call, _, err := c.compileExpr(sc, s.call)
		if err != nil {
			return err
		}
		*dst = append(*dst, vmStmt{deferCall: call})
		return nil
	}
	if s.brk {
		if pc.loopDepth == 0 {
			return fmt.Errorf("compile: break is not in a loop")
		}
		*dst = append(*dst, vmStmt{brk: true})
		return nil
	}
	if s.cont {
		if pc.loopDepth == 0 {
			return fmt.Errorf("compile: continue is not in a loop")
		}
		*dst = append(*dst, vmStmt{cont: true})
		return nil
	}

	if s.varType != "" {
		if err := pc.checkName(s.varName); err != nil {
			return err
		}
		t, ok := c.resolveType(sc, s.varType)
		if !ok {
			return fmt.Errorf("compile: unknown type %q, register it with BindType", s.varType)
		}
		slot := pc.newSlot(sc, s.varName, t, true)
		if top {
			p.inits = append(p.inits, slotInit{slot: slot, zero: reflect.Zero(t)})
		} else {
			// A block-scoped var re-zeroes at its position on
			// every pass, matching Go.
			*dst = append(*dst, vmStmt{init: &slotInit{slot: slot, zero: reflect.Zero(t)}})
		}
		return nil
	}

	if s.fieldLhs != nil {
		fs, err := c.compileFieldSet(sc, s)
		if err != nil {
			return err
		}
		*dst = append(*dst, vmStmt{fieldSet: fs})
		return nil
	}
	if s.sendCh != nil {
		sn, err := c.compileSend(sc, s)
		if err != nil {
			return err
		}
		*dst = append(*dst, vmStmt{send: sn})
		return nil
	}
	if s.lit != nil && s.lit.kind == argRecv {
		// The ok of Go's two-value receive is implicit, like the
		// trailing error of a call: a closed channel ends the
		// program with io.EOF, so there is no second name to bind.
		if len(s.lhs) > 1 {
			return fmt.Errorf("compile: a receive binds one name; ok is implicit, a closed channel ends the program with io.EOF")
		}
		for _, name := range s.lhs {
			if err := pc.checkName(name); err != nil {
				return err
			}
			if err := pc.checkDecl(sc, name, s.define); err != nil {
				return err
			}
		}
		if s.define {
			if err := pc.checkNew(s.lhs, sc); err != nil {
				return err
			}
		}
		rv, err := c.compileRecv(sc, *s.lit)
		if err != nil {
			return err
		}
		var out []int
		if len(s.lhs) == 1 {
			out = []int{pc.newSlot(sc, s.lhs[0], rv.elem, s.define)}
		}
		*dst = append(*dst, vmStmt{recv: rv, out: out})
		return nil
	}
	if s.lit != nil {
		if len(s.lhs) != 1 {
			return fmt.Errorf("compile: a literal assigns to exactly one name")
		}
		name := s.lhs[0]
		if err := pc.checkName(name); err != nil {
			return err
		}
		if err := pc.checkDecl(sc, name, s.define); err != nil {
			return err
		}
		if s.define {
			if err := pc.checkNew(s.lhs, sc); err != nil {
				return err
			}
		}
		// An operator expression compiles to a per-run evaluation;
		// its type is its own unless the name already has one.
		if isExprKind(s.lit.kind) {
			node, t, err := c.compileValueExpr(sc, *s.lit, sc.env[name])
			if err != nil {
				return err
			}
			st := t
			if prev, ok := sc.env[name]; ok && prev != t {
				if !t.AssignableTo(prev) {
					return fmt.Errorf("compile: %s: cannot use %s as %s", name, sc.typeName(t), sc.typeName(prev))
				}
				st = prev
			}
			slot := pc.newSlot(sc, name, st, s.define)
			*dst = append(*dst, vmStmt{assign: node, out: []int{slot}})
			return nil
		}
		// u = url.URL{...} binds the name to the literal's own type,
		// built fresh on every run.
		if s.lit.kind == argStruct {
			sa, st, err := c.compileStructLit(sc, *s.lit)
			if err != nil {
				return fmt.Errorf("compile: %s: %w", name, err)
			}
			if prev, ok := sc.env[name]; ok && prev != st {
				if !st.AssignableTo(prev) {
					return fmt.Errorf("compile: %s: cannot use %s as %s", name, sc.typeName(st), sc.typeName(prev))
				}
				st = prev
			}
			slot := pc.newSlot(sc, name, st, s.define)
			*dst = append(*dst, vmStmt{assign: sa, out: []int{slot}})
			return nil
		}
		t, ok := sc.env[name]
		if !ok {
			t = c.inferLiteralType(sc, prog, name, *s.lit)
			if t == nil {
				return fmt.Errorf("compile: %s = nil needs a var declaration or a use to take a type from", name)
			}
		}
		v, err := literalValue(t, *s.lit)
		if err != nil {
			return fmt.Errorf("compile: %s: %w", name, err)
		}
		slot := pc.newSlot(sc, name, t, s.define)
		*dst = append(*dst, vmStmt{lit: v, out: []int{slot}})
		return nil
	}

	// len(xs) in call position is the builtin, not a binding.
	if s.call != nil && c.isLenCall(sc, s.call) {
		node, t, err := c.compileLen(sc, s.call)
		if err != nil {
			return err
		}
		if s.ret {
			*dst = append(*dst, vmStmt{ret: true, retArg: node})
			return nil
		}
		if len(s.lhs) != 1 {
			return fmt.Errorf("compile: len returns one value")
		}
		name := s.lhs[0]
		if err := pc.checkName(name); err != nil {
			return err
		}
		if err := pc.checkDecl(sc, name, s.define); err != nil {
			return err
		}
		if s.define {
			if err := pc.checkNew(s.lhs, sc); err != nil {
				return err
			}
		}
		slot := pc.newSlot(sc, name, t, s.define)
		*dst = append(*dst, vmStmt{assign: node, out: []int{slot}})
		return nil
	}

	// x = int32(0) is a conversion hint. The right-hand side parses
	// as a call; a path that names a registered type rather than a
	// binding cannot be called, so it fixes the literal's type
	// instead, the way a var declaration would. The var form stays:
	// the hint only covers a name assigned a literal.
	if s.call != nil && !s.ret && len(s.lhs) > 0 {
		if t, ok := c.conversionType(sc, s.call); ok {
			if len(s.lhs) != 1 {
				return fmt.Errorf("compile: a conversion assigns to exactly one name")
			}
			name := s.lhs[0]
			if err := pc.checkName(name); err != nil {
				return err
			}
			if err := pc.checkDecl(sc, name, s.define); err != nil {
				return err
			}
			if s.define {
				if err := pc.checkNew(s.lhs, sc); err != nil {
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
			if prev, ok := sc.env[name]; ok && prev != t {
				if !t.AssignableTo(prev) {
					return fmt.Errorf("compile: %s: cannot use %s as %s", name, t, prev)
				}
				st = prev
			}
			slot := pc.newSlot(sc, name, st, s.define)
			*dst = append(*dst, vmStmt{lit: v, out: []int{slot}})
			return nil
		}
	}

	if s.retVal != nil {
		ra, err := c.compileRetVal(sc, *s.retVal)
		if err != nil {
			return err
		}
		*dst = append(*dst, vmStmt{ret: true, retArg: ra})
		return nil
	}
	if s.call == nil {
		// A bare "return;".
		*dst = append(*dst, vmStmt{ret: true})
		return nil
	}
	call, _, err := c.compileExpr(sc, s.call)
	if err != nil {
		return err
	}
	if len(s.lhs) > call.nres {
		return fmt.Errorf("compile: %s returns %d values, cannot assign %d", call.name, call.nres, len(s.lhs))
	}

	if s.define && len(s.lhs) > 0 {
		if err := pc.checkNew(s.lhs, sc); err != nil {
			return err
		}
	}
	out := make([]int, 0, len(s.lhs))
	for i, name := range s.lhs {
		if err := pc.checkName(name); err != nil {
			return err
		}
		if err := pc.checkDecl(sc, name, s.define); err != nil {
			return err
		}
		out = append(out, pc.newSlot(sc, name, c.resultType(call, i), s.define))
	}
	*dst = append(*dst, vmStmt{call: call, out: out, ret: s.ret})
	return nil
}

// walkFrames gives every call in the compiled tree its frame window,
// recursing through the control-flow blocks.
func (pc *progCompiler) walkFrames(stmts []vmStmt) {
	p := pc.p
	{
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
			if s.deferCall != nil {
				p.assignFrame(s.deferCall)
			}
			if s.ifs != nil {
				p.assignArg(s.ifs.cond)
				pc.walkFrames(s.ifs.then.stmts)
				if s.ifs.els != nil {
					pc.walkFrames(s.ifs.els.stmts)
				}
			}
			if s.loop != nil {
				pc.walkFrames(s.loop.init)
				if s.loop.cond != nil {
					p.assignArg(s.loop.cond)
				}
				pc.walkFrames(s.loop.post)
				pc.walkFrames(s.loop.body.stmts)
			}
			if s.rng != nil {
				p.assignArg(s.rng.over)
				pc.walkFrames(s.rng.body.stmts)
			}
		}
	}
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
