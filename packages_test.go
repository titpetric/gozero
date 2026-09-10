package gozero

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// TestRuntime_BindPackage covers the registration and activation pair:
// BindPackage alone exposes nothing to a snippet, Import activates
// the package under its base name, ImportAs under an alias, and the
// package's discovered types become nameable.
func TestRuntime_BindPackage(t *testing.T) {
	rt := NewRuntime()
	if err := rt.BindPackage("net/http", map[string]any{"NewRequest": http.NewRequest}); err != nil {
		t.Fatal(err)
	}

	if _, err := rt.Compile(`req := http.NewRequest("GET", "/"); return req;`); err == nil {
		t.Fatal("a registered package must not resolve before Import")
	}

	if err := rt.Import("net/http"); err != nil {
		t.Fatal(err)
	}
	fn, err := rt.Compile(`req := http.NewRequest("GET", "/x"); return req;`)
	if err != nil {
		t.Fatal(err)
	}
	req, err := fn.Exec[*http.Request](nil)
	if err != nil || req.URL.Path != "/x" {
		t.Fatalf("got %v, %v", req, err)
	}

	// The package's types merged into the registry.
	if _, err := rt.Compile(`var u url.URL; return u;`); err != nil {
		t.Errorf("discovered type after Import: %v", err)
	}

	if err := rt.ImportAs("web", "net/http"); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Compile(`req := web.NewRequest("GET", "/"); return req;`); err != nil {
		t.Errorf("aliased activation: %v", err)
	}

	if err := rt.Import("net/nosuch"); err == nil || !strings.Contains(err.Error(), "is not registered") {
		t.Errorf("err = %v", err)
	}
	if err := rt.BindPackage("x", map[string]any{"NotAFunc": 42}); err == nil || !strings.Contains(err.Error(), "want func") {
		t.Errorf("err = %v", err)
	}
}

// TestFileImports covers the header path: a file resolves types only
// through its import block, aliases map to the real reflect
// spellings, and an unregistered import fails at load.
func TestFileImports(t *testing.T) {
	rt := NewRuntime()
	if err := rt.BindPackage("net/http", map[string]any{"NewRequest": http.NewRequest}); err != nil {
		t.Fatal(err)
	}

	// A declarations-only file compiles; there is nothing to run yet.
	for _, src := range []string{
		"package demo\nimport \"net/http\"\ntype T struct { R *http.Request }",
		"package demo\nimport (\n\t\"net/http\"\n)\ntype T struct { H http.Header }",
		"package demo\nimport h \"net/http\"\ntype T struct { R *h.Request }",
		"package demo",
	} {
		if _, err := rt.Compile(src); err != nil {
			t.Errorf("%q: %v", src, err)
		}
	}

	for name, tc := range map[string]struct{ src, want string }{
		"unregistered": {
			"package demo\nimport \"net/url\"\ntype T struct { U *url.URL }",
			`import "net/url": package is not registered`,
		},
		"unimported type": {
			"package demo\ntype T struct { R *http.Request }",
			"undefined or recursive",
		},
		"duplicate local name": {
			"package demo\nimport (\n\t\"net/http\"\n\t\"net/http\"\n)\ntype T struct { R *http.Request }",
			"http redeclared as imported package name",
		},
		"statements in a file": {
			"package demo\nimport \"net/http\"\nreq := http.NewRequest(\"GET\", \"/\")",
			"expected a declaration",
		},
	} {
		_, err := rt.Compile(tc.src)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}

	// Two imports may not share a local name; each may be aliased
	// apart.
	dup := "package demo\nimport (\n\t\"net/http\"\n\tweb \"net/http\"\n)\ntype T struct { A *http.Request; B *web.Request }"
	if _, err := rt.Compile(dup); err != nil {
		t.Errorf("aliased twin import: %v", err)
	}
}

// TestParseFileHeader pins the parser side: header forms, import
// forms, and the sniff that keeps snippets intact.
func TestParseFileHeader(t *testing.T) {
	prog, err := (&Parser{}).Parse("package demo\nimport (\n\t\"net/http\"\n\t// a comment\n\th \"helpers\"\n)\nimport \"single\"\ntype T struct{}")
	if err != nil {
		t.Fatal(err)
	}
	if prog.pkg != "demo" {
		t.Fatalf("pkg = %q", prog.pkg)
	}
	want := []importSpec{{"", "net/http"}, {"h", "helpers"}, {"", "single"}}
	if len(prog.imports) != len(want) {
		t.Fatalf("imports = %+v", prog.imports)
	}
	for i, w := range want {
		if prog.imports[i] != w {
			t.Errorf("import %d = %+v, want %+v", i, prog.imports[i], w)
		}
	}

	// Snippets that start with the word package stay snippets.
	for _, src := range []string{`package := f();`, `packages := f();`} {
		prog, err := (&Parser{}).Parse(src)
		if err != nil {
			t.Errorf("%q: %v", src, err)
			continue
		}
		if prog.pkg != "" || len(prog.stmts) != 1 {
			t.Errorf("%q: parsed as file %+v", src, prog)
		}
	}

	for name, tc := range map[string]struct{ src, want string }{
		"blank import":   {"package p\nimport _ \"x\"", "blank imports are not supported"},
		"single quotes":  {"package p\nimport 'x'", "expected a double-quoted import path"},
		"empty path":     {"package p\nimport \"\"", "empty import path"},
		"unterminated":   {"package p\nimport (\n\"x\"", "unterminated import block"},
		"empty program?": {"", "empty program"},
	} {
		if _, err := (&Parser{}).Parse(tc.src); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
}

// TestRuntime_Invalidate pins the staleness fix: a Bind after a Compile
// is visible to the next Compile of the same source, and Invalidate
// empties the cache.
func TestRuntime_Invalidate(t *testing.T) {
	rt := NewRuntime()
	bindN := func(n int64) {
		t.Helper()
		if err := rt.Bind("n", func() int64 { return n }); err != nil {
			t.Fatal(err)
		}
	}
	run := func() int64 {
		t.Helper()
		got, err := rt.Eval[int64](`v := n(); return v;`, nil)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	bindN(1)
	if got := run(); got != 1 {
		t.Fatalf("got %d", got)
	}
	bindN(2)
	if got := run(); got != 2 {
		t.Fatalf("rebind not visible: got %d", got)
	}

	// A cache hit within one generation stays a hit.
	fn1, _ := rt.Compile(`v := n(); return v;`)
	fn2, _ := rt.Compile(`v := n(); return v;`)
	if fmt.Sprintf("%p", fn1) != fmt.Sprintf("%p", fn2) {
		t.Error("same generation should serve the cached func")
	}

	rt.Invalidate()
	if got := run(); got != 2 {
		t.Fatalf("after Invalidate: got %d", got)
	}
}

// TestConcurrentCompileBind races Compile against BindPackage and
// Import, for the generation counter and the shared maps.
func TestConcurrentCompileBind(t *testing.T) {
	rt := NewRuntime()
	if err := rt.Bind("f", func() string { return "ok" }); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if _, err := rt.Eval[string](`v := f(); return v;`, nil); err != nil {
					t.Error(err)
					return
				}
			}
		}()
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				path := fmt.Sprintf("pkg/p%d", i)
				if err := rt.BindPackage(path, map[string]any{"F": func() int64 { return 1 }}); err != nil {
					t.Error(err)
					return
				}
				if err := rt.Import(path); err != nil {
					t.Error(err)
					return
				}
			}
		}(i)
	}
	wg.Wait()
}

// TestRuntime_Import covers activation alone, apart from the
// registration test.
func TestRuntime_Import(t *testing.T) {
	rt := NewRuntime()
	if err := rt.Import("net/http"); err == nil {
		t.Fatal("Import before BindPackage must fail")
	}
	if err := rt.BindPackage("net/http", map[string]any{"NewRequest": http.NewRequest}); err != nil {
		t.Fatal(err)
	}
	if err := rt.Import("net/http"); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Compile(`req := http.NewRequest("GET", "/"); return req;`); err != nil {
		t.Fatal(err)
	}
}

// TestRuntime_ImportAs covers the aliased activation.
func TestRuntime_ImportAs(t *testing.T) {
	rt := NewRuntime()
	if err := rt.BindPackage("net/http", map[string]any{"NewRequest": http.NewRequest}); err != nil {
		t.Fatal(err)
	}
	if err := rt.ImportAs("web", "net/http"); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Compile(`req := web.NewRequest("GET", "/"); return req;`); err != nil {
		t.Fatal(err)
	}
	if err := rt.ImportAs("web", "net/nope"); err == nil {
		t.Fatal("aliasing an unregistered path must fail")
	}
}

// TestRuntime_BindPackageType covers the manual type registration a
// signature walk cannot reach.
func TestRuntime_BindPackageType(t *testing.T) {
	rt := NewRuntime()
	if err := rt.BindPackageType("x/y", "y.T", struct{ N int64 }{}); err == nil {
		t.Fatal("BindPackageType before BindPackage must fail")
	}
	if err := rt.BindPackage("net/http", map[string]any{"NewRequest": http.NewRequest}); err != nil {
		t.Fatal(err)
	}
	type extra struct{ N int64 }
	if err := rt.BindPackageType("net/http", "http.Extra", extra{}); err != nil {
		t.Fatal(err)
	}
	src := "package demo\nimport \"net/http\"\ntype T struct { E http.Extra }"
	if _, err := rt.Compile(src); err != nil {
		t.Fatal(err)
	}
	if err := rt.BindPackageType("net/http", "http.Nil", nil); err == nil {
		t.Fatal("a nil value has no type")
	}
}
