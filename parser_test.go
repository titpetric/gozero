package gozero

import (
	"testing"
)

// TestParser checks that one Parser value can be reused: Parse resets
// the position, so a second program does not see the first one's tail.
func TestParser(t *testing.T) {
	p := &Parser{}
	first, err := p.Parse(`return f("a"); return f("b");`)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.stmts) != 2 {
		t.Fatalf("first program has %d statements, want 2", len(first.stmts))
	}
	second, err := p.Parse(`return f("c");`)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.stmts) != 1 {
		t.Fatalf("second program has %d statements, want 1", len(second.stmts))
	}
}

func TestParser_Parse(t *testing.T) {
	for name, tc := range map[string]struct {
		src   string
		stmts int
	}{
		"flat call":     {`return f("GET", "https://example.com");`, 1},
		"single quotes": {`return f('GET');`, 1},
		"assignment":    {`req := f("GET"); return req;`, 2},
		"var and field": {`var r *http.Request; r.Method = "POST"; return r;`, 3},
		"dotted path":   {`return json.NewEncoder(dest).Encode(v);`, 1},
		"no semicolons": {"req := f(\"GET\")\nreturn req", 2},
		"mixed lines":   {"a := f(\"x\"); b := f(\"y\")\nreturn b", 3},
		"bare return":   {"f(\"GET\")\nreturn", 2},
	} {
		prog, err := (&Parser{}).Parse(tc.src)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if len(prog.stmts) != tc.stmts {
			t.Errorf("%s: %d statements, want %d", name, len(prog.stmts), tc.stmts)
		}
	}

	for name, src := range map[string]string{
		"empty":          ``,
		"unterminated":   `return f("GET`,
		"trailing input": `return f("GET"); extra`,
		"var no name":    `var ; return f();`,
	} {
		if _, err := (&Parser{}).Parse(src); err == nil {
			t.Errorf("%s: expected a parse error for %q", name, src)
		}
	}

	// A one-statement call is a flat call; a two-statement program is
	// not, which is what routes it to the VM.
	prog, err := (&Parser{}).Parse(`return f("x");`)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := prog.flatCall(); !ok {
		t.Error("a single return call should report as flat")
	}
	prog, err = (&Parser{}).Parse(`a := f("x"); return a;`)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := prog.flatCall(); ok {
		t.Error("a two-statement program should not report as flat")
	}
}
