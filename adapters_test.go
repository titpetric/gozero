package gozero

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// HandlerAdapter is the host's one-declaration cost per interface: a
// func field per method and a forwarding method over it.
type HandlerAdapter struct {
	ServeHTTPFunc func(http.ResponseWriter, *http.Request)
}

func (a *HandlerAdapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.ServeHTTPFunc(w, r)
}

// TestRuntime_BindAdapter carries a script type with the right
// method set into http.Handler: the script declares the type and the
// method, the host serves a request through it.
func TestRuntime_BindAdapter(t *testing.T) {
	rt := NewRuntime()
	if err := rt.BindAdapter[http.Handler]((*HandlerAdapter)(nil)); err != nil {
		t.Fatal(err)
	}
	served := ""
	if err := rt.Bind("serve", func(h http.Handler) string {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/probe", nil))
		served = rec.Body.String()
		return served
	}); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("respond", func(w http.ResponseWriter, body string) {
		w.Write([]byte(body))
	}); err != nil {
		t.Fatal(err)
	}
	if err := rt.BindType("http.ResponseWriter", (*http.ResponseWriter)(nil)); err != nil {
		t.Fatal(err)
	}

	src := `type Echo struct { Body string }
func (e Echo) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	respond(w, e.Body)
}
e := Echo{Body: "from script"}
out := serve(e)
return out`
	got, err := rt.Eval[string](src, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "from script" || served != "from script" {
		t.Fatalf("got %q, served %q", got, served)
	}

	// The failure modes: no adapter, and a missing method.
	rt2 := NewRuntime()
	if err := rt2.Bind("serve", func(h http.Handler) {}); err != nil {
		t.Fatal(err)
	}
	src = `type E struct { N int64 }
func (e E) ServeHTTP(w http.ResponseWriter, r *http.Request) { }
e := E{N: 1}
serve(e)`
	if _, err := rt2.Compile(src); err == nil || !strings.Contains(err.Error(), "no adapter registered") {
		t.Fatalf("no adapter: err = %v", err)
	}

	if err := rt2.BindAdapter[http.Handler]((*HandlerAdapter)(nil)); err != nil {
		t.Fatal(err)
	}
	src = `type Silent struct { N int64 }
s := Silent{N: 1}
serve(s)`
	if _, err := rt2.Compile(src); err == nil || !strings.Contains(err.Error(), "missing method ServeHTTP") {
		t.Fatalf("missing method: err = %v", err)
	}

	// Bind-time validation.
	type bad struct{}
	if err := rt2.BindAdapter[http.Handler]((*bad)(nil)); err == nil {
		t.Fatal("a struct that does not implement the interface must be refused")
	}
	if err := rt2.BindAdapter[int](nil); err == nil {
		t.Fatal("a non-interface must be refused")
	}
}
