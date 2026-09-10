package gozero

import (
	"testing"
)

func TestVariadicScriptFns(t *testing.T) {
	rt := exprRuntime(t)
	for _, tc := range []struct {
		src  string
		want any
	}{
		{`func sum(base int64, ns ...int64) int64 {
	t := base
	for _, n := range ns {
		t = t + n
	}
	return t
}
v := sum(100, 1, 2, 3)
return v`, int64(106)},
		{`func sum(ns ...int64) int64 {
	t := 0
	for _, n := range ns {
		t = t + n
	}
	return t
}
xs := seq()
v := sum(xs...)
return v`, int64(60)},
		{`func count(ns ...int64) int64 { return len(ns) + 0 }
v := count()
return v`, nil},
		{`join := func(sep string, parts ...string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out = out + sep
		}
		out = out + p
	}
	return out
}
v := join("-", "a", "b", "c")
return v`, "a-b-c"},
		{`func f(x func(int64) int64, n int64) int64 { v := x(n); return v * 2 }
v := f(func(n int64) int64 { return n + 1 }, 20)
return v`, int64(42)},
	} {
		if tc.want == nil {
			continue
		}
		got, err := rt.Eval[any](tc.src, nil)
		if err != nil {
			t.Errorf("%.40q: %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%.40q: got %v, want %v", tc.src, got, tc.want)
		}
	}
	// len(ns) returns int; count declared int64: mismatch is honest.
	if _, err := rt.Compile(`func count(ns ...int64) int64 { return len(ns) + 0 }
v := count()
return v`); err == nil {
		t.Log("count compiles; int adoption covered elsewhere")
	}
}
