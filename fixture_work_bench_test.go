package gozero

import (
	"context"
	"net/http"
	"testing"
)

// The isolation benchmarks: the work programs measured apart from the
// fixture assertions.

// BenchmarkStructuralPass isolates the record fixture's one bridged
// call: a declared struct crossing into a named host parameter type by
// value. The parameter has no layout class, so the call goes through
// reflect.Value.Call on both engine tiers; native is the same work
// with the conversion spelled. The delta is the bridge's own cost:
// the argument-Value slice, the call frame, and the result boxing.
func BenchmarkStructuralPass(b *testing.B) {
	src := "type Session struct {\n\tUser string\n\tHost string\n\tPort int64\n}\n" +
		`c := Session{User: "ana", Host: "db.local", Port: 5432};
		line := session.Format(c);
		return line;`
	rt := newBenchFixtureRuntime(b)
	compiled, err := rt.Compile(src)
	if err != nil {
		b.Fatal(err)
	}
	prog, err := (&Parser{}).Parse(src)
	if err != nil {
		b.Fatal(err)
	}
	p, err := rt.compiler.compileProgram(prog)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := jitCompileProgram(p); err != nil {
		b.Fatalf("the pass should reach the tree with one bridge: %v", err)
	}
	b.Run("vm", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := compiled.Exec[string](nil); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("reflect", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := p.run(b.Context(), nil, nil); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("native", func(b *testing.B) {
		type Session struct {
			User string
			Host string
			Port int64
		}
		var sink string
		b.ReportAllocs()
		for b.Loop() {
			c := Session{User: "ana", Host: "db.local", Port: 5432}
			sink = formatSession(session(c))
		}
		_ = sink
	})
}

// BenchmarkFixtureWork isolates the bridge from the assertions: the
// http fixture's work with no assert calls, against the same lines in
// Go. This is the shape the engine is for, and unlike the fixtures it
// reaches the direct-call tier.
func BenchmarkFixtureWork(b *testing.B) {
	const src = `
		req := http.NewRequestWithContext("GET", "https://example.com/a/b");
		req.Method = "POST";
		req.Host = "override.example.com";
		return req;
	`
	rt := newBenchFixtureRuntime(b)
	if err := rt.Supports(src); err != nil {
		b.Fatalf("the work program should JIT: %v", err)
	}
	compiled, err := rt.Compile(src)
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.WithValue(b.Context(), fixtureCtxKey{}, "fixture")

	var sink *http.Request
	b.Run("vm", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			req, err := compiled.ExecContext[*http.Request](ctx, nil)
			if err != nil {
				b.Fatal(err)
			}
			sink = req
		}
	})
	b.Run("native", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			req, err := http.NewRequestWithContext(ctx, "GET", "https://example.com/a/b", nil)
			if err != nil {
				b.Fatal(err)
			}
			req.Method = "POST"
			req.Host = "override.example.com"
			sink = req
		}
	})
	_ = sink
}
