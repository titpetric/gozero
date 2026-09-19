package gozero

import (
	"context"
	"fmt"
	"reflect"
)

// Header expressions, the L3 rung: boolean composition with && , ||
// and !, and arithmetic operands + - * / % feeding comparisons, all
// inside the if header and nowhere else. Composition is control
// flow: the right side of && and || runs only when the left does not
// decide, as in Go. Arithmetic follows the comparison rung's typing:
// both operands carry one static type, a literal side adopts the
// other, and the operation runs on the underlying kind, wrapping at
// the type's width the way Go's own operators do. An all-constant
// subtree folds at compile time, and a constant zero divisor is a
// compile error, Go's own rule for division by a zero constant.

// vmPred is a compiled header predicate: a composition node over two
// subtrees, or a leaf holding one comparison or one bool operand.
type vmPred struct {
	op   string // "&&", "||" or "!"; "" for a leaf
	x, y *vmPred
	cmp  *vmCmp // leaf comparison
	cond *vmArg // leaf bool operand
}

// eval runs the predicate. Short-circuit evaluation is the point:
// an error inside a right side a left side decided never surfaces,
// exactly as the skipped operand never runs in Go.
func (n *vmPred) eval(ctx context.Context, slots, frame []reflect.Value, ifaces []ifacePair, stack map[string]any, dest any) (bool, error) {
	switch n.op {
	case "&&":
		v, err := n.x.eval(ctx, slots, frame, ifaces, stack, dest)
		if err != nil || !v {
			return false, err
		}
		return n.y.eval(ctx, slots, frame, ifaces, stack, dest)
	case "||":
		v, err := n.x.eval(ctx, slots, frame, ifaces, stack, dest)
		if err != nil || v {
			return v, err
		}
		return n.y.eval(ctx, slots, frame, ifaces, stack, dest)
	case "!":
		v, err := n.x.eval(ctx, slots, frame, ifaces, stack, dest)
		if err != nil {
			return false, err
		}
		return !v, nil
	}
	if n.cmp != nil {
		lv, err := n.cmp.lhs.get(ctx, slots, frame, ifaces, stack, dest)
		if err != nil {
			return false, err
		}
		rv, err := n.cmp.rhs.get(ctx, slots, frame, ifaces, stack, dest)
		if err != nil {
			return false, err
		}
		return n.cmp.eval(lv, rv), nil
	}
	cv, err := n.cond.get(ctx, slots, frame, ifaces, stack, dest)
	if err != nil {
		return false, err
	}
	return cv.IsValid() && cv.Bool(), nil
}

// leaves appends every argument the predicate evaluates, descending
// composition and arithmetic, so the walkers behind vmIf.condArgs
// see operand calls wherever the tree buried them.
func (n *vmPred) leaves(out *[]*vmArg) {
	if n.op != "" {
		n.x.leaves(out)
		if n.y != nil {
			n.y.leaves(out)
		}
		return
	}
	if n.cmp != nil {
		arithLeaves(n.cmp.lhs, out)
		arithLeaves(n.cmp.rhs, out)
		return
	}
	*out = append(*out, n.cond)
}

// arithLeaves descends vaArith nodes to the arguments under them.
func arithLeaves(a *vmArg, out *[]*vmArg) {
	if a.kind == vaArith {
		arithLeaves(a.x, out)
		arithLeaves(a.y, out)
		return
	}
	*out = append(*out, a)
}

// arith runs one arithmetic node over resolved operands. The result
// is built at the node's own type, so SetInt and SetUint truncate to
// its width: wraparound is two's complement at the operand type, and
// a float32 result rounds per operation, both exactly what compiled
// Go produces. A division by a zero the compiler could not see
// panics in Go's own runtime, and the guard reports it as the same
// *PanicError a panicking binding becomes.
func (a *vmArg) arith(xv, yv reflect.Value) reflect.Value {
	out := reflect.New(a.typ).Elem()
	switch a.acls {
	case cmpInt:
		out.SetInt(arithOp(a.op, xv.Int(), yv.Int()))
	case cmpUint:
		out.SetUint(arithOp(a.op, xv.Uint(), yv.Uint()))
	default: // cmpFloat; the compiler admits no other class
		out.SetFloat(arithFloat(a.op, xv.Float(), yv.Float()))
	}
	return out
}

// arithOp is one integer operation. The bodies are Go's own
// operators, so signed division truncates toward zero and a zero
// divisor panics natively.
func arithOp[T int64 | uint64](op string, a, b T) T {
	switch op {
	case "+":
		return a + b
	case "-":
		return a - b
	case "*":
		return a * b
	case "/":
		return a / b
	}
	return a % b
}

// arithFloat is one float operation; % over floats is rejected at
// compile time, Go's own rule.
func arithFloat(op string, a, b float64) float64 {
	switch op {
	case "+":
		return a + b
	case "-":
		return a - b
	case "*":
		return a * b
	}
	return a / b
}

func isCmpOp(op string) bool {
	switch op {
	case "==", "!=", "<", "<=", ">", ">=":
		return true
	}
	return false
}

func isArithOp(op string) bool {
	switch op {
	case "+", "-", "*", "/", "%":
		return true
	}
	return false
}

// compilePred compiles the header tree to the predicate the arms
// test. Composition nodes stay control flow; a comparison or a bool
// operand is a leaf; an arithmetic root can only be a number, which
// no if condition is.
func (pc *progCompiler) compilePred(ce *condExpr) (*vmPred, error) {
	switch ce.op {
	case "&&", "||":
		x, err := pc.compilePred(ce.x)
		if err != nil {
			return nil, err
		}
		y, err := pc.compilePred(ce.y)
		if err != nil {
			return nil, err
		}
		return &vmPred{op: ce.op, x: x, y: y}, nil
	case "!":
		x, err := pc.compilePred(ce.x)
		if err != nil {
			return nil, err
		}
		return &vmPred{op: "!", x: x}, nil
	}
	if isCmpOp(ce.op) {
		cmp, err := pc.compileCmp(ce)
		if err != nil {
			return nil, err
		}
		return &vmPred{cmp: cmp}, nil
	}
	if isArithOp(ce.op) {
		return nil, fmt.Errorf("compile: %s produces a number, and the if condition must be bool (condition form)", ce.op)
	}
	cond, err := pc.compileCond(ce.leaf)
	if err != nil {
		return nil, err
	}
	return &vmPred{cond: cond}, nil
}

// cmpSide compiles one side of a comparison or of an arithmetic
// node. A side that folds to a constant reports the folded literal
// instead of a compiled node; it adopts the other side's type once
// that side is compiled, the comparison rung's own rule.
func (pc *progCompiler) cmpSide(ce *condExpr) (*vmArg, reflect.Type, arg, bool, error) {
	lit, ok, err := foldArith(ce)
	if err != nil {
		return nil, nil, arg{}, false, err
	}
	if ok {
		return nil, nil, lit, true, nil
	}
	switch {
	case ce.op == "":
		a, t, err := pc.cmpOperand(ce.leaf)
		return a, t, arg{}, false, err
	case isArithOp(ce.op):
		a, t, err := pc.compileArith(ce)
		return a, t, arg{}, false, err
	}
	return nil, nil, arg{}, false, fmt.Errorf("compile: %s produces a bool, which is not a comparison or arithmetic operand in this rung (comparison operand)", ce.op)
}

// compileArith compiles one arithmetic node. The typing rule is the
// comparison's: identical static types, a literal side adopting the
// other, and only the numeric kinds take part. A constant divisor of
// zero is a compile error; a value binding is a constant to this
// compiler, so a zero one is caught the same way.
func (pc *progCompiler) compileArith(ce *condExpr) (*vmArg, reflect.Type, error) {
	xa, xt, xlit, xIsLit, err := pc.cmpSide(ce.x)
	if err != nil {
		return nil, nil, err
	}
	ya, yt, ylit, yIsLit, err := pc.cmpSide(ce.y)
	if err != nil {
		return nil, nil, err
	}
	var t reflect.Type
	switch {
	case xIsLit && yIsLit:
		// Two folded sides mean the whole node folds, so the fold
		// paths return before this.
		return nil, nil, fmt.Errorf("compile: a constant %s expression did not fold", ce.op)
	case xIsLit:
		t = yt
	case yIsLit:
		t = xt
	default:
		if xt != yt {
			return nil, nil, fmt.Errorf("compile: mismatched types %s and %s in an if header (identical types)", xt, yt)
		}
		t = xt
	}
	class, ok := arithClassOf(t.Kind())
	if !ok {
		return nil, nil, fmt.Errorf("compile: %s is not defined on %s in this rung (numeric arithmetic)", ce.op, t)
	}
	if ce.op == "%" && class == cmpFloat {
		return nil, nil, fmt.Errorf("compile: %% is not defined on %s (numeric arithmetic)", t)
	}
	if xIsLit {
		if xa, err = arithLiteral(t, xlit, ce.op); err != nil {
			return nil, nil, err
		}
	}
	if yIsLit {
		if ya, err = arithLiteral(t, ylit, ce.op); err != nil {
			return nil, nil, err
		}
	}
	if (ce.op == "/" || ce.op == "%") && ya.kind == vaConst && ya.val.IsZero() {
		return nil, nil, fmt.Errorf("compile: division by a constant zero (constant division by zero)")
	}
	return &vmArg{kind: vaArith, op: ce.op, x: xa, y: ya, acls: class, typ: t, iface: -1}, t, nil
}

// arithLiteral converts a literal operand to the type the other side
// fixed. Only numbers take part; a string or bool literal names the
// rule instead of falling through to a strange conversion error.
func arithLiteral(t reflect.Type, a arg, op string) (*vmArg, error) {
	if !numLit(a) {
		return nil, fmt.Errorf("compile: %s is not defined on this operand (numeric arithmetic)", op)
	}
	lit, err := literalAs(t, a)
	if err != nil {
		return nil, fmt.Errorf("compile: %v in an if header (identical types)", err)
	}
	return &vmArg{kind: vaConst, val: lit, typ: t, iface: -1}, nil
}

// arithClassOf admits a kind to arithmetic and picks its machine
// family. Strings and bools are out: the rung is priced on numeric
// operands, and string + would be an allocation inside a header.
func arithClassOf(k reflect.Kind) (cmpClass, bool) {
	switch k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return cmpInt, true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return cmpUint, true
	case reflect.Float32, reflect.Float64:
		return cmpFloat, true
	}
	return 0, false
}

// numLit reports a numeric literal.
func numLit(a arg) bool {
	return a.kind == argInt || a.kind == argFloat
}

// litFloat reads a numeric literal as float64.
func litFloat(a arg) float64 {
	if a.kind == argInt {
		return float64(a.i)
	}
	return a.f
}

// foldArith folds an all-constant subtree into one literal, at int64
// and float64 widths. Integer division truncates toward zero, as Go
// constants do; a subtree mixing an int and a float literal folds as
// float, the untyped-constant behaviour of the mixed spelling. A
// zero divisor is the compile error even here, before any type is
// known, and the result literal adopts a type exactly like a written
// literal would.
func foldArith(ce *condExpr) (arg, bool, error) {
	if ce.op == "" {
		switch ce.leaf.kind {
		case argInt, argFloat, argString:
			return ce.leaf, true, nil
		case argVar:
			if ce.leaf.str == "true" || ce.leaf.str == "false" {
				return ce.leaf, true, nil
			}
		}
		return arg{}, false, nil
	}
	if !isArithOp(ce.op) {
		return arg{}, false, nil
	}
	x, xok, err := foldArith(ce.x)
	if err != nil {
		return arg{}, false, err
	}
	y, yok, err := foldArith(ce.y)
	if err != nil {
		return arg{}, false, err
	}
	if !xok || !yok {
		return arg{}, false, nil
	}
	if !numLit(x) || !numLit(y) {
		return arg{}, false, fmt.Errorf("compile: %s is not defined on this operand (numeric arithmetic)", ce.op)
	}
	if x.kind == argFloat || y.kind == argFloat {
		if ce.op == "%" {
			return arg{}, false, fmt.Errorf("compile: %% is not defined on a float constant (numeric arithmetic)")
		}
		xf, yf := litFloat(x), litFloat(y)
		if ce.op == "/" && yf == 0 {
			return arg{}, false, fmt.Errorf("compile: division by a constant zero (constant division by zero)")
		}
		return arg{kind: argFloat, f: arithFloat(ce.op, xf, yf)}, true, nil
	}
	if (ce.op == "/" || ce.op == "%") && y.i == 0 {
		return arg{}, false, fmt.Errorf("compile: division by a constant zero (constant division by zero)")
	}
	return arg{kind: argInt, i: arithOp(ce.op, x.i, y.i)}, true, nil
}
