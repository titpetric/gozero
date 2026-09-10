package gozero

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

func chanRuntime(t *testing.T) (*Runtime, chan string) {
	t.Helper()
	rt := NewRuntime()
	ch := make(chan string, 4)
	if err := rt.Bind("source", func() chan string { return ch }); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("recvOnly", func() <-chan string { return ch }); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("sendOnly", func() chan<- string { return ch }); err != nil {
		t.Fatal(err)
	}
	return rt, ch
}

// TestChannelRecvSend checks the receive and send statements against a
// buffered channel a binding hands out.
func TestChannelRecvSend(t *testing.T) {
	rt, ch := chanRuntime(t)
	ch <- "a"

	v, err := rt.Eval[string](`c := source(); v := <-c; return v`, nil)
	if err != nil || v != "a" {
		t.Fatalf("recv = %q, %v; want \"a\"", v, err)
	}

	// A send is observable from the host side of the same channel.
	if _, err := rt.Eval[any](`c := source(); c <- "sent";`, nil); err != nil {
		t.Fatal(err)
	}
	if got := <-ch; got != "sent" {
		t.Fatalf("host received %q, want \"sent\"", got)
	}

	// Send and receive through the same program, and a literal
	// converting to the element type at the send.
	v, err = rt.Eval[string](`c := source(); c <- "round"; r := <-c; return r`, nil)
	if err != nil || v != "round" {
		t.Fatalf("roundtrip = %q, %v", v, err)
	}
}

// TestChannelClosedIsEOF checks the implicit ok: a receive from a
// closed channel ends the program with io.EOF, the way an implicit
// error ends it, so a host loops Exec until errors.Is(err, io.EOF).
func TestChannelClosedIsEOF(t *testing.T) {
	rt, ch := chanRuntime(t)
	ch <- "last"
	close(ch)

	v, err := rt.Eval[string](`c := source(); v := <-c; return v`, nil)
	if err != nil || v != "last" {
		t.Fatalf("drain = %q, %v", v, err)
	}
	_, err = rt.Eval[string](`c := source(); v := <-c; return v`, nil)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("closed receive err = %v, want io.EOF", err)
	}

	// The two-value form does not exist: ok is implicit.
	if _, err := rt.Compile(`c := source(); v, ok := <-c;`); err == nil {
		t.Error("expected a compile error for the two-value receive")
	} else {
		t.Log(err)
	}
}

// TestChannelContext checks that cancellation ends a blocked receive
// and a blocked send, including on a nil channel, which otherwise
// blocks forever.
func TestChannelContext(t *testing.T) {
	rt := NewRuntime()
	empty := make(chan string)
	if err := rt.Bind("source", func() chan string { return empty }); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("nilChan", func() chan string { return nil }); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	for name, src := range map[string]string{
		"blocked receive":     `c := source(); v := <-c; return v`,
		"blocked send":        `c := source(); c <- "x";`,
		"nil channel receive": `c := nilChan(); v := <-c; return v`,
	} {
		if _, err := rt.EvalContext[any](ctx, src, nil); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("%s: err = %v, want deadline exceeded", name, err)
		}
	}
}

// TestChannelDirections checks the compile-time direction errors and
// that directional channels work in their legal direction.
func TestChannelDirections(t *testing.T) {
	rt, ch := chanRuntime(t)
	ch <- "dir"

	v, err := rt.Eval[string](`c := recvOnly(); v := <-c; return v`, nil)
	if err != nil || v != "dir" {
		t.Fatalf("recv-only receive = %q, %v", v, err)
	}
	if _, err := rt.Eval[any](`c := sendOnly(); c <- "s";`, nil); err != nil {
		t.Fatal(err)
	}
	<-ch

	for name, src := range map[string]string{
		"send to recv-only":   `c := recvOnly(); c <- "x";`,
		"recv from send-only": `c := sendOnly(); v := <-c;`,
		"recv from non-chan":  `s := "str"; v := <-s;`,
		"stack channel":       `v := <-outside;`,
	} {
		if _, err := rt.Compile(src); err == nil {
			t.Errorf("%s: expected a compile error", name)
		} else {
			t.Logf("%s: %v", name, err)
		}
	}
}

// TestChanTypeRef checks the chan spellings a var statement accepts:
// the registry holds them under reflect.Type.String once a binding
// mentions the type, and a declared channel is nil until assigned.
func TestChanTypeRef(t *testing.T) {
	rt, ch := chanRuntime(t)
	ch <- "typed"

	v, err := rt.Eval[string](`var c chan string
c = source()
v := <-c
return v`, nil)
	if err != nil || v != "typed" {
		t.Fatalf("declared chan = %q, %v", v, err)
	}
	if _, err := rt.Eval[any](`var c <-chan string; return c`, nil); err != nil {
		t.Errorf("<-chan string: %v", err)
	}
	if _, err := rt.Eval[any](`var c chan<- string; return c`, nil); err != nil {
		t.Errorf("chan<- string: %v", err)
	}
}
