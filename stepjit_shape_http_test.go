package gozero

import (
	"testing"
)

// TestHTTPShapesAreDirect pins the shapes this file adds: each
// statement using one must reach the direct tier, so a table miss is
// a failure here rather than a silent bridge in the fixture.
func TestHTTPShapesAreDirect(t *testing.T) {
	rt := funclitRuntime(t)
	if err := rt.Bind("keep", func(func()) {}); err != nil {
		t.Fatal(err)
	}
	for name, src := range map[string]string{
		// "P_": one pointer argument, no results.
		"P_": `keep(func() { });`,
		// "PSP_" registers, "SSI_P" builds the request, "PIP_" serves.
		"PSP_ PIP_ SSI_P": `
			mux := http.NewServeMux();
			mux.HandleFunc("/health", func(w, r) { fmt.Fprint(w, "ok") });
			req := httptest.NewRequest("GET", "/health");
			rec := httptest.NewRecorder();
			mux.ServeHTTP(rec, req);
			out := rec.Body.String();
			return out;
		`,
	} {
		if err := rt.Supports(src); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
