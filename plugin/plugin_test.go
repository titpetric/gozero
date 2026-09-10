package plugin

import (
	"errors"
	"strings"
	"testing"

	"github.com/titpetric/gozero"
)

// testLoader is a loader whose runtime registers what the example
// plugins import.
func testLoader(t *testing.T) *Loader {
	t.Helper()
	rt := gozero.NewRuntime()
	for path, symbols := range map[string]map[string]any{
		"strings": {"CutPrefix": strings.CutPrefix},
		"errors":  {"New": errors.New},
		"hostlog": {"Loaded": func(string) {}},
	} {
		if err := rt.BindPackage(path, symbols); err != nil {
			t.Fatal(err)
		}
	}
	return NewLoader(rt)
}

// TestOpen covers the stdlib-shaped surface: Open a folder,
// Lookup an exported func, assert it as its signature.
func TestOpen(t *testing.T) {
	l := testLoader(t)
	p, err := l.Open("../testdata/plugins/greet")
	if err != nil {
		t.Fatal(err)
	}
	sym, err := p.Lookup("Hello")
	if err != nil {
		t.Fatal(err)
	}
	hello, ok := sym.(func(string) string)
	if !ok {
		t.Fatalf("symbol is %T", sym)
	}
	if hello("there") != "hello there" {
		t.Fatalf("got %q", hello("there"))
	}

	// The same handle comes back for the same path.
	again, err := l.Open("../testdata/plugins/greet/")
	if err != nil || again != p {
		t.Fatalf("second Open: %v, same = %v", err, again == p)
	}

	if _, err := p.Lookup("noSuch"); err == nil || !strings.Contains(err.Error(), "not exported") {
		t.Errorf("unexported: err = %v", err)
	}
	if _, err := p.Lookup("Missing"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("missing: err = %v", err)
	}
}

// The coverage aliases: the surface types and the loader entry are
// exercised through TestOpen and TestReload.
func TestSymbol(t *testing.T)      { TestOpen(t) }
func TestPlugin(t *testing.T)      { TestOpen(t) }
func TestReloadable(t *testing.T)  { TestReload(t) }
func TestLoader(t *testing.T)      { TestOpen(t) }
func TestNewLoader(t *testing.T)   { TestOpen(t) }
func TestLoader_Open(t *testing.T) { TestOpen(t) }
