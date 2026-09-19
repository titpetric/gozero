package gozero

import (
	"fmt"
	"reflect"
)

// compileIf compiles one if chain. The arms compile in the same slot
// namespace as the enclosing level: the rung has no block scope, and
// the declaration forms that would need one are rejected inside an
// arm, so every name an arm writes exists before the if.
func (pc *progCompiler) compileIf(is *ifStmt, dst *[]vmStmt) error {
	cond, err := pc.compileCond(is.cond)
	if err != nil {
		return err
	}
	node := &vmIf{cond: cond}
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

// compileCond compiles the condition operand. The rung admits three
// forms, all of kind bool: a name bound by the program, a field path
// on one, and a call. The check is on the kind rather than the exact
// type, which is Go's own rule for an if condition.
func (pc *progCompiler) compileCond(a arg) (*vmArg, error) {
	switch a.kind {
	case argVar:
		if a.str == "true" || a.str == "false" {
			return nil, fmt.Errorf("compile: a constant if condition is not allowed (condition form), use a bool name, a bool field, or a call returning bool")
		}
		slot, ok := pc.slots[a.str]
		if !ok {
			return nil, fmt.Errorf("compile: the if condition %s is not a name bound by the program (condition form)", a.str)
		}
		t := pc.env[a.str]
		if t == nil || t.Kind() != reflect.Bool {
			return nil, fmt.Errorf("compile: the if condition must be bool, %s is %s", a.str, t)
		}
		return &vmArg{kind: vaSlot, slot: slot, name: a.str, typ: t, iface: -1}, nil

	case argPath:
		slot, ok := pc.slots[a.path[0]]
		if !ok {
			return nil, fmt.Errorf("compile: the if condition %s is not a name bound by the program (condition form)", a.path[0])
		}
		curType := pc.env[a.path[0]]
		cur := &vmArg{kind: vaSlot, slot: slot, name: a.path[0], typ: curType, iface: -1}
		for _, seg := range a.path[1:] {
			f, deref, ok := fieldOf(curType, seg)
			if !ok {
				return nil, fmt.Errorf("compile: %s has no field %s", curType, seg)
			}
			cur = &vmArg{kind: vaField, src: cur, index: f.Index, deref: deref, typ: f.Type, iface: -1}
			curType = f.Type
		}
		if curType.Kind() != reflect.Bool {
			return nil, fmt.Errorf("compile: the if condition must be bool, %s is %s", joinPath(a.path), curType)
		}
		return cur, nil

	case argCall:
		call, rt, err := pc.c.compileExpr(pc.slots, pc.env, a.sub)
		if err != nil {
			return nil, err
		}
		if call.nres == 0 || rt == nil {
			return nil, fmt.Errorf("compile: the if condition %s returns no value", call.name)
		}
		if rt.Kind() != reflect.Bool {
			return nil, fmt.Errorf("compile: the if condition must be bool, %s returns %s", call.name, rt)
		}
		return &vmArg{kind: vaCall, sub: call, typ: rt, iface: -1}, nil
	}
	return nil, fmt.Errorf("compile: the if condition is a bool name, a bool field, or a call returning bool (condition form)")
}
