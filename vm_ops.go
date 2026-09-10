package gozero

import (
	"fmt"
	"reflect"
)

// Constant folding and the operator evaluator closures. The closures
// are the single source of operator truth: chosen once at compile
// time per operator and operand kind, their bodies use Go's own
// operators, and both tiers run them, so wraparound, division panics
// and shift behaviour cannot diverge between implementations.

// foldExpr reduces an all-literal expression to one literal at
// compile time. ok is false when any operand is not a constant; an
// error is an operation the constants cannot perform, which is a
// compile error as it is in Go, division by zero included.
func foldExpr(a arg) (arg, bool, error) {
	switch a.kind {
	case argInt, argFloat, argString, argBool:
		return a, true, nil
	case argUnary:
		x, ok, err := foldExpr(*a.x)
		if err != nil || !ok {
			return arg{}, false, err
		}
		switch a.op {
		case "-":
			switch x.kind {
			case argInt:
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
		return arg{}, false, fmt.Errorf("compile: invalid operation: %s on this constant", a.op)
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

func foldBinary(op string, x, y arg) (arg, bool, error) {
	boolArg := func(b bool) (arg, bool, error) { return arg{kind: argBool, b: b}, true, nil }
	badOp := func() (arg, bool, error) {
		return arg{}, false, fmt.Errorf("compile: invalid operation: %s on these constants", op)
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
		xf, yf := x.f, y.f
		if x.kind == argInt {
			xf = float64(x.i)
		}
		if y.kind == argInt {
			yf = float64(y.i)
		}
		switch op {
		case "+":
			return arg{kind: argFloat, f: xf + yf}, true, nil
		case "-":
			return arg{kind: argFloat, f: xf - yf}, true, nil
		case "*":
			return arg{kind: argFloat, f: xf * yf}, true, nil
		case "/":
			if yf == 0 {
				return arg{}, false, fmt.Errorf("compile: division by zero")
			}
			return arg{kind: argFloat, f: xf / yf}, true, nil
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
		return badOp()
	}

	xi, yi := x.i, y.i
	intArg := func(i int64) (arg, bool, error) { return arg{kind: argInt, i: i}, true, nil }
	switch op {
	case "+":
		return intArg(xi + yi)
	case "-":
		return intArg(xi - yi)
	case "*":
		return intArg(xi * yi)
	case "/":
		if yi == 0 {
			return arg{}, false, fmt.Errorf("compile: division by zero")
		}
		return intArg(xi / yi)
	case "%":
		if yi == 0 {
			return arg{}, false, fmt.Errorf("compile: division by zero")
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
			return arg{}, false, fmt.Errorf("compile: negative shift count")
		}
		return intArg(xi << uint64(yi))
	case ">>":
		if yi < 0 {
			return arg{}, false, fmt.Errorf("compile: negative shift count")
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
	return badOp()
}

// shiftCount reads a shift's right operand, panicking the way the
// runtime does on a negative count.
func shiftCount(y reflect.Value) uint64 {
	if y.CanUint() {
		return y.Uint()
	}
	i := y.Int()
	if i < 0 {
		panic("runtime error: negative shift amount")
	}
	return uint64(i)
}

// binEval picks the evaluator for op over operands of type t, nil
// when the operator is not defined on the type. Arithmetic runs in
// the kind's widest domain and stores back through the typed Set,
// which truncates to the operand's width: the same mod-2^n result
// native code computes. Comparisons compare the widened values,
// which preserves order and equality at every width. Division and
// modulo panic in the closure body exactly as compiled Go panics.
func binEval(op string, t reflect.Type) func(x, y reflect.Value) reflect.Value {
	k := t.Kind()
	out := func(set func(v reflect.Value, x, y reflect.Value)) func(x, y reflect.Value) reflect.Value {
		return func(x, y reflect.Value) reflect.Value {
			v := reflect.New(t).Elem()
			set(v, x, y)
			return v
		}
	}
	cmp := func(f func(x, y reflect.Value) bool) func(x, y reflect.Value) reflect.Value {
		return func(x, y reflect.Value) reflect.Value {
			return reflect.ValueOf(f(x, y))
		}
	}
	switch {
	case k >= reflect.Int && k <= reflect.Int64:
		switch op {
		case "+":
			return out(func(v, x, y reflect.Value) { v.SetInt(x.Int() + y.Int()) })
		case "-":
			return out(func(v, x, y reflect.Value) { v.SetInt(x.Int() - y.Int()) })
		case "*":
			return out(func(v, x, y reflect.Value) { v.SetInt(x.Int() * y.Int()) })
		case "/":
			return out(func(v, x, y reflect.Value) { v.SetInt(x.Int() / y.Int()) })
		case "%":
			return out(func(v, x, y reflect.Value) { v.SetInt(x.Int() % y.Int()) })
		case "&":
			return out(func(v, x, y reflect.Value) { v.SetInt(x.Int() & y.Int()) })
		case "|":
			return out(func(v, x, y reflect.Value) { v.SetInt(x.Int() | y.Int()) })
		case "^":
			return out(func(v, x, y reflect.Value) { v.SetInt(x.Int() ^ y.Int()) })
		case "&^":
			return out(func(v, x, y reflect.Value) { v.SetInt(x.Int() &^ y.Int()) })
		case "<<":
			return out(func(v, x, y reflect.Value) { v.SetInt(x.Int() << shiftCount(y)) })
		case ">>":
			return out(func(v, x, y reflect.Value) { v.SetInt(x.Int() >> shiftCount(y)) })
		case "==":
			return cmp(func(x, y reflect.Value) bool { return x.Int() == y.Int() })
		case "!=":
			return cmp(func(x, y reflect.Value) bool { return x.Int() != y.Int() })
		case "<":
			return cmp(func(x, y reflect.Value) bool { return x.Int() < y.Int() })
		case "<=":
			return cmp(func(x, y reflect.Value) bool { return x.Int() <= y.Int() })
		case ">":
			return cmp(func(x, y reflect.Value) bool { return x.Int() > y.Int() })
		case ">=":
			return cmp(func(x, y reflect.Value) bool { return x.Int() >= y.Int() })
		}
	case k >= reflect.Uint && k <= reflect.Uintptr:
		switch op {
		case "+":
			return out(func(v, x, y reflect.Value) { v.SetUint(x.Uint() + y.Uint()) })
		case "-":
			return out(func(v, x, y reflect.Value) { v.SetUint(x.Uint() - y.Uint()) })
		case "*":
			return out(func(v, x, y reflect.Value) { v.SetUint(x.Uint() * y.Uint()) })
		case "/":
			return out(func(v, x, y reflect.Value) { v.SetUint(x.Uint() / y.Uint()) })
		case "%":
			return out(func(v, x, y reflect.Value) { v.SetUint(x.Uint() % y.Uint()) })
		case "&":
			return out(func(v, x, y reflect.Value) { v.SetUint(x.Uint() & y.Uint()) })
		case "|":
			return out(func(v, x, y reflect.Value) { v.SetUint(x.Uint() | y.Uint()) })
		case "^":
			return out(func(v, x, y reflect.Value) { v.SetUint(x.Uint() ^ y.Uint()) })
		case "&^":
			return out(func(v, x, y reflect.Value) { v.SetUint(x.Uint() &^ y.Uint()) })
		case "<<":
			return out(func(v, x, y reflect.Value) { v.SetUint(x.Uint() << shiftCount(y)) })
		case ">>":
			return out(func(v, x, y reflect.Value) { v.SetUint(x.Uint() >> shiftCount(y)) })
		case "==":
			return cmp(func(x, y reflect.Value) bool { return x.Uint() == y.Uint() })
		case "!=":
			return cmp(func(x, y reflect.Value) bool { return x.Uint() != y.Uint() })
		case "<":
			return cmp(func(x, y reflect.Value) bool { return x.Uint() < y.Uint() })
		case "<=":
			return cmp(func(x, y reflect.Value) bool { return x.Uint() <= y.Uint() })
		case ">":
			return cmp(func(x, y reflect.Value) bool { return x.Uint() > y.Uint() })
		case ">=":
			return cmp(func(x, y reflect.Value) bool { return x.Uint() >= y.Uint() })
		}
	case k == reflect.Float64:
		switch op {
		case "+":
			return out(func(v, x, y reflect.Value) { v.SetFloat(x.Float() + y.Float()) })
		case "-":
			return out(func(v, x, y reflect.Value) { v.SetFloat(x.Float() - y.Float()) })
		case "*":
			return out(func(v, x, y reflect.Value) { v.SetFloat(x.Float() * y.Float()) })
		case "/":
			return out(func(v, x, y reflect.Value) { v.SetFloat(x.Float() / y.Float()) })
		case "==":
			return cmp(func(x, y reflect.Value) bool { return x.Float() == y.Float() })
		case "!=":
			return cmp(func(x, y reflect.Value) bool { return x.Float() != y.Float() })
		case "<":
			return cmp(func(x, y reflect.Value) bool { return x.Float() < y.Float() })
		case "<=":
			return cmp(func(x, y reflect.Value) bool { return x.Float() <= y.Float() })
		case ">":
			return cmp(func(x, y reflect.Value) bool { return x.Float() > y.Float() })
		case ">=":
			return cmp(func(x, y reflect.Value) bool { return x.Float() >= y.Float() })
		}
	case k == reflect.Float32:
		// float32 arithmetic rounds at 32 bits per operation; running
		// it in float64 and truncating once would double-round.
		f32 := func(v reflect.Value) float32 { return float32(v.Float()) }
		switch op {
		case "+":
			return out(func(v, x, y reflect.Value) { v.SetFloat(float64(f32(x) + f32(y))) })
		case "-":
			return out(func(v, x, y reflect.Value) { v.SetFloat(float64(f32(x) - f32(y))) })
		case "*":
			return out(func(v, x, y reflect.Value) { v.SetFloat(float64(f32(x) * f32(y))) })
		case "/":
			return out(func(v, x, y reflect.Value) { v.SetFloat(float64(f32(x) / f32(y))) })
		case "==":
			return cmp(func(x, y reflect.Value) bool { return f32(x) == f32(y) })
		case "!=":
			return cmp(func(x, y reflect.Value) bool { return f32(x) != f32(y) })
		case "<":
			return cmp(func(x, y reflect.Value) bool { return f32(x) < f32(y) })
		case "<=":
			return cmp(func(x, y reflect.Value) bool { return f32(x) <= f32(y) })
		case ">":
			return cmp(func(x, y reflect.Value) bool { return f32(x) > f32(y) })
		case ">=":
			return cmp(func(x, y reflect.Value) bool { return f32(x) >= f32(y) })
		}
	case k == reflect.String:
		switch op {
		case "+":
			return out(func(v, x, y reflect.Value) { v.SetString(x.String() + y.String()) })
		case "==":
			return cmp(func(x, y reflect.Value) bool { return x.String() == y.String() })
		case "!=":
			return cmp(func(x, y reflect.Value) bool { return x.String() != y.String() })
		case "<":
			return cmp(func(x, y reflect.Value) bool { return x.String() < y.String() })
		case "<=":
			return cmp(func(x, y reflect.Value) bool { return x.String() <= y.String() })
		case ">":
			return cmp(func(x, y reflect.Value) bool { return x.String() > y.String() })
		case ">=":
			return cmp(func(x, y reflect.Value) bool { return x.String() >= y.String() })
		}
	case k == reflect.Bool:
		switch op {
		case "==":
			return cmp(func(x, y reflect.Value) bool { return x.Bool() == y.Bool() })
		case "!=":
			return cmp(func(x, y reflect.Value) bool { return x.Bool() != y.Bool() })
		}
	case k == reflect.Pointer:
		switch op {
		case "==":
			return cmp(func(x, y reflect.Value) bool { return x.Pointer() == y.Pointer() })
		case "!=":
			return cmp(func(x, y reflect.Value) bool { return x.Pointer() != y.Pointer() })
		}
	}
	return nil
}

// unEval picks the evaluator for a unary operator over t, nil when
// undefined.
func unEval(op string, t reflect.Type) func(x reflect.Value) reflect.Value {
	k := t.Kind()
	out := func(set func(v, x reflect.Value)) func(x reflect.Value) reflect.Value {
		return func(x reflect.Value) reflect.Value {
			v := reflect.New(t).Elem()
			set(v, x)
			return v
		}
	}
	switch {
	case op == "-" && k >= reflect.Int && k <= reflect.Int64:
		return out(func(v, x reflect.Value) { v.SetInt(-x.Int()) })
	case op == "-" && k >= reflect.Uint && k <= reflect.Uintptr:
		return out(func(v, x reflect.Value) { v.SetUint(-x.Uint()) })
	case op == "-" && (k == reflect.Float32 || k == reflect.Float64):
		return out(func(v, x reflect.Value) { v.SetFloat(-x.Float()) })
	case op == "^" && k >= reflect.Int && k <= reflect.Int64:
		return out(func(v, x reflect.Value) { v.SetInt(^x.Int()) })
	case op == "^" && k >= reflect.Uint && k <= reflect.Uintptr:
		return out(func(v, x reflect.Value) { v.SetUint(^x.Uint()) })
	case op == "!" && k == reflect.Bool:
		return out(func(v, x reflect.Value) { v.SetBool(!x.Bool()) })
	}
	return nil
}
