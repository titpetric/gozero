package gozero

import (
	"context"
	"net/http"
	"strings"
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

// TestCompilerRejectsUnsupportedArg pins the failure mode of the
// single-statement path for an argument kind it does not carry. A
// dotted path or a nil literal in a flat call left the argument's
// reflect.Value zero, and the assignability check reached v.Type() on
// it and panicked; both are a named compile error instead.
func TestCompilerRejectsUnsupportedArg(t *testing.T) {
	rt := newRuntime(t)
	for _, src := range []string{
		`return NewRequest(a.b, "https://example.com");`,
		`return NewRequest("GET", "https://example.com", nil);`,
	} {
		prog, err := (&Parser{}).Parse(src)
		if err != nil {
			t.Fatal(err)
		}
		_, err = rt.compiler.Compile(prog)
		if err == nil || !strings.Contains(err.Error(), "unsupported argument") {
			t.Errorf("%s: want a named unsupported-argument error, got %v", src, err)
		}
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
