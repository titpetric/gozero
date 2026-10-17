package gozero

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"net/url"
	"os"
	"sort"
	"strings"
	"testing"
	"text/tabwriter"
)

func typeRuntime(t *testing.T) (*Runtime, *any) {
	t.Helper()
	rt := NewRuntime()
	var seen any
	for name, fns := range map[string]map[string]any{
		"http": {"NewRequest": http.NewRequest},
		"json": {"NewEncoder": json.NewEncoder},
		"url":  {"Parse": url.Parse},
	} {
		if err := rt.BindScope(name, fns); err != nil {
			t.Fatal(err)
		}
	}
	for name, fn := range map[string]any{
		"takesInt": func(n int) (*url.URL, error) { seen = n; return &url.URL{}, nil },
		"takesI8":  func(n int8) (*url.URL, error) { seen = n; return &url.URL{}, nil },
		"takesU32": func(n uint32) (*url.URL, error) { seen = n; return &url.URL{}, nil },
		"takesF32": func(n float32) (*url.URL, error) { seen = n; return &url.URL{}, nil },
		// The binding contract makes v borrowed: copyBoxed moves it
		// into a fresh box before it is stored, or a pooled box would
		// be cleared under the recorder after the run.
		"takesAny":  func(v any) (*url.URL, error) { seen = copyBoxed(v); return &url.URL{}, nil },
		"takesI64":  func(n int64) (*url.URL, error) { seen = n; return &url.URL{}, nil },
		"takesBool": func(b bool) (*url.URL, error) { seen = b; return &url.URL{}, nil },
	} {
		if err := rt.Bind(name, fn); err != nil {
			t.Fatal(err)
		}
	}
	return rt, &seen
}

// run compiles and executes src, returning what dest received.
func runProgram(t *testing.T, rt *Runtime, src string) (string, error) {
	t.Helper()
	fn, err := rt.Compile(src)
	if err != nil {
		return "", err
	}
	var dest bytes.Buffer
	if err := fn.Scan(&dest, nil); err != nil {
		return "", err
	}
	return dest.String(), nil
}

// TestVarDeclaration checks that var puts the zero value of a named
// type in scope and fixes the type of what is assigned to it.
func TestVarDeclaration(t *testing.T) {
	rt, seen := typeRuntime(t)
	for _, tc := range []struct{ name, src, want string }{
		{"assign then read", `var x int64; x = 123; json.NewEncoder(dest).Encode(x);`, "123\n"},
		{"narrower type", `var x int32; x = 7; json.NewEncoder(dest).Encode(x);`, "7\n"},
		{"zero value", `var x int64; json.NewEncoder(dest).Encode(x);`, "0\n"},
		{"string", `var s string; s = "hi"; json.NewEncoder(dest).Encode(s);`, "\"hi\"\n"},
		{"discovered struct", `var u url.URL; json.NewEncoder(dest).Encode(u.Path);`, "\"\"\n"},
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

	// The declared type reaches the binding.
	*seen = nil
	if _, err := runProgram(t, rt, `var x int64; x = 123; takesI64(x); json.NewEncoder(dest).Encode("ok");`); err != nil {
		t.Fatal(err)
	}
	if *seen != int64(123) {
		t.Errorf("binding saw %v (%T), want int64(123)", *seen, *seen)
	}
}

// TestTypeRegistry checks what a var statement can name. Almost
// everything comes from walking the bindings; BindType covers the rest.
func TestRuntime_BindType(t *testing.T) {
	rt, _ := typeRuntime(t)
	for _, want := range []string{
		"int", "int64", "uint32", "float32", "string", "bool", "any", "error", "[]uint8",
		"*http.Request", "http.Request", "http.Header", "io.Reader", "io.Writer",
		"*json.Encoder", "*url.URL", "url.URL",
	} {
		if _, ok := rt.compiler.lookupType(want); !ok {
			t.Errorf("%s is not in the registry", want)
		}
	}

	if _, err := rt.Compile(`var c io.Closer; json.NewEncoder(dest).Encode("x");`); err == nil {
		t.Fatal("io.Closer should not be reachable from these bindings")
	}
	if err := rt.BindType("io.Closer", (*io.Closer)(nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Compile(`var c io.Closer; json.NewEncoder(dest).Encode("x");`); err != nil {
		t.Errorf("after BindType: %v", err)
	}
}

// TestRuntime_Types checks that the listing carries both the
// predeclared names and what discovery found.
func TestRuntime_Types(t *testing.T) {
	rt, _ := typeRuntime(t)
	names := map[string]bool{}
	for _, name := range rt.Types() {
		names[name] = true
	}
	for _, want := range []string{"string", "*http.Request"} {
		if !names[want] {
			t.Errorf("%s is not listed by Types", want)
		}
	}
}

// TestVarPointerAndSliceTypes checks the prefixes a type name can carry.
func TestVarPointerAndSliceTypes(t *testing.T) {
	rt, _ := typeRuntime(t)
	for _, src := range []string{
		`var r *http.Request; json.NewEncoder(dest).Encode("x");`,
		`var b []uint8; json.NewEncoder(dest).Encode("x");`,
	} {
		if _, err := rt.Compile(src); err != nil {
			t.Errorf("%s: %v", src, err)
		}
	}
	if _, err := rt.Compile(`var x nope.Thing; json.NewEncoder(dest).Encode("x");`); err == nil {
		t.Fatal("expected an unknown type to be reported")
	}
}

// TestScalarsJIT checks that the scalar work reaches the direct-call
// tier rather than sending the program to the reflect evaluator: a var
// declaration, a literal assignment, a scalar read back out of a slot,
// a scalar argument at several widths, and a scalar boxed into an
// interface.
func TestScalarsJIT(t *testing.T) {
	rt, seen := typeRuntime(t)
	for _, tc := range []struct {
		name, src, want string
		// wantSeen is the value the binding must receive on the direct
		// tier, for the cases that pass one; nil skips the check.
		wantSeen any
	}{
		{"var and assign", `var x int64; x = 1; json.NewEncoder(dest).Encode(x);`, "1\n", nil},
		{"inferred literal", `x := 1; json.NewEncoder(dest).Encode(x);`, "1\n", nil},
		{"declared int32", `var x int32; x = 7; json.NewEncoder(dest).Encode(x);`, "7\n", nil},
		{"conversion hint", `x := int32(7); json.NewEncoder(dest).Encode(x);`, "7\n", nil},
		{"declared float64", `var x float64; x = 2.5; json.NewEncoder(dest).Encode(x);`, "2.5\n", nil},
		{"declared bool zero", `var b bool; json.NewEncoder(dest).Encode(b);`, "false\n", nil},
		{"scalar argument", `var x int64; x = 3; takesI64(x); json.NewEncoder(dest).Encode(x);`, "3\n", int64(3)},
		{"literal argument", `json.NewEncoder(dest).Encode(takesInt(42));`, "", int(42)},
		{"scalar field", `req := http.NewRequest("GET", "/"); json.NewEncoder(dest).Encode(req.ContentLength);`, "0\n", nil},
	} {
		if err := rt.Supports(tc.src); err != nil {
			t.Errorf("%s: did not JIT: %v", tc.name, err)
			continue
		}
		*seen = nil
		got, err := runProgram(t, rt, tc.src)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if tc.want != "" && got != tc.want {
			t.Errorf("%s: dest = %q, want %q", tc.name, got, tc.want)
		}
		if tc.wantSeen != nil && *seen != tc.wantSeen {
			t.Errorf("%s: binding saw %v (%T), want %v (%T)", tc.name, *seen, *seen, tc.wantSeen, tc.wantSeen)
		}
	}
}

// constructors is the set of standard library functions the discovery
// test binds. They are ordinary constructors, chosen because between
// them they name most of the shapes a binding can have: a pointer
// result, an interface result, an error result, a variadic, and a
// function that takes an interface.
//
// net/http has no NewClient. Its only exported constructors are
// NewRequest and NewRequestWithContext, so the client side is
// represented by the types those reach.
var constructors = map[string]any{
	"http.NewRequest":            http.NewRequest,
	"http.NewRequestWithContext": http.NewRequestWithContext,
	"http.NewServeMux":           http.NewServeMux,
	"http.FileServer":            http.FileServer,
	"http.StripPrefix":           http.StripPrefix,
	"pprof.Handler":              pprof.Handler,
	"json.NewEncoder":            json.NewEncoder,
	"json.NewDecoder":            json.NewDecoder,
	"url.Parse":                  url.Parse,
	"url.ParseQuery":             url.ParseQuery,
}

// TestDiscovery binds each constructor into its own runtime and reports
// what the walk found, then binds them all together and reports the
// union. Run it with -v to see the tables and the log.
//
//	atkins test:discovery
func TestDiscovery(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			// The timestamp is noise in a golden-ish log.
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	}))

	names := make([]string, 0, len(constructors))
	for name := range constructors {
		names = append(names, name)
	}
	sort.Strings(names)

	out := &bytes.Buffer{}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "BINDING\tOWN TYPES\tNOTABLE")

	for _, name := range names {
		rt := NewRuntime()
		rt.SetLogger(logger)
		if err := rt.Bind(name, constructors[name]); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		found := discovered(rt)
		fmt.Fprintf(tw, "%s\t%d\t%s\n", name, len(found), strings.Join(notable(found), " "))
	}

	all := NewRuntime()
	all.SetLogger(logger)
	for _, name := range names {
		if err := all.Bind(name, constructors[name]); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	union := discovered(all)
	fmt.Fprintf(tw, "%s\t%d\t\n", "(all together)", len(union))
	tw.Flush()

	// http.Handler is not named by any signature in net/http's
	// constructors; it arrives because pprof.Handler returns one.
	if _, ok := all.compiler.lookupType("http.Handler"); !ok {
		t.Error("http.Handler was not discovered from pprof.Handler")
	}
	if _, ok := all.compiler.lookupType("*http.ServeMux"); !ok {
		t.Error("*http.ServeMux was not discovered")
	}

	if !testing.Verbose() {
		t.Logf("%d types discovered from %d bindings, run with -v for the tables", len(union), len(names))
		return
	}

	fmt.Println()
	fmt.Println("=== discovery per binding ===")
	fmt.Print(out.String())

	fmt.Println()
	fmt.Println("=== every name a var statement can use ===")
	printColumns(union)

	fmt.Println()
	fmt.Println("=== slog output, http.NewRequest and pprof.Handler only ===")
	printLogFor(logBuf.String(), "http.NewRequest", "pprof.Handler")
}

// discovered lists the registry minus the predeclared seed, so the
// count is what the bindings actually contributed.
func discovered(rt *Runtime) []string {
	seed := predeclared()
	var out []string
	for _, name := range rt.Types() {
		if _, ok := seed[name]; !ok {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// notable picks a few recognisable names so the per-binding row says
// something without printing the whole set.
func notable(names []string) []string {
	want := []string{"*http.Request", "http.Handler", "*http.ServeMux", "*url.URL", "*json.Encoder", "url.Values", "io.Reader", "io.Writer"}
	var out []string
	for _, w := range want {
		for _, n := range names {
			if n == w {
				out = append(out, w)
				break
			}
		}
	}
	return out
}

func printColumns(names []string) {
	const cols = 3
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for i := 0; i < len(names); i += cols {
		row := names[i:min(i+cols, len(names))]
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	tw.Flush()
}

// printLogFor prints the discovery log lines belonging to the named
// bindings, which is the walk as it happened.
func printLogFor(log string, bindings ...string) {
	keep := map[string]bool{}
	for _, b := range bindings {
		keep[b] = true
	}
	shown := 0
	for _, line := range strings.Split(log, "\n") {
		if line == "" {
			continue
		}
		for b := range keep {
			if strings.Contains(line, `via='`+b+`'`) || strings.Contains(line, `via=`+b+` `) ||
				strings.Contains(line, `name=`+b+` `) || strings.Contains(line, `via="`+b+`"`) {
				fmt.Println(line)
				shown++
				break
			}
		}
	}
	fmt.Printf("(%d lines)\n", shown)
}
