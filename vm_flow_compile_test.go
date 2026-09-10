package gozero

import (
	"strings"
	"testing"
)

// TestFlowCompileNesting drives compileFlow through deep nesting:
// loops in ifs in loops, with the scope chain and jump checks intact
// at every depth.
func TestFlowCompileNesting(t *testing.T) {
	rt := exprRuntime(t)
	src := `total := 0
for i := 0; i < 3; i++ {
	if i > 0 {
		for j := 0; j < i; j++ {
			if j == 1 {
				continue
			}
			total = total + 10*i + j
		}
	} else {
		total++
	}
}
return total`
	got, err := rt.Eval[any](src, nil)
	if err != nil {
		t.Fatal(err)
	}
	// i=0: else, +1. i=1: j=0 +10. i=2: j=0 +20, j=1 skipped.
	if got != int64(31) {
		t.Fatalf("got %v", got)
	}

	// break placement is checked per nesting level, not globally.
	if _, err := rt.Compile(`for { if 1 > 0 { break } }; break;`); err == nil || !strings.Contains(err.Error(), "break is not in a loop") {
		t.Errorf("err = %v", err)
	}
}
