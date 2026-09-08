package gozero

import (
	"net/http"
	"net/url"
	"testing"
)

// TestStructLiteral checks composite literals: T{} and &T{} with
// keyed and positional elements, matching Go syntax, against any
// struct type in the registry.
func TestStructLiteral(t *testing.T) {
	rt, seen := typeRuntime(t)
	for _, tc := range []struct{ name, src, want string }{
		{"empty value", `u = url.URL{}; json.NewEncoder(dest).Encode(u.Path);`, "\"\"\n"},
		{"keyed", `u = url.URL{Path: "/x", Host: "h"}; json.NewEncoder(dest).Encode(u.Path);`, "\"/x\"\n"},
		{"pointer", `u = &url.URL{Path: "/p"}; json.NewEncoder(dest).Encode(u.Path);`, "\"/p\"\n"},
		{"positional", `u = url.URL{"https"}; json.NewEncoder(dest).Encode(u.Scheme);`, "\"https\"\n"},
		{"nested", `r = &http.Request{Method: "POST", URL: &url.URL{Path: "/n"}}; json.NewEncoder(dest).Encode(r.URL.Path);`, "\"/n\"\n"},
		{"field write after", `u := url.URL{}; u.Path = "/w"; json.NewEncoder(dest).Encode(u.Path);`, "\"/w\"\n"},
		{"literal converts to field width", `r = &http.Request{ProtoMajor: 1}; json.NewEncoder(dest).Encode(r.ProtoMajor);`, "1\n"},
		{"call as element", `r = &http.Request{URL: url.Parse("http://h/c")}; json.NewEncoder(dest).Encode(r.URL.Path);`, "\"/c\"\n"},
		{"method on the value", `u := &url.URL{Path: "/m"}; json.NewEncoder(dest).Encode(u.String());`, "\"/m\"\n"},
		{"trailing comma multiline", "u = url.URL{\n\tPath: \"/t\",\n};\njson.NewEncoder(dest).Encode(u.Path);", "\"/t\"\n"},
	} {
		got, err := runProgram(t, rt, tc.src)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: dest = %q, want %q", tc.name, got, tc.want)
		}
	}

	// A literal in argument position carries its own type.
	*seen = nil
	if _, err := runProgram(t, rt, `takesAny(url.URL{Path: "/a"});`); err != nil {
		t.Fatal(err)
	}
	u, ok := (*seen).(url.URL)
	if !ok || u.Path != "/a" {
		t.Errorf("binding saw %#v, want url.URL{Path: %q}", *seen, "/a")
	}

	// &T{} allocates per evaluation: two runs of one compiled program
	// must not share the struct.
	fn, err := rt.Compile(`r := &http.Request{Method: "X"}; return r;`)
	if err != nil {
		t.Fatal(err)
	}
	a, err := fn.Exec[*http.Request](nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := fn.Exec[*http.Request](nil)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("two runs returned the same *http.Request")
	}
	a.Method = "mutated"
	if b.Method != "X" {
		t.Errorf("mutating one run's value reached the other: Method = %q", b.Method)
	}
}

// TestStructLiteralSupports checks that a program with a composite
// literal is declined by the direct tier with a reason, rather than
// mishandled: no JIT node builds a struct, so it runs on reflect.
func TestStructLiteralSupports(t *testing.T) {
	rt, _ := typeRuntime(t)
	err := rt.Supports(`u = url.URL{Path: "/"}; json.NewEncoder(dest).Encode(u.Path);`)
	if err == nil {
		t.Fatal("a composite literal should not reach the direct tier")
	}
	t.Log(err)
}

// TestStructLiteralErrors checks that a composite literal reports its
// mismatches at compile time, the way a call does.
func TestStructLiteralErrors(t *testing.T) {
	rt, _ := typeRuntime(t)
	for _, tc := range []struct{ name, src string }{
		{"unknown type", `u = nope.Thing{};`},
		{"not a struct", `x = int64{};`},
		{"unknown field", `u = url.URL{Nope: 1};`},
		{"duplicate field", `u = url.URL{Path: "/", Path: "/y"};`},
		{"mixed keyed and positional", `u = url.URL{Path: "/", "x"};`},
		{"value type mismatch", `u = url.URL{Path: 5};`},
		{"pointer where value declared", `var u url.URL; u = &url.URL{};`},
	} {
		_, err := rt.Compile(tc.src)
		if err == nil {
			t.Errorf("%s: expected a compile error", tc.name)
			continue
		}
		t.Logf("%s: %v", tc.name, err)
	}
}
