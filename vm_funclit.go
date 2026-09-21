package gozero

import (
	"context"
	"fmt"
	"reflect"
)

// Func literals in argument position. The literal's parameter types
// come from the func signature of the parameter it fills, so a literal
// in any other position is a compile error; the body is compiled as
// its own program with the parameters as typed slots. The rung is
// capture-free: the body sees its parameters, the names it defines,
// and the bindings, nothing of the enclosing program or stack. That is
// what makes the value a compile-time constant on both tiers: with
// nothing captured, there is nothing per-run to close over.

// vmFuncLit is a compiled func literal. The vmArg carrying it is a
// vaConst whose val is the reflect.MakeFunc materialization; the step
// JIT reads body and ft to build the direct closure instead.
type vmFuncLit struct {
	ft   reflect.Type // the signature the literal fills
	body *vmProgram   // the body, parameters as its leading slots
	name string       // the argument position, for diagnostics
}

// vmParam is one parameter a func literal body is compiled with: the
// name written in the literal and the type the target signature gives
// it.
type vmParam struct {
	name string
	typ  reflect.Type
}

// declareParams seeds a body's parameters as its leading slots, using
// the slot allocator and the name rules compileProgramWith already
// applies to every name. The parameters add their own: no repeats, and
// the returned set guards var statements from redeclaring one.
func (pc *progCompiler) declareParams(params []vmParam) (map[string]bool, error) {
	if len(params) == 0 {
		// A top-level program pays nothing; reads of the nil set miss.
		return nil, nil
	}
	paramName := map[string]bool{}
	for _, prm := range params {
		if err := pc.checkName(prm.name); err != nil {
			return nil, fmt.Errorf("compile: func literal: parameter %q shadows a binding or keyword and could not be read", prm.name)
		}
		if paramName[prm.name] {
			return nil, fmt.Errorf("compile: func literal: parameter %q repeats", prm.name)
		}
		paramName[prm.name] = true
		pc.p.params = append(pc.p.params, pc.newSlot(prm.name, prm.typ))
	}
	return paramName, nil
}

// fieldSetFuncLit finishes a field assignment whose value is a func
// literal: the field's type is the signature, so the literal goes
// through compileArg like any argument.
func (c *Compiler) fieldSetFuncLit(slots map[string]int, env map[string]reflect.Type, fs *vmFieldSet, pt reflect.Type, a arg) (*vmFieldSet, error) {
	va, err := c.compileArg(slots, env, fs.field, 0, pt, a)
	if err != nil {
		return nil, err
	}
	fs.val = va
	return fs, nil
}

// compileFuncLit compiles func(a, b) { body } against the parameter
// type pt. Each rule below is a compile error that names itself:
//
//   - the target: pt must be a non-variadic func signature, or there
//     is nothing to infer the parameter types from;
//   - the arity: the literal names exactly the signature's parameters;
//   - the results: at most one value besides a trailing error, and the
//     body's returns must match;
//   - capture: the body reads no enclosing name, checked by
//     funcLitScope before the body compiles.
func (c *Compiler) compileFuncLit(slots map[string]int, env map[string]reflect.Type, name string, pos int, pt reflect.Type, a arg) (*vmArg, error) {
	fl := a.fn
	at := fmt.Sprintf("%s argument %d", name, pos+1)
	if pt == nil || pt.Kind() != reflect.Func {
		return nil, fmt.Errorf("compile: %s: a func literal fills only a func-typed parameter, and %s is not a func signature", at, pt)
	}
	if pt.IsVariadic() {
		return nil, fmt.Errorf("compile: %s: %s is variadic and a func literal has no parameter to pack the tail into", at, pt)
	}
	if pt.NumIn() != len(fl.params) {
		return nil, fmt.Errorf("compile: %s: the literal names %d parameters and %s has %d", at, len(fl.params), pt, pt.NumIn())
	}
	resIdx := -1
	for i := 0; i < pt.NumOut(); i++ {
		if pt.Out(i) == errType {
			if i != pt.NumOut()-1 {
				return nil, fmt.Errorf("compile: %s: the error result of %s must be last", at, pt)
			}
			continue
		}
		if resIdx >= 0 {
			return nil, fmt.Errorf("compile: %s: %s has more than one result besides error, and a body returns one value", at, pt)
		}
		resIdx = i
	}

	outer := make(map[string]bool, len(slots))
	for n := range slots {
		outer[n] = true
	}
	if err := c.funcLitScope(outer, fl); err != nil {
		return nil, fmt.Errorf("compile: %s: %w", at, err)
	}

	params := make([]vmParam, len(fl.params))
	for i, pn := range fl.params {
		params[i] = vmParam{name: pn, typ: pt.In(i)}
	}
	body, err := c.compileProgramWith(params, fl.body)
	if err != nil {
		return nil, err
	}

	returns := bodyReturns(body.stmts)
	if returns && resIdx < 0 {
		return nil, fmt.Errorf("compile: %s: the body returns a value and %s has no value result", at, pt)
	}
	if !returns && resIdx >= 0 {
		return nil, fmt.Errorf("compile: %s: %s returns a value and the body never does", at, pt)
	}

	fv, err := materializeBody(pt, body)
	if err != nil {
		return nil, fmt.Errorf("compile: %s: %w", at, err)
	}
	return &vmArg{kind: vaConst, val: fv, typ: pt, iface: -1, funclit: &vmFuncLit{ft: pt, body: body, name: at}}, nil
}

// bodyReturns reports whether any statement of the body returns a
// value. A bare "return;" does not: it ends the body, and a signature
// without a value result is satisfied by it. The walk descends into
// if arms, where a return may also stand.
func bodyReturns(stmts []vmStmt) bool {
	for i := range stmts {
		s := &stmts[i]
		if s.retArg != nil || (s.ret && s.call != nil && s.call.nres > 0) {
			return true
		}
		if s.ifs != nil && (bodyReturns(s.ifs.then) || bodyReturns(s.ifs.els)) {
			return true
		}
	}
	return false
}

// funcLitScope enforces the capture rule before the body compiles: the
// body reads its parameters, the names its own statements define, and
// the bindings. Any other name is rejected here, where the message can
// say whether the name lives in the enclosing scope, rather than
// compiling into the silent zero an unknown stack name reads as at the
// top level.
func (c *Compiler) funcLitScope(outer map[string]bool, fl *funcLit) error {
	defined := map[string]bool{}
	for _, p := range fl.params {
		defined[p] = true
	}
	resolve := func(n string) error {
		if defined[n] {
			return nil
		}
		if outer[n] {
			return fmt.Errorf("func literal: %s is a name of the enclosing program, and a func literal captures nothing", n)
		}
		return fmt.Errorf("func literal: %s is neither a parameter nor a name the body defines, and a func literal reads no enclosing names", n)
	}
	var walkArg func(a *arg) error
	var walkCall func(e *callExpr) error
	walkArg = func(a *arg) error {
		switch a.kind {
		case argVar:
			return resolve(a.str)
		case argPath:
			return resolve(a.path[0])
		case argCall:
			return walkCall(a.sub)
		case argStruct:
			// The path names a type, not a value; only the elements
			// read names.
			for i := range a.elems {
				if err := walkArg(&a.elems[i].val); err != nil {
					return err
				}
			}
		case argRecv:
			return walkArg(a.recv)
		case argFuncLit:
			// A nested literal's enclosing scope is this body.
			inner := make(map[string]bool, len(outer)+len(defined))
			for n := range outer {
				inner[n] = true
			}
			for n := range defined {
				inner[n] = true
			}
			return c.funcLitScope(inner, a.fn)
		}
		return nil
	}
	walkCall = func(e *callExpr) error {
		// The longest binding prefix wins, as in compileExpr; only when
		// no prefix names a binding is the head a name of the body.
		bound := false
		for i := len(e.path); i >= 1; i-- {
			if _, ok := c.bindings[joinPath(e.path[:i])]; ok {
				bound = true
				break
			}
		}
		if !bound {
			if err := resolve(e.path[0]); err != nil {
				return err
			}
		}
		for i := range e.args {
			if err := walkArg(&e.args[i]); err != nil {
				return err
			}
		}
		for _, l := range e.chain {
			for i := range l.args {
				if err := walkArg(&l.args[i]); err != nil {
					return err
				}
			}
		}
		return nil
	}
	// walkStmts covers the nested lists an if arm and a loop body
	// hold. A name a nested list defines is a name of the body: the
	// scope is flat, which is what the arm and the header rules in
	// the compiler already enforce.
	var walkStmts func(list []stmt) error
	walkStmts = func(list []stmt) error {
		for i := range list {
			s := &list[i]
			if s.varType != "" {
				defined[s.varName] = true
				continue
			}
			if s.ifs != nil {
				if s.ifs.cmp != nil {
					if err := walkArg(&s.ifs.cmp.lhs); err != nil {
						return err
					}
					if err := walkArg(&s.ifs.cmp.rhs); err != nil {
						return err
					}
				} else if err := walkArg(&s.ifs.cond); err != nil {
					return err
				}
				if err := walkStmts(s.ifs.then); err != nil {
					return err
				}
				if err := walkStmts(s.ifs.els); err != nil {
					return err
				}
				continue
			}
			if s.rng != nil {
				if err := walkArg(&s.rng.over); err != nil {
					return err
				}
				if s.rng.key != "" {
					defined[s.rng.key] = true
				}
				if s.rng.val != "" {
					defined[s.rng.val] = true
				}
				if err := walkStmts(s.rng.body); err != nil {
					return err
				}
				continue
			}
			if s.fors != nil {
				if s.fors.cond != nil {
					if err := walkArg(s.fors.cond); err != nil {
						return err
					}
				}
				if s.fors.initName != "" {
					if err := walkArg(&s.fors.initVal); err != nil {
						return err
					}
					defined[s.fors.initName] = true
				}
				if s.fors.cmp != nil {
					if err := walkArg(&s.fors.cmp.lhs); err != nil {
						return err
					}
					if err := walkArg(&s.fors.cmp.rhs); err != nil {
						return err
					}
				}
				if err := walkStmts(s.fors.body); err != nil {
					return err
				}
				continue
			}
			if s.incName != "" {
				if err := resolve(s.incName); err != nil {
					return err
				}
			}
			if s.fieldLhs != nil {
				if err := resolve(s.fieldLhs[0]); err != nil {
					return err
				}
			}
			if s.sendCh != nil {
				if err := resolve(s.sendCh[0]); err != nil {
					return err
				}
			}
			if s.call != nil {
				if err := walkCall(s.call); err != nil {
					return err
				}
			}
			if s.lit != nil {
				if err := walkArg(s.lit); err != nil {
					return err
				}
			}
			if s.retVal != nil {
				if err := walkArg(s.retVal); err != nil {
					return err
				}
			}
			if s.sendVal != nil {
				if err := walkArg(s.sendVal); err != nil {
					return err
				}
			}
			for _, n := range s.lhs {
				defined[n] = true
			}
		}
		return nil
	}
	return walkStmts(fl.body.stmts)
}

// materializeBody wraps a compiled body as a func value of the
// signature the literal fills. This is the reflect-tier
// materialization the design doc names: reflect.MakeFunc dispatches
// any signature and the body runs on the reflect evaluator, its
// parameters stored into their slots. Built once at compile time;
// capture-free, there is nothing per-run to close over. The step JIT
// replaces the whole value with an ordinary Go closure when it can.
func materializeBody(ft reflect.Type, body *vmProgram) (reflect.Value, error) {
	return materializeVia(ft, func(ctx context.Context, args []reflect.Value) (any, error) {
		return body.runWith(ctx, args, nil, nil)
	})
}
