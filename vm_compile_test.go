package gozero

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path"
	"strings"
	"testing"
)

func featRuntime(t *testing.T) *Runtime {
	t.Helper()
	rt := NewRuntime()
	for scope, fns := range map[string]map[string]any{
		"http":    {"NewRequest": http.NewRequest, "NewRequestWithContext": http.NewRequestWithContext},
		"url":     {"Parse": url.Parse},
		"json":    {"NewEncoder": json.NewEncoder},
		"context": {"Background": context.Background},
		"path":    {"Join": path.Join},
		"strings": {"Fields": strings.Fields},
	} {
		if err := rt.BindScope(scope, fns); err != nil {
			t.Fatal(err)
		}
	}
	return rt
}

// TestNoShadow pins the rule that a program name may not collide with a
// binding's namespace: url := ... with url.Parse bound would compile
// and then resolve the other way on every later use.
func TestNoShadow(t *testing.T) {
	rt := featRuntime(t)
	for _, src := range []string{
		`url := http.NewRequest("GET", "/");`,
		`http = 5;`,
		`var json int64;`,
		`var dest int64;`,
	} {
		_, err := rt.Compile(src)
		if err == nil || !strings.Contains(err.Error(), "shadows") && !strings.Contains(err.Error(), "dest") {
			t.Errorf("%s: err = %v, want a shadowing error", src, err)
		}
	}
	// A name that is not a binding prefix stays legal.
	if _, err := rt.Compile(`u := url.Parse("/ok"); return u;`); err != nil {
		t.Errorf("non-shadowing name: %v", err)
	}
}

// TestFieldAssignment covers the write side of field access, on both
// tiers, with a literal and with a call result.
func TestFieldAssignment(t *testing.T) {
	rt := featRuntime(t)
	const src = `
		req := http.NewRequest("GET", "/");
		req.Method = "POST";
		json.NewEncoder(dest).Encode(req.Method);
	`
	if err := rt.Supports(src); err != nil {
		t.Fatalf("a literal field store should JIT: %v", err)
	}
	fn, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := fn.Scan(&buf, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := buf.String(), "\"POST\"\n"; got != want {
		t.Errorf("dest = %q, want %q", got, want)
	}

	// A call result on the right side.
	const call = `
		req := http.NewRequest("GET", "/old");
		req.URL = url.Parse("/new");
		json.NewEncoder(dest).Encode(req.URL.Path);
	`
	fn, err = rt.Compile(call)
	if err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := fn.Scan(&buf, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := buf.String(), "\"/new\"\n"; got != want {
		t.Errorf("dest = %q, want %q", got, want)
	}

	// A var-declared struct is settable in place on the reflect tier.
	const value = `
		var u url.URL;
		u.Path = "/v";
		json.NewEncoder(dest).Encode(u.Path);
	`
	fn, err = rt.Compile(value)
	if err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := fn.Scan(&buf, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := buf.String(), "\"/v\"\n"; got != want {
		t.Errorf("dest = %q, want %q", got, want)
	}

	// Errors: unknown field, unexported field, name on the right.
	for _, bad := range []string{
		`req := http.NewRequest("GET", "/"); req.Nope = "x";`,
		`req := http.NewRequest("GET", "/"); req.ctx = "x";`,
		`req := http.NewRequest("GET", "/"); s := req.Host; req.Host = s;`,
	} {
		if _, err := rt.Compile(bad); err == nil {
			t.Errorf("%s: expected a compile error", bad)
		}
	}
}

// TestFieldAccess covers reading a struct field, which is not a method
// and so is resolved separately: as an argument, as the receiver of a
// method, and through a chain of two fields.
func TestFieldAccess(t *testing.T) {
	rt := vmRuntime(t)
	for _, tc := range []struct{ name, src, want string }{
		{
			"field as argument",
			`req := http.NewRequest("GET", "/");
			 json.NewEncoder(dest).Encode(req.Header);`,
			"{}\n",
		},
		{
			"field of a field",
			`req := http.NewRequest("GET", "/a/b");
			 json.NewEncoder(dest).Encode(req.URL.Path);`,
			"\"/a/b\"\n",
		},
		{
			"method on a field mutates through it",
			`req := http.NewRequest("GET", "/");
			 req.Header.Set("X-A", "1");
			 json.NewEncoder(dest).Encode(req.Header);`,
			"{\"X-A\":[\"1\"]}\n",
		},
	} {
		fn, err := rt.Compile(tc.src)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		var dest bytes.Buffer
		if err := fn.Scan(&dest, nil); err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if got := dest.String(); got != tc.want {
			t.Errorf("%s: dest = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestFieldAccessErrors checks what a field cannot do: be called, and
// be read when it is unexported or absent.
func TestFieldAccessErrors(t *testing.T) {
	rt := vmRuntime(t)
	for _, tc := range []struct{ name, src string }{
		{"called as a method", `req := http.NewRequest("GET", "/"); req.Header();`},
		{"unexported", `req := http.NewRequest("GET", "/"); json.NewEncoder(dest).Encode(req.ctx);`},
		{"absent", `req := http.NewRequest("GET", "/"); json.NewEncoder(dest).Encode(req.Nope);`},
		{"on an opaque stack name", `json.NewEncoder(dest).Encode(v.Field);`},
	} {
		if _, err := rt.Compile(tc.src); err == nil {
			t.Errorf("%s: expected a compile error", tc.name)
		} else {
			t.Logf("%s: %v", tc.name, err)
		}
	}
}
