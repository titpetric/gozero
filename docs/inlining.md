---
title: Inlining and the vm/native gap
date: "2026-09-07T10:02:54+02:00"
---

`BenchmarkFixtures` compared across two builds of the same tree: the
default build, and `-gcflags=all=-l`, which disables inlining in every
package including the standard library. Pinned core (`taskset -c 3`),
Intel N150, go1.27, one 1s run per benchmark. The tables were
remeasured on 2026-09-10, after the channels fixture landed on the
direct tier ([changelog](changelog.md)).

## With inlining (default build)

| fixture  | vm                        | native            | ratio |
|----------|---------------------------|-------------------|-------|
| channels | 1982ns, 640 B, 15 allocs  | 1277ns, 400 B, 8  | 1.6x  |
| fmt      | 893ns, 128 B, 6 allocs    | 609ns, 48 B, 4    | 1.5x  |
| http     | 1636ns, 592 B, 8 allocs   | 1452ns, 592 B, 8  | 1.1x  |
| json     | 2139ns, 984 B, 16 allocs  | 1435ns, 728 B, 14 | 1.5x  |
| structs  | 4528ns, 1640 B, 25 allocs | 3111ns, 680 B, 19 | 1.5x  |
| types    | 3120ns, 248 B, 20 allocs  | 2143ns, 120 B, 11 | 1.5x  |
| url      | 2542ns, 784 B, 14 allocs  | 2117ns, 384 B, 12 | 1.2x  |
| variadic | 376ns, 69 B, 3 allocs     | 319ns, 69 B, 3    | 1.2x  |

## Without inlining (-gcflags=all=-l)

| fixture  | vm                        | native            | ratio |
|----------|---------------------------|-------------------|-------|
| channels | 4299ns, 640 B, 15 allocs  | 2071ns, 400 B, 8  | 2.1x  |
| fmt      | 1522ns, 128 B, 6 allocs   | 913ns, 48 B, 4    | 1.7x  |
| http     | 3090ns, 592 B, 8 allocs   | 2315ns, 592 B, 8  | 1.3x  |
| json     | 4434ns, 984 B, 16 allocs  | 3546ns, 984 B, 16 | 1.3x  |
| structs  | 7837ns, 1640 B, 25 allocs | 5285ns, 680 B, 19 | 1.5x  |
| types    | 4569ns, 248 B, 20 allocs  | 3284ns, 120 B, 11 | 1.4x  |
| url      | 4572ns, 784 B, 14 allocs  | 3968ns, 784 B, 14 | 1.2x  |
| variadic | 557ns, 69 B, 3 allocs     | 491ns, 69 B, 3    | 1.1x  |

The frame pool ([changelog](changelog.md), 2026-09-10) removed one
allocation from every fixture whose frame the compiler proves does
not escape, which is six of the eight: json and url sit at
allocation parity with their mirrors under `-l`, and http is
byte-identical to its mirror in both builds, 592 B and 8 allocations
on each side. An allocation profile of the http pair caught a
harness defect while confirming these counts: the mirror built its
context.WithValue per iteration where the vm side built it once, an
extra allocation that belonged to the benchmark, not to either
implementation. The mirror's context is built once now, matching
the vm side. channels and variadic are unchanged by the pool -
variadic needs no frame, and the channels fixture aliases string
slots into its asserts, so its frame escapes and allocates fresh.

## What the difference says

Disabling inlining roughly doubles both columns: most of every
fixture's time is shared work in fmt and net/*, and that work
inflates equally on both sides. The ratios still move, in two
directions.

The compiled program is a graph of closures called through function
pointers. The compiler can never inline across those calls, in either
build, so the vm column changes only by what the standard library
loses. The native column additionally loses the inlining of its own
statements. Fixtures whose per-statement work is small show it most:
fmt goes 1.3x to 1.7x, because a fixed per-node dispatch cost stands
out once the statements around it stop being folded away. variadic
sits near 1.1x in both builds: its work is one spread call, and the
vm and the mirror make the same calls once nothing inlines. channels
moves furthest, 1.5x to 2.1x: its per-operation cost is the reflect
receive and the element box, both immune to inlining, while the
mirror's channel operations are runtime calls whose surroundings
inline away.

Inlining also feeds escape analysis, which shows in the alloc columns.
With inlining, json/native drops from 16 allocs to 14 and url/native
from 14 to 12: inlined callees let the compiler prove pointers do not
escape and keep values on the stack. The vm's counts are identical in
both builds, because its values travel through closure returns and
interface boxes the compiler cannot see through.

So the no-inline ratio is the structural cost of interpreting: one
indirect call and one boxed transport per node, immune to compiler
optimisation. The default-build ratio is the one a caller sees, and is
lower because the interpreter's fixed cost is diluted by library work
that inlining has already made faster on both sides.

## Reproducing

```
taskset -c 3 go test -run '^$' -bench 'BenchmarkFixtures/' -benchmem -benchtime 1s
taskset -c 3 go test -run '^$' -bench 'BenchmarkFixtures/' -benchmem -benchtime 1s -gcflags=all=-l
```

## Learnings

- Disabling inlining roughly doubles both columns because most of every
  fixture's time is shared library work; the ratios still move, and in
  both directions.
- The no-inline ratio approximates the structural cost of interpreting,
  one indirect call and one boxed transport per node, immune to the
  compiler in either build. The default-build ratio is the one a
  caller sees.
- Inlining feeds escape analysis: the native mirrors lose allocations
  with it on, the vm's counts do not move, because its values travel
  through closures the compiler cannot see through.
- A fixture whose work is one call runs at native parity once nothing
  inlines: variadic reads 1.0x-1.1x across sweeps, which locates the
  interpreter's overhead in the statements around a call rather than
  in the call.
