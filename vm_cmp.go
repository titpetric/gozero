package gozero

import (
	"context"
	"fmt"
	"reflect"
)

// Comparisons on the reflect tier: ==, !=, <, <=, > and >= between
// two operands, inside a condition header. The typing rule is one
// line: both sides carry identical static types, with a literal side
// adopting the other side's type. The comparison itself runs on the
// underlying kind, which is what lets a named scalar type such as
// time.Duration order against time.Hour like the int64 it is.
//
// The file is the comparison kit every header form shares: the
// compiled node and its evaluator, the class table that closes the
// operand set, and the operand typing a header reads its two sides
// with. stepjit_cmp.go is the same kit on the direct tier.

// cmpClass names the machine comparison a kind runs.
type cmpClass int

const (
	cmpInt   cmpClass = iota // sign-extended integer kinds
	cmpUint                  // zero-extended integer kinds
	cmpFloat
	cmpStr
	cmpBool // == and != only; bool does not order
)

// vmCmp is a compiled header comparison.
type vmCmp struct {
	op       string
	lhs, rhs *vmArg
	typ      reflect.Type // the shared static type of both sides
	class    cmpClass
}

// test resolves both operands and compares them, which is the whole
// of a header's answer: an if picks its arm with it and a
// three-clause loop decides whether to run another iteration.
func (c *vmCmp) test(ctx context.Context, slots, frame []reflect.Value, ifaces []ifacePair, stack map[string]any, dest any) (bool, error) {
	lv, err := c.lhs.get(ctx, slots, frame, ifaces, stack, dest)
	if err != nil {
		return false, err
	}
	rv, err := c.rhs.get(ctx, slots, frame, ifaces, stack, dest)
	if err != nil {
		return false, err
	}
	return c.eval(lv, rv), nil
}

// eval runs the comparison over two resolved operands.
func (c *vmCmp) eval(lv, rv reflect.Value) bool {
	switch c.class {
	case cmpInt:
		return cmpOrdered(c.op, lv.Int(), rv.Int())
	case cmpUint:
		return cmpOrdered(c.op, lv.Uint(), rv.Uint())
	case cmpFloat:
		return cmpOrdered(c.op, lv.Float(), rv.Float())
	case cmpStr:
		return cmpOrdered(c.op, lv.String(), rv.String())
	case cmpBool:
		if c.op == "==" {
			return lv.Bool() == rv.Bool()
		}
		return lv.Bool() != rv.Bool()
	}
	return false
}

// cmpOrdered is one comparison over an ordered scalar.
func cmpOrdered[T int64 | uint64 | float64 | string](op string, a, b T) bool {
	switch op {
	case "==":
		return a == b
	case "!=":
		return a != b
	case "<":
		return a < b
	case "<=":
		return a <= b
	case ">":
		return a > b
	}
	return a >= b
}

// cmpClassOf admits a kind to the rung and picks its machine
// comparison. Ordering exists for numbers and strings, Go's own
// rule; equality adds bool. Everything else, pointers and
// interfaces and structs among it, is out of the rung.
func cmpClassOf(k reflect.Kind, op string) (cmpClass, bool) {
	ordered := op != "==" && op != "!="
	switch k {
	case reflect.Bool:
		if ordered {
			return 0, false
		}
		return cmpBool, true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return cmpInt, true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return cmpUint, true
	case reflect.Float32, reflect.Float64:
		return cmpFloat, true
	case reflect.String:
		return cmpStr, true
	}
	return 0, false
}

// compileCmp compiles one header comparison.
func (pc *progCompiler) compileCmp(ce *cmpExpr) (*vmCmp, error) {
	la, lt, err := pc.cmpOperand(ce.lhs)
	if err != nil {
		return nil, err
	}
	ra, rt, err := pc.cmpOperand(ce.rhs)
	if err != nil {
		return nil, err
	}
	switch {
	case lt == nil && rt == nil:
		return nil, fmt.Errorf("compile: both sides of %s are literals (constant comparison)", ce.op)
	case lt == nil:
		if la, err = cmpLiteral(rt, ce.lhs); err != nil {
			return nil, err
		}
		lt = rt
	case rt == nil:
		if ra, err = cmpLiteral(lt, ce.rhs); err != nil {
			return nil, err
		}
	default:
		if lt != rt {
			return nil, fmt.Errorf("compile: mismatched types %s and %s in a header comparison (identical types)", lt, rt)
		}
	}
	class, ok := cmpClassOf(lt.Kind(), ce.op)
	if !ok {
		return nil, fmt.Errorf("compile: %s does not compare %s values in this rung (comparable scalar)", ce.op, lt)
	}
	return &vmCmp{op: ce.op, lhs: la, rhs: ra, typ: lt, class: class}, nil
}

// cmpOperand compiles one side of a comparison: a program-bound
// name, a field path on one, a bound value, or a call. A literal
// returns nil arg and nil type; it adopts the other side's type once
// that side is compiled.
func (pc *progCompiler) cmpOperand(a arg) (*vmArg, reflect.Type, error) {
	switch a.kind {
	case argString, argInt, argFloat:
		return nil, nil, nil
	case argVar:
		switch a.str {
		case "true", "false":
			return nil, nil, nil
		case "nil":
			return nil, nil, fmt.Errorf("compile: nil does not compare in this rung (comparable scalar)")
		}
		if slot, ok := pc.slots[a.str]; ok {
			t := pc.env[a.str]
			if t == nil {
				return nil, nil, fmt.Errorf("compile: the comparison operand %s has no type", a.str)
			}
			return &vmArg{kind: vaSlot, slot: slot, name: a.str, typ: t, iface: -1}, t, nil
		}
		if cv, ok := pc.c.consts[a.str]; ok {
			return &vmArg{kind: vaConst, val: cv, typ: cv.Type(), iface: -1}, cv.Type(), nil
		}
		return nil, nil, fmt.Errorf("compile: %s in a header comparison is not a name bound by the program (comparison operand)", a.str)
	case argPath:
		if _, ok := pc.slots[a.path[0]]; ok {
			return pc.pathOperand(a.path)
		}
		if cv, ok := pc.c.consts[joinPath(a.path)]; ok {
			return &vmArg{kind: vaConst, val: cv, typ: cv.Type(), iface: -1}, cv.Type(), nil
		}
		return nil, nil, fmt.Errorf("compile: %s in a header comparison is not a name bound by the program (comparison operand)", a.path[0])
	case argCall:
		call, rt, err := pc.c.compileExpr(pc.slots, pc.env, a.sub)
		if err != nil {
			return nil, nil, err
		}
		if call.nres == 0 || rt == nil {
			return nil, nil, fmt.Errorf("compile: %s in a header comparison returns no value", call.name)
		}
		return &vmArg{kind: vaCall, sub: call, typ: rt, iface: -1}, rt, nil
	}
	return nil, nil, fmt.Errorf("compile: this operand does not compare (comparison operand)")
}

// pathOperand compiles a dotted path rooted in a program slot to a
// field-read chain and its static type. The caller has checked the
// root is a slot.
func (pc *progCompiler) pathOperand(path []string) (*vmArg, reflect.Type, error) {
	slot := pc.slots[path[0]]
	curType := pc.env[path[0]]
	cur := &vmArg{kind: vaSlot, slot: slot, name: path[0], typ: curType, iface: -1}
	for _, seg := range path[1:] {
		f, deref, ok := fieldOf(curType, seg)
		if !ok {
			return nil, nil, fmt.Errorf("compile: %s has no field %s", curType, seg)
		}
		cur = &vmArg{kind: vaField, src: cur, index: f.Index, deref: deref, typ: f.Type, iface: -1}
		curType = f.Type
	}
	return cur, curType, nil
}

// cmpLiteral converts a literal side to the type the other side
// fixed. Numbers go through literalAs, which already handles named
// scalar types and rejects overflow; strings and bools convert here
// because assignability is not the rule, adoption is.
func cmpLiteral(t reflect.Type, a arg) (*vmArg, error) {
	v := reflect.New(t).Elem()
	switch {
	case a.kind == argVar: // "true" or "false"
		if t.Kind() != reflect.Bool {
			return nil, fmt.Errorf("compile: cannot use %s as %s in a header comparison (identical types)", a.str, t)
		}
		v.SetBool(a.str == "true")
	case a.kind == argString:
		if t.Kind() != reflect.String {
			return nil, fmt.Errorf("compile: cannot use a string as %s in a header comparison (identical types)", t)
		}
		v.SetString(a.str)
	default:
		lit, err := literalAs(t, a)
		if err != nil {
			return nil, fmt.Errorf("compile: %v in a header comparison (identical types)", err)
		}
		v = lit
	}
	return &vmArg{kind: vaConst, val: v, typ: t, iface: -1}, nil
}
