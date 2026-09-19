package gozero

import (
	"bytes"
	"strings"
	"testing"
)

// TestFieldAssign covers the field assignment statement: a write
// through a pointer base, a write through a chain, and a write into a
// var-declared struct held by value.
func TestFieldAssign(t *testing.T) {
	rt := vmRuntime(t)
	for _, tc := range []struct{ name, src, want string }{
		{
			"through a pointer",
			`req := http.NewRequest("GET", "/");
			 req.Method = "POST";
			 json.NewEncoder(dest).Encode(req.Method);`,
			"\"POST\"\n",
		},
		{
			"through a chain",
			`req := http.NewRequest("GET", "/a");
			 req.URL.Path = "/b";
			 json.NewEncoder(dest).Encode(req.URL.Path);`,
			"\"/b\"\n",
		},
		{
			"into a var-declared struct",
			`var u url.URL;
			 u.Path = "/w";
			 json.NewEncoder(dest).Encode(u.Path);`,
			"\"/w\"\n",
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

// TestFieldAssignErrors pins the rules compileFieldSet and apply
// enforce: the base must be bound, the field must exist, the value
// must fit, and a nil pointer fails at run time, not silently.
func TestFieldAssignErrors(t *testing.T) {
	rt := vmRuntime(t)
	for _, tc := range []struct{ name, src, want string }{
		{
			"unbound base",
			`ghost.Method = "POST";`,
			"not a name bound by the program",
		},
		{
			"absent field",
			`req := http.NewRequest("GET", "/"); req.Nope = "x";`,
			"has no field Nope",
		},
		{
			"wrong value type",
			`req := http.NewRequest("GET", "/"); req.Method = 5;`,
			"cannot use a number as string",
		},
	} {
		_, err := rt.Compile(tc.src)
		if err == nil {
			t.Errorf("%s: expected a compile error", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q does not name the rule %q", tc.name, err, tc.want)
		}
	}

	// A nil pointer in the chain is a run-time error naming the field.
	fn, err := rt.Compile(`var r *http.Request; r.Method = "POST";`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fn.Exec[any](nil); err == nil || !strings.Contains(err.Error(), "field write on a nil") {
		t.Errorf("a nil base should fail the write, got %v", err)
	}
}
