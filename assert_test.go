package gozero

import (
	"fmt"
	"reflect"
	"testing"
)

// assertEqual is the assertion surface the fixtures bind, without the
// testify dependency: reflect.DeepEqual and an Errorf. tb arrives as
// any because a fixture program passes it back through the stack, and
// message is a plain trailing parameter so the binding keeps a shape
// the direct-call tier can compile; an omitted message zero-fills.
func assertEqual(tb any, want, got any, message string) {
	t, ok := tb.(testing.TB)
	if !ok {
		panic(fmt.Sprintf("assert.Equal: tb is %T, want a testing.TB", tb))
	}
	t.Helper()
	// Two comparable values compare as interfaces without reflection,
	// which is every string and scalar the fixtures assert on; DeepEqual
	// is the fallback for the rest.
	if wt := reflect.TypeOf(want); wt != nil && wt.Comparable() && reflect.TypeOf(got) == wt {
		if want == got {
			return
		}
	} else if reflect.DeepEqual(want, got) {
		return
	}
	if message != "" {
		t.Errorf("%s: got %v (%T), want %v (%T)", message, got, got, want, want)
		return
	}
	t.Errorf("got %v (%T), want %v (%T)", got, got, want, want)
}

// assertTrue is assertEqual for a lone condition.
func assertTrue(tb any, value bool, message string) {
	t, ok := tb.(testing.TB)
	if !ok {
		panic(fmt.Sprintf("assert.True: tb is %T, want a testing.TB", tb))
	}
	t.Helper()
	if value {
		return
	}
	if message != "" {
		t.Errorf("%s: condition is false", message)
		return
	}
	t.Error("condition is false")
}
