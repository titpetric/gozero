package gozero

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
)

// TestChannelJIT checks that a program of receives and sends is a
// direct program: Supports reports nil, and the operations behave the
// same as on the reflect evaluator.
func TestChannelJIT(t *testing.T) {
	rt, ch := chanRuntime(t)
	src := `c := source(); c <- "jit"; v := <-c; return v`
	if err := rt.Supports(src); err != nil {
		t.Fatalf("channel ops kept the program off the direct tier: %v", err)
	}
	v, err := rt.Eval[string](src, nil)
	if err != nil || v != "jit" {
		t.Fatalf("jit roundtrip = %q, %v", v, err)
	}
	_ = ch
}

// TestChannelTiersMatch runs the same channel programs down both
// tiers and compares results and errors, the way the general
// equivalence suite does.
func TestChannelTiersMatch(t *testing.T) {
	for name, src := range map[string]string{
		"roundtrip":      `c := source(); c <- "eq"; v := <-c; return v`,
		"closed is EOF":  `c := closed(); v := <-c; return v`,
		"bare receive":   `c := source(); c <- "fx"; <-c;`,
		"field channel":  `b := box(); b.C <- "fld"; v := <-b.C; return v`,
		"declared unset": `var c chan string; c = source(); c <- "d"; v := <-c; return v`,
	} {
		rt, _ := chanRuntime(t)
		if err := rt.Bind("closed", func() chan string {
			c := make(chan string)
			close(c)
			return c
		}); err != nil {
			t.Fatal(err)
		}
		type chanBox struct{ C chan string }
		if err := rt.Bind("box", func() *chanBox { return &chanBox{C: make(chan string, 1)} }); err != nil {
			t.Fatal(err)
		}

		jit, slow := compilePair(t, rt, src)
		jitRes, jitErr := jit(context.Background(), nil, nil)
		slowRes, slowErr := slow(context.Background(), nil, nil)

		switch {
		case (jitErr == nil) != (slowErr == nil):
			t.Errorf("%s: err = %v (jit) vs %v (reflect)", name, jitErr, slowErr)
		case jitErr != nil && jitErr.Error() != slowErr.Error():
			t.Errorf("%s: err = %q (jit) vs %q (reflect)", name, jitErr, slowErr)
		}
		if fmt.Sprintf("%v", jitRes) != fmt.Sprintf("%v", slowRes) {
			t.Errorf("%s: result = %v (jit) vs %v (reflect)", name, jitRes, slowRes)
		}
		if name == "closed is EOF" && !errors.Is(jitErr, io.EOF) {
			t.Errorf("%s: err = %v, want io.EOF", name, jitErr)
		}
	}
}
