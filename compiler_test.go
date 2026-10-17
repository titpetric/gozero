package gozero

import (
	"context"
	"net/http"
	"net/url"
	"path"
	"reflect"
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
// anything else. The table walks every argument kind the parser
// produces, plus the spread forms: each one either runs or reports a
// compile error, and none may panic.
func TestCompilerFlatReturnArgKinds(t *testing.T) {
	rt := NewRuntime()
	for name, fn := range map[string]any{
		"wantAny": func(v any) any { return v },
		"give":    func() string { return "g" },
		"Parse":   url.Parse,
		"Join":    path.Join,
	} {
		if err := rt.Bind(name, fn); err != nil {
			t.Fatal(err)
		}
	}
	stack := map[string]any{"x": 7, "xs": []string{"a", "b"}}
	for name, tc := range map[string]struct {
		src     string
		want    any
		wantErr bool
	}{
		"string":     {src: `return wantAny("s");`, want: "s"},
		"int":        {src: `return wantAny(5);`, want: int64(5)},
		"float":      {src: `return wantAny(2.5);`, want: 2.5},
		"bool":       {src: `return wantAny(true);`, want: true},
		"nil":        {src: `return wantAny(nil);`, want: nil},
		"var":        {src: `return wantAny(x);`, want: 7},
		"call":       {src: `return wantAny(give());`, want: "g"},
		"struct":     {src: `return wantAny(url.URL{Path: "/x"});`, want: url.URL{Path: "/x"}},
		"struct ptr": {src: `return wantAny(&url.URL{Path: "/x"});`, want: &url.URL{Path: "/x"}},
		"spread":     {src: `return Join(xs...);`, want: "a/b"},
		// A path needs a program-bound base, and a spread needs a
		// variadic parameter; both report at compile time.
		"path":           {src: `return wantAny(a.b);`, wantErr: true},
		"spread non-var": {src: `return wantAny(xs...);`, wantErr: true},
	} {
		v, err := rt.Eval[any](tc.src, stack)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%s: expected a compile error for %q", name, tc.src)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !reflect.DeepEqual(v, tc.want) {
			t.Errorf("%s: got %#v, want %#v", name, v, tc.want)
		}
	}
}
