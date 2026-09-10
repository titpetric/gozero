package gozero

import (
	"context"
	"fmt"
	"io"
	"reflect"
)

// Channel receive and send. Both operations run a two-case
// reflect.Select: the operation itself, and a receive on the execution
// context's Done channel, so a blocked program ends with ctx.Err()
// when the context is cancelled instead of hanging past its deadline.
//
// The ok of Go's two-value receive is implicit the way the trailing
// error of a call is: never a value, checked after every receive, and
// a receive from a closed channel ends the program with io.EOF. The
// host distinguishes end-of-data from failure with errors.Is. A nil
// channel blocks until cancellation, and a send on a closed channel
// panics as it does in Go, arriving as *PanicError through the guard.

// vmRecv is a compiled receive, v := <-c. The bound slot is the
// statement's out.
type vmRecv struct {
	ch   *vmArg
	elem reflect.Type
}

// vmSend is a compiled send, c <- v.
type vmSend struct {
	ch  *vmArg
	val *vmArg
}

// chanSource resolves the channel of a receive or send to an argument
// with a static channel type: a name the program bound, a field read
// off one, or a call. A stack name is rejected the way a method on one
// is, because its type is only known at execution.
func (c *Compiler) chanSource(slots map[string]int, env map[string]reflect.Type, a arg) (*vmArg, reflect.Type, error) {
	switch a.kind {
	case argVar:
		slot, ok := slots[a.str]
		if !ok {
			return nil, nil, fmt.Errorf("compile: %s is not a name bound by the program; a channel from the stack has no static type", a.str)
		}
		t := env[a.str]
		return &vmArg{kind: vaSlot, slot: slot, name: a.str, typ: t, iface: -1}, t, nil
	case argPath:
		slot, ok := slots[a.path[0]]
		if !ok {
			return nil, nil, fmt.Errorf("compile: %s is not a name bound by the program", a.path[0])
		}
		cur := &vmArg{kind: vaSlot, slot: slot, name: a.path[0], typ: env[a.path[0]], iface: -1}
		curType := env[a.path[0]]
		for _, seg := range a.path[1:] {
			f, deref, ok := fieldOf(curType, seg)
			if !ok {
				return nil, nil, fmt.Errorf("compile: %s has no field %s", curType, seg)
			}
			cur = &vmArg{kind: vaField, src: cur, index: f.Index, deref: deref, typ: f.Type, iface: -1}
			curType = f.Type
		}
		return cur, curType, nil
	case argCall:
		sub, st, err := c.compileExpr(slots, env, a.sub)
		if err != nil {
			return nil, nil, err
		}
		return &vmArg{kind: vaCall, sub: sub, typ: st, iface: -1}, st, nil
	}
	return nil, nil, fmt.Errorf("compile: expected a channel")
}

// compileRecv compiles the channel half of a receive; the caller binds
// the value and ok slots against elem.
func (c *Compiler) compileRecv(slots map[string]int, env map[string]reflect.Type, a arg) (*vmRecv, error) {
	ch, t, err := c.chanSource(slots, env, *a.recv)
	if err != nil {
		return nil, err
	}
	if t == nil || t.Kind() != reflect.Chan {
		return nil, fmt.Errorf("compile: cannot receive from a %s", t)
	}
	if t.ChanDir() == reflect.SendDir {
		return nil, fmt.Errorf("compile: cannot receive from the send-only %s", t)
	}
	return &vmRecv{ch: ch, elem: t.Elem()}, nil
}

// compileSend compiles c <- v: the channel from the statement's path,
// the value against the element type like any argument.
func (c *Compiler) compileSend(slots map[string]int, env map[string]reflect.Type, s stmt) (*vmSend, error) {
	src := arg{kind: argVar, str: s.sendCh[0]}
	if len(s.sendCh) > 1 {
		src = arg{kind: argPath, path: s.sendCh}
	}
	ch, t, err := c.chanSource(slots, env, src)
	if err != nil {
		return nil, err
	}
	if t == nil || t.Kind() != reflect.Chan {
		return nil, fmt.Errorf("compile: cannot send to a %s", t)
	}
	if t.ChanDir() == reflect.RecvDir {
		return nil, fmt.Errorf("compile: cannot send to the receive-only %s", t)
	}
	val, err := c.compileArg(slots, env, "send", 0, t.Elem(), *s.sendVal)
	if err != nil {
		return nil, err
	}
	return &vmSend{ch: ch, val: val}, nil
}

// exec performs the receive. A closed channel is io.EOF: the implicit
// ok, ending the program the way an implicit error does.
func (r *vmRecv) exec(ctx context.Context, slots, frame []reflect.Value, ifaces []ifacePair, stack map[string]any, dest any) (reflect.Value, error) {
	chv, err := r.ch.get(ctx, slots, frame, ifaces, stack, dest)
	if err != nil {
		return reflect.Value{}, err
	}
	return chanRecv(ctx, chv)
}

// chanRecv receives with the context armed. The ready case goes
// through TryRecv, which allocates nothing; only a receive that would
// block pays reflect.Select's case slice. TryRecv distinguishes the
// three outcomes: a value, a closed channel (valid zero value, ok
// false), and not ready (invalid value).
func chanRecv(ctx context.Context, chv reflect.Value) (reflect.Value, error) {
	if v, ok := chv.TryRecv(); ok {
		return v, nil
	} else if v.IsValid() {
		return reflect.Value{}, io.EOF
	}
	chosen, v, ok := reflect.Select([]reflect.SelectCase{
		{Dir: reflect.SelectRecv, Chan: chv},
		{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ctx.Done())},
	})
	if chosen == 1 {
		return reflect.Value{}, ctx.Err()
	}
	if !ok {
		return reflect.Value{}, io.EOF
	}
	return v, nil
}

// exec performs the send.
func (s *vmSend) exec(ctx context.Context, slots, frame []reflect.Value, ifaces []ifacePair, stack map[string]any, dest any) error {
	chv, err := s.ch.get(ctx, slots, frame, ifaces, stack, dest)
	if err != nil {
		return err
	}
	v, err := s.val.get(ctx, slots, frame, ifaces, stack, dest)
	if err != nil {
		return err
	}
	return chanSend(ctx, chv, v)
}

// chanSend sends with the context armed, through TrySend when the
// channel is ready; a send to a closed channel panics inside TrySend
// exactly as it does in Go.
func chanSend(ctx context.Context, chv, v reflect.Value) error {
	if chv.TrySend(v) {
		return nil
	}
	chosen, _, _ := reflect.Select([]reflect.SelectCase{
		{Dir: reflect.SelectSend, Chan: chv, Send: v},
		{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ctx.Done())},
	})
	if chosen == 1 {
		return ctx.Err()
	}
	return nil
}
