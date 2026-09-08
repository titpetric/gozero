---
title: Fixture tests, programs evaluated hot
date: 2026-09-06T12:31:21+02:00
---

The programs under `testdata/` are the VM's test suite and its use case
demonstration at once. Each `.txt` file is one program in the VM's own
language, compiled and executed inside a running Go test, against live
bindings, with the test's own `testing.TB` handed in on the stack. The
program makes its own assertions.

```
// http: build a request, read and write its fields, check the context
// arrives without being written in the source.
req := http.NewRequestWithContext("GET", "https://example.com/a/b");
assert.Equal(tb, "GET", req.Method);

req.Method = "POST";
assert.Equal(tb, "POST", req.Method);
```

Hot evaluation means there is no build step between the text and the
run. The runner reads the file, `Compile` turns it into a tree of typed
closures over the live binding funcvals, and the program executes in
the same process, in the same test, immediately. Editing a fixture and
re-running `go test` is the whole loop; nothing is generated, nothing
is linked, and the same text can be run against changed bindings
without touching it.

## How the runner works

`TestFixtures` globs `testdata/*.txt` and runs one subtest per file.
Each subtest:

1. builds a runtime and binds a standard library surface per scope:
   `http`, `url`, `json`, `bytes`, `fmt`, `strings`, `path`, plus
   `assert`,
2. compiles the file's source, cached per source string,
3. logs the tier: `tier: JIT` when every call is direct, otherwise the
   calls that bridge through reflect, by name,
4. executes with `ExecContext`, the context derived from `t.Context()`
   so cancellation and deadlines flow into the bindings, and the
   subtest's `testing.TB` on the stack as `tb`.

A failing assertion inside the program calls `tb.Errorf` through the
bound helper, which fails the subtest the ordinary way. The runner adds
nothing: the program is the test. `TestAssertFailurePropagates` pins
that a failing assertion actually reaches the TB, so the suite cannot
pass vacuously.

Adding a test is adding a file. Scope it the way the existing ones are
scoped, one concern per file: `http.txt`, `url.txt`, `json.txt`,
`fmt.txt`, `types.txt`, `variadic.txt`.

## The assert bindings

`assert.Equal` is a local helper, not testify: an assertion library's
signature ends in `msgAndArgs ...interface{}`, and a packed
`...interface{}` of arbitrary arity is a reflect call. The fixture
runtime binds a fixed shape instead:

```go
"Equal": assertEqual // func(tb any, want, got any, message string)
```

Every parameter has a layout class, so an assertion is one direct call,
and the message stays optional because every trailing argument is:
`assert.Equal(tb, want, got)` zero-fills it to `""`. `tb` is typed
`any` and asserted to `testing.TB` inside the helper, which keeps
`testing` out of the runtime's own types.

This is the pattern for binding any variadic API into the hot path:
wrap it at the arity the scripts use and let zero-fill make the tail
optional. The general forms still work unwrapped, spread through the
slice ABI directly and pack through a compiled slice-building closure,
with the reflect bridge as the floor for anything else.

## The context

No fixture ever writes a context. `http.NewRequestWithContext` is
called with two arguments and the runtime fills the `context.Context`
parameter with the execution context, which the runner derives from
`t.Context()`. The http fixture proves the plumbing end to end:

```
ctx := req.Context();
assert.Equal(tb, "fixture", ctxValue(ctx));
```

`ctxValue` is a test binding that reads a value the runner attached to
the execution context. It comes back `"fixture"` only if the context
travelled from the subtest, through the auto-filled parameter, into
the request, and back out.

## What this costs

`BenchmarkFixtures` runs every fixture against a handwritten mirror: a
`testFixtures` method doing the same work with the same assertions,
context from `tb.Context()` on both sides. All six fixtures run fully
on the direct-call tier. Pinned core, inlining disabled:

| fixture | vm | native | ratio |
|---|---|---|---|
| fmt | 1.7us, 7 allocs | 1.1us, 4 | 1.6x |
| http | 3.3us, 9 allocs | 2.8us, 9 | 1.2x |
| json | 5.2us, 17 allocs | 4.0us, 16 | 1.3x |
| types | 2.5us, 9 allocs | 1.6us, 4 | 1.6x |
| url | 4.9us, 15 allocs | 4.5us, 14 | 1.1x |
| variadic | 0.6us, 3 allocs | 0.6us, 3 | 1.0x |

http and variadic reach allocation parity with their mirrors; json and
url are within one allocation, which is the frame. The fixtures that lean
on formatting and assertion plumbing sit under 2x. For the bridge cost
of a call the shape table cannot express, and for the work-only
comparison without assertions, see the benchmarks in
`fixture_bench_test.go`. For how these numbers move when inlining is
enabled, which is what a caller sees, see [inlining.md](inlining.md).

Two details of the direct tier show up in these columns. A scalar
whose bits fit a byte boxes into a static cell rather than a fresh
one, the trick runtime.staticuint64s plays, which is one allocation
off fmt and types. A stack name the program reads more than once, tb
in every fixture, is loaded into a hidden frame field once when the
program starts; each use is then an offset read instead of a map
lookup, so a binding that mutates the stack mid-run is not seen by
later uses of the same name.

## Structs by value

`types.txt` declares `var u url.URL` and reads `u.Path`. A struct held
by value has no layout class, so it cannot travel between calls as a
closure return value; what it can do is live in the frame. The frame is
one `reflect.StructOf` allocation, so the struct is a field in it, the
declaration's zero value is the zeroed frame, and a field read or write
is an offset from the frame pointer. A whole-struct value only needs
transport when it is passed to a binding, where it goes through the
interface alias or, when the slot is written more than once, a copy:
`TestValueStructIntoInterface` pins that a binding that retains the
interface sees the value as it was at the call, not what a later field
write put in the slot.

## Learnings

- A fixture program is the test suite and the use-case demonstration at
  once: compiled hot inside the test process against live bindings,
  making its own assertions through a bound assert, with the subtest's
  `testing.TB` travelling on the stack.
- Adding a test is adding a file. Nothing regenerates and nothing
  relinks; the same binary runs new programs as long as the APIs they
  call are already bound.
- A variadic API binds into the hot path by wrapping it at the arity
  the scripts use and letting zero-fill make the tail optional; the
  fixed shape is what keeps an assertion a direct call.
- Context plumbing is provable end to end from inside a fixture: the
  value attached by the runner comes back out of the request only if
  the auto-filled parameter carried it.
- Against handwritten mirrors the fixtures run at 1.0x-1.6x with
  allocation parity on two of six, and the assert helper's comparable
  fast path is worth an allocation per assertion.
- A struct held by value lives in the frame: field access is an offset,
  and a whole-struct value only needs transport when a binding takes
  it.
