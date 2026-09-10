package gozero

import (
	"strings"
	"testing"
)

// TestParseStructDecl covers the struct declaration forms: fields,
// name lists, tags in both spellings, embedded types, and the
// declarations landing in program.types apart from the statements.
func TestParseStructDecl(t *testing.T) {
	src := `type Point struct {
	X int64 ` + "`json:\"x\"`" + `
	Y, Z int64
	Label string "json:\"label\""
	Nested *Point
	url.URL
	*bytes.Buffer
	Inner
}
p := mk();`
	prog, err := (&Parser{}).Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(prog.types) != 1 || len(prog.stmts) != 1 {
		t.Fatalf("types = %d, stmts = %d", len(prog.types), len(prog.stmts))
	}
	td := prog.types[0]
	if td.name != "Point" || td.iface {
		t.Fatalf("decl = %+v", td)
	}
	want := []fieldDecl{
		{name: "X", typ: "int64", tag: `json:"x"`},
		{name: "Y", typ: "int64"},
		{name: "Z", typ: "int64"},
		{name: "Label", typ: "string", tag: `json:"label"`},
		{name: "Nested", typ: "*Point"},
		{name: "URL", typ: "url.URL", embedded: true},
		{name: "Buffer", typ: "*bytes.Buffer", embedded: true},
		{name: "Inner", typ: "Inner", embedded: true},
	}
	if len(td.fields) != len(want) {
		t.Fatalf("fields = %+v", td.fields)
	}
	for i, w := range want {
		if td.fields[i] != w {
			t.Errorf("field %d = %+v, want %+v", i, td.fields[i], w)
		}
	}

	// One-line and empty bodies.
	prog, err = (&Parser{}).Parse(`type A struct{}; type B struct{ N int64 }; f();`)
	if err != nil {
		t.Fatal(err)
	}
	if len(prog.types) != 2 || len(prog.types[1].fields) != 1 {
		t.Fatalf("types = %+v", prog.types)
	}
}

// TestParseInterfaceDecl covers method signatures: named and unnamed
// parameters, grouped names, result forms.
func TestParseInterfaceDecl(t *testing.T) {
	src := `type Store interface {
	Get(key string) (string, error)
	Put(key, value string) error
	Len() int64
	Reset()
	Both(int64, string)
}
f();`
	prog, err := (&Parser{}).Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	td := prog.types[0]
	if !td.iface || td.name != "Store" || len(td.methods) != 5 {
		t.Fatalf("decl = %+v", td)
	}
	check := func(i int, name string, params, results []param) {
		t.Helper()
		m := td.methods[i]
		if m.name != name || len(m.params) != len(params) || len(m.results) != len(results) {
			t.Fatalf("method %d = %+v", i, m)
		}
		for j := range params {
			if m.params[j] != params[j] {
				t.Errorf("%s param %d = %+v, want %+v", name, j, m.params[j], params[j])
			}
		}
		for j := range results {
			if m.results[j] != results[j] {
				t.Errorf("%s result %d = %+v, want %+v", name, j, m.results[j], results[j])
			}
		}
	}
	check(0, "Get", []param{{"key", "string"}}, []param{{"", "string"}, {"", "error"}})
	check(1, "Put", []param{{"key", "string"}, {"value", "string"}}, []param{{"", "error"}})
	check(2, "Len", nil, []param{{"", "int64"}})
	check(3, "Reset", nil, nil)
	check(4, "Both", []param{{"", "int64"}, {"", "string"}}, nil)
}

// TestParseTypeDeclRejects pins the failure modes and the rewinds
// that keep old programs parsing.
func TestParseTypeDeclRejects(t *testing.T) {
	for name, tc := range map[string]struct{ src, want string }{
		"missing brace":       {`type X struct f();`, "expected '{'"},
		"unterminated struct": {`type X struct { A int64`, "unterminated struct body"},
		"embedded slice":      {`type X struct { []int64 }; f();`, "cannot embed"},
		"iface embed":         {`type I interface { io.Reader }; f();`, "embedding an interface is not supported"},
		// Go's grouping makes M(int64, a string) legal, int64 being a
		// parameter name; only a non-ident type mixes.
		"mixed params": {`type I interface { M(*url.URL, a string) }; f();`, "cannot mix named and unnamed parameters"},
	} {
		if _, err := (&Parser{}).Parse(tc.src); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}

	// The sniff rewinds: "type" not followed by the declaration shape
	// stays an ordinary identifier, and "types" is not the keyword.
	for _, src := range []string{`type := f();`, `types := f();`} {
		prog, err := (&Parser{}).Parse(src)
		if err != nil {
			t.Errorf("%q: %v", src, err)
			continue
		}
		if len(prog.stmts) != 1 || len(prog.types) != 0 {
			t.Errorf("%q: parsed as %+v", src, prog)
		}
	}
}

// TestParseMapTypeRef pins the map spelling typeRef produces, which
// must match reflect.Type.String.
func TestParseMapTypeRef(t *testing.T) {
	for src, want := range map[string]string{
		`var m map[string]int64;f();`:     "map[string]int64",
		`var m map[string]*url.URL;f();`:  "map[string]*url.URL",
		`var m map[string][]byte;f();`:    "map[string][]byte",
		`var m []map[int64]string;f();`:   "[]map[int64]string",
		`var m map[string]chan bool;f();`: "map[string]chan bool",
		`var m map[int64]map[k]v; f();`:   "map[int64]map[k]v",
	} {
		prog, err := (&Parser{}).Parse(src)
		if err != nil {
			t.Errorf("%q: %v", src, err)
			continue
		}
		if got := prog.stmts[0].varType; got != want {
			t.Errorf("%q: varType = %q, want %q", src, got, want)
		}
	}
}
