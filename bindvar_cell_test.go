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

// TestBindVarCellAssign covers assignment to an immutable binding: it
// writes the run's cell, the same storage &name addresses, so a read
// afterwards sees it and the host's variable does not.
func TestBindVarCellAssign(t *testing.T) {
	rt, seen := bvRuntime(t)
	host := []string{"a", "b"}
	if err := rt.BindVar("os.Args", host); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("three", func() ([]string, error) { return []string{"1", "2", "3"}, nil }); err != nil {
		t.Fatal(err)
	}

	if err := bvRun(t, rt, "os.Args = three()\nrecordL(os.Args)"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(seen.l, ",") != "1,2,3" {
		t.Errorf("program read %v after the write, want the assigned value", seen.l)
	}
	if strings.Join(host, ",") != "a,b" {
		t.Errorf("host reads %v, want its own variable untouched", host)
	}

	// A write with no & anywhere still gets a cell, and the next run
	// starts from the bound value again.
	if err := bvRun(t, rt, `recordL(os.Args)`); err != nil {
		t.Fatal(err)
	}
	if strings.Join(seen.l, ",") != "a,b" {
		t.Errorf("a later program read %v, want the bound value", seen.l)
	}
}

// TestBindVarCellAssignAndAppend pins that the assignment and the
// address reach one cell rather than two.
func TestBindVarCellAssignAndAppend(t *testing.T) {
	rt, seen := bvRuntime(t)
	if err := rt.BindVar("args", []string{"a"}); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("append", Append); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("three", func() ([]string, error) { return []string{"1"}, nil }); err != nil {
		t.Fatal(err)
	}
	src := "args = three()\nappend(&args, \"2\")\nrecordL(args)"
	if err := bvRun(t, rt, src); err != nil {
		t.Fatal(err)
	}
	if strings.Join(seen.l, ",") != "1,2" {
		t.Errorf("read %v, want the assignment and the append in one cell", seen.l)
	}
}

// TestBindVarMutableAssignReachesHost is the other half: the same
// statement on a mutable binding writes the host's variable, and the
// name is the variable rather than a pointer to it, so *name does not
// compile.
func TestBindVarMutableAssignReachesHost(t *testing.T) {
	rt, _ := bvRuntime(t)
	host := []string{"a"}
	if err := rt.BindVar("args", Mutable(&host)); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("three", func() ([]string, error) { return []string{"1", "2", "3"}, nil }); err != nil {
		t.Fatal(err)
	}
	if err := bvRun(t, rt, `args = three()`); err != nil {
		t.Fatal(err)
	}
	if strings.Join(host, ",") != "1,2,3" {
		t.Errorf("host reads %v, want the program's write", host)
	}
	err := bvRun(t, rt, `*args = three()`)
	if err == nil || !strings.Contains(err.Error(), "not a pointer") {
		t.Errorf("*args should not compile, got %v", err)
	}
}
