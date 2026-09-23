package gozero

import (
	"testing"
)

// TestBindVarRebindIsNotAnUpdate pins the rule a host gets wrong: a
// second BindVar under the same name is a new binding, and neither a
// compiled program nor the next Compile of the same source sees it,
// because the node captured the binding and the compilation is
// cached. Tracking the host over time is what Mutable is for.
func TestBindVarRebindIsNotAnUpdate(t *testing.T) {
	rt, seen := bvRuntime(t)
	if err := rt.BindVar("label", "first"); err != nil {
		t.Fatal(err)
	}
	const src = `recordS(label)`
	fn, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fn.Exec[any](nil); err != nil {
		t.Fatal(err)
	}
	if seen.s != "first" {
		t.Fatalf("baseline read %q", seen.s)
	}

	if err := rt.BindVar("label", "second"); err != nil {
		t.Fatal(err)
	}
	if _, err := fn.Exec[any](nil); err != nil {
		t.Fatal(err)
	}
	if seen.s != "first" {
		t.Errorf("the compiled program read %q after a rebind, want first", seen.s)
	}
	again, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := again.Exec[any](nil); err != nil {
		t.Fatal(err)
	}
	if seen.s != "first" {
		t.Errorf("the cached compilation read %q after a rebind, want first", seen.s)
	}
}

// TestBindVarImmutableSliceAliasing is the half of the shallow copy
// that surprises: an immutable slice binding shares its elements with
// the host in both directions, and only the header is frozen.
func TestBindVarImmutableSliceAliasing(t *testing.T) {
	rt, seen := bvRuntime(t)
	host := []string{"a", "b"}
	if err := rt.BindVar("args", host); err != nil {
		t.Fatal(err)
	}

	// The host writes an element: the program sees it.
	host[0] = "host-wrote"
	if err := bvRun(t, rt, `recordL(args)`); err != nil {
		t.Fatal(err)
	}
	if seen.l[0] != "host-wrote" {
		t.Errorf("program read %v, want the host's element write", seen.l)
	}

	// The program writes an element: the host sees it.
	if err := bvRun(t, rt, `pokeL(args)`); err != nil {
		t.Fatal(err)
	}
	if host[0] != "poked" {
		t.Errorf("host reads %v, want the program's element write", host)
	}

	// The host replaces the whole slice: the program does not see it,
	// because the binding froze the header.
	host = []string{"replaced"}
	if err := bvRun(t, rt, `recordL(args)`); err != nil {
		t.Fatal(err)
	}
	if len(seen.l) != 2 {
		t.Errorf("program read %v, want the frozen header", seen.l)
	}
}
