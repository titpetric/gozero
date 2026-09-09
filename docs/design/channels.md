---
title: What if we implemented channels?
date: "2026-09-09T00:00:00+02:00"
---

Channels are half in the language already. A channel is one pointer
word, so `layoutOf` classifies it `lPtr` (stepjit_layout.go), the
discovery walk descends into channel element types (types.go), and
`nil` fills a channel parameter like any other nilable. What the
language lacks is every operation Go spells with syntax: `<-` in both
positions, `select`, `close`, `make`, and `go`. This document records
what channel usage the VM carries today, as values and as fields,
what the missing syntax would cost, and where `context.Context` fits,
because `ctx.Done()` is a channel and the auto-filled context turns
out to replace most of what channel syntax is for.

## What works today

Probed against the current tree, with a `chans` scope bound as:

```go
rt.BindScope("chans", map[string]any{
	"Make": func() chan string { return make(chan string, 1) },
	"Send": func(c chan string, v string) { c <- v },
	"Recv": func(ctx context.Context, c chan string) (string, error) {
		select {
		case v := <-c:
			return v, nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	},
})
```

**As values.** A channel made by a binding flows through slots and
arguments like any pointer-shaped value:

```go
c := chans.Make()
chans.Send(c, "x")
v := chans.Recv(c)
return v
```

This returns `"x"` on the reflect tier. All three calls bridge:
`Supports` reports shapes `_P`, `PS_` and `IP_SE` as not in the
table. The class is right and the shapes are ordinary, so putting
them in the table is the additive extension the shape table was
built for; nothing about a channel keeps it off the direct tier.

**From the stack.** A channel in the stack map passes into a binding
parameter of the same channel type and the receive comes back with
the value a goroutine had buffered. A pointer-shaped stack value
bridges (dynamicNode carries strings, interfaces and scalars), so
this too is reflect today.

**As fields.** A struct field of channel type reads like any field:
the class is `lPtr`, so a single field off a pointer-to-struct is a
compile-time offset on the direct tier and anything deeper stays on
the reflect evaluator, the same split every field type gets.

**Not declarable.** `reflect.Type.String()` spells channel types with
a space (`chan string`, `<-chan struct {}`), and the `typeref`
grammar accepts only `*` and `[]` prefixes on a dotted path. A
channel type is in the registry but no `var` statement can name it;
a channel-typed name gets its type by inference from a binding
result. Fixing that means grammar (`var c chan string`), and unlike
the operators it is a small, closed addition.

## The context connection

`context.Context` is the one channel API the language already leans
on. Three facts line up:

- `ctx.Done()` returns `<-chan struct{}`: a channel is reachable
  from a value every program already has.
- Explicit context use exists. A written argument that is statically
  a context overrides the auto-fill (vm_call.go): `ctxValue(ctx)` in
  the http fixture passes the context the program got from
  `req.Context()`, while the same parameter with no written argument
  takes the execution context.
- The idiomatic consumer of `ctx.Done()` is
  `select { case v := <-ch: ... case <-ctx.Done(): return ctx.Err() }`,
  and that whole shape collapses into one binding call. `Recv` above
  runs the select in Go, the auto-filled context supplies the
  cancellation arm, and the error contract turns `ctx.Err()` into
  program termination. Probed: `chans.Recv(c)` on an empty channel
  under a 20ms `ExecContext` deadline returns
  `context deadline exceeded` and ends the program.

So the program never needs to see the Done channel: cancellation
enters through the parameter the language already fills, and leaves
through the error path the language already checks. Receive with
cancellation, the common reason to write `select`, is expressible
today with no syntax.

One defect the probe found, independent of channels: `ctx.Done()`
itself did not compile. A method called on an interface-typed name
reached `compileCall` through `MethodByName`, which returns a nil
`Func` for interface types, and `Compile` panicked on the zero
`reflect.Value`. Every interface-typed name had the problem
(`ctx.Err()`, a method on an `io.Reader` slot); channels only made
it visible because contexts are where interface-typed names are
common. Fixed since: `ifaceMethodFunc` synthesizes the dispatch on
the dynamic value, so `ctx.Done()` compiles like any method call
([changelog](../changelog.md)).

## What syntax would cost

**Receive and send are operators.** `v := <-c` is an expression form
and `c <- v` a statement form of the one token the grammar does not
have. Everything [expressions.md](expressions.md) records applies;
receive adds two results (`v, ok := <-c`), blocking, and a nil
channel that blocks forever.

**select is a conditional and then some.** A multi-way branch on
channel readiness, with blocking, a default arm, and per-arm bodies:
it needs blocks, branch compilation and the early-exit signal from
[conditions.md](conditions.md), plus readiness semantics of its own.
`reflect.Select` implements the runtime part in the stdlib, so a
select node is buildable; the grammar and control-flow costs are the
same ones already declined.

**The language cannot spawn the peer.** There is no `go` statement,
and a program is one synchronous straight line. A channel program
needs a peer on the other end, and in this
design that peer is the host's: a compiled program is safe for
concurrent use, so the host can run `fn.Exec` in as many goroutines
as it wants and hand each the same channels through the stack.
Adding `go` would put program-spawned concurrency inside the sandbox
boundary and is a larger decision than the channel syntax itself.

**range over a channel** is [loops.md](loops.md) with a channel as
the sequence; `reflect.Value.Seq` already iterates channels, so the
reflect tier cost is the loop machinery, not the channel.

## Alternatives

**Typed channel bindings.** The probed pattern: the host binds
`Make`, `Send`, `Recv`, `Close` at the element types its programs
use, with `Recv` taking the auto-filled context. Semantics are
compiled Go's, cancellation is free, and the calls sit one shape
table entry away from the direct tier. Cons:

- One binding set per element type, the same multiplication the
  operation bindings in [expressions.md](expressions.md) pay.
- No select over two data channels: a binding can select over the
  channels it was passed, but the arms and their bodies are fixed in
  Go, not written in the program.
- `v, ok := <-c` needs a second result or a sentinel; closed-channel
  handling is the binding author's convention rather than a language
  rule.

**Generic channel bindings over reflect.** One `Recv(ctx, ch any)`
using `reflect.Select` covers every element type. Cons: the result
comes back as `any`, so the static type the bindings otherwise
provide is lost at the receive, and every call pays reflect
regardless of tier.

**iter.Seq instead of channels.** For streams consumed in order, a
binding returning `iter.Seq[T]` gives the consumer side of a channel
without the concurrency, and lines up with the range-over-func cut
in [loops.md](loops.md). Cons: no send side, no select, and the
producer runs in the consumer's goroutine.

**Host owns the concurrency.** Goroutines, wait groups and channel
topology stay in Go; programs are the bodies, run via `Exec` from
the host's goroutines, with channels passed in on the stack and
consumed through bindings. This is the shell model: the shell owns
the processes and the pipes, and a command only reads and writes the
ends it was handed. Cons: a program cannot set up a pipeline of its
own, and the topology is invisible in the program text that uses it.

## In other imperative settings

A test that waits on asynchronous work is the imperative use case
that fits: `v := chans.Recv(done); assert.Equal(tb, "ok", v)` reads
like the testify code it mirrors, and a deadline on the test context
fails it cleanly through the error contract instead of hanging. An
`http.Handler` body already holds the request context implicitly;
its channel use in Go is almost always `ctx.Done()` inside a select,
which the auto-fill covers without the program mentioning it.
Worker pools, fan-in and fan-out are topology, and every imperative
language that stayed small solved topology outside the program:
shell pipes are channels the shell wires up before any command
runs. Middleware has no channel use to speak of.

## Verdict

Channel values and channel fields are already carried by both tiers,
one shape-table entry short of direct calls, and nothing about the
data type needs language work. The operations are where the costs
live, and they decompose onto the other documents: receive and send
are operators, select is a conditional, range-over-channel is a
loop, and `go` is a sandbox decision. The context connection removes
the most common reason to want any of them: `<-ctx.Done()` inside a
select is one `Recv(ctx, ch)` binding with the context auto-filled
and the error contract as the cancellation path. Typed channel
bindings with a context parameter are the recorded position; the
interface-method compile panic the probe found was a defect in
method resolution, fixed since.
