package gozero

import (
	"net/http"
	"net/url"
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
