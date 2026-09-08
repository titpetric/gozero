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
	p := &vmProgram{}
	slots := map[string]int{}
	env := map[string]reflect.Type{}

	// A name that collides with a binding can never be read back:
	// path resolution prefers the binding, so url := ... with url.Parse
	// bound compiles and then silently resolves the other way. Shadowing
	// is rejected instead.
	reserved := map[string]bool{"dest": true, "true": true, "false": true, "nil": true, "var": true, "return": true}
	for name := range c.bindings {
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

		if s.fieldLhs != nil {
			fs, err := c.compileFieldSet(slots, env, s)
			if err != nil {
				return nil, err
			}
			p.stmts = append(p.stmts, vmStmt{fieldSet: fs})
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

		out := make([]int, 0, len(s.lhs))
		for i, name := range s.lhs {
			if err := checkName(name); err != nil {
				return nil, err
			}
			if _, ok := slots[name]; !ok && !s.define {
				return nil, fmt.Errorf("compile: %s is not defined, use := or var", name)
			}
			out = append(out, newSlot(name, c.resultType(call, i)))
		}
		p.stmts = append(p.stmts, vmStmt{call: call, out: out, ret: s.ret})
	}

	for i := range p.stmts {
		if p.stmts[i].call != nil {
			p.assignFrame(p.stmts[i].call)
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
		for a.kind == vaField {
			a = a.src
		}
		switch a.kind {
		case vaCall:
			p.assignFrame(a.sub)
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
}

// inferLiteralType picks the type of a name a literal is assigned to,
// when no var statement fixed it. The first place the program passes
// the name to a binding decides, because that is the only type the
// value has to satisfy. Failing that the literal keeps the width the
// parser gave it.
func (c *Compiler) inferLiteralType(prog *program, name string, lit arg) reflect.Type {
	for si := range prog.stmts {
		if call := prog.stmts[si].call; call != nil {
			if t := c.useType(call, name); t != nil {
				return t
			}
		}
	}
	switch lit.kind {
	case argFloat:
		return reflect.TypeFor[float64]()
	case argString:
		return reflect.TypeFor[string]()
	case argBool:
		return reflect.TypeFor[bool]()
	case argNil:
		// nil alone names no type; the any it would infer to is never
		// what the program meant, so the caller reports it.
		return nil
	}
	return reflect.TypeFor[int64]()
}

// useType is the parameter type a call gives to name, looking through
// nested calls. Only a call whose whole path is a binding is
// considered: a method's receiver type may itself depend on a type not
// worked out yet.
func (c *Compiler) useType(e *callExpr, name string) reflect.Type {
	if b, ok := c.bindings[joinPath(e.path)]; ok && len(e.chain) == 0 {
		ft := b.rv.Type()
		fixed := ft.NumIn()
		if ft.IsVariadic() {
			fixed--
		}
		i := 0
		for _, a := range e.args {
			// Mirror compileCall: a context parameter consumes no
			// written argument.
			for i < fixed && ft.In(i) == ctxType {
				i++
			}
			var pt reflect.Type
			switch {
			case i < fixed:
				pt = ft.In(i)
			case ft.IsVariadic():
				pt = ft.In(fixed).Elem()
			default:
				return nil
			}
			if a.kind == argVar && a.str == name {
				// An empty interface accepts anything, so it says
				// nothing about what the name should be.
				if pt.Kind() == reflect.Interface && pt.NumMethod() == 0 {
					return nil
				}
				return pt
			}
			i++
		}
	}
	for _, a := range e.args {
		if a.kind == argCall {
			if t := c.useType(a.sub, name); t != nil {
				return t
			}
		}
	}
	for _, l := range e.chain {
		for _, a := range l.args {
			if a.kind == argCall {
				if t := c.useType(a.sub, name); t != nil {
					return t
				}
			}
		}
	}
	return nil
}

// staticType is the compile-time type of an argument when it has one:
// a program-bound name. Everything else answers nil, which for the
// context rule means auto-fill.
func (c *Compiler) staticType(env map[string]reflect.Type, a arg) reflect.Type {
	if a.kind == argVar {
		return env[a.str]
	}
	return nil
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

	if s.lit != nil {
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
	case argPath:
		// compileArg resolves the fields and checks assignability
		// against pt, so any is what lets the field keep its own type.
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
