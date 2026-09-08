package gozero

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"unsafe"
)

// The JIT: a shape table of typed call templates. Under Go's internal
// ABI, argument and result passing depends only on the layout classes
// of the types, not their names: io.Reader and struct{ tab, data
// unsafe.Pointer } are the same two pointer words, *http.Request and
// unsafe.Pointer the same one, string is string. A bound func whose
// signature matches a shape in the table is reinterpreted as the shape
// type via unsafe and called directly - no reflect.Value.Call, no
// argument frame, no results slice, zero allocations on top of the
// callee's own.
//
// A signature outside the table falls back to the reflect path in
// Statement.call, which stays the general mechanism. Extending the JIT
// is additive: one more shape type and one more constructor case.
//
// The cast relies on Go's internal ABI layout classes; the equivalence
// test in runtime_test.go pins it against the reflect path.

// eface mirrors the two words of an empty interface (any).
type eface struct{ typ, data unsafe.Pointer }

// ifacePair mirrors the two words of any interface value, empty or not.
type ifacePair struct{ tab, data unsafe.Pointer }

// funcPtr returns the funcval pointer of a func value held in an any.
// Func values are pointer-shaped, so the eface data word is the funcval
// pointer itself; this holds for top-level funcs, closures and method
// values alike.
func funcPtr(fn any) unsafe.Pointer {
	return (*eface)(unsafe.Pointer(&fn)).data
}

// rtypePtr returns the *rtype behind a reflect.Type: the data word of
// the interface. Used as the type word when boxing a JIT result into an
// any.
func rtypePtr(t reflect.Type) unsafe.Pointer {
	return (*ifacePair)(unsafe.Pointer(&t)).data
}

// castFn reinterprets a funcval pointer as a func of the shape type F.
// A func variable is a single pointer to its funcval, so writing the
// pointer over a zero F is the whole conversion.
func castFn[F any](p unsafe.Pointer) F {
	var f F
	*(*unsafe.Pointer)(unsafe.Pointer(&f)) = p
	return f
}

// Shape types. s = string, e = interface (two words), p = pointer
// result, E = error result. Results are (pointer, error) or error
// alone.
type (
	fnS_PE   = func(string) (unsafe.Pointer, ifacePair)
	fnSS_PE  = func(string, string) (unsafe.Pointer, ifacePair)
	fnSSE_PE = func(string, string, ifacePair) (unsafe.Pointer, ifacePair)
	fnEE_E   = func(ifacePair, ifacePair) ifacePair
)

var strType = reflect.TypeOf("")

// ifaceConv turns a stack value into the two words of a specific
// interface type, reporting whether the value implements it.
//
// The words cannot be assembled by hand. An empty interface holds a
// *rtype where a non-empty one holds an *itab, and the itab for a
// (concrete, interface) pair is built and cached by the runtime; there
// is no exported way to reach it. A type assertion is that lookup, so
// each converter asserts to its own interface type in ordinary Go and
// reinterprets the result. The assertion is the only part that has to
// know the type statically, which is why this is a table keyed by
// reflect.Type rather than a generic function: the parameter type is
// only known at compile time of the statement, as a reflect.Type.
type ifaceConv func(v any) (ifacePair, bool)

func convTo[I any](v any) (ifacePair, bool) {
	i, ok := v.(I)
	if !ok {
		return ifacePair{}, false
	}
	return *(*ifacePair)(unsafe.Pointer(&i)), true
}

// ifaceConvs is the set of interface parameter types the JIT can fill
// from the stack. A parameter whose type is absent falls to the reflect
// tier. Extending it is one more entry.
//
// any is in the table because an eface argument is already the two
// words the callee wants; the assertion in convTo[any] always succeeds
// and copies them.
var ifaceConvs = map[reflect.Type]ifaceConv{
	reflect.TypeFor[any]():       convTo[any],
	reflect.TypeFor[io.Writer](): convTo[io.Writer],
	reflect.TypeFor[io.Reader](): convTo[io.Reader],
}

// ifaceArg is one interface parameter of a JIT'd call: left zero (a nil
// interface) when the statement omits it, otherwise a variable resolved
// against the stack per call and converted to the parameter's interface
// type.
type ifaceArg struct {
	name string // variable name; "" when the slot is zero-filled
	typ  reflect.Type
	conv ifaceConv
}

func (a *ifaceArg) get(stack map[string]any) (ifacePair, error) {
	if a.name == "" {
		return ifacePair{}, nil
	}
	v, ok := stack[a.name]
	if !ok || v == nil {
		return ifacePair{}, nil
	}
	p, ok := a.conv(v)
	if !ok {
		return ifacePair{}, fmt.Errorf("exec: variable %q: cannot use %T as %s", a.name, v, a.typ)
	}
	return p, nil
}

// strArg is one string parameter of a JIT'd call: a literal value, or a
// variable name resolved against the stack per call with the same
// semantics as __get (unset or nil fills "", a non-string is an error).
type strArg struct {
	name string // variable name; "" for a literal
	val  string // literal value
}

func (a *strArg) get(stack map[string]any) (string, error) {
	if a.name == "" {
		return a.val, nil
	}
	v, ok := stack[a.name]
	if !ok || v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("exec: variable %q: cannot use %T as string", a.name, v)
	}
	return s, nil
}

// jitResult boxes the two raw result words back into (any, error). The
// error pair, when set, already is a valid error interface. The pointer
// result becomes an any by writing the eface words directly: type word
// from the binding's result type, data word the pointer - the exact
// representation the compiler would produce, without an allocation.
func jitResult(out, p unsafe.Pointer, e ifacePair) (any, error) {
	if e.tab != nil {
		return nil, *(*error)(unsafe.Pointer(&e))
	}
	var res any
	*(*eface)(unsafe.Pointer(&res)) = eface{typ: out, data: p}
	return res, nil
}

// jitCompile builds the direct-call closure for a validated statement,
// or returns nil when the binding's signature is outside the shape
// table. Supported: results (pointer, error) or error alone; parameters
// that are plain strings (literal, variable, or missing and zero-filled
// to ""), followed by interface parameters that are either omitted, or
// fed from the stack when the interface type is in ifaceConvs.
func jitCompile(fn any, ft reflect.Type, call *callExpr) CompiledFunc {
	if fn == nil {
		return nil
	}
	var ptrResult bool
	switch {
	case ft.NumOut() == 2 && ft.Out(0).Kind() == reflect.Pointer && ft.Out(1) == errType:
		ptrResult = true
	case ft.NumOut() == 1 && ft.Out(0) == errType:
		ptrResult = false
	default:
		return nil
	}

	n := ft.NumIn()
	var sa [2]strArg
	var ea [2]ifaceArg
	ns, ne := 0, 0 // leading string params, trailing interface params
	for i := 0; i < n; i++ {
		pt := ft.In(i)
		switch {
		case pt == strType && ne == 0 && ns < len(sa):
			if i < len(call.args) {
				switch call.args[i].kind {
				case argString:
					sa[ns].val = call.args[i].str
				case argVar:
					sa[ns].name = call.args[i].str
				default:
					return nil
				}
			}
			ns++
		case pt.Kind() == reflect.Interface && ne < len(ea):
			conv, ok := ifaceConvs[pt]
			if !ok {
				return nil
			}
			ea[ne].typ, ea[ne].conv = pt, conv
			if i < len(call.args) {
				if call.args[i].kind != argVar {
					return nil
				}
				ea[ne].name = call.args[i].str
			}
			ne++
		default:
			return nil
		}
	}

	var out unsafe.Pointer
	if ptrResult {
		out = rtypePtr(ft.Out(0))
	}
	fptr := funcPtr(fn)
	switch {
	case ptrResult && ns == 1 && ne == 0 && n == 1:
		f, a0 := castFn[fnS_PE](fptr), sa[0]
		return func(_ context.Context, stack map[string]any, _ any) (any, error) {
			s0, err := a0.get(stack)
			if err != nil {
				return nil, err
			}
			p, e := f(s0)
			return jitResult(out, p, e)
		}
	case ptrResult && ns == 2 && ne == 0 && n == 2:
		f, a0, a1 := castFn[fnSS_PE](fptr), sa[0], sa[1]
		return func(_ context.Context, stack map[string]any, _ any) (any, error) {
			s0, err := a0.get(stack)
			if err != nil {
				return nil, err
			}
			s1, err := a1.get(stack)
			if err != nil {
				return nil, err
			}
			p, e := f(s0, s1)
			return jitResult(out, p, e)
		}
	case ptrResult && ns == 2 && ne == 1 && n == 3:
		f, a0, a1, e0 := castFn[fnSSE_PE](fptr), sa[0], sa[1], ea[0]
		return func(_ context.Context, stack map[string]any, _ any) (any, error) {
			s0, err := a0.get(stack)
			if err != nil {
				return nil, err
			}
			s1, err := a1.get(stack)
			if err != nil {
				return nil, err
			}
			i0, err := e0.get(stack)
			if err != nil {
				return nil, err
			}
			p, e := f(s0, s1, i0)
			return jitResult(out, p, e)
		}
	case !ptrResult && ns == 0 && ne == 2 && n == 2:
		f, e0, e1 := castFn[fnEE_E](fptr), ea[0], ea[1]
		return func(_ context.Context, stack map[string]any, _ any) (any, error) {
			i0, err := e0.get(stack)
			if err != nil {
				return nil, err
			}
			i1, err := e1.get(stack)
			if err != nil {
				return nil, err
			}
			return jitError(f(i0, i1))
		}
	}
	return nil
}

// jitError turns the raw error words of an error-only result back into
// an error. A zero tab is a nil interface, so a nil error.
func jitError(e ifacePair) (any, error) {
	if e.tab != nil {
		return nil, *(*error)(unsafe.Pointer(&e))
	}
	return nil, nil
}
