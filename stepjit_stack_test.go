package gozero

import (
	"net/url"
	"testing"
)

// TestStackScalarReads pins the stack scalar read on the direct
// tier, through the one in-table shape carrying a scalar parameter:
// a present entry asserts directly, an absent one reads as zero, a
// mistyped one errors.
func TestStackScalarReads(t *testing.T) {
	rt := NewRuntime()
	var got int64
	if err := rt.Bind("put", func(u *url.URL, s string, n int64) {
		got = n
	}); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("mk", url.Parse); err != nil {
		t.Fatal(err)
	}
	const src = `u := mk("/x"); put(u, "k", n);`
	if err := rt.Supports(src); err != nil {
		t.Fatalf("did not JIT: %v", err)
	}
	fn, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fn.Exec[any](map[string]any{"n": int64(7)}); err != nil || got != 7 {
		t.Fatalf("got %d, %v", got, err)
	}
	if _, err := fn.Exec[any](nil); err != nil || got != 0 {
		t.Fatalf("zero fill: got %d, %v", got, err)
	}
	if _, err := fn.Exec[any](map[string]any{"n": "no"}); err == nil {
		t.Fatal("a mistyped stack scalar must fail")
	}
}
