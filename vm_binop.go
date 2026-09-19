package gozero

import (
	"fmt"
	"reflect"
	"strconv"
)

// The operator assignment, "s := a + b;". One binary operator per
// statement is the whole expression grammar: + concatenates strings
// and adds integers and floats, == and != compare booleans, integers,
// floats and strings. The typing rule is one line of Go's: operands
// need identical static types, and a literal side adopts the named
// side's type, checked for representability at compile time. Two
// literal operands are rejected rather than folded, because folding
// is go/constant's job and reimplementing its arithmetic is exactly
// what this design refuses.

// vmBinop is a compiled operator assignment: the operands, the
// operator, and the type the operation runs at. An operand is vaSlot
// or vaConst, never anything that could call or allocate.
type vmBinop struct {
	op   string       // "+", "==" or "!="
	x, y *vmArg       // vaSlot or vaConst
	t    reflect.Type // the identical operand type
	name string       // "a + b", for diagnostics
}

// resultType is the static type of the assigned name: the operand
// type for +, the predeclared bool for a comparison, as in Go.
func (b *vmBinop) resultType() reflect.Type {
	if b.op == "+" {
		return b.t
	}
	return reflect.TypeFor[bool]()
}

// spellArg spells an operand back the way the source wrote it, for
// error messages.
func spellArg(a arg) string {
	switch a.kind {
	case argString:
		return strconv.Quote(a.str)
	case argInt:
		return strconv.FormatInt(a.i, 10)
	case argFloat:
		return strconv.FormatFloat(a.f, 'g', -1, 64)
	case argBool:
		return strconv.FormatBool(a.b)
	case argVar:
		return a.str
	case argPath:
		return joinPath(a.path)
	case argNil:
		return "nil"
	case argCall:
		return joinPath(a.sub.path) + "(...)"
	case argStruct:
		return joinPath(a.path) + "{...}"
	case argRecv:
		return "<-" + spellArg(*a.recv)
	}
	return "a value"
}

// addsAt reports whether + is defined at a kind: strings, integers
// and floats. Go also adds complex numbers; no layout class carries
// one, so they stay rejected by the same rule that rejects them for
// ++ and --.
func addsAt(k reflect.Kind) bool {
	return k == reflect.String || stepsAt(k)
}

// comparesAt reports the kinds == and != cover here: the built-in
// comparable scalars and strings. Go compares more - pointers,
// channels, interfaces, comparable structs and arrays - and an
// interface comparison can panic at run time; all of those stay
// rejected, so no operator adds a panic site to the language.
func comparesAt(k reflect.Kind) bool {
	return k == reflect.Bool || k == reflect.String || stepsAt(k)
}

// compileBinop compiles "s := x op y". Both operands resolve here: a
// name to its slot and static type, a literal to a constant at the
// other side's type.
func (c *Compiler) compileBinop(slots map[string]int, env map[string]reflect.Type, s stmt) (*vmBinop, error) {
	expr := spellArg(*s.binX) + " " + s.binOp + " " + spellArg(*s.binY)
	xa, xt, err := c.binopOperand(slots, env, expr, *s.binX)
	if err != nil {
		return nil, err
	}
	ya, yt, err := c.binopOperand(slots, env, expr, *s.binY)
	if err != nil {
		return nil, err
	}
	var t reflect.Type
	switch {
	case xa == nil && ya == nil:
		return nil, fmt.Errorf("compile: %s: both operands are literals, write the value it folds to", expr)
	case xa == nil:
		t = yt
		v, err := binopLiteral(t, *s.binX)
		if err != nil {
			return nil, fmt.Errorf("compile: %s: %w", expr, err)
		}
		xa = &vmArg{kind: vaConst, val: v, typ: t, iface: -1}
	case ya == nil:
		t = xt
		v, err := binopLiteral(t, *s.binY)
		if err != nil {
			return nil, fmt.Errorf("compile: %s: %w", expr, err)
		}
		ya = &vmArg{kind: vaConst, val: v, typ: t, iface: -1}
	default:
		if xt != yt {
			return nil, fmt.Errorf("compile: %s: invalid operation: mismatched types %s and %s", expr, xt, yt)
		}
		t = xt
	}
	if t == nil {
		return nil, fmt.Errorf("compile: %s: an operand has no type", expr)
	}
	switch s.binOp {
	case "+":
		if !addsAt(t.Kind()) {
			return nil, fmt.Errorf("compile: %s: operator + is not defined on %s, it concatenates strings and adds integers and floats", expr, t)
		}
	default:
		if !comparesAt(t.Kind()) {
			return nil, fmt.Errorf("compile: %s: == and != compare booleans, integers, floats and strings, not %s", expr, t)
		}
	}
	return &vmBinop{op: s.binOp, x: xa, y: ya, t: t, name: expr}, nil
}

// binopOperand resolves one operand. A bound name comes back as its
// slot with its static type; a literal comes back nil, typed by the
// caller once the other side's type is known. Everything else is
// rejected by rule: an operand never calls, reads a field, builds a
// value or blocks.
func (c *Compiler) binopOperand(slots map[string]int, env map[string]reflect.Type, expr string, a arg) (*vmArg, reflect.Type, error) {
	switch a.kind {
	case argVar:
		if slot, ok := slots[a.str]; ok {
			t := env[a.str]
			return &vmArg{kind: vaSlot, slot: slot, name: a.str, typ: t, iface: -1}, t, nil
		}
		return nil, nil, fmt.Errorf("compile: %s: %s has no static type here; a name read from the stack cannot be an operand, bind it with := first", expr, a.str)
	case argString, argInt, argFloat, argBool:
		return nil, nil, nil
	}
	return nil, nil, fmt.Errorf("compile: %s: %s cannot be an operand, only a name bound by the program or a literal can", expr, spellArg(a))
}

// binopLiteral converts a literal operand to the named side's type.
// It exists beside literalValue because Go's untyped constants
// convert to named string and bool types, which AssignableTo does not
// cover; the numeric kinds go through the same representability
// checks every literal argument gets.
func binopLiteral(t reflect.Type, a arg) (reflect.Value, error) {
	switch t.Kind() {
	case reflect.String:
		if a.kind != argString {
			return reflect.Value{}, fmt.Errorf("cannot use %s as %s", spellArg(a), t)
		}
		v := reflect.New(t).Elem()
		v.SetString(a.str)
		return v, nil
	case reflect.Bool:
		if a.kind != argBool {
			return reflect.Value{}, fmt.Errorf("cannot use %s as %s", spellArg(a), t)
		}
		v := reflect.New(t).Elem()
		v.SetBool(a.b)
		return v, nil
	}
	if a.kind == argString || a.kind == argBool {
		return reflect.Value{}, fmt.Errorf("cannot use %s as %s", spellArg(a), t)
	}
	return literalAs(t, a)
}

// operand reads one operand's current value.
func (b *vmBinop) operand(slots []reflect.Value, a *vmArg) reflect.Value {
	if a.kind == vaConst {
		return a.val
	}
	v := slots[a.slot]
	if !v.IsValid() {
		return reflect.Zero(a.typ)
	}
	return v
}

// exec computes the statement's value. The arithmetic mirrors what
// compiled Go does at the type's width: an integer sum wraps by
// masking unsigned bits or shift-truncating signed ones, the way a
// step does in vm_inc.go; a float32 sum rounds once at 32 bits, and
// an overflow becomes the infinity the native operation produces.
// Comparisons are Go's own == on the extracted values, so a NaN
// compares unequal to itself.
func (b *vmBinop) exec(slots []reflect.Value) (reflect.Value, error) {
	x := b.operand(slots, b.x)
	y := b.operand(slots, b.y)
	if b.op == "+" {
		out := reflect.New(b.t).Elem()
		switch b.t.Kind() {
		case reflect.String:
			out.SetString(x.String() + y.String())
		case reflect.Float32, reflect.Float64:
			sum := x.Float() + y.Float()
			if b.t.Kind() == reflect.Float32 {
				// The float64 sum of two float32 values is exact enough
				// that rounding it once to 32 bits is the rounding
				// compiled Go performs; SetFloat then sees a value that
				// is either representable or an infinity.
				sum = float64(float32(sum))
			}
			out.SetFloat(sum)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			u := x.Uint() + y.Uint()
			if bits := b.t.Bits(); bits < 64 {
				u &= 1<<bits - 1
			}
			out.SetUint(u)
		default:
			n := x.Int() + y.Int()
			if bits := b.t.Bits(); bits < 64 {
				sh := 64 - bits
				n = n << sh >> sh
			}
			out.SetInt(n)
		}
		return out, nil
	}
	var eq bool
	switch b.t.Kind() {
	case reflect.String:
		eq = x.String() == y.String()
	case reflect.Bool:
		eq = x.Bool() == y.Bool()
	case reflect.Float32, reflect.Float64:
		eq = x.Float() == y.Float()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		eq = x.Uint() == y.Uint()
	default:
		eq = x.Int() == y.Int()
	}
	if b.op == "!=" {
		eq = !eq
	}
	return reflect.ValueOf(eq), nil
}
