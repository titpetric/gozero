package gozero

import (
	"strings"
	"testing"
)

// TestBindVarImmutableAddress covers &name on a binding the host
// passed by value: it yields a pointer to a per-run copy, so a
// program may write through it and the host's variable keeps the
// header it had.
func TestBindVarImmutableAddress(t *testing.T) {
	rt, seen := bvRuntime(t)
	host := []string{"a", "b"}
	if err := rt.BindVar("os.Args", host); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("append", Append); err != nil {
		t.Fatal(err)
	}

	// Appending through the address grows the copy, and reading the
	// name afterwards reads the same copy.
	src := "append(&os.Args, \"-x\")\nrecordL(os.Args)"
	if err := bvRun(t, rt, src); err != nil {
		t.Fatal(err)
	}
	if strings.Join(seen.l, ",") != "a,b,-x" {
		t.Errorf("program read %v, want the appended copy", seen.l)
	}
	if strings.Join(host, ",") != "a,b" {
		t.Errorf("host reads %v, want its own header untouched", host)
	}

	// Replacing the whole slice through the pointer is the same deal.
	if err := bvRun(t, rt, "setL(&os.Args)\nrecordL(os.Args)"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(seen.l, ",") != "replaced" {
		t.Errorf("program read %v after setL", seen.l)
	}
	if strings.Join(host, ",") != "a,b" {
		t.Errorf("host reads %v, want its own header untouched", host)
	}
}

// TestBindVarCellIsPerRun is the property the per-run copy exists
// for: a second run starts from the bound value, not from what the
// first run appended, so concurrent Execs cannot see each other.
func TestBindVarCellIsPerRun(t *testing.T) {
	rt, seen := bvRuntime(t)
	if err := rt.BindVar("os.Args", []string{"a"}); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("append", Append); err != nil {
		t.Fatal(err)
	}
	fn, err := rt.Compile("append(&os.Args, \"-x\")\nrecordL(os.Args)")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := fn.Exec[any](nil); err != nil {
			t.Fatal(err)
		}
		if strings.Join(seen.l, ",") != "a,-x" {
			t.Fatalf("run %d read %v, want a,-x on every run", i, seen.l)
		}
	}
}

// TestBindVarCellIsShared pins that two &name in one program address
// the same storage, the way two &x do in Go.
func TestBindVarCellIsShared(t *testing.T) {
	rt, seen := bvRuntime(t)
	if err := rt.BindVar("os.Args", []string{"a"}); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("append", Append); err != nil {
		t.Fatal(err)
	}
	src := "append(&os.Args, \"-x\")\nappend(&os.Args, \"-y\")\nrecordL(os.Args)"
	if err := bvRun(t, rt, src); err != nil {
		t.Fatal(err)
	}
	if strings.Join(seen.l, ",") != "a,-x,-y" {
		t.Errorf("read %v, want both appends in one cell", seen.l)
	}
}
