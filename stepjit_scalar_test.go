package gozero

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"testing"
)

// TestScalarFromStackJITs pins the tier for a scalar read off the
// caller's stack, which used to send the program to reflect.
func TestScalarFromStackJITs(t *testing.T) {
	rt, seen := litRuntime(t)
	const src = `wantI64(n); json.NewEncoder(dest).Encode("ok");`
	if err := rt.Supports(src); err != nil {
		t.Fatalf("did not JIT: %v", err)
	}
	fn, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	for _, tc := range []struct {
		stack map[string]any
		want  any
		fails bool
	}{
		{map[string]any{"n": int64(9)}, int64(9), false},
		{nil, int64(0), false},
		{map[string]any{"n": "not a number"}, nil, true},
	} {
		*seen = nil
		b.Reset()
		err := fn.Scan(&b, tc.stack)
		if tc.fails {
			if err == nil {
				t.Errorf("stack %v: expected a type error", tc.stack)
			}
			continue
		}
		if err != nil {
			t.Errorf("stack %v: %v", tc.stack, err)
			continue
		}
		if *seen != tc.want {
			t.Errorf("stack %v: callee saw %v, want %v", tc.stack, *seen, tc.want)
		}
	}
}

// TestMixedScalarShapeJITs pins the tier for a call mixing a string and
// a scalar parameter, which used to be outside the table.
func TestMixedScalarShapeJITs(t *testing.T) {
	rt, seen := litRuntime(t)
	const src = `u := mixed("a", 5); return u;`
	if err := rt.Supports(src); err != nil {
		t.Fatalf("did not JIT: %v", err)
	}
	fn, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	*seen = nil
	u, err := fn.Exec[*url.URL](nil)
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != "a" || *seen != int64(5) {
		t.Errorf("path=%q seen=%v", u.Path, *seen)
	}
}

// scalarRuntime binds one representative of each scalar shape family
// that had no coverage: pointer-in scalar-out getters at several
// widths, scalar-in error-only results, and the mixed pairs the table
// reaches through mixedScalarCall.
func scalarRuntime(t *testing.T) *Runtime {
	t.Helper()
	rt := NewRuntime()
	for name, fns := range map[string]map[string]any{
		"url":  {"Parse": url.Parse},
		"json": {"NewEncoder": json.NewEncoder},
	} {
		if err := rt.BindScope(name, fns); err != nil {
			t.Fatal(err)
		}
	}
	for name, fn := range map[string]any{
		"plen":  func(u *url.URL) int64 { return int64(len(u.Path)) },
		"pneg":  func(u *url.URL) int8 { return int8(-len(u.Path)) },
		"pu8":   func(u *url.URL) uint8 { return uint8(len(u.Path)) },
		"pbool": func(u *url.URL) bool { return u.Path != "" },
		"pf64":  func(u *url.URL) float64 { return float64(len(u.Path)) / 2 },
		"pf32":  func(u *url.URL) float32 { return float32(len(u.Path)) },
		"checkN": func(n int64) error {
			if n < 0 {
				return errors.New("negative")
			}
			return nil
		},
		"checkF": func(f float64) error {
			if f < 0 {
				return errors.New("negative")
			}
			return nil
		},
		"pmix": func(u *url.URL, n int64) (*url.URL, error) {
			return &url.URL{Path: fmt.Sprintf("%s/%d", u.Path, n)}, nil
		},
		"smixE": func(s string, n int64) error {
			if int64(len(s)) != n {
				return fmt.Errorf("%q is not %d long", s, n)
			}
			return nil
		},
		"fmix": func(s string, f float64) (*url.URL, error) {
			return &url.URL{Path: fmt.Sprintf("%s/%g", s, f)}, nil
		},
	} {
		if err := rt.Bind(name, fn); err != nil {
			t.Fatal(err)
		}
	}
	return rt
}

// TestPtrScalarShapesJIT pins the func(*T) scalar family, which had no
// binding of that shape anywhere in the suite: every width goes
// through ptrScalarCall, floats through pF and the rest through pN.
func TestPtrScalarShapesJIT(t *testing.T) {
	rt := scalarRuntime(t)
	for name, tc := range map[string]struct {
		src   string
		check func(fn CompiledFunc) error
	}{
		"int64": {`u := url.Parse("/abc"); n := plen(u); return n`, func(fn CompiledFunc) error {
			n, err := fn.Exec[int64](nil)
			if err != nil || n != 4 {
				return fmt.Errorf("got %v, %v, want 4", n, err)
			}
			return nil
		}},
		"int8 negative": {`u := url.Parse("/abc"); n := pneg(u); return n`, func(fn CompiledFunc) error {
			n, err := fn.Exec[int8](nil)
			if err != nil || n != -4 {
				return fmt.Errorf("got %v, %v, want -4", n, err)
			}
			return nil
		}},
		"uint8": {`u := url.Parse("/abc"); n := pu8(u); return n`, func(fn CompiledFunc) error {
			n, err := fn.Exec[uint8](nil)
			if err != nil || n != 4 {
				return fmt.Errorf("got %v, %v, want 4", n, err)
			}
			return nil
		}},
		"bool": {`u := url.Parse("/abc"); b := pbool(u); return b`, func(fn CompiledFunc) error {
			b, err := fn.Exec[bool](nil)
			if err != nil || !b {
				return fmt.Errorf("got %v, %v, want true", b, err)
			}
			return nil
		}},
		"float64": {`u := url.Parse("/abc"); f := pf64(u); return f`, func(fn CompiledFunc) error {
			f, err := fn.Exec[float64](nil)
			if err != nil || f != 2 {
				return fmt.Errorf("got %v, %v, want 2", f, err)
			}
			return nil
		}},
		"float32": {`u := url.Parse("/abc"); f := pf32(u); return f`, func(fn CompiledFunc) error {
			f, err := fn.Exec[float32](nil)
			if err != nil || f != 4 {
				return fmt.Errorf("got %v, %v, want 4", f, err)
			}
			return nil
		}},
	} {
		if err := rt.Supports(tc.src); err != nil {
			t.Errorf("%s: did not JIT: %v", name, err)
			continue
		}
		fn, err := rt.Compile(tc.src)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if err := tc.check(fn); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// TestScalarErrorOnlyShapesJIT pins a scalar parameter with a lone
// error result, the nE and fE constructors, on both outcomes.
func TestScalarErrorOnlyShapesJIT(t *testing.T) {
	rt := scalarRuntime(t)
	for name, tc := range map[string]struct {
		src   string
		fails bool
	}{
		"int ok":    {`checkN(5); json.NewEncoder(dest).Encode("ok")`, false},
		"int err":   {`checkN(-5); json.NewEncoder(dest).Encode("ok")`, true},
		"float ok":  {`checkF(2.5); json.NewEncoder(dest).Encode("ok")`, false},
		"float err": {`checkF(-2.5); json.NewEncoder(dest).Encode("ok")`, true},
	} {
		if err := rt.Supports(tc.src); err != nil {
			t.Errorf("%s: did not JIT: %v", name, err)
			continue
		}
		fn, err := rt.Compile(tc.src)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		var dest bytes.Buffer
		err = fn.Scan(&dest, nil)
		if tc.fails != (err != nil) {
			t.Errorf("%s: err = %v, want failure=%v", name, err, tc.fails)
		}
		if tc.fails && dest.Len() > 0 {
			t.Errorf("%s: the failing call must stop the program, dest = %q", name, dest.String())
		}
	}
}

// TestMixedScalarWidthsJIT widens the mixed pair beyond the one
// (string, int64) case: pointer first, error-only results, and a float
// second parameter.
func TestMixedScalarWidthsJIT(t *testing.T) {
	rt := scalarRuntime(t)
	for name, tc := range map[string]struct {
		src, want string
		fails     bool
	}{
		"pointer first":  {`u := url.Parse("/p"); v := pmix(u, 7); return v`, "/p/7", false},
		"float second":   {`v := fmix("f", 2.5); return v`, "f/2.5", false},
		"error only ok":  {`smixE("abc", 3); v := url.Parse("/ok"); return v`, "/ok", false},
		"error only err": {`smixE("abc", 4); v := url.Parse("/ok"); return v`, "", true},
	} {
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
		if err != nil || u.Path != tc.want {
			t.Errorf("%s: got %v, %v, want path %q", name, u, err, tc.want)
		}
	}
}

// TestFloatFromStackJITs pins the float halves of the stack scalar
// reads, which only the integer widths covered.
func TestFloatFromStackJITs(t *testing.T) {
	rt := scalarRuntime(t)
	const src = `checkF(f); json.NewEncoder(dest).Encode("ok")`
	if err := rt.Supports(src); err != nil {
		t.Fatalf("did not JIT: %v", err)
	}
	fn, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	var dest bytes.Buffer
	for name, tc := range map[string]struct {
		stack map[string]any
		fails bool
	}{
		"present": {map[string]any{"f": 2.5}, false},
		"failing": {map[string]any{"f": -2.5}, true},
		"unset":   {nil, false},
		"wrong":   {map[string]any{"f": "not a float"}, true},
	} {
		dest.Reset()
		err := fn.Scan(&dest, tc.stack)
		if tc.fails != (err != nil) {
			t.Errorf("%s: err = %v, want failure=%v", name, err, tc.fails)
		}
	}
}
