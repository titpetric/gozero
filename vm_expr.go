package gozero

import (
	"fmt"
	"reflect"
)

// The operator assignment, "total := n*3 + 1;". The right side is a
// full expression tree over Go's five binary precedence levels, unary
// - ! ^ and parentheses. The typing rule stays the one line of Go's
// that L2 established, applied per node: operands need identical
// static types, a constant subtree adopts the typed side's type,
// checked for representability, and an all-constant tree folds away
// in vm_fold.go before any of this runs.

// vmExpr is one compiled expression node. A leaf holds the operand,
// vaSlot or vaConst; an interior node holds the operator, its
// operands, and the evaluator closure chosen at compile time. The
// short-circuit operators carry no closure: laziness is control flow,
// so eval runs them itself.
type vmExpr struct {
	op    string  // "" for a leaf
	x, y  *vmExpr // y is nil for a unary node
	leaf  *vmArg
	t     reflect.Type // the static type of this node's value
	binFn func(x, y reflect.Value) reflect.Value
	unFn  func(x reflect.Value) reflect.Value
}

// leaves calls fn for every operand leaf, which is how the frame
// post-pass and the planner see through the tree.
func (e *vmExpr) leaves(fn func(*vmArg)) {
	if e == nil {
		return
	}
	if e.leaf != nil {
		fn(e.leaf)
		return
	}
	e.x.leaves(fn)
	e.y.leaves(fn)
}

// eval computes the node's value. Leaves cannot fail: an operand is a
// slot read or a constant by rule. Arithmetic faults, division by
// zero and a negative shift count, panic inside the chosen closure
// exactly as compiled Go panics, and arrive as *PanicError through
// the guard.
func (e *vmExpr) eval(slots []reflect.Value) reflect.Value {
	if e.leaf != nil {
		if e.leaf.kind == vaConst {
			return e.leaf.val
		}
		v := slots[e.leaf.slot]
		if !v.IsValid() {
			return reflect.Zero(e.leaf.typ)
		}
		return v
	}
	if e.unFn != nil {
		return e.unFn(e.x.eval(slots))
	}
	switch e.op {
	case "&&", "||":
		// The right side only runs when the left does not decide, as
		// in Go.
		xb := e.x.eval(slots).Bool()
		res := xb
		if (e.op == "&&") == xb {
			res = e.y.eval(slots).Bool()
		}
		v := reflect.New(e.t).Elem()
		v.SetBool(res)
		return v
	}
	return e.binFn(e.x.eval(slots), e.y.eval(slots))
}

// compileValueExpr compiles an expression tree bottom-up. Constant
// subtrees folded before the recursion reached them, so a also
// contains at least one typed operand on every path that gets here.
func (c *Compiler) compileValueExpr(slots map[string]int, env map[string]reflect.Type, expr string, a arg) (*vmExpr, error) {
	switch a.kind {
	case argBinary:
		return c.compileBinary(slots, env, expr, a)
	case argUnary:
		return c.compileUnary(slots, env, expr, a)
	}
	va, t, err := c.binopOperand(slots, env, expr, a)
	if err != nil {
		return nil, err
	}
	if va == nil {
		return nil, fmt.Errorf("compile: %s: a constant subtree did not fold", expr)
	}
	return &vmExpr{leaf: va, t: t}, nil
}

// compileBinary types one binary node. Each side folds first: a
// constant side adopts the typed side's type the way an untyped Go
// constant does, a shift count stays its own integer type, and two
// typed sides must match exactly.
func (c *Compiler) compileBinary(slots map[string]int, env map[string]reflect.Type, expr string, a arg) (*vmExpr, error) {
	op := a.op
	fx, xConst, err := foldExpr(*a.x)
	if err != nil {
		return nil, fmt.Errorf("compile: %s: %w", expr, err)
	}
	fy, yConst, err := foldExpr(*a.y)
	if err != nil {
		return nil, fmt.Errorf("compile: %s: %w", expr, err)
	}

	// Short-circuit logic wants bool on both sides; the result takes
	// the operands' type, which for a named bool type is that type.
	if op == "&&" || op == "||" {
		return c.compileLogic(slots, env, expr, a, fx, fy, xConst, yConst)
	}

	// A shift's count may be any integer type; everything else wants
	// identical operand types, with a constant side adopting the
	// other.
	shift := op == "<<" || op == ">>"
	var x, y *vmExpr
	switch {
	case yConst:
		// A constant divisor must not be zero, as in Go, whatever the
		// dividend is.
		if (op == "/" || op == "%") && ((fy.kind == argInt && fy.i == 0) || (fy.kind == argFloat && fy.f == 0)) {
			return nil, fmt.Errorf("compile: %s: division by zero", expr)
		}
		x, err = c.compileValueExpr(slots, env, expr, *a.x)
		if err != nil {
			return nil, err
		}
		lt := x.t
		if shift {
			lt = reflect.TypeFor[int64]()
			if fy.kind == argInt && fy.i < 0 {
				return nil, fmt.Errorf("compile: %s: invalid operation: negative shift count %d", expr, fy.i)
			}
		}
		y, err = constExpr(lt, fy)
		if err != nil {
			return nil, fmt.Errorf("compile: %s: %w", expr, err)
		}
	case xConst:
		y, err = c.compileValueExpr(slots, env, expr, *a.y)
		if err != nil {
			return nil, err
		}
		lt := y.t
		if shift {
			// The count is typed; the shifted constant defaults.
			if fx.kind != argInt {
				return nil, fmt.Errorf("compile: %s: invalid operation: shift of %s", expr, spellArg(fx))
			}
			lt = reflect.TypeFor[int64]()
		}
		x, err = constExpr(lt, fx)
		if err != nil {
			return nil, fmt.Errorf("compile: %s: %w", expr, err)
		}
	default:
		x, err = c.compileValueExpr(slots, env, expr, *a.x)
		if err != nil {
			return nil, err
		}
		y, err = c.compileValueExpr(slots, env, expr, *a.y)
		if err != nil {
			return nil, err
		}
	}
	if !shift && x.t != y.t {
		return nil, fmt.Errorf("compile: %s: invalid operation: mismatched types %s and %s", expr, x.t, y.t)
	}
	if shift && !intKind(y.t.Kind()) {
		return nil, fmt.Errorf("compile: %s: invalid operation: shift count type %s, must be integer", expr, y.t)
	}
	if err := opDefinedAt(op, x.t); err != nil {
		return nil, fmt.Errorf("compile: %s: %w", expr, err)
	}
	rt := x.t
	if cmpOp(op) {
		rt = reflect.TypeFor[bool]()
	}
	fn := binEval(op, x.t)
	if fn == nil {
		return nil, fmt.Errorf("compile: %s: invalid operation: operator %s is not defined on %s", expr, op, x.t)
	}
	return &vmExpr{op: op, x: x, y: y, t: rt, binFn: fn}, nil
}

// compileLogic types && and ||: both sides bool, a constant side at
// the typed side's type.
func (c *Compiler) compileLogic(slots map[string]int, env map[string]reflect.Type, expr string, a arg, fx, fy arg, xConst, yConst bool) (*vmExpr, error) {
	var x, y *vmExpr
	var err error
	if !xConst {
		x, err = c.compileValueExpr(slots, env, expr, *a.x)
		if err != nil {
			return nil, err
		}
	}
	if !yConst {
		y, err = c.compileValueExpr(slots, env, expr, *a.y)
		if err != nil {
			return nil, err
		}
	}
	t := reflect.TypeFor[bool]()
	switch {
	case x != nil && y != nil:
		if x.t != y.t {
			return nil, fmt.Errorf("compile: %s: invalid operation: mismatched types %s and %s", expr, x.t, y.t)
		}
		t = x.t
	case x != nil:
		t = x.t
	case y != nil:
		t = y.t
	}
	if t.Kind() != reflect.Bool {
		return nil, fmt.Errorf("compile: %s: invalid operation: operator %s wants bool operands, not %s", expr, a.op, t)
	}
	if x == nil {
		if x, err = constExpr(t, fx); err != nil {
			return nil, fmt.Errorf("compile: %s: %w", expr, err)
		}
	}
	if y == nil {
		if y, err = constExpr(t, fy); err != nil {
			return nil, fmt.Errorf("compile: %s: %w", expr, err)
		}
	}
	return &vmExpr{op: a.op, x: x, y: y, t: t}, nil
}

// compileUnary types - ^ and !. The operand is never constant here:
// a constant subtree folded before the recursion descended.
func (c *Compiler) compileUnary(slots map[string]int, env map[string]reflect.Type, expr string, a arg) (*vmExpr, error) {
	x, err := c.compileValueExpr(slots, env, expr, *a.x)
	if err != nil {
		return nil, err
	}
	fn := unEval(a.op, x.t)
	if fn == nil {
		return nil, fmt.Errorf("compile: %s: invalid operation: operator %s is not defined on %s", expr, a.op, x.t)
	}
	return &vmExpr{op: a.op, x: x, t: x.t, unFn: fn}, nil
}

// constExpr lands a folded constant at the type its context fixed.
func constExpr(t reflect.Type, lit arg) (*vmExpr, error) {
	v, err := binopLiteral(t, lit)
	if err != nil {
		return nil, err
	}
	return &vmExpr{leaf: &vmArg{kind: vaConst, val: v, typ: t, iface: -1}, t: t}, nil
}

// opDefinedAt is the admission rule per operator: which kinds each
// one is defined on here. Go defines more, + on complex, == on
// pointers, interfaces and comparable structs, ordering on nothing
// else; all of that stays rejected, so no operator accepts what a
// layout class cannot carry or adds a panic site beyond arithmetic.
func opDefinedAt(op string, t reflect.Type) error {
	k := t.Kind()
	ok := false
	switch op {
	case "+":
		ok = addsAt(k)
	case "-", "*", "/":
		ok = stepsAt(k)
	case "%", "&", "|", "^", "&^", "<<", ">>":
		ok = intKind(k)
	case "==", "!=":
		ok = comparesAt(k)
	case "<", "<=", ">", ">=":
		ok = ordersAt(k)
	}
	if !ok {
		return fmt.Errorf("invalid operation: operator %s is not defined on %s", op, t)
	}
	return nil
}

// cmpOp reports the operators whose result is the untyped bool of
// Go's comparisons.
func cmpOp(op string) bool {
	switch op {
	case "==", "!=", "<", "<=", ">", ">=":
		return true
	}
	return false
}

// intKind reports the integer kinds, where %, the bitwise operators
// and the shifts are defined.
func intKind(k reflect.Kind) bool {
	return (k >= reflect.Int && k <= reflect.Int64) || (k >= reflect.Uint && k <= reflect.Uintptr)
}

// ordersAt reports the ordered kinds: integers, floats and strings,
// as in Go.
func ordersAt(k reflect.Kind) bool {
	return k == reflect.String || stepsAt(k)
}
