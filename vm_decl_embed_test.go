package gozero

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// embedDecl is the embedded-record pair the embed tests share: Server
// embeds Endpoint, so Host and Port promote through it.
const embedDecl = "type Endpoint struct {\n\tHost string `json:\"host\"`\n\tPort int64 `json:\"port\"`\n}\n" +
	"type Server struct {\n\tEndpoint\n\tName string `json:\"name\"`\n}\n"

// TestTypeDeclEmbed checks embedded record fields end to end on
// whatever tier Compile picks: the promoted names read and write
// through the embedded field, the explicit path still works, the
// literal sets the embedded field whole, and json flattens it.
func TestTypeDeclEmbed(t *testing.T) {
	for name, tc := range map[string]struct{ src, want string }{
		"promoted read":  {embedDecl + `s := Server{Endpoint: Endpoint{Host: "h", Port: 1}, Name: "n"}; json.NewEncoder(dest).Encode(s.Host);`, "\"h\"\n"},
		"promoted write": {embedDecl + `var w Server; w.Port = 9; json.NewEncoder(dest).Encode(w.Port);`, "9\n"},
		"explicit path":  {embedDecl + `var w Server; w.Host = "e"; json.NewEncoder(dest).Encode(w.Endpoint.Host);`, "\"e\"\n"},
		"json flatten":   {embedDecl + `s := Server{Endpoint: Endpoint{Host: "h", Port: 1}, Name: "n"}; json.NewEncoder(dest).Encode(s);`, "{\"host\":\"h\",\"port\":1,\"name\":\"n\"}\n"},
		"embed of embed": {embedDecl + "type Fleet struct {\n\tServer\n}\n" + `var f Fleet; f.Host = "deep"; json.NewEncoder(dest).Encode(f.Host);`, "\"deep\"\n"},
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

	// A promoted field is not a field of the literal's type, as in Go.
	rt, _ := typeRuntime(t)
	if _, err := rt.Compile(embedDecl + `s := Server{Host: "h"}; return s;`); err == nil || !strings.Contains(err.Error(), "promoted field") {
		t.Errorf("promoted literal key: err = %v", err)
	}
}

// TestTypeDeclEmbedBothTiers runs the promoted reads and writes down
// both tiers and requires identical bytes; TestTypeDeclEmbedSupports
// pins that the JIT side really is the direct tier.
func TestTypeDeclEmbedBothTiers(t *testing.T) {
	src := embedDecl + `
		s := Server{Endpoint: Endpoint{Host: "example.com", Port: 8080}, Name: "edge"};
		var w Server;
		w.Host = "db.local";
		w.Port = 5432;
		json.NewEncoder(dest).Encode(s);
		json.NewEncoder(dest).Encode(s.Host);
		json.NewEncoder(dest).Encode(w.Endpoint.Host);
		json.NewEncoder(dest).Encode(w.Port);
	`
	rt := pairRuntime(t)
	jit, slow := compilePair(t, rt, src)
	want := "{\"host\":\"example.com\",\"port\":8080,\"name\":\"edge\"}\n" +
		"\"example.com\"\n\"db.local\"\n5432\n"
	for name, fn := range map[string]CompiledFunc{"jit": jit, "reflect": slow} {
		var dest bytes.Buffer
		if _, err := fn(context.Background(), nil, &dest); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if dest.String() != want {
			t.Errorf("%s: dest = %q, want %q", name, dest.String(), want)
		}
	}
}

// TestTypeDeclEmbedSupports pins the tier for the embed forms: a
// promoted read and write are single summed offsets and stay direct.
func TestTypeDeclEmbedSupports(t *testing.T) {
	for name, src := range map[string]string{
		"promoted read":  embedDecl + `s := Server{Endpoint: Endpoint{Host: "h", Port: 1}}; json.NewEncoder(dest).Encode(s.Host);`,
		"promoted write": embedDecl + `var w Server; w.Port = 9; json.NewEncoder(dest).Encode(w.Port);`,
		"explicit path":  embedDecl + `var w Server; w.Host = "e"; json.NewEncoder(dest).Encode(w.Endpoint.Host);`,
	} {
		rt, _ := typeRuntime(t)
		if err := rt.Supports(src); err != nil {
			t.Errorf("%s: fell back to reflect: %v", name, err)
		}
	}
}

// TestTypeDeclEmbedIdentity pins the StructOf fact embedding adds: the
// embedded flag is part of the type's identity, so the same two fields
// named and embedded are two distinct types.
func TestTypeDeclEmbedIdentity(t *testing.T) {
	build := func(src string) reflect.Type {
		rt, _ := typeRuntime(t)
		fn, err := rt.Compile(src)
		if err != nil {
			t.Fatal(err)
		}
		res, err := fn.Exec[any](nil)
		if err != nil {
			t.Fatal(err)
		}
		return reflect.TypeOf(res)
	}
	decl := "type Endpoint struct {\n\tHost string\n}\n"
	embedded := build(decl + "type S struct {\n\tEndpoint\n}\nreturn S{};")
	named := build(decl + "type S struct {\n\tEndpoint Endpoint\n}\nreturn S{};")
	if embedded == named {
		t.Error("an embedded and a named field of the same type share one type; the embedded flag should be part of identity")
	}
	if !embedded.Field(0).Anonymous {
		t.Error("the embedded field lost its Anonymous flag")
	}
}

// hostSession is a named host parameter type no binding constructs: the
// structural-pass tests declare a struct of the same shape and pass it
// by value, which is the capability widening the design doc flags.
type hostSession struct {
	User string
	Host string
	Port int64
}

// sessionDecl spells hostSession's shape as a program declaration.
const sessionDecl = "type Session struct {\n\tUser string\n\tHost string\n\tPort int64\n}\n"

func sessionRuntime(t *testing.T) *Runtime {
	t.Helper()
	rt := pairRuntime(t)
	if err := rt.Bind("format", func(s hostSession) string {
		return fmt.Sprintf("%s@%s:%d", s.User, s.Host, s.Port)
	}); err != nil {
		t.Fatal(err)
	}
	return rt
}

// TestTypeDeclStructuralPass checks a declared struct held in a name
// crosses into a named host parameter type by value, on both tiers:
// the reflect evaluator through Value.Call's assignability, the direct
// tier through the one bridged call the parameter forces.
func TestTypeDeclStructuralPass(t *testing.T) {
	src := sessionDecl + `
		c := Session{User: "ana", Host: "db.local", Port: 5432};
		line := format(c);
		json.NewEncoder(dest).Encode(line);
	`
	rt := sessionRuntime(t)
	jit, slow := compilePairBridged(t, rt, src)
	want := "\"ana@db.local:5432\"\n"
	for name, fn := range map[string]CompiledFunc{"jit": jit, "reflect": slow} {
		var dest bytes.Buffer
		if _, err := fn(context.Background(), nil, &dest); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if dest.String() != want {
			t.Errorf("%s: dest = %q, want %q", name, dest.String(), want)
		}
	}

	// The decline is the shape table's own: a struct passed by value
	// has no layout class, so the one call bridges, by name.
	if err := rt.Supports(src); err == nil || !strings.Contains(err.Error(), "has no layout class") {
		t.Errorf("Supports = %v, want the layout-class reason", err)
	}

	// The shape must still match: one field off is a compile error.
	bad := "type Wrong struct {\n\tUser string\n\tHost string\n}\n" + `return format(Wrong{User: "a"});`
	if _, err := sessionRuntime(t).Compile(bad); err == nil || !strings.Contains(err.Error(), "cannot use") {
		t.Errorf("mismatched shape compiled: err = %v", err)
	}

	// Pointers do not carry the assignability, as in Go: a pointer to
	// the declared shape is not a pointer to the named type.
	rtp := sessionRuntime(t)
	if err := rtp.Bind("formatPtr", func(s *hostSession) string { return s.User }); err != nil {
		t.Fatal(err)
	}
	ptr := sessionDecl + `return formatPtr(&Session{User: "ana"});`
	if _, err := rtp.Compile(ptr); err == nil || !strings.Contains(err.Error(), "cannot use") {
		t.Errorf("pointer structural pass compiled: err = %v", err)
	}
}

