package gozero

import (
	"fmt"
	"reflect"
)

// compileIf compiles one if chain. The arms compile in the same slot
// namespace as the enclosing level: there is no block scope, and the
// declaration forms that would need one are rejected inside an arm,
// so every name an arm writes exists before the if.
func (pc *progCompiler) compileIf(is *ifStmt, dst *[]vmStmt) error {
	node := &vmIf{}
	if is.cmp != nil {
		cmp, err := pc.compileCmp(is.cmp)
		if err != nil {
			return err
		}
		node.cmp = cmp
	} else {
		cond, err := pc.compileCond(is.cond, "if condition")
		if err != nil {
			return err
		}
		node.cond = cond
	}
	var err error
	pc.branch++
	err = pc.compileStmts(is.then, &node.then)
	if err == nil {
		err = pc.compileStmts(is.els, &node.els)
	}
	pc.branch--
	if err != nil {
		return err
	}
	*dst = append(*dst, vmStmt{ifs: node})
	return nil
}

// compileCond compiles the condition operand of a header, an if's or
// a loop's, which is what the what parameter names in its errors.
// Three forms are admitted, all of kind bool: a name bound by the
// program, a field path on one, and a call. The check is on the kind
// rather than the exact type, which is Go's own rule for a
// condition.
func (pc *progCompiler) compileCond(a arg, what string) (*vmArg, error) {
	switch a.kind {
	case argVar:
		if a.str == "true" || a.str == "false" {
			return nil, fmt.Errorf("compile: a constant %s is not allowed (condition form), use a bool name, a bool field, or a call returning bool", what)
		}
		slot, ok := pc.slots[a.str]
		if !ok {
			return nil, fmt.Errorf("compile: the %s %s is not a name bound by the program (condition form)", what, a.str)
		}
		t := pc.env[a.str]
		if t == nil || t.Kind() != reflect.Bool {
			return nil, fmt.Errorf("compile: the %s must be bool, %s is %s", what, a.str, t)
		}
		return &vmArg{kind: vaSlot, slot: slot, name: a.str, typ: t, iface: -1}, nil

	case argPath:
		if _, ok := pc.slots[a.path[0]]; !ok {
			return nil, fmt.Errorf("compile: the %s %s is not a name bound by the program (condition form)", what, a.path[0])
		}
		cur, curType, err := pc.pathOperand(a.path)
		if err != nil {
			return nil, err
		}
		if curType.Kind() != reflect.Bool {
			return nil, fmt.Errorf("compile: the %s must be bool, %s is %s", what, joinPath(a.path), curType)
		}
		return cur, nil

	case argCall:
		call, rt, err := pc.c.compileExpr(pc.slots, pc.env, a.sub)
		if err != nil {
			return nil, err
		}
		if call.nres == 0 || rt == nil {
			return nil, fmt.Errorf("compile: the %s %s returns no value", what, call.name)
		}
		if rt.Kind() != reflect.Bool {
			return nil, fmt.Errorf("compile: the %s must be bool, %s returns %s", what, call.name, rt)
		}
		return &vmArg{kind: vaCall, sub: call, typ: rt, iface: -1}, nil
	}
	return nil, fmt.Errorf("compile: the %s is a bool name, a bool field, or a call returning bool (condition form)", what)
}
