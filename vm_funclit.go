package gozero

import (
	"context"
	"fmt"
	"reflect"
)

// Func literals in argument position. The literal's parameter types
// come from the func signature of the parameter it fills, so a literal
// in any other position is a compile error; the body is compiled as
// its own program with the parameters as typed slots. The body sees
// its parameters, the names it defines, the bindings, and the
// enclosing program's names, which it captures by cell: the captured
// slot's storage is one addressable cell on both sides, so the body
// observes the enclosing program's later writes and the enclosing
// program observes the body's, per Go semantics. A capture-free
// literal stays a compile-time constant; a capturing one closes over
// per-run storage and is built on every run, the construction the Go
// compiler emits for an escaping literal.

// vmFuncLit is a compiled func literal. Capture-free, the vmArg
// carrying it is a vaConst whose val is the reflect.MakeFunc
// materialization built once at compile time. Capturing, the vmArg is
// a vaFuncLit and the value binds per run over the cells capOuter
// names; the step JIT reads body and ft to build the direct closure
// instead in both cases.
type vmFuncLit struct {
	ft   reflect.Type // the signature the literal fills
	body *vmProgram   // the body, parameters as its leading slots
	name string       // the argument position, for diagnostics

	// capOuter are the enclosing program's captured slots, capLocal
	// the body slots that receive their cells, capTypes their static
	// types, all in capture order.
	capOuter []int
	capLocal []int
	capTypes []reflect.Type

	// plan is the validated MakeFunc materialization a capturing
	// literal binds per run; nil for a capture-free literal, whose
	// value was built at compile time.
	plan *matPlan
}

// vmParam is one parameter a func literal body is compiled with: the
// name written in the literal and the type the target signature gives
// it.
type vmParam struct {
	name string
	typ  reflect.Type
}

// declareParams seeds a body's parameters as its leading slots inside
// compileProgramWith, whose reserved set and slot allocator it
// borrows. The names follow the rules any program name does, plus
// their own: no repeats, and the returned set guards var statements
// from redeclaring one.
func declareParams(params []vmParam, reserved map[string]bool, p *vmProgram, newSlot func(string, reflect.Type) int) (map[string]bool, error) {
	if len(params) == 0 {
		// A top-level program pays nothing; reads of the nil set miss.
		return nil, nil
	}
	paramName := map[string]bool{}
	for _, prm := range params {
		if reserved[prm.name] {
			return nil, fmt.Errorf("compile: func literal: parameter %q shadows a binding or keyword and could not be read", prm.name)
		}
		if paramName[prm.name] {
			return nil, fmt.Errorf("compile: func literal: parameter %q repeats", prm.name)
		}
		paramName[prm.name] = true
		p.params = append(p.params, newSlot(prm.name, prm.typ))
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
//   - capture: an enclosing name the body reads or assigns is captured
//     by cell, resolved by funcLitCaptures before the body compiles; a
//     name that is neither local nor enclosing is still an error.
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
	errIdx, resIdx := -1, -1
	for i := 0; i < pt.NumOut(); i++ {
		if pt.Out(i) == errType {
			if i != pt.NumOut()-1 {
				return nil, fmt.Errorf("compile: %s: the error result of %s must be last", at, pt)
			}
			errIdx = i
			continue
		}
		if resIdx >= 0 {
			return nil, fmt.Errorf("compile: %s: %s has more than one result besides error, and a body returns one value", at, pt)
		}
		resIdx = i
	}
	_ = errIdx

	caps, err := c.funcLitCaptures(slots, env, fl)
	if err != nil {
		return nil, fmt.Errorf("compile: %s: %w", at, err)
	}

	params := make([]vmParam, len(fl.params))
	for i, pn := range fl.params {
		params[i] = vmParam{name: pn, typ: pt.In(i)}
	}
	body, err := c.compileProgramWith(params, caps, fl.body)
	if err != nil {
		return nil, err
	}
	// A captured cell holds one type for its whole life; a body that
	// reassigns the name at another type would silently detach from it.
	for i, slot := range body.capSlots {
		if body.slotTypes[slot] != caps[i].typ {
			return nil, fmt.Errorf("compile: %s: func literal: captured %s is reassigned from %s to %s, and a captured name keeps one type", at, caps[i].name, caps[i].typ, body.slotTypes[slot])
		}
	}

	returns := false
	for i := range body.stmts {
		s := &body.stmts[i]
		if s.retArg != nil || (s.ret && s.call != nil && s.call.nres > 0) {
			returns = true
		}
	}
	if returns && resIdx < 0 {
		return nil, fmt.Errorf("compile: %s: the body returns a value and %s has no value result", at, pt)
	}
	if !returns && resIdx >= 0 {
		return nil, fmt.Errorf("compile: %s: %s returns a value and the body never does", at, pt)
	}

	vf := &vmFuncLit{ft: pt, body: body, name: at, capLocal: body.capSlots}
	for _, cr := range caps {
		vf.capOuter = append(vf.capOuter, cr.slot)
		vf.capTypes = append(vf.capTypes, cr.typ)
	}
	if len(caps) == 0 {
		fv, err := materializeBody(pt, body)
		if err != nil {
			return nil, fmt.Errorf("compile: %s: %w", at, err)
		}
		return &vmArg{kind: vaConst, val: fv, typ: pt, iface: -1, funclit: vf}, nil
	}
	plan, err := newMatPlan(pt)
	if err != nil {
		return nil, fmt.Errorf("compile: %s: %w", at, err)
	}
	vf.plan = plan
	return &vmArg{kind: vaFuncLit, typ: pt, iface: -1, funclit: vf}, nil
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
