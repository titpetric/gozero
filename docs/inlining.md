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
| channels | 1883ns, 640 B, 15 allocs  | 1223ns, 400 B, 8  | 1.5x  |
| fmt      | 822ns, 152 B, 7 allocs    | 615ns, 48 B, 4    | 1.3x  |
| http     | 1723ns, 616 B, 9 allocs   | 1498ns, 640 B, 9  | 1.2x  |
| json     | 2128ns, 1016 B, 17 allocs | 1467ns, 728 B, 14 | 1.5x  |
| structs  | 4761ns, 2120 B, 26 allocs | 3206ns, 680 B, 19 | 1.5x  |
| types    | 2848ns, 440 B, 21 allocs  | 2131ns, 120 B, 11 | 1.3x  |
| url      | 2586ns, 816 B, 15 allocs  | 2175ns, 384 B, 12 | 1.2x  |
| variadic | 369ns, 69 B, 3 allocs     | 317ns, 69 B, 3    | 1.2x  |

## Without inlining (-gcflags=all=-l)

| fixture  | vm                        | native            | ratio |
|----------|---------------------------|-------------------|-------|
| channels | 4346ns, 640 B, 15 allocs  | 1978ns, 400 B, 8  | 2.2x  |
| fmt      | 1528ns, 152 B, 7 allocs   | 922ns, 48 B, 4    | 1.7x  |
| http     | 2765ns, 616 B, 9 allocs   | 2664ns, 640 B, 9  | 1.0x  |
| json     | 4166ns, 1016 B, 17 allocs | 3510ns, 984 B, 16 | 1.2x  |
| structs  | 7763ns, 2120 B, 26 allocs | 5025ns, 680 B, 19 | 1.5x  |
| types    | 4871ns, 440 B, 21 allocs  | 3117ns, 120 B, 11 | 1.6x  |
| url      | 4323ns, 816 B, 15 allocs  | 4007ns, 784 B, 14 | 1.1x  |
| variadic | 525ns, 69 B, 3 allocs     | 490ns, 69 B, 3    | 1.1x  |

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
moves furthest, 1.5x to 2.2x: its per-operation cost is the reflect
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
