package gozero

import (
	"strings"
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

// TestParser_IncDec covers the step statement: "n++;" parses to a
// statement of its own, a step never stands where a value belongs,
// and a dotted target is rejected by name.
func TestParser_IncDec(t *testing.T) {
	for name, tc := range map[string]struct {
		src   string
		delta int64
	}{
		"increment":       {`n++;`, 1},
		"decrement":       {`n--;`, -1},
		"spaced operator": {`n ++;`, 1},
		"no semicolon":    {"n++\n", 1},
	} {
		prog, err := (&Parser{}).Parse(tc.src)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		s := prog.stmts[0]
		if s.incName != "n" || s.incDelta != tc.delta {
			t.Errorf("%s: parsed %q delta %d, want n delta %d", name, s.incName, s.incDelta, tc.delta)
		}
	}

	// A step statement ends at the line: a "++" on the next line is
	// not joined to a name above it.
	if _, err := (&Parser{}).Parse("n\n++;"); err == nil {
		t.Error("a name and a ++ on separate lines should not join")
	}

	for name, tc := range map[string]struct {
		src, want string
	}{
		"argument":       {`f(n++);`, "n++ is a statement, not a value"},
		"assignment rhs": {`m := n++;`, "n++ is a statement, not a value"},
		"return value":   {`return n--;`, "n-- is a statement, not a value"},
		"send value":     {`c <- n++;`, "n++ is a statement, not a value"},
		"field target":   {`u.Path++;`, "u.Path++: ++ and -- step a name, not a field"},
		"unterminated":   {`n++ f();`, "expected ';' or end of line"},
	} {
		_, err := (&Parser{}).Parse(tc.src)
		if err == nil {
			t.Errorf("%s: expected a parse error for %q", name, tc.src)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q does not name the rule %q", name, err, tc.want)
		}
	}
}
