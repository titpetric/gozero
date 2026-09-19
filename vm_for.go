package gozero

import (
	"context"
	"fmt"
	"reflect"
)

// The condition and three-clause loops on the reflect tier. Both are
// header-scoped: the condition is a bool name, field or call read per
// iteration, and the three-clause header is exactly an init
// assignment, one comparison, and an increment or decrement of the
// loop variable. Neither form carries a data bound the way range
// does, so the per-iteration ctx.Err() check is the only bound either
// keeps: a cancelled or expired execution context ends a spinning
// loop with its error.
//
// Scope stays flat. The loop variable is a program-level slot the way
// a range key is: one addressable cell reused per iteration, defined
// after the loop with its last value, on both tiers. The post clause
// steps it in the 64-bit bit domain and truncates at the variable's
// own width, which is the wrap Go's ++ has and the same store the
// direct tier's storeN performs.

// cmpOp is one comparison operator of a three-clause header.
type cmpOp int

const (
	cmpEq cmpOp = iota
	cmpNe
	cmpLt
	cmpLe
	cmpGt
	cmpGe
)

// cmpOps maps the source spelling the parser read to its operator.
var cmpOps = map[string]cmpOp{"==": cmpEq, "!=": cmpNe, "<": cmpLt, "<=": cmpLe, ">": cmpGt, ">=": cmpGe}

// vmCmp is a compiled comparison: two integer operands and the
// operator. Each operand widens to 64 bits at its own signedness;
// mixing signed with unsigned is rejected when the loop compiles.
type vmCmp struct {
	op       cmpOp
	x, y     *vmArg
	unsigned bool
}

// vmFor is a compiled condition or three-clause loop. cond is set on
// the condition form; initSlot, initVal, varType, cmp and postDec on
// the other.
type vmFor struct {
	cond *vmArg

	initSlot int // -1 on the condition form
	initVal  *vmArg
	varType  reflect.Type
	cmp      *vmCmp
	postDec  bool

	body []vmStmt
}

// intKind reports whether t is an integer the three-clause header can
// count with, and whether it is unsigned.
func intKind(t reflect.Type) (unsigned, ok bool) {
	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return false, true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return true, true
	}
	return false, false
}

// compileFor compiles one condition or three-clause loop. newSlot,
// checkName and compileStmt are compileProgram's own, closing over
// its slot table, exactly as compileRange receives them.
func (c *Compiler) compileFor(slots map[string]int, env map[string]reflect.Type, fs *forStmt, newSlot func(string, reflect.Type) int, checkName func(string) error, compileStmt func(stmt, *[]vmStmt) error) (*vmFor, error) {
	f := &vmFor{initSlot: -1}
	if fs.cond != nil {
		cond, ct, err := c.sourceArg(slots, env, *fs.cond, "loop condition")
		if err != nil {
			return nil, err
		}
		if ct == nil || ct.Kind() != reflect.Bool {
			return nil, fmt.Errorf("compile: a loop condition must be a bool, not %s", ct)
		}
		f.cond = cond
	} else {
		if err := checkName(fs.initName); err != nil {
			return nil, err
		}
		iv, it, err := c.intSource(slots, env, *fs.initVal, "loop variable's init")
		if err != nil {
			return nil, err
		}
		f.initVal, f.varType = iv, it
		f.postDec = fs.postDec
		// The slot exists before the comparison compiles, so the header
		// can read the variable it declares.
		f.initSlot = newSlot(fs.initName, it)
		cmp, err := c.compileCmp(slots, env, fs)
		if err != nil {
			return nil, err
		}
		f.cmp = cmp
	}
	for _, bs := range fs.body {
		if err := compileStmt(bs, &f.body); err != nil {
			return nil, err
		}
	}
	// A body may reassign a header name, but not away from the type
	// the header runs on: the post clause counts an integer and the
	// condition reads a bool on every iteration.
	if f.initSlot >= 0 {
		if t := env[fs.initName]; t == nil || t != f.varType {
			return nil, fmt.Errorf("compile: the loop variable %s is reassigned as %s inside the body", fs.initName, t)
		}
	}
	if fs.cond != nil && fs.cond.kind == argVar {
		if t := env[fs.cond.str]; t == nil || t.Kind() != reflect.Bool {
			return nil, fmt.Errorf("compile: the loop condition %s is reassigned as %s inside the body", fs.cond.str, t)
		}
	}
	return f, nil
}

// intSource resolves a header operand to an argument with a static
// integer type: an integer literal, a name, a field, or a call.
func (c *Compiler) intSource(slots map[string]int, env map[string]reflect.Type, a arg, what string) (*vmArg, reflect.Type, error) {
	switch a.kind {
	case argInt:
		v := reflect.ValueOf(a.i)
		return &vmArg{kind: vaConst, val: v, typ: v.Type(), iface: -1}, v.Type(), nil
	case argFloat, argString, argBool, argNil:
		return nil, nil, fmt.Errorf("compile: a %s must be an integer", what)
	}
	va, t, err := c.sourceArg(slots, env, a, what)
	if err != nil {
		return nil, nil, err
	}
	if t == nil {
		return nil, nil, fmt.Errorf("compile: a %s has no static type", what)
	}
	if _, ok := intKind(t); !ok {
		return nil, nil, fmt.Errorf("compile: a %s must be an integer, not %s", what, t)
	}
	return va, t, nil
}

// compileCmp compiles the comparison of a three-clause header. An
// integer literal adapts to the other operand's signedness the way an
// untyped Go constant does; two named operands must agree on theirs,
// because a signed and an unsigned value do not order in one domain.
func (c *Compiler) compileCmp(slots map[string]int, env map[string]reflect.Type, fs *forStmt) (*vmCmp, error) {
	op, ok := cmpOps[fs.cmpOp]
	if !ok {
		return nil, fmt.Errorf("compile: %q is not a comparison", fs.cmpOp)
	}
	x, xt, err := c.intSource(slots, env, *fs.cmpX, "comparison operand")
	if err != nil {
		return nil, err
	}
	y, yt, err := c.intSource(slots, env, *fs.cmpY, "comparison operand")
	if err != nil {
		return nil, err
	}
	xu, _ := intKind(xt)
	yu, _ := intKind(yt)
	xLit, yLit := fs.cmpX.kind == argInt, fs.cmpY.kind == argInt
	switch {
	case xLit && !yLit && yu:
		if err := unsignConst(x, fs.cmpX.i, yt); err != nil {
			return nil, err
		}
		xu = true
	case yLit && !xLit && xu:
		if err := unsignConst(y, fs.cmpY.i, xt); err != nil {
			return nil, err
		}
		yu = true
	}
	if xu != yu {
		return nil, fmt.Errorf("compile: cannot compare %s with %s; the operands' signedness must match", xt, yt)
	}
	return &vmCmp{op: op, x: x, y: y, unsigned: xu}, nil
}

// unsignConst rebinds an integer literal's constant as unsigned, so
// it compares in the other operand's domain.
func unsignConst(a *vmArg, lit int64, other reflect.Type) error {
	if lit < 0 {
		return fmt.Errorf("compile: constant %d cannot compare with the unsigned %s", lit, other)
	}
	a.val = reflect.ValueOf(uint64(lit))
	a.typ = a.val.Type()
	return nil
}

// cmpSigned and cmpUnsigned are the comparison bodies, shared by both
// tiers so the answer cannot diverge.
func cmpSigned(op cmpOp, a, b int64) bool {
	switch op {
	case cmpEq:
		return a == b
	case cmpNe:
		return a != b
	case cmpLt:
		return a < b
	case cmpLe:
		return a <= b
	case cmpGt:
		return a > b
	}
	return a >= b
}

func cmpUnsigned(op cmpOp, a, b uint64) bool {
	switch op {
	case cmpEq:
		return a == b
	case cmpNe:
		return a != b
	case cmpLt:
		return a < b
	case cmpLe:
		return a <= b
	case cmpGt:
		return a > b
	}
	return a >= b
}

// test evaluates the loop's condition or comparison for one
// iteration.
func (f *vmFor) test(ctx context.Context, slots, frame []reflect.Value, ifaces []ifacePair, stack map[string]any, dest any) (bool, error) {
	if f.cond != nil {
		v, err := f.cond.get(ctx, slots, frame, ifaces, stack, dest)
		if err != nil {
			return false, err
		}
		if !v.IsValid() {
			return false, nil
		}
		return v.Bool(), nil
	}
	x, err := f.cmp.x.get(ctx, slots, frame, ifaces, stack, dest)
	if err != nil {
		return false, err
	}
	y, err := f.cmp.y.get(ctx, slots, frame, ifaces, stack, dest)
	if err != nil {
		return false, err
	}
	if f.cmp.unsigned {
		return cmpUnsigned(f.cmp.op, x.Uint(), y.Uint()), nil
	}
	return cmpSigned(f.cmp.op, x.Int(), y.Int()), nil
}

// runFor executes one condition or three-clause loop. The loop
// variable's cell is allocated once and stepped in place; a body that
// reassigns the name replaces the slot, and the post clause reads it
// back before stepping, so the reassignment counts.
func (p *vmProgram) runFor(ctx context.Context, f *vmFor, slots, frame []reflect.Value, ifaces []ifacePair, stack map[string]any, dest any) error {
	var cell reflect.Value
	unsigned := false
	if f.initSlot >= 0 {
		v, err := f.initVal.get(ctx, slots, frame, ifaces, stack, dest)
		if err != nil {
			return err
		}
		cell = reflect.New(f.varType).Elem()
		cell.Set(v)
		slots[f.initSlot] = cell
		unsigned, _ = intKind(f.varType)
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		more, err := f.test(ctx, slots, frame, ifaces, stack, dest)
		if err != nil || !more {
			return err
		}
		switch err := p.runBody(ctx, f.body, slots, frame, ifaces, stack, dest); err {
		case nil, errLoopContinue:
		case errLoopBreak:
			return nil
		default:
			return err
		}
		if f.initSlot >= 0 {
			// SetInt and SetUint truncate at the cell's width, which is
			// the wrap storeN performs on the direct tier.
			cur := slots[f.initSlot]
			if unsigned {
				n := cur.Uint()
				if f.postDec {
					n--
				} else {
					n++
				}
				cell.SetUint(n)
			} else {
				n := cur.Int()
				if f.postDec {
					n--
				} else {
					n++
				}
				cell.SetInt(n)
			}
			slots[f.initSlot] = cell
		}
	}
}
