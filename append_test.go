package gozero

import (
	"strings"
	"testing"
)

// TestAppend covers the append that works through a pointer held in an
// any, on both the type-switched and the reflect paths.
func TestAppend(t *testing.T) {
	ss := []string{"a"}
	if err := Append(&ss, "b"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(ss, ",") != "a,b" {
		t.Errorf("[]string path left %v", ss)
	}

	ns := []int{1}
	if err := Append(&ns, 2); err != nil {
		t.Fatal(err)
	}
	if len(ns) != 2 || ns[1] != 2 {
		t.Errorf("[]int path left %v", ns)
	}

	as := []any{1}
	if err := Append(&as, "x"); err != nil {
		t.Fatal(err)
	}
	if len(as) != 2 {
		t.Errorf("[]any path left %v", as)
	}

	// A nil slice appends to an empty one, as in Go.
	var nils []string
	if err := Append(&nils, "first"); err != nil {
		t.Fatal(err)
	}
	if len(nils) != 1 {
		t.Errorf("nil slice left %v", nils)
	}

	// The reflect path: an element type the switch does not name.
	type point struct{ X int }
	ps := []point{{X: 1}}
	if err := Append(&ps, point{X: 2}); err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 || ps[1].X != 2 {
		t.Errorf("reflect path left %v", ps)
	}

	// A convertible element.
	is := []int64{1}
	if err := Append(&is, 2); err != nil {
		t.Fatal(err)
	}
	if len(is) != 2 {
		t.Errorf("a convertible element did not append: %v", is)
	}
}

// TestAppendRejections pins what Append refuses, with the message that
// names the fix.
func TestAppendRejections(t *testing.T) {
	if err := Append(nil, "x"); err == nil || !strings.Contains(err.Error(), "destination is nil") {
		t.Errorf("nil destination: %v", err)
	}
	if err := Append([]string{"a"}, "b"); err == nil || !strings.Contains(err.Error(), "write &name") {
		t.Errorf("a slice by value should name the fix, got %v", err)
	}
	var p *[]string
	if err := Append(p, "x"); err == nil || !strings.Contains(err.Error(), "pointer is nil") {
		t.Errorf("nil pointer: %v", err)
	}
	n := 1
	if err := Append(&n, 2); err == nil || !strings.Contains(err.Error(), "not a slice") {
		t.Errorf("not a slice: %v", err)
	}
	ss := []string{"a"}
	if err := Append(&ss, 1); err == nil || !strings.Contains(err.Error(), "as a []string element") {
		t.Errorf("wrong element type: %v", err)
	}
	type point struct{ X int }
	ps := []point{}
	if err := Append(&ps, "x"); err == nil || !strings.Contains(err.Error(), "as a []gozero.point element") {
		t.Errorf("reflect path element type: %v", err)
	}
}
