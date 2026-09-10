---
title: Channels
date: "2026-09-10T11:31:00+02:00"
---

Channels moved from research into the syntax on 2026-09-10. This
document records what landed and its semantics, how the operations
run on each tier with the measured cost, and what stayed out with
the reasons the research established. The original question, "what
if we implemented channels?", is answered: receive and send are
language operations, and everything else about a channel was already
a value.

## What landed

```go
c := chanOf("a", "b");   a channel is a value a binding produces
v := <-c;                receive, binds the element
c <- "sent";             send
<-c;                     bare receive, for its blocking effect
var d chan string;       chan, <-chan and chan<- in a typeref
b.C <- "field";          the channel can live in a field
```

The implicit rules carry over from the rest of the language:

- The `ok` of Go's two-value receive is implicit the way a trailing
  error is: never a value, checked after every receive. A receive
  from a closed channel ends the program with `io.EOF`, so a host
  loops `Exec` until `errors.Is(err, io.EOF)` and the program never
  writes the check. `v, ok := <-c` is a compile error naming the
  rule.
- Both operations are armed with the execution context. A blocked
  receive or send ends the program with `ctx.Err()` when the
  `ExecContext` deadline passes or the context is cancelled, which
  also bounds a nil channel; without a deadline a blocked operation
  blocks, as it does in Go. The select-on-`ctx.Done()` pattern that
  motivated the research became the operation itself.
- A send on a closed channel panics as in Go and arrives as
  `*PanicError` through the guard.
- The channel must have a static type: a name the program bound, a
  field read off one, or a call result. A stack name is rejected at
  compile time the way a method on one is, because its type is only
  known at execution.
- Declaration rules apply: `:=` or `var` declares the received
  name, `=` assigns to a declared one.

There is no `go` statement. The peer is the host's: goroutines,
channel construction and topology stay in Go, and a compiled program
is safe to run from as many goroutines as the host starts. That is
the shell model the research pointed at: the shell owns the
processes and the pipes, a command only reads and writes the ends it
was handed. The worker pattern is the host looping `Exec` per
message until `io.EOF`; [syntax.md](../syntax.md) shows it next to
the Go it replaces.

## How it runs

Both tiers share `chanRecv` and `chanSend` (vm_chan.go): a
non-blocking `TryRecv`/`TrySend` first, which allocates nothing when
the channel is ready and distinguishes ready, closed and would-block,
then the two-case `reflect.Select` with the context's `Done` for the
blocking path. On the direct tier the operations are language nodes
(stepjit_chan.go), not bound calls: the channel resolves through the
same producers the bridge uses, the received element stores into the
frame through a typed `reflect.Set`, and neither operation appears
in `Supports` output. The channels fixture reports `tier: JIT`; the
`_P` and `L_P` shapes its constructor bindings needed are in the
table.

Measured against its handwritten mirror, pinned core: 1883ns against
1223ns in a default build (1.5x, inside the band the other fixtures
sit in) and 4346ns against 1978ns with inlining disabled (2.2x). The
gap is allocation: 15 against the mirror's 8, because every reflect
receive boxes the element where the mirror receives into a local.
The unexplored next step is a typed fast path per element class,
reinterpreting the frame's channel word as the concrete channel type
so a `chan string` receive is a native select; the reflect path
would remain for named and struct elements.

## What stayed out

The research decomposed the remaining channel surface onto the other
documents, and that decomposition held:

- `select` is a multi-way conditional with blocking and per-arm
  bodies: block syntax, branch compilation and the early-exit signal
  from [conditions.md](conditions.md), plus readiness semantics of
  its own. The context arm, its most common use, is built into every
  receive and send instead.
- `range` over a channel is [loops.md](loops.md) with a channel as
  the sequence. The host loops; a program handles one element.
- `go` would put program-spawned concurrency inside the sandbox
  boundary, a larger decision than any syntax: today a program's
  concurrency is exactly what the host starts.
- `make` stays a binding. The host constructs channels and chooses
  their capacity, the way it constructs every other resource the
  binding set hands out.

Two research alternatives were superseded by the syntax: typed
channel bindings (`chans.Recv(ctx, c)`) are no longer needed, and
the generic reflect binding's type loss does not arise, because the
element type comes from the channel's static type at compile time.
`iter.Seq` bindings consumed by a host loop remain the answer for
streams that need no send side.
