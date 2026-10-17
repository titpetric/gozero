package gozero

import (
	"testing"
)

// concurrencyRuntime binds one MutexMap behind a nullary constructor,
// so the program is one constructor call, one write, one read.
func concurrencyRuntime(tb testing.TB) *Runtime {
	tb.Helper()
	rt := NewRuntime()
	mm := NewMutexMap()
	if err := rt.Bind("store", func() *MutexMap { return mm }); err != nil {
		tb.Fatal(err)
	}
	return rt
}

const mutexProgram = `m := store(); m.Set("k", 42); v := m.Get("k"); return v`

// TestConcurrencyPrograms pins that the benchmark program runs on the
// direct tier and produces the value it stored, so the benchmark
// compares the tiers it claims to compare.
func TestConcurrencyPrograms(t *testing.T) {
	rt := concurrencyRuntime(t)
	if err := rt.Supports(mutexProgram); err != nil {
		t.Errorf("mutex: not on the direct tier: %v", err)
	}
	v, err := rt.Eval[int](mutexProgram, nil)
	if err != nil || v != 42 {
		t.Errorf("mutex: v = %v, err = %v; want 42", v, err)
	}
}

// BenchmarkConcurrency measures a write and a read of shared state,
// native against the compiled program, through a mutex-protected map.
// The programs are uncontended: the question is the per-operation cost
// of the API from the language, not lock behaviour under load.
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
}
