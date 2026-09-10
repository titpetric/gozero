package gozero

import (
	"fmt"
	"reflect"
)

// Compiling functions: each declaration or literal is its own unit
// with its own slot space, and a literal captures across unit
// boundaries by cell. The scope chain carries the unit, so name
// resolution notices a crossing and registers the capture where it
// happens.

// fnCompile is the compile-time state of one unit.
type fnCompile struct {
	parent *fnCompile
	p      *vmProgram
	// caps holds the enclosing unit's slot per capture, in capture
	// order; capOf dedupes; capLocal is this unit's receiving slot.
	caps     []int
	capOf    map[int]int
	capLocal []int
	// results and errIdx describe the declared signature return
	// statements compile against.
	results []reflect.Type
	errIdx  int
	name    string
}

// capture registers that this unit reads slot of owner, capturing
// transitively through intermediate literals, and returns the local
// slot the cell arrives in. Both sides go address-taken: the owner's
// storage becomes a write-through cell, and the local slot writes
// through it.
func (f *fnCompile) capture(owner *fnCompile, slot int, t reflect.Type) int {
	if f.parent != owner {
		slot = f.parent.capture(owner, slot, t)
		owner = f.parent
	}
	if local, ok := f.capOf[slot]; ok {
		return local
	}
	if f.capOf == nil {
		f.capOf = map[int]int{}
	}
	owner.p.addrTaken[slot] = true
	local := f.p.nslots
	f.p.nslots++
	f.p.slotTypes = append(f.p.slotTypes, t)
	f.p.addrTaken[local] = true
	f.capOf[slot] = local
	f.caps = append(f.caps, slot)
	f.capLocal = append(f.capLocal, local)
	return local
}

// declareFuncs builds the signature of every declared function and
// method before any body compiles, so bodies call each other in any
// order, and rejects what the surface excludes.
func (pc *progCompiler) declareFuncs(decls []funcDecl, sc *cscope) error {
	c := pc.c
	for i := range decls {
		fd := &decls[i]
		if fd.name == "init" && fd.recv == nil {
			if len(fd.params) != 0 || len(fd.results) != 0 {
				return fmt.Errorf("compile: func init takes no parameters and returns nothing")
			}
			continue
		}
		fn := &scriptFn{name: fd.name, errIdx: -1}
		in := make([]reflect.Type, 0, len(fd.params)+1)
		if fd.recv != nil {
			rt, ok := c.resolveType(sc, fd.recv.typ)
			if !ok {
				return fmt.Errorf("compile: func %s: unknown receiver type %q", fd.name, fd.recv.typ)
			}
			base := rt
			if base.Kind() == reflect.Pointer {
				base = base.Elem()
			}
			if sc.script == nil || sc.script.names[base] == "" {
				return fmt.Errorf("compile: func %s: methods declare on the program's own types; %s is not one", fd.name, fd.recv.typ)
			}
			fn.recvT = rt
			in = append(in, rt)
		}
		for _, prm := range fd.params {
			t, ok := c.resolveType(sc, prm.typ)
			if !ok {
				return fmt.Errorf("compile: func %s: unknown parameter type %q", fd.name, prm.typ)
			}
			in = append(in, t)
		}
		out := make([]reflect.Type, 0, len(fd.results))
		for _, r := range fd.results {
			t, ok := c.resolveType(sc, r.typ)
			if !ok {
				return fmt.Errorf("compile: func %s: unknown result type %q", fd.name, r.typ)
			}
			out = append(out, t)
		}
		fn.results = out
		if n := len(out); n > 0 && out[n-1] == errType {
			fn.errIdx = n - 1
		}
		fn.sig = reflect.FuncOf(in, out, false)
		if fd.recv != nil {
			mt := sc.methods[fn.recvT]
			if mt == nil {
				mt = map[string]*scriptFn{}
				sc.methods[fn.recvT] = mt
			}
			if mt[fd.name] != nil {
				return fmt.Errorf("compile: method %s redeclared on %s", fd.name, fd.recv.typ)
			}
			mt[fd.name] = fn
			continue
		}
		if sc.funcs[fd.name] != nil {
			return fmt.Errorf("compile: func %s redeclared", fd.name)
		}
		sc.funcs[fd.name] = fn
	}
	return nil
}

// compileFuncs compiles every declared body against the signatures
// declareFuncs registered, init functions included, in declaration
// order.
func (pc *progCompiler) compileFuncs(decls []funcDecl, sc *cscope) error {
	for i := range decls {
		fd := &decls[i]
		if fd.name == "init" && fd.recv == nil {
			fn := &scriptFn{name: "init", errIdx: -1, sig: reflect.FuncOf(nil, nil, false)}
			if err := pc.compileFuncBody(sc, fd, fn); err != nil {
				return err
			}
			pc.p.scriptInits = append(pc.p.scriptInits, fn)
			continue
		}
		var fn *scriptFn
		if fd.recv != nil {
			rt, _ := pc.c.resolveType(sc, fd.recv.typ)
			fn = sc.methods[rt][fd.name]
		} else {
			fn = sc.funcs[fd.name]
		}
		if err := pc.compileFuncBody(sc, fd, fn); err != nil {
			return err
		}
	}
	return nil
}

// compileFuncBody compiles one body into its unit. The body's scope
// chains to the file scope, so it sees the imports, the types and
// the other functions; a literal chains to its lexical scope instead
// and captures.
func (pc *progCompiler) compileFuncBody(outer *cscope, fd *funcDecl, fn *scriptFn) error {
	unit := &vmProgram{addrTaken: map[int]bool{}}
	fu := &fnCompile{parent: outer.fn, p: unit, capOf: map[int]int{}, results: fn.results, errIdx: fn.errIdx, name: fn.name}
	fpc := &progCompiler{c: pc.c, p: unit, prog: pc.prog, reserved: pc.reserved, fn: fu}
	fsc := &cscope{
		slots:    map[string]int{},
		env:      map[string]reflect.Type{},
		script:   outer.script,
		parent:   outer,
		bindings: outer.bindings,
		pkgs:     outer.pkgs,
		funcs:    outer.funcs,
		methods:  outer.methods,
		fn:       fu,
		pc:       fpc,
	}

	sigIn := 0
	declare := func(prm *param) error {
		t := fn.sig.In(sigIn)
		sigIn++
		fn.paramNames = append(fn.paramNames, prm.name)
		if prm.name == "" || prm.name == "_" {
			// An unnamed parameter still owns a slot, so positions
			// stay aligned with the signature.
			slot := unit.nslots
			unit.nslots++
			unit.slotTypes = append(unit.slotTypes, t)
			fn.params = append(fn.params, slot)
			return nil
		}
		if err := fpc.checkName(prm.name); err != nil {
			return err
		}
		fn.params = append(fn.params, fpc.newSlot(fsc, prm.name, t, true))
		return nil
	}
	if fd.recv != nil {
		if err := declare(fd.recv); err != nil {
			return err
		}
	}
	for i := range fd.params {
		if err := declare(&fd.params[i]); err != nil {
			return err
		}
	}
	if err := fpc.compileBlock(fsc, fd.body.stmts, &unit.stmts); err != nil {
		return fmt.Errorf("func %s: %w", fn.name, err)
	}
	fpc.walkFrames(unit.stmts)
	fn.unit = unit
	fn.capLocal = fu.capLocal
	fn.capsOuter = fu.caps
	return nil
}

// compileFuncLit compiles a literal where a value of type want is
// expected. want may be a named func type, which the materialized
// value adopts, carrying that type's method set.
func (pc *progCompiler) compileFuncLit(sc *cscope, a arg, want reflect.Type) (*vmArg, reflect.Type, error) {
	fd := a.fn
	c := pc.c
	in := make([]reflect.Type, 0, len(fd.params))
	for _, prm := range fd.params {
		t, ok := c.resolveType(sc, prm.typ)
		if !ok {
			return nil, nil, fmt.Errorf("compile: func literal: unknown parameter type %q", prm.typ)
		}
		in = append(in, t)
	}
	out := make([]reflect.Type, 0, len(fd.results))
	for _, r := range fd.results {
		t, ok := c.resolveType(sc, r.typ)
		if !ok {
			return nil, nil, fmt.Errorf("compile: func literal: unknown result type %q", r.typ)
		}
		out = append(out, t)
	}
	sig := reflect.FuncOf(in, out, false)
	ft := sig
	if want != nil && want.Kind() == reflect.Func {
		if underlyingFunc(want) != sig {
			return nil, nil, fmt.Errorf("compile: func literal signature %s does not match %s", sig, sc.typeName(want))
		}
		// The literal adopts the wanted type, named or not.
		ft = want
	}
	fn := &scriptFn{name: "func literal", sig: sig, results: out, errIdx: -1}
	if n := len(out); n > 0 && out[n-1] == errType {
		fn.errIdx = n - 1
	}
	if err := pc.compileFuncBody(sc, fd, fn); err != nil {
		return nil, nil, err
	}
	node := &vmArg{kind: vaFuncLit, fnLit: fn, litType: ft, caps: fn.capsOuter, typ: ft, iface: -1}
	return node, ft, nil
}

// underlyingFunc is the structural func type behind a possibly named
// one.
func underlyingFunc(t reflect.Type) reflect.Type {
	in := make([]reflect.Type, t.NumIn())
	for i := range in {
		in[i] = t.In(i)
	}
	out := make([]reflect.Type, t.NumOut())
	for i := range out {
		out[i] = t.Out(i)
	}
	return reflect.FuncOf(in, out, t.IsVariadic())
}

// compileScriptCall compiles a call of a declared function or method
// with Go's strict arity; the trailing error follows the hybrid
// rule at the statement level, exactly as a binding's does.
func (c *Compiler) compileScriptCall(sc *cscope, fn *scriptFn, name string, recv *vmArg, src []arg) (*vmCall, error) {
	call := &vmCall{script: fn, name: name, errIdx: fn.errIdx}
	sigOff := 0
	if recv != nil {
		if !recv.typ.AssignableTo(fn.sig.In(0)) {
			return nil, fmt.Errorf("compile: %s: receiver is %s, want %s", name, sc.typeName(recv.typ), sc.typeName(fn.sig.In(0)))
		}
		call.args = append(call.args, recv)
		sigOff = 1
	}
	if len(src) != fn.sig.NumIn()-sigOff {
		return nil, fmt.Errorf("compile: %s takes %d arguments, got %d", name, fn.sig.NumIn()-sigOff, len(src))
	}
	for i, a := range src {
		if a.spread {
			return nil, fmt.Errorf("compile: %s: a script function is not variadic", name)
		}
		va, err := c.compileArg(sc, name, i, fn.sig.In(i+sigOff), a)
		if err != nil {
			return nil, err
		}
		call.args = append(call.args, va)
	}
	call.nres = len(fn.results)
	if call.errIdx >= 0 {
		call.nres--
	}
	return call, nil
}

// compileFuncReturn compiles a return inside a function body: the
// values check against the declared results positionally, a literal
// adopting its result's type, nil included.
func (pc *progCompiler) compileFuncReturn(sc *cscope, s stmt, dst *[]vmStmt) error {
	c := pc.c
	vals := s.rets
	if vals == nil {
		switch {
		case s.retVal != nil:
			vals = []arg{*s.retVal}
		case s.call != nil:
			vals = []arg{{kind: argCall, sub: s.call}}
		}
	}
	if len(vals) != len(pc.fn.results) {
		// One value standing for (T, error) is not Go; the arity is
		// the declaration's.
		return fmt.Errorf("compile: func %s returns %d values, got %d", pc.fn.name, len(pc.fn.results), len(vals))
	}
	if len(vals) == 0 {
		*dst = append(*dst, vmStmt{retList: []*vmArg{}})
		return nil
	}
	list := make([]*vmArg, len(vals))
	for i, a := range vals {
		va, err := c.compileArg(sc, "return", i, pc.fn.results[i], a)
		if err != nil {
			return err
		}
		list[i] = va
	}
	*dst = append(*dst, vmStmt{retList: list})
	return nil
}

// compileDynCall compiles a call of a func-typed value with Go's
// strict arity; the trailing error follows the hybrid rule.
func (c *Compiler) compileDynCall(sc *cscope, fnVal *vmArg, ft reflect.Type, name string, src []arg) (*vmCall, error) {
	call := &vmCall{dyn: fnVal, name: name, errIdx: -1}
	if ft.IsVariadic() {
		return nil, fmt.Errorf("compile: %s: a variadic func value is not callable yet", name)
	}
	if len(src) != ft.NumIn() {
		return nil, fmt.Errorf("compile: %s takes %d arguments, got %d", name, ft.NumIn(), len(src))
	}
	for i, a := range src {
		if a.spread {
			return nil, fmt.Errorf("compile: %s: ... spreads only into a variadic parameter", name)
		}
		va, err := c.compileArg(sc, name, i, ft.In(i), a)
		if err != nil {
			return nil, err
		}
		call.args = append(call.args, va)
	}
	call.nres = ft.NumOut()
	if n := ft.NumOut(); n > 0 && ft.Out(n-1) == errType {
		call.errIdx = n - 1
		call.nres--
	}
	return call, nil
}

// funcConversion reports a call-shaped expression that is a
// conversion to a func type the program can name: one argument, no
// chain, a path that is a type rather than a binding. The conversion
// carries a func literal or a func value into a named func type,
// which is how a handler-func adapter pattern spells.
func (c *Compiler) funcConversion(sc *cscope, e *callExpr) (reflect.Type, bool) {
	if len(e.chain) != 0 || len(e.args) != 1 || e.args[0].spread {
		return nil, false
	}
	name := joinPath(e.path)
	if _, bound := sc.bindings[name]; bound {
		return nil, false
	}
	t, ok := c.resolveType(sc, name)
	if !ok || t.Kind() != reflect.Func {
		return nil, false
	}
	return t, true
}

// scriptMethodOf resolves a declared method on t or its pointee.
func scriptMethodOf(sc *cscope, t reflect.Type, name string) *scriptFn {
	if sc.methods == nil {
		return nil
	}
	if fn := sc.methods[t][name]; fn != nil {
		return fn
	}
	if t.Kind() == reflect.Pointer {
		if fn := sc.methods[t.Elem()][name]; fn != nil {
			return fn
		}
		return nil
	}
	return sc.methods[reflect.PointerTo(t)][name]
}
