package gozero

import (
	"context"
	"fmt"
	"reflect"
	"unsafe" // also required by go:linkname
)

// stmtNode compiles one statement into the closure the program runs.
func (c *jitCompiler) stmtNode(s plannedStmt, jp *jitProgram) (nodeE, error) {
	if s.fieldSet != nil {
		return c.fieldSetNode(s.fieldSet)
	}
	var n node
	if s.lit.IsValid() {
		field, ok := c.slotOf[s.out]
		if !ok {
			return nil, fmt.Errorf("a literal is assigned to a name with no slot")
		}
		cl := layoutOf(c.types[field])
		if cl == lBad {
			return nil, fmt.Errorf("a literal of type %s has no layout class", c.types[field])
		}
		lit, err := constNode(s.lit, cl)
		if err != nil {
			return nil, err
		}
		n = lit
	} else {
		var err error
		n, err = c.exprNode(s.call)
		if err != nil {
			return nil, err
		}
	}
	if s.out < 0 {
		// The result is dropped, but the call still runs and its error
		// still ends the program.
		return c.dropped(n)
	}

	field, ok := c.slotOf[s.out]
	if !ok {
		return c.dropped(n)
	}
	off := c.offs[field]
	if s.ret {
		jp.retType, jp.retOff = c.types[field], off
	}

	if n.class.scalar() {
		cl := n.class
		if cl.float() {
			f := n.F
			return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
				v, err := f(fr, ctx, st, d)
				if err != nil {
					return err
				}
				storeF(cl, unsafe.Add(fr, off), v)
				return nil
			}, nil
		}
		f := n.N
		return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
			v, err := f(fr, ctx, st, d)
			if err != nil {
				return err
			}
			storeN(cl, unsafe.Add(fr, off), v)
			return nil
		}, nil
	}
	switch n.class {
	case lPtr:
		f := n.P
		return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
			v, err := f(fr, ctx, st, d)
			if err != nil {
				return err
			}
			*(*unsafe.Pointer)(unsafe.Add(fr, off)) = v
			return nil
		}, nil
	case lSlice:
		f := n.L
		return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
			v, err := f(fr, ctx, st, d)
			if err != nil {
				return err
			}
			*(*sliceHdr)(unsafe.Add(fr, off)) = v
			return nil
		}, nil
	case lStr:
		f := n.S
		return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
			v, err := f(fr, ctx, st, d)
			if err != nil {
				return err
			}
			*(*string)(unsafe.Add(fr, off)) = v
			return nil
		}, nil
	case lIface:
		f := n.I
		return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
			v, err := f(fr, ctx, st, d)
			if err != nil {
				return err
			}
			*(*ifacePair)(unsafe.Add(fr, off)) = v
			return nil
		}, nil
	}
	return nil, fmt.Errorf("a result of class %s cannot be stored", n.class)
}

// fieldSetNode compiles req.Method = value to a typed store at a
// compile-time offset. Only the single field of a pointer to a struct
// is in the table; a value-struct base or a deeper chain stays on the
// reflect evaluator.
func (c *jitCompiler) fieldSetNode(fs *vmFieldSet) (nodeE, error) {
	field, ok := c.slotOf[fs.base]
	if !ok {
		return nil, fmt.Errorf("a field target has no slot")
	}
	bt := c.types[field]
	if len(fs.steps) != 1 || len(fs.steps[0].index) != 1 {
		return nil, fmt.Errorf("only a single field is in the table")
	}
	var sf reflect.StructField
	var direct bool // the struct lives in the frame itself
	switch {
	case fs.steps[0].deref && bt.Kind() == reflect.Pointer && bt.Elem().Kind() == reflect.Struct:
		sf = bt.Elem().Field(fs.steps[0].index[0])
	case !fs.steps[0].deref && bt.Kind() == reflect.Struct:
		sf = bt.Field(fs.steps[0].index[0])
		direct = true
	default:
		return nil, fmt.Errorf("a field of a %s is not in the table", bt)
	}
	cl := layoutOf(sf.Type)
	if cl == lBad {
		return nil, fmt.Errorf("field %s of type %s has no layout class", sf.Name, sf.Type)
	}

	var val node
	switch fs.val.kind {
	case vaConst:
		v, err := constNode(fs.val.val, cl)
		if err != nil {
			return nil, err
		}
		val = v
	case vaCall:
		v, err := c.exprNode(fs.val.sub)
		if err != nil {
			return nil, err
		}
		if v.class != cl {
			return nil, fmt.Errorf("a %s result cannot fill a %s field", v.class, cl)
		}
		val = v
	default:
		return nil, fmt.Errorf("this field value is not in the table")
	}

	baseOff, fieldOff, name, srcType := c.offs[field], sf.Offset, fs.field, bt
	var target func(fr unsafe.Pointer) (unsafe.Pointer, error)
	if direct {
		at := baseOff + fieldOff
		target = func(fr unsafe.Pointer) (unsafe.Pointer, error) {
			return unsafe.Add(fr, at), nil
		}
	} else {
		target = func(fr unsafe.Pointer) (unsafe.Pointer, error) {
			base := *(*unsafe.Pointer)(unsafe.Add(fr, baseOff))
			if base == nil {
				return nil, fmt.Errorf("exec: %s: field write on a nil %s", name, srcType)
			}
			return unsafe.Add(base, fieldOff), nil
		}
	}

	if cl.scalar() {
		if cl.float() {
			f := val.F
			return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
				v, err := f(fr, ctx, st, d)
				if err != nil {
					return err
				}
				at, err := target(fr)
				if err != nil {
					return err
				}
				storeF(cl, at, v)
				return nil
			}, nil
		}
		f := val.N
		return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
			v, err := f(fr, ctx, st, d)
			if err != nil {
				return err
			}
			at, err := target(fr)
			if err != nil {
				return err
			}
			storeN(cl, at, v)
			return nil
		}, nil
	}
	switch cl {
	case lStr:
		f := val.S
		return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
			v, err := f(fr, ctx, st, d)
			if err != nil {
				return err
			}
			at, err := target(fr)
			if err != nil {
				return err
			}
			*(*string)(at) = v
			return nil
		}, nil
	case lPtr:
		f := val.P
		return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
			v, err := f(fr, ctx, st, d)
			if err != nil {
				return err
			}
			at, err := target(fr)
			if err != nil {
				return err
			}
			*(*unsafe.Pointer)(at) = v
			return nil
		}, nil
	case lSlice:
		f := val.L
		return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
			v, err := f(fr, ctx, st, d)
			if err != nil {
				return err
			}
			at, err := target(fr)
			if err != nil {
				return err
			}
			*(*sliceHdr)(at) = v
			return nil
		}, nil
	case lIface:
		f := val.I
		return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
			v, err := f(fr, ctx, st, d)
			if err != nil {
				return err
			}
			at, err := target(fr)
			if err != nil {
				return err
			}
			*(*ifacePair)(at) = v
			return nil
		}, nil
	}
	return nil, fmt.Errorf("a %s field cannot be stored", cl)
}

// dropped wraps a node whose value nothing binds, reporting a class
// that cannot be run rather than returning a nil closure.
func (c *jitCompiler) dropped(n node) (nodeE, error) {
	if e := dropNode(n); e != nil {
		return e, nil
	}
	return nil, fmt.Errorf("a result of class %s cannot be discarded", n.class)
}

// dropNode runs a node for its effects and discards its value. It
// returns nil for a class it cannot run, which the caller reports.
func dropNode(n node) nodeE {
	if n.class.scalar() {
		if n.class.float() {
			f := n.F
			return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
				_, err := f(fr, ctx, st, d)
				return err
			}
		}
		f := n.N
		return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
			_, err := f(fr, ctx, st, d)
			return err
		}
	}
	switch n.class {
	case lNone:
		return n.E
	case lPtr:
		f := n.P
		return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
			_, err := f(fr, ctx, st, d)
			return err
		}
	case lSlice:
		f := n.L
		return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
			_, err := f(fr, ctx, st, d)
			return err
		}
	case lStr:
		f := n.S
		return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
			_, err := f(fr, ctx, st, d)
			return err
		}
	case lIface:
		f := n.I
		return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
			_, err := f(fr, ctx, st, d)
			return err
		}
	}
	return nil
}
