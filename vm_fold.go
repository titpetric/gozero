package gozero

import (
	"fmt"
	"math"
)

// Constant folding. An all-literal subtree reduces to one literal at
// compile time, in the int64 and float64 domains the parser gives
// literals; this is deliberately not go/constant, whose exact
// arithmetic the design refuses to depend on. Where Go's exact
// constants and a 64-bit fold could silently differ, the fold errors
// instead: division by zero, an intermediate int64 overflow and a
// float result outside float64 are all compile errors, so a folded
// value is never a wrapped or saturated one.

// foldExpr reduces an all-literal expression to one literal. ok is
// false when any operand is not a constant; an error is an operation
// the constants cannot perform, which is a compile error as it is in
// Go, division by zero included.
func foldExpr(a arg) (arg, bool, error) {
	switch a.kind {
	case argInt, argFloat, argString, argBool:
		return a, true, nil
	case argUnary:
		x, ok, err := foldExpr(*a.x)
		if err != nil || !ok {
			return arg{}, false, err
		}
		return foldUnary(a.op, x)
	case argBinary:
		x, okx, err := foldExpr(*a.x)
		if err != nil {
			return arg{}, false, err
		}
		y, oky, err := foldExpr(*a.y)
		if err != nil {
			return arg{}, false, err
		}
		if !okx || !oky {
			return arg{}, false, nil
		}
		return foldBinary(a.op, x, y)
	}
	return arg{}, false, nil
}

func foldUnary(op string, x arg) (arg, bool, error) {
	switch op {
	case "-":
		switch x.kind {
		case argInt:
			if x.i == math.MinInt64 {
				return arg{}, false, fmt.Errorf("constant overflow")
			}
			return arg{kind: argInt, i: -x.i}, true, nil
		case argFloat:
			return arg{kind: argFloat, f: -x.f}, true, nil
		}
	case "^":
		if x.kind == argInt {
			return arg{kind: argInt, i: ^x.i}, true, nil
		}
	case "!":
		if x.kind == argBool {
			return arg{kind: argBool, b: !x.b}, true, nil
		}
	}
	return arg{}, false, fmt.Errorf("invalid operation: operator %s is not defined on %s", op, spellArg(x))
}

func foldBinary(op string, x, y arg) (arg, bool, error) {
	boolArg := func(b bool) (arg, bool, error) { return arg{kind: argBool, b: b}, true, nil }
	badOp := func() (arg, bool, error) {
		return arg{}, false, fmt.Errorf("invalid operation: operator %s is not defined on %s and %s", op, spellArg(x), spellArg(y))
	}

	// Strings and bools do not mix with numbers.
	if x.kind == argString || y.kind == argString {
		if x.kind != argString || y.kind != argString {
			return badOp()
		}
		switch op {
		case "+":
			return arg{kind: argString, str: x.str + y.str}, true, nil
		case "==":
			return boolArg(x.str == y.str)
		case "!=":
			return boolArg(x.str != y.str)
		case "<":
			return boolArg(x.str < y.str)
		case "<=":
			return boolArg(x.str <= y.str)
		case ">":
			return boolArg(x.str > y.str)
		case ">=":
			return boolArg(x.str >= y.str)
		}
		return badOp()
	}
	if x.kind == argBool || y.kind == argBool {
		if x.kind != argBool || y.kind != argBool {
			return badOp()
		}
		switch op {
		case "&&":
			return boolArg(x.b && y.b)
		case "||":
			return boolArg(x.b || y.b)
		case "==":
			return boolArg(x.b == y.b)
		case "!=":
			return boolArg(x.b != y.b)
		}
		return badOp()
	}

	// A float on either side lifts both to float, the way untyped
	// constants unify in Go.
	if x.kind == argFloat || y.kind == argFloat {
		return foldFloat(op, x, y)
	}
	return foldInt(op, x.i, y.i)
}

func foldFloat(op string, x, y arg) (arg, bool, error) {
	xf, yf := x.f, y.f
	if x.kind == argInt {
		xf = float64(x.i)
	}
	if y.kind == argInt {
		yf = float64(y.i)
	}
	floatArg := func(f float64) (arg, bool, error) {
		if math.IsInf(f, 0) {
			return arg{}, false, fmt.Errorf("constant overflow")
		}
		return arg{kind: argFloat, f: f}, true, nil
	}
	boolArg := func(b bool) (arg, bool, error) { return arg{kind: argBool, b: b}, true, nil }
	switch op {
	case "+":
		return floatArg(xf + yf)
	case "-":
		return floatArg(xf - yf)
	case "*":
		return floatArg(xf * yf)
	case "/":
		if yf == 0 {
			return arg{}, false, fmt.Errorf("division by zero")
		}
		return floatArg(xf / yf)
	case "==":
		return boolArg(xf == yf)
	case "!=":
		return boolArg(xf != yf)
	case "<":
		return boolArg(xf < yf)
	case "<=":
		return boolArg(xf <= yf)
	case ">":
		return boolArg(xf > yf)
	case ">=":
		return boolArg(xf >= yf)
	}
	return arg{}, false, fmt.Errorf("invalid operation: operator %s is not defined on these constants", op)
}

func foldInt(op string, xi, yi int64) (arg, bool, error) {
	intArg := func(i int64) (arg, bool, error) { return arg{kind: argInt, i: i}, true, nil }
	boolArg := func(b bool) (arg, bool, error) { return arg{kind: argBool, b: b}, true, nil }
	overflow := func() (arg, bool, error) { return arg{}, false, fmt.Errorf("constant overflow") }
	switch op {
	case "+":
		s := xi + yi
		if (s > xi) != (yi > 0) {
			return overflow()
		}
		return intArg(s)
	case "-":
		d := xi - yi
		if (d < xi) != (yi > 0) {
			return overflow()
		}
		return intArg(d)
	case "*":
		p := xi * yi
		if xi != 0 && (p/xi != yi || (xi == -1 && yi == math.MinInt64)) {
			return overflow()
		}
		return intArg(p)
	case "/":
		if yi == 0 {
			return arg{}, false, fmt.Errorf("division by zero")
		}
		if xi == math.MinInt64 && yi == -1 {
			return overflow()
		}
		return intArg(xi / yi)
	case "%":
		if yi == 0 {
			return arg{}, false, fmt.Errorf("division by zero")
		}
		return intArg(xi % yi)
	case "&":
		return intArg(xi & yi)
	case "|":
		return intArg(xi | yi)
	case "^":
		return intArg(xi ^ yi)
	case "&^":
		return intArg(xi &^ yi)
	case "<<":
		if yi < 0 {
			return arg{}, false, fmt.Errorf("invalid operation: negative shift count %d", yi)
		}
		if yi >= 64 || xi<<uint64(yi)>>uint64(yi) != xi {
			if xi == 0 {
				return intArg(0)
			}
			return overflow()
		}
		return intArg(xi << uint64(yi))
	case ">>":
		if yi < 0 {
			return arg{}, false, fmt.Errorf("invalid operation: negative shift count %d", yi)
		}
		if yi >= 64 {
			if xi < 0 {
				return intArg(-1)
			}
			return intArg(0)
		}
		return intArg(xi >> uint64(yi))
	case "==":
		return boolArg(xi == yi)
	case "!=":
		return boolArg(xi != yi)
	case "<":
		return boolArg(xi < yi)
	case "<=":
		return boolArg(xi <= yi)
	case ">":
		return boolArg(xi > yi)
	case ">=":
		return boolArg(xi >= yi)
	}
	return arg{}, false, fmt.Errorf("invalid operation: operator %s is not defined on these constants", op)
}
