package gozero

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"testing"
)

// TestStatement exercises the reflect path's argument pool: repeated
// calls with a variable slot borrow and release the same buffers, and
// an unassignable stack value errors without corrupting them.
func TestStatement(t *testing.T) {
	rt := newRuntime(t)
	s, err := compileFlat(rt, `return NewRequest("GET", url);`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		link := fmt.Sprintf("https://example.com/%d", i)
		res, err := s.call(context.Background(), map[string]any{"url": link}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if req := res.(*http.Request); req.URL.String() != link {
			t.Errorf("call %d: url = %q, want %q", i, req.URL, link)
		}
	}
	if _, err := s.call(context.Background(), map[string]any{"url": 42}, nil); err == nil {
		t.Fatal("expected a type mismatch for an int in a string slot")
	}
	// The pool buffer released by the failed call must still work.
	if _, err := s.call(context.Background(), map[string]any{"url": "https://example.com/after"}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestStatement_Func(t *testing.T) {
	rt := newRuntime(t)
	s, err := compileFlat(rt, `return NewRequest("GET", url);`)
	if err != nil {
		t.Fatal(err)
	}
	if s.fast == nil {
		t.Fatal("NewRequest should JIT")
	}
	if reflect.ValueOf(s.Func()).Pointer() != reflect.ValueOf(s.fast).Pointer() {
		t.Error("Func did not return the JIT'd call")
	}

	// Out of shape, Func falls back to the reflect path.
	if err := rt.Bind("Str", func(v fmt.Stringer) (*http.Request, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	s, err = compileFlat(rt, `return Str(v);`)
	if err != nil {
		t.Fatal(err)
	}
	if s.fast != nil {
		t.Fatal("fmt.Stringer should not JIT")
	}
	if reflect.ValueOf(s.Func()).Pointer() != reflect.ValueOf(CompiledFunc(s.call)).Pointer() {
		t.Error("Func did not return the reflect path")
	}
}
