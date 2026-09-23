package gozero

import (
	"net/url"
	"strings"
	"testing"
)

// TestFieldSetValueForms covers the right-hand sides a field write
// accepts, which are the ones every assignment accepts: a literal, a
// composite literal, a call, a name, and a pointer read.
func TestFieldSetValueForms(t *testing.T) {
	rt, seen := bvRuntime(t)
	if err := rt.Bind("parse", url.Parse); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("path", func() (string, error) { return "/from-a-call", nil }); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("pathPtr", func() (*string, error) { s := "/from-a-pointer"; return &s, nil }); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ src, want string }{
		{"u := parse(\"https://e.com/\")\nu.Path = \"/literal\"\nrecordS(u.Path)", "/literal"},
		{"u := parse(\"https://e.com/\")\nu.Path = path()\nrecordS(u.Path)", "/from-a-call"},
		{"u := parse(\"https://e.com/\")\ns := path()\nu.Path = s\nrecordS(u.Path)", "/from-a-call"},
		{"u := parse(\"https://e.com/\")\np := pathPtr()\nu.Path = *p\nrecordS(u.Path)", "/from-a-pointer"},
	} {
		if err := bvRun(t, rt, tc.src); err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if seen.s != tc.want {
			t.Errorf("%s: got %q, want %q", tc.src, seen.s, tc.want)
		}
	}

	// The target's type is the check, and it is made when the program
	// compiles.
	err := bvRun(t, rt, "u := parse(\"https://e.com/\")\nu.Path = 1")
	if err == nil || !strings.Contains(err.Error(), "cannot use") {
		t.Errorf("an int into a string field: %v", err)
	}
	err = bvRun(t, rt, "u := parse(\"https://e.com/\")\nu.Nope = \"x\"")
	if err == nil || !strings.Contains(err.Error(), "has no field Nope") {
		t.Errorf("an unknown field: %v", err)
	}
	err = bvRun(t, rt, "u.Path = \"x\"")
	if err == nil || !strings.Contains(err.Error(), "not a name bound by the program") {
		t.Errorf("an unbound base: %v", err)
	}
}

// TestReturnValueForms covers the value form of return, whose
// parameter type is its own rather than a binding's.
func TestReturnValueForms(t *testing.T) {
	rt, _ := bvRuntime(t)
	if err := rt.Bind("parse", url.Parse); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("label", "bound"); err != nil {
		t.Fatal(err)
	}

	s, err := rt.Eval[string]("s := \"named\"\nreturn s", nil)
	if err != nil || s != "named" {
		t.Errorf("return of a name: %q %v", s, err)
	}
	n, err := rt.Eval[int64]("return 7", nil)
	if err != nil || n != 7 {
		t.Errorf("return of a literal: %d %v", n, err)
	}
	p, err := rt.Eval[string]("u := parse(\"https://e.com/p\")\nreturn u.Path", nil)
	if err != nil || p != "/p" {
		t.Errorf("return of a field: %q %v", p, err)
	}
	if _, err := rt.Eval[any]("return nil", nil); err == nil || !strings.Contains(err.Error(), "returns no value") {
		t.Errorf("return nil should name the fix: %v", err)
	}
}
