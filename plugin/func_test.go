package plugin

import (
	"strings"
	"testing"
)

// TestFunc covers the typed lookup: exact signatures, named func
// types by conversion, and the mismatch error.
func TestFunc(t *testing.T) {
	l := testLoader(t)
	p, err := l.Open("../testdata/plugins/greet")
	if err != nil {
		t.Fatal(err)
	}
	hello, err := Func[func(string) string](p, "Hello")
	if err != nil {
		t.Fatal(err)
	}
	if got := hello("x"); got != "hello x" {
		t.Fatalf("got %q", got)
	}

	type Greeting func(string) string
	named, err := Func[Greeting](p, "Hello")
	if err != nil {
		t.Fatal(err)
	}
	if got := named("y"); got != "hello y" {
		t.Fatalf("named: %q", got)
	}

	if _, err := Func[func() int64](p, "Hello"); err == nil || !strings.Contains(err.Error(), "not func() int64") {
		t.Errorf("mismatch: err = %v", err)
	}
}
