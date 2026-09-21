package gozero

import (
	"errors"
	"strings"
	"testing"
)

// TestSignalStaysInside pins the containment property the control
// channel rests on: a program that returns from inside an arm hands
// its caller a value and a nil error, never the sentinel. A leak
// would turn an ordinary return into a failure at the call site.
func TestSignalStaysInside(t *testing.T) {
	rt := ifCompileRuntime(t)
	for name, tc := range map[string]struct {
		src  string
		want any
	}{
		"value from an arm": {`s := record("v"); if yes("") { return s; }; return;`, "v"},
		"bare from an arm":  {`s := record("v"); if yes("") { return; }; return s;`, nil},
		"arm not taken":     {`s := record("v"); if no("") { return; }; return s;`, "v"},
	} {
		got, err := rt.Eval[any](tc.src, nil)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}

// TestSignalNotAnError pins the other half: the sentinel is not a
// value a program can produce or a binding can forge, so a real
// failure and a return never read the same.
func TestSignalNotAnError(t *testing.T) {
	rt := ifCompileRuntime(t)
	if err := rt.Bind("fail", func() error { return errors.New("boom") }); err != nil {
		t.Fatal(err)
	}
	_, err := rt.Eval[any](`if yes("") { fail(); }; record("tail");`, nil)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want the binding's own error", err)
	}
	if errors.Is(err, errProgramReturn) {
		t.Error("a binding's error must not read as the return signal")
	}
}
