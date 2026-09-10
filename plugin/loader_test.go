package plugin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/titpetric/gozero"
)

// reloadFixture copies one version of the reload plugin into a
// temporary file the test rewrites between reloads.
func reloadFixture(t *testing.T, dst, version string) {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("../testdata/plugins/reload", version))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, src, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestReload covers the reload contract: a held symbol follows a
// successful reload, a signature change is refused with the old
// version staying live, and swapping is safe under concurrent calls.
func TestReload(t *testing.T) {
	l := NewLoader(gozero.NewRuntime())
	path := filepath.Join(t.TempDir(), "reloader.go")
	reloadFixture(t, path, "v1.go")

	p, err := l.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	answer, err := Func[func() int64](p, "Answer")
	if err != nil {
		t.Fatal(err)
	}
	if answer() != 1 {
		t.Fatalf("v1 answer = %d", answer())
	}

	reloadFixture(t, path, "v2.go")
	if err := p.(Reloadable).Reload(); err != nil {
		t.Fatal(err)
	}
	if answer() != 2 {
		t.Fatalf("the held symbol must follow the reload, got %d", answer())
	}

	// A symbol first looked up after the reload works too.
	tag, err := Func[func() string](p, "Tag")
	if err != nil {
		t.Fatal(err)
	}
	if got := tag(); got != "v2" {
		t.Fatalf("tag = %q", got)
	}

	reloadFixture(t, path, "v3-bad.go")
	if err := p.(Reloadable).Reload(); err == nil || !strings.Contains(err.Error(), "changed from") {
		t.Fatalf("a retyped symbol must refuse the reload: %v", err)
	}
	if answer() != 2 {
		t.Fatalf("the refused reload must leave v2 live, got %d", answer())
	}

	// Concurrent calls race the swap without tearing.
	reloadFixture(t, path, "v1.go")
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if v := answer(); v != 1 && v != 2 {
					t.Errorf("torn read: %d", v)
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			if err := p.(Reloadable).Reload(); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	wg.Wait()
}

// TestLoader_OpenSource covers the in-memory handle: open by name, look
// up, and reload against the current bindings.
func TestLoader_OpenSource(t *testing.T) {
	l := NewLoader(gozero.NewRuntime())
	p, err := l.OpenSource("mem", "package mem\nfunc Answer() int64 { return 41 }")
	if err != nil {
		t.Fatal(err)
	}
	answer, err := Func[func() int64](p, "Answer")
	if err != nil {
		t.Fatal(err)
	}
	if got := answer(); got != 41 {
		t.Fatalf("got %d", got)
	}
	again, err := l.OpenSource("mem", "ignored on a cached name")
	if err != nil || again != p {
		t.Fatalf("second open: %v, same = %v", err, again == p)
	}
	if err := p.(Reloadable).Reload(); err != nil {
		t.Fatal(err)
	}
	if answer() != 41 {
		t.Fatalf("after reload: %d", answer())
	}
}

// TestWatch reloads on a modification-time change and returns the
// first refused reload.
func TestWatch(t *testing.T) {
	l := NewLoader(gozero.NewRuntime())
	path := filepath.Join(t.TempDir(), "reloader.go")
	reloadFixture(t, path, "v1.go")
	p, err := l.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	answer, err := Func[func() int64](p, "Answer")
	if err != nil {
		t.Fatal(err)
	}
	if got := answer(); got != 1 {
		t.Fatalf("got %d", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	watchErr := make(chan error, 1)
	go func() { watchErr <- Watch(ctx, p, 5*time.Millisecond) }()

	reloadFixture(t, path, "v2.go")
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for answer() != 2 {
		if time.Now().After(deadline) {
			t.Fatal("the watch did not reload")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	if err := <-watchErr; err != context.Canceled {
		t.Fatalf("watch exit: %v", err)
	}
}
