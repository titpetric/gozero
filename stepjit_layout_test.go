package gozero

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"path"
	"runtime"
	"strings"
	"testing"
	"unsafe"
)

// TestStepJITFrameSurvivesGC forces collections between the steps'
// stores and their reads. A frame allocated with the wrong pointer map,
// or a store that skipped its write barrier, shows up here as a lost or
// corrupted value rather than as a wrong benchmark.
func TestStepJITFrameSurvivesGC(t *testing.T) {
	rt := pairRuntime(t)
	if err := rt.Bind("churn", func(v any) (*url.URL, error) {
		for i := 0; i < 24; i++ {
			runtime.GC()
			_ = make([]byte, 1<<15)
		}
		// v is reached only through the frame slot the previous step
		// wrote, and must still be intact after the collections.
		return &url.URL{Path: fmt.Sprintf("/%d", len(fmt.Sprint(v)))}, nil
	}); err != nil {
		t.Fatal(err)
	}

	const src = `
		cookies := http.NewRequest("GET", "/").Cookies();
		u := churn(cookies);
		json.NewEncoder(dest).Encode(u);
	`
	jit, slow := compilePair(t, rt, src)
	var a, b bytes.Buffer
	if _, err := jit(context.Background(), nil, &a); err != nil {
		t.Fatal(err)
	}
	if _, err := slow(context.Background(), nil, &b); err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() {
		t.Errorf("after GC: %q (jit) vs %q (reflect)", a.String(), b.String())
	}
}

// TestVariadicSliceABI pins the assumption behind spread and pack: a
// variadic function is ABI-identical to the same signature with the
// variadic parameter as a plain slice, so casting the funcval and
// passing a slice header is a correct call. Covers a top-level func, a
// closure with captured state and mixed parameters, an empty slice,
// and ...any elements.
func TestVariadicSliceABI(t *testing.T) {
	parts := []string{"a", "b", "c"}
	h := *(*sliceHdr)(unsafe.Pointer(&parts))

	join := castFn[func(sliceHdr) string](funcPtr(path.Join))
	if got := join(h); got != "a/b/c" {
		t.Errorf("path.Join = %q, want a/b/c", got)
	}
	var empty []string
	if got := join(*(*sliceHdr)(unsafe.Pointer(&empty))); got != "" {
		t.Errorf("empty spread = %q, want empty", got)
	}

	sep := "|"
	cl := func(prefix string, elems ...string) string {
		return prefix + ":" + strings.Join(elems, sep)
	}
	cast := castFn[func(string, sliceHdr) string](funcPtr(cl))
	if got, want := cast("p", h), cl("p", parts...); got != want {
		t.Errorf("closure = %q, want %q", got, want)
	}

	vals := []any{"x", 1, true}
	sprint := castFn[func(sliceHdr) string](funcPtr(fmt.Sprint))
	if got, want := sprint(*(*sliceHdr)(unsafe.Pointer(&vals))), fmt.Sprint(vals...); got != want {
		t.Errorf("...any = %q, want %q", got, want)
	}
}
