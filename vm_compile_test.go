package gozero

import (
	"bytes"
	"testing"
)

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
