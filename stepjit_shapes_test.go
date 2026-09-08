package gozero

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestContextShapeMatchesReflect pins the ISSI_PE shape, added for
// http.NewRequestWithContext: the auto-filled context and both string
// arguments must arrive identically on both tiers.
func TestContextShapeMatchesReflect(t *testing.T) {
	rt := pairRuntime(t)
	if err := rt.Bind("http.NewRequestWithContext", http.NewRequestWithContext); err != nil {
		t.Fatal(err)
	}
	const src = `
		req := http.NewRequestWithContext("PATCH", "https://example.com/two-tier");
		json.NewEncoder(dest).Encode(req.Method);
	`
	jit, slow := compilePair(t, rt, src)
	ctx := context.Background()
	var a, b bytes.Buffer
	if _, err := jit(ctx, nil, &a); err != nil {
		t.Fatal(err)
	}
	if _, err := slow(ctx, nil, &b); err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() || a.String() != "\"PATCH\"\n" {
		t.Errorf("dest = %q (jit) vs %q (reflect), want %q", a.String(), b.String(), "\"PATCH\"\n")
	}
}

// TestUncoveredShapeKeys builds the table entries no other test
// reaches: two-string constructors, pointer transforms and the
// error-only pairs. Each binding records what it received, each
// program asserts the tier and the value.
func TestUncoveredShapeKeys(t *testing.T) {
	rt := NewRuntime()
	var seen []string
	record := func(vs ...any) {
		seen = append(seen, fmt.Sprint(vs...))
	}
	for name, fn := range map[string]any{
		// SS_PE
		"joinURL": func(host, path string) (*url.URL, error) {
			return &url.URL{Host: host, Path: path}, nil
		},
		// P_P
		"clone": func(u *url.URL) *url.URL {
			c := *u
			c.Path += "/clone"
			return &c
		},
		// P_E
		"checkURL": func(u *url.URL) error {
			record("checkURL", u.Path)
			if u.Path == "" {
				return errors.New("empty path")
			}
			return nil
		},
		// PP_E
		"comparePaths": func(a, b *url.URL) error {
			record("compare", a.Path, b.Path)
			if a.Path == b.Path {
				return nil
			}
			return errors.New("paths differ")
		},
		// II_E
		"pair": func(a, b any) error {
			record("pair", a, b)
			return nil
		},
		// SS_E
		"prefix": func(s, p string) error {
			if !strings.HasPrefix(s, p) {
				return fmt.Errorf("%q does not start with %q", s, p)
			}
			return nil
		},
	} {
		if err := rt.Bind(name, fn); err != nil {
			t.Fatal(err)
		}
	}
	if err := rt.BindScope("url", map[string]any{"Parse": url.Parse}); err != nil {
		t.Fatal(err)
	}

	for name, tc := range map[string]struct {
		src   string
		fails bool
		want  string // recorded call, "" to skip
		path  string // returned path, "" to skip
	}{
		"SS_PE": {`u := joinURL("example.com", "/two"); return u`, false, "", "/two"},
		"P_P":   {`u := url.Parse("/base"); v := clone(u); return v`, false, "", "/base/clone"},
		"P_E ok": {`u := url.Parse("/here"); checkURL(u); return u`,
			false, "checkURL/here", "/here"},
		"P_E err": {`u := url.Parse(""); checkURL(u); return u`, true, "checkURL", ""},
		"PP_E": {`a := url.Parse("/same"); b := url.Parse("/same"); comparePaths(a, b); return a`,
			false, "compare/same/same", "/same"},
		"II_E": {`a := url.Parse("/x"); pair(a, "lit"); return a`, false, "pair/xlit", "/x"},
		"SS_E": {`prefix("striking", "strik"); u := url.Parse("/s"); return u`, false, "", "/s"},
		"SS_E err": {`prefix("striking", "nope"); u := url.Parse("/s"); return u`,
			true, "", ""},
	} {
		seen = nil
		if err := rt.Supports(tc.src); err != nil {
			t.Errorf("%s: did not JIT: %v", name, err)
			continue
		}
		fn, err := rt.Compile(tc.src)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		u, err := fn.Exec[*url.URL](nil)
		if tc.fails {
			if err == nil {
				t.Errorf("%s: expected the binding error", name)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if tc.path != "" && u.Path != tc.path {
			t.Errorf("%s: path = %q, want %q", name, u.Path, tc.path)
		}
		if tc.want != "" && (len(seen) == 0 || seen[0] != tc.want) {
			t.Errorf("%s: callee saw %v, want first %q", name, seen, tc.want)
		}
	}
}
