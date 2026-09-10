package gozero

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"unsafe" // also required by go:linkname
)

// Control flow on the direct tier. Blocks are slices of nodeE; break,
// continue and return travel as package-private sentinel errors a
// binding cannot forge, intercepted by the loop nodes and by run.
// A return inside flow boxes its value into a hidden any field and
// signals; run reads the field back, so every return site shares one
// typed channel out whatever each returns.

var (
	errJITBreak    = errors.New("jit: break escaped its loop")
	errJITContinue = errors.New("jit: continue escaped its loop")
	errJITReturn   = errors.New("jit: return")
)

// flowNode compiles one control-flow statement.
func (c *jitCompiler) flowNode(s *vmStmt, jp *jitProgram) (nodeE, error) {
	switch {
	case s.deferCall != nil:
		return c.deferFlowNode(s, jp)
	case s.call != nil && s.call.bindErr:
		return c.bindErrNode(s)
	case s.ifs != nil:
		return c.ifFlowNode(s.ifs, jp)
	case s.loop != nil:
		return c.forFlowNode(s.loop, jp)
	case s.rng != nil:
		return c.rangeFlowNode(s.rng, jp)
	case s.brk:
		return func(unsafe.Pointer, context.Context, map[string]any, any) error {
			return errJITBreak
		}, nil
	case s.cont:
		return func(unsafe.Pointer, context.Context, map[string]any, any) error {
			return errJITContinue
		}, nil
	case s.init != nil:
		return c.initFlowNode(s.init)
	case s.retList != nil:
		return c.retListNode(s.retList)
	case s.retArg != nil:
		return c.returnFlowNode(s.retArg, jp)
	case s.ret:
		return c.bareReturnNode(jp)
	}
	return nil, fmt.Errorf("this flow statement is not in the table")
}

// blockNodes compiles a nested statement list.
func (c *jitCompiler) blockNodes(stmts []vmStmt, jp *jitProgram) ([]nodeE, error) {
	out := make([]nodeE, 0, len(stmts))
	for i := range stmts {
		ps, err := planStmt(&stmts[i])
		if err != nil {
			return nil, err
		}
		n, err := c.stmtNode(ps, jp)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

func runNodes(stmts []nodeE, fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
	for _, stmt := range stmts {
		if err := stmt(fr, ctx, st, d); err != nil {
			return err
		}
	}
	return nil
}

// condNode compiles a condition to the bool bits the branches test.
func (c *jitCompiler) condNode(a *vmArg) (nodeN, error) {
	n, err := c.valueNode(a)
	if err != nil {
		return nil, err
	}
	if n.class != lBool {
		return nil, fmt.Errorf("a condition of class %s is not in the table", n.class)
	}
	return n.N, nil
}

func (c *jitCompiler) ifFlowNode(n *vmIf, jp *jitProgram) (nodeE, error) {
	cond, err := c.condNode(n.cond)
	if err != nil {
		return nil, err
	}
	then, err := c.blockNodes(n.then.stmts, jp)
	if err != nil {
		return nil, err
	}
	var els []nodeE
	if n.els != nil {
		if els, err = c.blockNodes(n.els.stmts, jp); err != nil {
			return nil, err
		}
	}
	return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
		v, err := cond(fr, ctx, st, d)
		if err != nil {
			return err
		}
		if v != 0 {
			return runNodes(then, fr, ctx, st, d)
		}
		return runNodes(els, fr, ctx, st, d)
	}, nil
}

func (c *jitCompiler) forFlowNode(l *vmFor, jp *jitProgram) (nodeE, error) {
	init, err := c.blockNodes(l.init, jp)
	if err != nil {
		return nil, err
	}
	var cond nodeN
	if l.cond != nil {
		if cond, err = c.condNode(l.cond); err != nil {
			return nil, err
		}
	}
	post, err := c.blockNodes(l.post, jp)
	if err != nil {
		return nil, err
	}
	body, err := c.blockNodes(l.body.stmts, jp)
	if err != nil {
		return nil, err
	}
	return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
		if err := runNodes(init, fr, ctx, st, d); err != nil {
			return err
		}
		for {
			// The context bounds the loop, as it does on the reflect
			// tier: a cancelled run does not spin on.
			if err := ctx.Err(); err != nil {
				return err
			}
			if cond != nil {
				v, err := cond(fr, ctx, st, d)
				if err != nil {
					return err
				}
				if v == 0 {
					return nil
				}
			}
			switch err := runNodes(body, fr, ctx, st, d); err {
			case nil, errJITContinue:
			case errJITBreak:
				return nil
			default:
				return err
			}
			if err := runNodes(post, fr, ctx, st, d); err != nil {
				return err
			}
		}
	}, nil
}

// initFlowNode re-zeroes a block-scoped var at its position on every
// pass, the typed clear keeping the write barriers.
func (c *jitCompiler) initFlowNode(in *slotInit) (nodeE, error) {
	field, ok := c.slotOf[in.slot]
	if !ok {
		return nil, fmt.Errorf("a block var has no slot")
	}
	off := c.offs[field]
	rt := rtypePtr(c.types[field])
	return func(fr unsafe.Pointer, _ context.Context, _ map[string]any, _ any) error {
		typedmemclr(rt, unsafe.Add(fr, off))
		return nil
	}, nil
}

// returnFlowNode boxes the return value into the hidden any field
// and signals; run reads it back.
func (c *jitCompiler) returnFlowNode(a *vmArg, jp *jitProgram) (nodeE, error) {
	if c.retAnyField < 0 {
		return nil, fmt.Errorf("a return has no boxed channel")
	}
	n, err := c.valueNode(a)
	if err != nil {
		return nil, err
	}
	t := a.typ
	if t == nil {
		return nil, fmt.Errorf("a returned value has no static type")
	}
	box, err := boxAnyNode(t, n)
	if err != nil {
		return nil, err
	}
	off := c.offs[c.retAnyField]
	return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
		v, err := box(fr, ctx, st, d)
		if err != nil {
			return err
		}
		*(*any)(unsafe.Add(fr, off)) = v
		return errJITReturn
	}, nil
}

func (c *jitCompiler) bareReturnNode(jp *jitProgram) (nodeE, error) {
	if c.retAnyField < 0 {
		return nil, fmt.Errorf("a return has no boxed channel")
	}
	off := c.offs[c.retAnyField]
	return func(fr unsafe.Pointer, _ context.Context, _ map[string]any, _ any) error {
		*(*any)(unsafe.Add(fr, off)) = nil
		return errJITReturn
	}, nil
}

// boxAnyNode wraps a node's value as any at the value's static type:
// a typed cell filled by the class store, read out once through
// reflect. A return runs once per run, so the two allocations stay
// off every hot path.
func boxAnyNode(t reflect.Type, n node) (func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (any, error), error) {
	rt := rtypePtr(t)
	cl := n.class
	store := func(cell unsafe.Pointer, fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
		switch {
		case cl.scalar() && cl.float():
			v, err := n.F(fr, ctx, st, d)
			if err != nil {
				return err
			}
			storeF(cl, cell, v)
		case cl.scalar():
			v, err := n.N(fr, ctx, st, d)
			if err != nil {
				return err
			}
			storeN(cl, cell, v)
		case cl == lPtr:
			v, err := n.P(fr, ctx, st, d)
			if err != nil {
				return err
			}
			*(*unsafe.Pointer)(cell) = v
		case cl == lStr:
			v, err := n.S(fr, ctx, st, d)
			if err != nil {
				return err
			}
			*(*string)(cell) = v
		case cl == lSlice:
			v, err := n.L(fr, ctx, st, d)
			if err != nil {
				return err
			}
			*(*sliceHdr)(cell) = v
		case cl == lIface:
			v, err := n.I(fr, ctx, st, d)
			if err != nil {
				return err
			}
			*(*ifacePair)(cell) = v
		default:
			return fmt.Errorf("a %s value cannot be boxed", cl)
		}
		return nil
	}
	if layoutOf(t) != cl {
		return nil, fmt.Errorf("a %s value does not box as %s", cl, t)
	}
	return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) (any, error) {
		cell := unsafeNew(rt)
		if err := store(cell, fr, ctx, st, d); err != nil {
			return nil, err
		}
		return reflect.NewAt(t, cell).Elem().Interface(), nil
	}, nil
}

// retListNode stores a function body's declared results into their
// typed fields and signals the return.
func (c *jitCompiler) retListNode(rets []*vmArg) (nodeE, error) {
	if len(rets) != len(c.resultFields) {
		return nil, fmt.Errorf("a return of %d values does not fit %d result fields", len(rets), len(c.resultFields))
	}
	type slotStore struct {
		off uintptr
		cl  layout
		n   node
	}
	stores := make([]slotStore, len(rets))
	for i, ra := range rets {
		n, err := c.valueNode(ra)
		if err != nil {
			return nil, err
		}
		field := c.resultFields[i]
		cl := layoutOf(c.types[field])
		if n.class != cl {
			if cl == lIface {
				conv, err := c.toIface(ra.typ, c.types[field], n)
				if err != nil {
					return nil, err
				}
				n = conv
			} else {
				return nil, fmt.Errorf("a %s value does not fill a %s result", n.class, cl)
			}
		}
		stores[i] = slotStore{off: c.offs[field], cl: cl, n: n}
	}
	return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
		for _, sp := range stores {
			at := unsafe.Add(fr, sp.off)
			switch {
			case sp.cl.scalar() && sp.cl.float():
				v, err := sp.n.F(fr, ctx, st, d)
				if err != nil {
					return err
				}
				storeF(sp.cl, at, v)
			case sp.cl.scalar():
				v, err := sp.n.N(fr, ctx, st, d)
				if err != nil {
					return err
				}
				storeN(sp.cl, at, v)
			case sp.cl == lPtr:
				v, err := sp.n.P(fr, ctx, st, d)
				if err != nil {
					return err
				}
				*(*unsafe.Pointer)(at) = v
			case sp.cl == lStr:
				v, err := sp.n.S(fr, ctx, st, d)
				if err != nil {
					return err
				}
				*(*string)(at) = v
			case sp.cl == lSlice:
				v, err := sp.n.L(fr, ctx, st, d)
				if err != nil {
					return err
				}
				*(*sliceHdr)(at) = v
			case sp.cl == lIface:
				v, err := sp.n.I(fr, ctx, st, d)
				if err != nil {
					return err
				}
				*(*ifacePair)(at) = v
			}
		}
		return errJITReturn
	}, nil
}
