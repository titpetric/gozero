package gozero

import (
	"bytes"
	"encoding/json"
	"fmt"
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
