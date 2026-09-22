package gozero

import (
	"testing"
)

// TestRef covers the marker and the two helpers that make one. The
// copy semantics are the part a caller gets wrong, so they are the
// part pinned here.
func TestRef(t *testing.T) {
	n := 1
	ref := Mutable(&n)
	if !ref.val.IsValid() || !ref.val.CanSet() {
		t.Fatal("Mutable produced an unsettable handle")
	}
	ref.val.SetInt(7)
	if n != 7 {
		t.Errorf("a write through the handle left n at %d, want 7", n)
	}

	// Ptr points at a copy of its argument, which is why Mutable takes
	// an address rather than a value.
	host := []string{"original"}
	p := Ptr(host)
	*p = []string{"through the copy"}
	if host[0] != "original" {
		t.Errorf("Ptr aliased its argument, host is now %v", host)
	}
	if got := *Ptr(3); got != 3 {
		t.Errorf("Ptr(3) reads back %d", got)
	}

	// The copy is only of the header, as it is anywhere in Go.
	shared := []string{"a"}
	q := Ptr(shared)
	(*q)[0] = "b"
	if shared[0] != "b" {
		t.Errorf("elements should stay shared, host is %v", shared)
	}

	// A zero Ref is what BindVar has to reject, since nothing named it.
	var zero Ref
	if zero.val.IsValid() {
		t.Error("the zero Ref should carry no value")
	}
}
