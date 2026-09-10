// Package tests is the end-to-end platform: suites that cross the
// package boundary, load real plugin sources, and benchmark the
// same workload down every path. Unit suites stay next to their
// packages; what runs here proves whole flows.
package tests

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"plugin"
	"sync"
	"testing"

	"github.com/titpetric/gozero"
	gozeroplugin "github.com/titpetric/gozero/plugin"
)

// pluginDir is the one source both loaders consume.
const pluginDir = "../testdata/plugins/httpd"

// handlerRuntime registers what the httpd plugin imports.
func handlerRuntime(tb testing.TB) *gozero.Runtime {
	tb.Helper()
	rt := gozero.NewRuntime()
	if err := rt.BindPackage("fmt", map[string]any{"Fprintf": fmt.Fprintf}); err != nil {
		tb.Fatal(err)
	}
	if err := rt.BindPackage("net/http", map[string]any{"Error": http.Error}); err != nil {
		tb.Fatal(err)
	}
	for name, v := range map[string]any{
		"http.Handler":        (*http.Handler)(nil),
		"http.HandlerFunc":    http.HandlerFunc(nil),
		"http.ResponseWriter": (*http.ResponseWriter)(nil),
		"http.Request":        http.Request{},
	} {
		if err := rt.BindPackageType("net/http", name, v); err != nil {
			tb.Fatal(err)
		}
	}
	return rt
}

// gozeroHandler loads the plugin source hot and returns its handler.
func gozeroHandler(tb testing.TB) http.Handler {
	tb.Helper()
	l := gozeroplugin.NewLoader(handlerRuntime(tb))
	p, err := l.Open(pluginDir)
	if err != nil {
		tb.Fatal(err)
	}
	mk, err := gozeroplugin.Func[func() http.Handler](p, "Handler")
	if err != nil {
		tb.Fatal(err)
	}
	return mk()
}

// nativePlugin builds the .so once per test binary and opens it with
// the standard library's loader. Loading a compiled plugin needs the
// host and the plugin built from identical packages, which coverage
// or race instrumentation breaks, so a failure to build or open is a
// skip with the reason, not a failure.
var nativePlugin struct {
	once    sync.Once
	handler http.Handler
	err     error
}

func nativeHandler(tb testing.TB) http.Handler {
	tb.Helper()
	nativePlugin.once.Do(func() {
		so := filepath.Join(tb.TempDir(), "httpd.so")
		cmd := exec.Command("go", "build", "-buildmode=plugin", "-o", so, "./testdata/plugins/httpd")
		cmd.Dir = ".."
		if out, err := cmd.CombinedOutput(); err != nil {
			nativePlugin.err = fmt.Errorf("building the plugin: %v: %s", err, out)
			return
		}
		p, err := plugin.Open(so)
		if err != nil {
			nativePlugin.err = fmt.Errorf("opening the plugin: %w", err)
			return
		}
		sym, err := p.Lookup("Handler")
		if err != nil {
			nativePlugin.err = err
			return
		}
		mk, ok := sym.(func() http.Handler)
		if !ok {
			nativePlugin.err = fmt.Errorf("Handler is %T", sym)
			return
		}
		nativePlugin.handler = mk()
	})
	if nativePlugin.err != nil {
		tb.Skipf("native plugin unavailable: %v", nativePlugin.err)
	}
	return nativePlugin.handler
}

// serve runs one request through a handler and returns the recorder.
func serve(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec
}

// TestPluginHandlerGozero pins the hot-loaded handler's behaviour.
func TestPluginHandlerGozero(t *testing.T) {
	h := gozeroHandler(t)
	rec := serve(h, "/probe")
	if rec.Code != 200 || rec.Body.String() != "hello /probe" || rec.Header().Get("X-Plugin") != "httpd" {
		t.Fatalf("code %d, body %q, header %q", rec.Code, rec.Body.String(), rec.Header().Get("X-Plugin"))
	}
}

// TestPluginHandlerEquivalence serves the same requests through both
// loaders of the one source and requires identical responses.
func TestPluginHandlerEquivalence(t *testing.T) {
	native := nativeHandler(t)
	hot := gozeroHandler(t)
	for _, path := range []string{"/", "/a", "/a/b?q=1"} {
		nrec, grec := serve(native, path), serve(hot, path)
		if nrec.Code != grec.Code || nrec.Body.String() != grec.Body.String() ||
			nrec.Header().Get("X-Plugin") != grec.Header().Get("X-Plugin") {
			t.Errorf("%s: native (%d, %q) vs gozero (%d, %q)",
				path, nrec.Code, nrec.Body.String(), grec.Code, grec.Body.String())
		}
	}
}

// BenchmarkPluginHandler stresses the one handler workload down both
// paths: the hot-compiled source and the compiled .so.
func BenchmarkPluginHandler(b *testing.B) {
	run := func(b *testing.B, h http.Handler) {
		b.Helper()
		req := httptest.NewRequest("GET", "/bench", nil)
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != 200 {
				b.Fatalf("code %d", rec.Code)
			}
		}
	}
	b.Run("gozero", func(b *testing.B) {
		run(b, gozeroHandler(b))
	})
	b.Run("native", func(b *testing.B) {
		run(b, nativeHandler(b))
	})
}
