package gozero

import (
	"strings"
	"testing"
)

// TestBindOption checks that options apply through Bind and
// BindScope.
func TestBindOption(t *testing.T) {
	rt := NewRuntime()
	if err := rt.BindScope("s", map[string]any{"join": strings.Join}, NonRetaining()); err != nil {
		t.Fatal(err)
	}
	if !rt.compiler.bindings["s.join"].nonRetaining {
		t.Error("BindScope did not apply the option")
	}
}

// TestNonRetaining checks the flag lands on the binding and on the
// compiled call.
func TestNonRetaining(t *testing.T) {
	rt := NewRuntime()
	if err := rt.Bind("j", strings.Join, NonRetaining()); err != nil {
		t.Fatal(err)
	}
	if !rt.compiler.bindings["j"].nonRetaining {
		t.Fatal("Bind did not apply the option")
	}
	prog, err := (&Parser{}).Parse(`s := j(parts, ","); return s`)
	if err != nil {
		t.Fatal(err)
	}
	p, err := rt.compiler.compileProgram(prog)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range p.stmts {
		if st.call != nil && !st.call.nonRet {
			t.Error("the compiled call lost the NonRetaining flag")
		}
	}
}
