package gozero

import (
	"reflect"
)

// The operator evaluator closures of the reflect tier. Chosen once at
// compile time per operator and operand kind, their bodies use Go's
// own operators, so wraparound, division panics and shift behaviour
// are native by construction: SetInt and SetUint truncate to the
// value's width, which is the mod-2^n result compiled code computes,
// and a divide or a negative shift panics inside the closure exactly
// where compiled Go panics.

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
// when the operator is not defined on the kind. Arithmetic runs in
// the kind's widest domain and stores back through the typed Set,
// which truncates to the operand's width; comparisons compare the
// widened values, which preserves order and equality at every width.
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
