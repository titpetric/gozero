package gozero

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// TestTypeDeclRun checks the declaration end to end on whatever tier
// Compile picks: composite literals, field reads and writes, var zero
// values, forward references, and json output.
func TestTypeDeclRun(t *testing.T) {
	decl := "type Point struct {\n\tX int64\n\tY int64\n}\n"
	for name, tc := range map[string]struct{ src, want string }{
		"literal and read":  {decl + `p := Point{X: 3, Y: 4}; json.NewEncoder(dest).Encode(p.X);`, "3\n"},
		"positional":        {decl + `p := Point{7}; json.NewEncoder(dest).Encode(p.X);`, "7\n"},
		"field write":       {decl + `p := Point{X: 3}; p.X = 5; json.NewEncoder(dest).Encode(p.X);`, "5\n"},
		"addr literal":      {decl + `q := &Point{X: 1}; json.NewEncoder(dest).Encode(q.X);`, "1\n"},
		"var zero value":    {decl + `var w Point; json.NewEncoder(dest).Encode(w.Y);`, "0\n"},
		"var field write":   {decl + `var w Point; w.Y = 7; json.NewEncoder(dest).Encode(w.Y);`, "7\n"},
		"whole value":       {decl + `p := Point{X: 1, Y: 2}; json.NewEncoder(dest).Encode(p);`, "{\"X\":1,\"Y\":2}\n"},
		"unused":            {decl + `json.NewEncoder(dest).Encode("ok");`, "\"ok\"\n"},
		"registry field":    {"type Req struct {\n\tU *url.URL\n}\nr := Req{U: url.Parse(\"https://h/p\")}; json.NewEncoder(dest).Encode(r.U.Path);", "\"/p\"\n"},
		"decl after use":    {`p := Point{X: 6}; json.NewEncoder(dest).Encode(p.X);` + "\n" + decl, "6\n"},
		"forward reference": {"type Wrap struct {\n\tInner Base\n\tM int64\n}\ntype Base struct { N int64 }\nb := Wrap{Inner: Base{N: 3}, M: 4}; json.NewEncoder(dest).Encode(b.Inner.N);", "3\n"},
	} {
		rt, _ := typeRuntime(t)
		got, err := runProgram(t, rt, tc.src)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: dest = %q, want %q", name, got, tc.want)
		}
	}
}

// TestTypeDeclBothTiers runs one program through the step JIT and the
// reflect evaluator and requires identical effects: the declaration
// itself is compile-time only, so the tiers can only diverge in what
// they compile it into.
func TestTypeDeclBothTiers(t *testing.T) {
	const src = `
		type A struct { B B }
		type B struct { C C }
		type C struct { X int64 }
		a := A{B: B{C: C{X: 42}}};
		q := &A{B: B{C: C{X: 7}}};
		json.NewEncoder(dest).Encode(a.B.C.X);
		json.NewEncoder(dest).Encode(q.B.C.X);
		json.NewEncoder(dest).Encode(a);
	`
	rt := pairRuntime(t)
	jit, slow := compilePair(t, rt, src)
	want := "42\n7\n{\"B\":{\"C\":{\"X\":42}}}\n"
	for name, fn := range map[string]CompiledFunc{"jit": jit, "reflect": slow} {
		var dest bytes.Buffer
		res, err := fn(context.Background(), nil, &dest)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res != nil {
			t.Errorf("%s: result = %v, want nil", name, res)
		}
		if dest.String() != want {
			t.Errorf("%s: dest = %q, want %q", name, dest.String(), want)
		}
	}
}

// TestTypeDeclTags checks a tag reaches reflect.StructField.Tag as
// written and shapes the encoder's output: keys renamed, an empty
// field omitted, a "-" field never emitted.
func TestTypeDeclTags(t *testing.T) {
	decl := "type Reply struct {\n" +
		"\tStatus string `json:\"status\"`\n" +
		"\tCount int64 `json:\"count,omitempty\"`\n" +
		"\tSkip string `json:\"-\"`\n" +
		"}\n"
	rt, _ := typeRuntime(t)
	fn, err := rt.Compile(decl + `return Reply{Status: "ok", Skip: "never"};`)
	if err != nil {
		t.Fatal(err)
	}
	res, err := fn.Exec[any](nil)
	if err != nil {
		t.Fatal(err)
	}
	typ := reflect.TypeOf(res)
	for i, want := range []string{`json:"status"`, `json:"count,omitempty"`, `json:"-"`} {
		if got := string(typ.Field(i).Tag); got != want {
			t.Errorf("field %d tag = %q, want %q", i, got, want)
		}
	}

	rt2, _ := typeRuntime(t)
	got, err := runProgram(t, rt2, decl+`json.NewEncoder(dest).Encode(Reply{Status: "ok", Skip: "never"});`)
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\"status\":\"ok\"}\n"; got != want {
		t.Errorf("dest = %q, want %q", got, want)
	}
}

// TestTypeDeclTagIdentity pins the StructOf fact tags add: the tag is
// part of the type's identity. The same fields with the same tags stay
// one canonical type; changing only a tag mints a second.
func TestTypeDeclTagIdentity(t *testing.T) {
	build := func(tag string) reflect.Type {
		rt, _ := typeRuntime(t)
		fn, err := rt.Compile("type P struct {\n\tX int64 `" + tag + "`\n}\nreturn P{X: 1};")
		if err != nil {
			t.Fatal(err)
		}
		res, err := fn.Exec[any](nil)
		if err != nil {
			t.Fatal(err)
		}
		return reflect.TypeOf(res)
	}
	a, b, c := build(`json:"x"`), build(`json:"x"`), build(`json:"y"`)
	if a != b {
		t.Errorf("the same tagged shape built two types: %v and %v", a, b)
	}
	if a == c {
		t.Error("shapes differing only in tags share one type; the tag should be part of identity")
	}
}

// TestTypeDeclTagsBothTiers runs a tagged program through the step JIT
// and the reflect evaluator and requires byte-identical output, with a
// tagged declared struct nested in another and a var of a declared
// name written after the fact.
func TestTypeDeclTagsBothTiers(t *testing.T) {
	src := "type Reply struct {\n" +
		"\tStatus Status `json:\"status\"`\n" +
		"\tCount int64 `json:\"count\"`\n" +
		"\tNote string `json:\"note,omitempty\"`\n" +
		"}\n" +
		"type Status struct {\n" +
		"\tCode int64 `json:\"code\"`\n" +
		"}\n" +
		"r := Reply{Status: Status{Code: 7}, Count: 2};\n" +
		"var d Reply;\n" +
		"d.Note = \"retry\";\n" +
		"json.NewEncoder(dest).Encode(r);\n" +
		"json.NewEncoder(dest).Encode(d);\n" +
		"json.NewEncoder(dest).Encode(r.Status.Code);\n"
	rt := pairRuntime(t)
	jit, slow := compilePair(t, rt, src)
	want := "{\"status\":{\"code\":7},\"count\":2}\n" +
		"{\"status\":{\"code\":0},\"count\":0,\"note\":\"retry\"}\n" +
		"7\n"
	for name, fn := range map[string]CompiledFunc{"jit": jit, "reflect": slow} {
		var dest bytes.Buffer
		res, err := fn(context.Background(), nil, &dest)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res != nil {
			t.Errorf("%s: result = %v, want nil", name, res)
		}
		if dest.String() != want {
			t.Errorf("%s: dest = %q, want %q", name, dest.String(), want)
		}
	}
}

// TestTypeDeclSupports pins the tier: the rung's constructs reach the
// direct tier, and the ones that do not name why.
func TestTypeDeclSupports(t *testing.T) {
	decl := "type Point struct {\n\tX int64\n\tY int64\n}\n"
	nested := "type Wrap struct {\n\tInner Point\n\tM int64\n}\n"
	for name, src := range map[string]string{
		"literal and read": decl + `p := Point{X: 3}; json.NewEncoder(dest).Encode(p.X);`,
		"field write":      decl + `p := Point{X: 3}; p.X = 5; json.NewEncoder(dest).Encode(p.X);`,
		"nested chain":     decl + nested + `b := Wrap{Inner: Point{X: 3}}; json.NewEncoder(dest).Encode(b.Inner.X);`,
		"pointer chain":    decl + nested + `q := &Wrap{Inner: Point{X: 3}}; json.NewEncoder(dest).Encode(q.Inner.X);`,
		"whole value":      decl + `p := Point{X: 1}; json.NewEncoder(dest).Encode(p);`,
	} {
		rt, _ := typeRuntime(t)
		if err := rt.Supports(src); err != nil {
			t.Errorf("%s: fell back to reflect: %v", name, err)
		}
	}

	// The writes the table does not have stay on the reflect
	// evaluator, each declined with the table's own reason; the result
	// is still right on that tier.
	for name, tc := range map[string]struct{ src, reason, want string }{
		"deep write":         {decl + nested + `var b Wrap; b.Inner.X = 9; json.NewEncoder(dest).Encode(b.Inner.X);`, "only a single field is in the table", "9\n"},
		"struct field write": {decl + nested + `var b Wrap; b.Inner = Point{X: 8}; json.NewEncoder(dest).Encode(b.Inner.X);`, "has no layout class", "8\n"},
		// A struct slot written more than once cannot alias the frame,
		// and a struct has no transport class to copy through, so
		// encoding it whole bridges; the write-once slot one case up
		// stays direct.
		"whole value rewritten": {decl + `var p Point; p.X = 1; json.NewEncoder(dest).Encode(p);`, "cannot become an interface", "{\"X\":1,\"Y\":0}\n"},
	} {
		rt, _ := typeRuntime(t)
		if err := rt.Supports(tc.src); err == nil || !strings.Contains(err.Error(), tc.reason) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.reason)
		}
		got, err := runProgram(t, rt, tc.src)
		if err != nil || got != tc.want {
			t.Errorf("%s: dest = %q, err = %v", name, got, err)
		}
	}
}

// TestTypeDeclErrors pins the compile errors: every reflect.StructOf
// ceiling and every rung cut is a named rule, not a panic or a
// misbuild.
func TestTypeDeclErrors(t *testing.T) {
	for name, tc := range map[string]struct{ src, want string }{
		"redeclared":        {"type P struct { X int64 }\ntype P struct { Y int64 }\nreturn 1;", "type P redeclared"},
		"shadows registry":  {"type string struct { X int64 }\nreturn 1;", "shadows a registered type"},
		"shadows binding":   {"type json struct { X int64 }\nreturn 1;", "shadows a binding"},
		"unexported field":  {"type P struct { x int64 }\nreturn 1;", "field x must be exported; reflect.StructOf cannot build unexported fields"},
		"underscore field":  {"type P struct { _ int64 }\nreturn 1;", "must be exported"},
		"duplicate field":   {"type P struct {\n\tX int64\n\tX string\n}\nreturn 1;", "duplicate field X"},
		"self reference":    {"type P struct { P P }\nreturn 1;", "type P is recursive; reflect.StructOf cannot build a struct that contains itself"},
		"mutual recursion":  {"type A struct { B B }\ntype B struct { A A }\nreturn 1;", "is recursive"},
		"pointer field":     {"type A struct { X int64 }\ntype B struct { P *A }\nreturn 1;", "a field holds a declared type by value only"},
		"slice field":       {"type A struct { X int64 }\ntype B struct { S []A }\nreturn 1;", "by value only"},
		"self pointer":      {"type P struct { Next *P }\nreturn 1;", "by value only"},
		"unknown type":      {"type P struct { X Bogus }\nreturn 1;", `unknown field type "Bogus", register it with BindType`},
		"assign type name":  {"type P struct { X int64 }\nP := 5; return P;", "shadows a binding or keyword"},
		"type as keyword":   {"type := 5; return 1;", "shadows a binding or keyword"},
		"struct as keyword": {"struct := 5; return 1;", "shadows a binding or keyword"},
	} {
		rt, _ := typeRuntime(t)
		_, err := rt.Compile(tc.src)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
}

// TestTypeDeclCanonical pins the probed reflect.StructOf fact the
// design record keeps: structurally identical declarations return the
// identical runtime type, even across runtimes, and the declared name
// never reaches reflect.
func TestTypeDeclCanonical(t *testing.T) {
	const src = "type Point struct {\n\tX int64\n\tY int64\n}\np := Point{X: 1};\nreturn p;"
	types := make([]reflect.Type, 2)
	for i := range types {
		rt, _ := typeRuntime(t)
		fn, err := rt.Compile(src)
		if err != nil {
			t.Fatal(err)
		}
		res, err := fn.Exec[any](nil)
		if err != nil {
			t.Fatal(err)
		}
		types[i] = reflect.TypeOf(res)
	}
	if types[0] != types[1] {
		t.Errorf("the same shape built two types: %v and %v", types[0], types[1])
	}
	if got := types[0].String(); got != "struct { X int64; Y int64 }" {
		t.Errorf("the type prints as %q; the declared name cannot reach reflect", got)
	}
}

// TestTypeDeclStructuralAssign pins the other probed fact: an unnamed
// struct is assignable to a named host type with the same fields, so
// a declared value reaches a binding the host typed for itself.
func TestTypeDeclStructuralAssign(t *testing.T) {
	type hostPoint struct {
		X int64
		Y int64
	}
	rt := NewRuntime()
	if err := rt.Bind("takesPoint", func(p hostPoint) string {
		return fmt.Sprintf("%d/%d", p.X, p.Y)
	}); err != nil {
		t.Fatal(err)
	}
	fn, err := rt.Compile("type Point struct {\n\tX int64\n\tY int64\n}\nreturn takesPoint(Point{X: 2, Y: 3});")
	if err != nil {
		t.Fatal(err)
	}
	got, err := fn.Exec[string](nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "2/3" {
		t.Errorf("got %q, want 2/3", got)
	}
}

// TestTypeDeclConcurrentCompile compiles typed programs concurrently
// on one runtime. Compile holds a read lock, so compilations overlap;
// the declared registry lives on a per-compilation copy, and the race
// detector holds this to it.
func TestTypeDeclConcurrentCompile(t *testing.T) {
	rt, _ := typeRuntime(t)
	done := make(chan error, 8)
	for i := 0; i < 8; i++ {
		src := fmt.Sprintf("type P struct { X int64 }\np := P{X: %d}; json.NewEncoder(dest).Encode(p.X);", i)
		go func() {
			fn, err := rt.compileUncached(src)
			if err != nil {
				done <- err
				return
			}
			var dest bytes.Buffer
			if _, err := fn(context.Background(), nil, &dest); err != nil {
				done <- err
				return
			}
			done <- nil
		}()
	}
	for i := 0; i < 8; i++ {
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
}

// TestTypeDeclScoped checks the registry is per compilation: a
// declared name neither leaks into the shared Compiler nor into the
// next program.
func TestTypeDeclScoped(t *testing.T) {
	rt, _ := typeRuntime(t)
	if _, err := rt.Compile("type Point struct { X int64 }\np := Point{X: 1}; json.NewEncoder(dest).Encode(p.X);"); err != nil {
		t.Fatal(err)
	}
	if rt.compiler.declaredTypes != nil {
		t.Fatal("the shared compiler kept a program's declared types")
	}
	if _, err := rt.Compile(`var p Point; return p;`); err == nil || !strings.Contains(err.Error(), "unknown type") {
		t.Errorf("Point leaked into the next program: err = %v", err)
	}
}
