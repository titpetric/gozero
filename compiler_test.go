package gozero

import (
	"context"
	"net/http"
	"testing"
)

// TestCompiler checks the compile-time validation the type promises: a
// literal that does not fit the parameter type fails at Compile, before
// anything runs.
func TestCompiler(t *testing.T) {
	rt := newRuntime(t)
	prog, err := (&Parser{}).Parse(`return NewRequest(42, "https://example.com");`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.compiler.Compile(prog); err == nil {
		t.Error("expected an int literal in a string parameter to fail at compile time")
	}
	prog, err = (&Parser{}).Parse(`return Missing("GET");`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.compiler.Compile(prog); err == nil {
		t.Error("expected an unknown binding to fail at compile time")
	}
}

func TestCompiler_Compile(t *testing.T) {
	rt := newRuntime(t)

	// A flat call takes the single-statement path.
	prog, err := (&Parser{}).Parse(`return NewRequest("GET", "https://example.com/flat");`)
	if err != nil {
		t.Fatal(err)
	}
	fn, err := rt.compiler.Compile(prog)
	if err != nil {
		t.Fatal(err)
	}
	res, err := fn(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if req := res.(*http.Request); req.URL.Path != "/flat" {
		t.Errorf("path = %q, want /flat", req.URL.Path)
	}

	// A two-statement program compiles to the VM and runs the same.
	prog, err = (&Parser{}).Parse(`req := NewRequest("GET", "https://example.com/vm"); return req;`)
	if err != nil {
		t.Fatal(err)
	}
	fn, err = rt.compiler.Compile(prog)
	if err != nil {
		t.Fatal(err)
	}
	res, err = fn(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if req := res.(*http.Request); req.URL.Path != "/vm" {
		t.Errorf("path = %q, want /vm", req.URL.Path)
	}
}

// TestCompilerFlatReturnArgKinds pins the fix for a compile-time
// panic: a flat "return f(x);" whose argument was nil or a bool
// reached the single-statement compiler, which handles only strings,
// numbers and names and called Type on an invalid reflect.Value for
// anything else. Those argument kinds route to the program compiler
// now, which handles them all.
func TestCompilerFlatReturnArgKinds(t *testing.T) {
	rt := NewRuntime()
	if err := rt.Bind("wantAny", func(v any) any { return v }); err != nil {
		t.Fatal(err)
	}
	res, err := rt.Eval[any](`return wantAny(nil);`, nil)
	if err != nil {
		t.Fatalf("return wantAny(nil): %v", err)
	}
	if res != nil {
		t.Errorf("wantAny(nil) = %v, want nil", res)
	}
	b, err := rt.Eval[bool](`return wantAny(true);`, nil)
	if err != nil {
		t.Fatalf("return wantAny(true): %v", err)
	}
	if !b {
		t.Errorf("wantAny(true) = %v, want true", b)
	}
}
