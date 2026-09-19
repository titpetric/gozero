package gozero

import (
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"testing"
)

// TestReflectTierIsConcurrent runs one cached compiled func from many
// goroutines on the reflect path, where the argument slice used to be
// patched in place and raced.
func TestReflectTierIsConcurrent(t *testing.T) {
	rt := NewRuntime()
	// bool has no layout class, so this cannot JIT.
	if err := rt.Bind("f", func(s string, b bool) (*url.URL, error) {
		return &url.URL{Path: s}, nil
	}); err != nil {
		t.Fatal(err)
	}
	const src = `return f(name);`
	if err := rt.Supports(src); err == nil {
		t.Fatal("this test needs the reflect tier")
	}
	fn, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		want := string(rune('a' + i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 400; j++ {
				u, err := fn.Exec[*url.URL](map[string]any{"name": want})
				if err != nil {
					t.Error(err)
					return
				}
				if u.Path != want {
					t.Errorf("path = %q, want %q", u.Path, want)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// TestJITTierIsConcurrent is the direct-call mirror of the reflect
// test: the program is asserted onto the JIT, reads a stack name three
// times so the hoisted frame field is exercised, and writes a field.
// The frame is per-run; nothing here may be shared between goroutines
// but the compiled closures themselves.
func TestJITTierIsConcurrent(t *testing.T) {
	rt := NewRuntime()
	for name, fns := range map[string]map[string]any{
		"http": {"NewRequest": http.NewRequest},
		"url":  {"Parse": url.Parse},
	} {
		if err := rt.BindScope(name, fns); err != nil {
			t.Fatal(err)
		}
	}
	const src = `
		req := http.NewRequest("GET", link)
		req.Host = "raced"
		u := url.Parse(link)
		return u
	`
	if err := rt.Supports(src); err != nil {
		t.Fatalf("this test needs the JIT tier: %v", err)
	}
	fn, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		want := "/race/" + string(rune('a'+i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 400; j++ {
				u, err := fn.Exec[*url.URL](map[string]any{"link": want})
				if err != nil {
					t.Error(err)
					return
				}
				if u.Path != want {
					t.Errorf("path = %q, want %q", u.Path, want)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// TestRangeIsConcurrent runs a compiled range program from many
// goroutines. The loop variable is a frame slot and the frame is
// per-run, so concurrent loops share nothing but the closures and
// the pools.
func TestRangeIsConcurrent(t *testing.T) {
	rt := NewRuntime()
	if err := rt.Bind("fields", strings.Fields); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("join", path.Join); err != nil {
		t.Fatal(err)
	}
	const src = `
		xs := fields(line)
		for _, s := range xs {
			j = join(j, s)
		}
		return j
	`
	fullSrc := "j := \"\"\n" + src
	if err := rt.Supports(fullSrc); err != nil {
		t.Fatalf("this test needs the JIT tier: %v", err)
	}
	fn, err := rt.Compile(fullSrc)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		part := string(rune('a' + i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 400; j++ {
				got, err := fn.Exec[string](map[string]any{"line": part + " " + part})
				if err != nil {
					t.Error(err)
					return
				}
				if want := part + "/" + part; got != want {
					t.Errorf("got %q, want %q", got, want)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// TestLoopsAreConcurrent runs a compiled map-and-iterator program
// from many goroutines. The pooled reflect.MapIter is the one piece
// of shared state the L2 loops add, so this is what pins its reuse
// as race-free.
func TestLoopsAreConcurrent(t *testing.T) {
	rt := NewRuntime()
	if err := rt.Bind("sized", func() map[string]int64 {
		return map[string]int64{"a": 1, "bb": 2, "ccc": 3}
	}); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("lines", strings.Lines); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("mk", counterNew); err != nil {
		t.Fatal(err)
	}
	if err := rt.Bind("slen", func(s string) int64 { return int64(len(s)) }); err != nil {
		t.Fatal(err)
	}
	const src = `
		c := mk()
		m := sized()
		for _, v := range m {
			c.Add(v)
		}
		for line := range lines(text) {
			c.Add(slen(line))
		}
		sum := c.Sum()
		return sum
	`
	if err := rt.Supports(src); err != nil {
		t.Fatalf("this test needs the JIT tier: %v", err)
	}
	fn, err := rt.Compile(src)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 400; j++ {
				got, err := fn.Exec[int64](map[string]any{"text": "xx\nyyy\n"})
				if err != nil {
					t.Error(err)
					return
				}
				if got != 13 {
					t.Errorf("got %d, want 13", got)
					return
				}
			}
		}()
	}
	wg.Wait()
}
