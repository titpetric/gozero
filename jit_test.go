package gozero

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestJITMatchesReflect pins the unsafe shape-cast against the reflect
// path: the same compiled Statement runs through both s.fast and
// s.call, on the literal and the variable statement, on the success and
// the error path, and the results must agree.
func TestJITMatchesReflect(t *testing.T) {
	rt := newRuntime(t)
	for name, tc := range map[string]struct {
		stmt  string
		stack map[string]any
	}{
		"literal":   {`return NewRequest("GET", "https://example.com/jit");`, nil},
		"variable":  {`return NewRequest("GET", url);`, map[string]any{"url": "https://example.com/var"}},
		"unset var": {`return NewRequest("GET", url);`, map[string]any{}},
		"error":     {`return NewRequest("bad method", "https://example.com/");`, nil},
	} {
		s, err := compileFlat(rt, tc.stmt)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if s.fast == nil {
			t.Fatalf("%s: statement did not JIT", name)
		}

		fastRes, fastErr := s.fast(context.Background(), tc.stack, nil)
		slowRes, slowErr := s.call(context.Background(), tc.stack, nil)
		if (fastErr == nil) != (slowErr == nil) {
			t.Fatalf("%s: err = %v (jit) vs %v (reflect)", name, fastErr, slowErr)
		}
		if fastErr != nil {
			if fastErr.Error() != slowErr.Error() {
				t.Errorf("%s: err = %q (jit) vs %q (reflect)", name, fastErr, slowErr)
			}
			continue
		}
		fastReq, ok := fastRes.(*http.Request)
		if !ok {
			t.Fatalf("%s: jit result is %T, want *http.Request", name, fastRes)
		}
		slowReq := slowRes.(*http.Request)
		if fastReq.Method != slowReq.Method || fastReq.URL.String() != slowReq.URL.String() {
			t.Errorf("%s: jit %s %s vs reflect %s %s",
				name, fastReq.Method, fastReq.URL, slowReq.Method, slowReq.URL)
		}
	}
}

// TestJITFallback checks that a statement outside the shape table
// compiles to the reflect path. The interface type here is not in
// ifaceConvs, so the JIT cannot build its itab and declines.
func TestJITFallback(t *testing.T) {
	rt := newRuntime(t)
	if err := rt.Bind("Fprint", func(s fmt.Stringer) (*http.Request, error) {
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	s, err := compileFlat(rt, `return Fprint(v);`)
	if err != nil {
		t.Fatal(err)
	}
	if s.fast != nil {
		t.Fatal("an interface outside ifaceConvs should not JIT")
	}
}

// TestJITInterfaceFromStack pins the itab conversion for an interface
// argument read off the stack: the JIT builds the io.Reader words with
// a type assertion, and the body must arrive at the callee intact and
// identical to what the reflect path produces.
func TestJITInterfaceFromStack(t *testing.T) {
	rt := newRuntime(t)
	s, err := compileFlat(rt, `return NewRequest("POST", "https://example.com/", body);`)
	if err != nil {
		t.Fatal(err)
	}
	if s.fast == nil {
		t.Fatal("an io.Reader from the stack should JIT")
	}

	for _, tc := range []struct {
		name string
		body any
		want string
	}{
		{"reader", strings.NewReader("payload"), "payload"},
		{"unset", nil, ""},
	} {
		stack := map[string]any{}
		if tc.body != nil {
			stack["body"] = tc.body
		}
		fastReq, fastErr := s.fast(context.Background(), stack, nil)
		if tc.body != nil {
			stack["body"] = strings.NewReader("payload")
		}
		slowReq, slowErr := s.call(context.Background(), stack, nil)
		if fastErr != nil || slowErr != nil {
			t.Fatalf("%s: %v (jit) %v (reflect)", tc.name, fastErr, slowErr)
		}
		fast := readBody(t, fastReq.(*http.Request))
		slow := readBody(t, slowReq.(*http.Request))
		if fast != tc.want || slow != tc.want {
			t.Errorf("%s: body = %q (jit) %q (reflect), want %q", tc.name, fast, slow, tc.want)
		}
	}
}

// TestJITErrorOnlyShape pins the error-only result shape: two interface
// parameters and a lone error back, the shape json encoding uses.
func TestJITErrorOnlyShape(t *testing.T) {
	rt := NewRuntime()
	var gotW, gotV any
	if err := rt.Bind("Encode", func(w io.Writer, v any) error {
		gotW, gotV = w, v
		if v == nil {
			return errors.New("nil value")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s, err := compileFlat(rt, `return Encode(w, v);`)
	if err != nil {
		t.Fatal(err)
	}
	if s.fast == nil {
		t.Fatal("(io.Writer, any) error should JIT")
	}

	var buf bytes.Buffer
	res, err := s.fast(context.Background(), map[string]any{"w": &buf, "v": "payload"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res != nil {
		t.Errorf("result = %v, want nil for an error-only binding", res)
	}
	if gotW != io.Writer(&buf) {
		t.Errorf("writer = %v, want the buffer", gotW)
	}
	if gotV != "payload" {
		t.Errorf("value = %v, want payload", gotV)
	}

	if _, err := s.fast(context.Background(), map[string]any{"w": &buf}, nil); err == nil {
		t.Fatal("expected the binding error to come back through the JIT")
	}
}

func readBody(t *testing.T, req *http.Request) string {
	t.Helper()
	if req.Body == nil {
		return ""
	}
	b, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// compileFlat parses a one-statement program and builds its Statement
// directly, so a test can reach both tiers of the same compilation.
func compileFlat(rt *Runtime, src string) (*Statement, error) {
	prog, err := (&Parser{}).Parse(src)
	if err != nil {
		return nil, err
	}
	call, ok := prog.flatCall()
	if !ok {
		return nil, fmt.Errorf("compileFlat: %q is not a single flat call", src)
	}
	return rt.compiler.compileStatement(call)
}
