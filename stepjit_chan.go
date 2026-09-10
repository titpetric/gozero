package gozero

import (
	"context"
	"reflect"
	"unsafe" // also required by go:linkname
)

// The channel operations on the direct tier. Receive and send are
// language operations rather than bound calls: the channel and the
// value resolve through the same producers the bridge uses, the
// operation goes through chanRecv and chanSend in vm_chan.go (a try
// fast path, then the context-armed select), and the received value
// stores into the frame through a typed reflect.Set, which keeps the
// write barrier. Neither appears in Supports output: a program of
// receives and sends is a direct program.

// recvNode compiles v := <-c. A closed channel returns io.EOF, the
// implicit ok; cancellation returns ctx.Err(); both end the program.
func (c *jitCompiler) recvNode(s plannedStmt) (nodeE, error) {
	chGet, err := c.bridgeArg(s.recv.ch)
	if err != nil {
		return nil, err
	}
	et := s.recv.elem
	valOff, hasVal := uintptr(0), false
	if s.out >= 0 {
		if field, ok := c.slotOf[s.out]; ok {
			valOff, hasVal = c.offs[field], true
		}
	}
	return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
		chv, err := chGet(fr, ctx, st, d)
		if err != nil {
			return err
		}
		v, err := chanRecv(ctx, chv)
		if err != nil {
			return err
		}
		if hasVal {
			reflect.NewAt(et, unsafe.Add(fr, valOff)).Elem().Set(v)
		}
		return nil
	}, nil
}

// sendNode compiles c <- v.
func (c *jitCompiler) sendNode(s *vmSend) (nodeE, error) {
	chGet, err := c.bridgeArg(s.ch)
	if err != nil {
		return nil, err
	}
	valGet, err := c.bridgeArg(s.val)
	if err != nil {
		return nil, err
	}
	return func(fr unsafe.Pointer, ctx context.Context, st map[string]any, d any) error {
		chv, err := chGet(fr, ctx, st, d)
		if err != nil {
			return err
		}
		v, err := valGet(fr, ctx, st, d)
		if err != nil {
			return err
		}
		return chanSend(ctx, chv, v)
	}, nil
}
