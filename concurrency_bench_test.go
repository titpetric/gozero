package gozero

import (
	"testing"
)

// concurrencyRuntime binds one MutexMap and one buffered channel,
// each behind a nullary constructor so the two programs have the same
// shape: one constructor call, one write, one read.
func concurrencyRuntime(tb testing.TB) *Runtime {
	tb.Helper()
	rt := NewRuntime()
	mm := NewMutexMap()
	ch := make(chan int, 1)
	if err := rt.Bind("store", func() *MutexMap { return mm }); err != nil {
		tb.Fatal(err)
	}
	if err := rt.Bind("source", func() chan int { return ch }); err != nil {
		tb.Fatal(err)
	}
	return rt
}

const (
	mutexProgram   = `m := store(); m.Set("k", 42); v := m.Get("k"); return v`
	channelProgram = `c := source(); c <- 42; v := <-c; return v`
)

// TestConcurrencyPrograms pins that both benchmark programs run on
// the direct tier and produce the value they stored, so the benchmark
// compares the tiers it claims to compare.
func TestConcurrencyPrograms(t *testing.T) {
	rt := concurrencyRuntime(t)
	for name, src := range map[string]string{
		"mutex":   mutexProgram,
		"channel": channelProgram,
	} {
		if err := rt.Supports(src); err != nil {
			t.Errorf("%s: not on the direct tier: %v", name, err)
		}
		v, err := rt.Eval[int](src, nil)
		if err != nil || v != 42 {
			t.Errorf("%s: v = %v, err = %v; want 42", name, v, err)
		}
	}
}

// BenchmarkConcurrency measures a write and a read of shared state,
// native against the compiled program, through a mutex-protected map
// and through a buffered channel. The programs are uncontended: the
// question is the per-operation cost of each API from the language,
// not lock behaviour under load.
func BenchmarkConcurrency(b *testing.B) {
	b.Run("mutex/native", func(b *testing.B) {
		mm := NewMutexMap()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			mm.Set("k", 42)
			v := mm.Get("k")
			if v != 42 {
				b.Fatal(v)
			}
		}
	})
	b.Run("mutex/vm", func(b *testing.B) {
		rt := concurrencyRuntime(b)
		fn, err := rt.Compile(mutexProgram)
		if err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := fn.Exec[int](nil); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("channel/native", func(b *testing.B) {
		ch := make(chan int, 1)
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			ch <- 42
			v := <-ch
			if v != 42 {
				b.Fatal(v)
			}
		}
	})
	b.Run("channel/vm", func(b *testing.B) {
		rt := concurrencyRuntime(b)
		fn, err := rt.Compile(channelProgram)
		if err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := fn.Exec[int](nil); err != nil {
				b.Fatal(err)
			}
		}
	})
}
