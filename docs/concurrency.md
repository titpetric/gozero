---
title: 'Concurrency: a mutex binding against channel sends'
date: "2026-09-10T12:30:00+02:00"
---

A program has two ways to touch shared state. Channel receive and
send are syntax ([syntax.md](syntax.md)); everything else goes
through a binding, and the simplest binding for shared state is a
mutex-protected map. This chapter measures the two against each
other and against the same operations in Go, and records when to
reach for which.

## The mutex binding

`MutexMap` is a `map[string]int` behind a `sync.Mutex`, exported by
the package so a host binds a constructor or closes over a shared
instance:

<table>
<tr>
<th>go</th>
</tr>
<tr>
<td>

```go
shared := gozero.NewMutexMap()
rt.Bind("store", func() *gozero.MutexMap { return shared })
```

</td>
</tr>
<tr>
<th>gozero</th>
</tr>
<tr>
<td>

```go
m := store()
m.Set("k", 42)
v := m.Get("k")
```

</td>
</tr>
</table>

`Set` and `Get` are ordinary method calls, so they compile like
every other bound call and land on the direct tier through the
shape table: a funcval cast and a plain Go call,
no reflection anywhere on the path. The binding is the
synchronization: one compiled program run from many goroutines
against one shared map is race-free because the map is
(`TestMutexMap` runs exactly that under the race detector).

## The measurement

`BenchmarkConcurrency` runs a write and a read of shared state four
ways: the mutex map and a buffered channel, each native and as a
compiled program. Both programs have the same shape - one nullary
constructor call, one write, one read - and both run on the direct
tier, asserted by `TestConcurrencyPrograms` before the numbers mean
anything. Each figure is the best of three fixed-count runs,
uncontended.

Table 1, the default build, best of three fixed-count runs,
remeasured 2026-09-10:

| write + read | native | vm               | vm overhead |
|--------------|--------|------------------|-------------|
| mutex map    | 40.6ns | 138.4ns, 8 B, 1  | +98ns       |
| channel      | 45.2ns | 255.2ns, 40 B, 3 | +210ns      |

Table 2, with `-gcflags=all=-l`:

| write + read | native | vm               |
|--------------|--------|------------------|
| mutex map    | 57.4ns | 190.2ns, 8 B, 1  |
| channel      | 58.2ns | 458.1ns, 40 B, 3 |

Both programs qualify for the frame pool, which removed the per-run
frame allocation and 14% of the mutex program's time when it landed
([changelog](changelog.md)); the remaining allocation on the mutex
side is the returned value's box. Neither program has NonRetaining
bindings, so argument pooling does not apply here.

## What the numbers say

**The channel itself is not the cost.** The native columns sit 5ns
apart: an uncontended send-and-receive on a buffered channel costs
45ns against the mutex round trip's 41ns. A channel send carries no
meaningful inherent overhead at this grain, so the 2.0x of the
channels fixture ([fixtures.md](fixtures.md)) cannot be the channel:
the gap has another owner.

**The reflect layer is the cost.** The compiled mutex program pays
+98ns over native; the compiled channel program pays +210ns. The
difference is where the two paths run: the mutex methods are direct
shape-table calls with no reflection, while receive and send are
runtime-generic and go through `reflect.Value` - `TryRecv` and
`TrySend`, each boxing the element it moves. The allocation columns
carry the same story: one allocation is the returned value's box,
shared by both programs, and the channel program's two extra are the
element boxes, one per operation.

**Method calls normalize onto the fast path; channel operations do
not, yet.** A bound method with in-table shapes runs as a cast and
a call, so any API expressible as methods inherits the tens of
nanoseconds the shape table gives everything else. The channel
operations bypass the table because they are generic over the
element type. The typed fast path recorded in
[design/channels.md](design/channels.md), reinterpreting the frame's
channel word as the concrete channel type, would close the gap.

## Choosing

- Shared mutable state - counters, registries, last-seen values -
  belongs behind a mutex binding. It is the faster path from a
  program, allocates less, and never blocks the program's run.
- Handoff and streams belong on channels: a receive blocks until a
  peer produces, ends with `io.EOF` when the stream closes, and the
  execution context bounds the wait. A mutex map can express none
  of that; polling one from a host loop is the workaround channels
  exist to remove.
- The numbers above are uncontended, and measure API cost rather
  than lock behaviour. Under contention both paths serialize on the
  same runtime primitives, and the choice goes back to shape:
  state is a lock's job, flow is a channel's.
