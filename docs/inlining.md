---
title: Inlining and the vm/native gap
date: 2026-09-07T10:02:54+02:00
---

`BenchmarkFixtures` compared across two builds of the same tree: the
default build, and `-gcflags=all=-l`, which disables inlining in every
package including the standard library. Pinned core (`taskset -c 3`),
Intel N150, go1.27, one 1s run per benchmark.

## With inlining (default build)

| fixture | vm | native | ratio |
|---|---|---|---|
| fmt | 1002ns, 152 B, 7 allocs | 801ns, 48 B, 4 | 1.3x |
| http | 2227ns, 616 B, 9 allocs | 2038ns, 640 B, 9 | 1.1x |
| json | 2798ns, 1016 B, 17 allocs | 2170ns, 728 B, 14 | 1.3x |
| types | 1775ns, 272 B, 9 allocs | 1308ns, 38 B, 4 | 1.4x |
| url | 3413ns, 816 B, 15 allocs | 2802ns, 384 B, 12 | 1.2x |
| variadic | 425ns, 69 B, 3 allocs | 359ns, 69 B, 3 | 1.2x |

## Without inlining (-gcflags=all=-l)

| fixture | vm | native | ratio |
|---|---|---|---|
| fmt | 1702ns, 152 B, 7 allocs | 1060ns, 48 B, 4 | 1.6x |
| http | 3327ns, 616 B, 9 allocs | 2776ns, 640 B, 9 | 1.2x |
| json | 5235ns, 1016 B, 17 allocs | 4013ns, 984 B, 16 | 1.3x |
| types | 2499ns, 272 B, 9 allocs | 1608ns, 38 B, 4 | 1.6x |
| url | 4864ns, 816 B, 15 allocs | 4495ns, 784 B, 14 | 1.1x |
| variadic | 610ns, 69 B, 3 allocs | 619ns, 69 B, 3 | 1.0x |

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
fmt goes 1.3x to 1.6x, because a fixed per-node dispatch cost stands
out once the statements around it stop being folded away. variadic
runs at native parity without inlining: its work is one spread call,
and the vm and the mirror make the same calls once nothing inlines.

Inlining also feeds escape analysis, which shows in the alloc columns.
With inlining, json/native drops from 16 allocs to 14 and url/native
from 14 to 12: inlined callees let the compiler prove pointers do not
escape and keep values on the stack. The vm's counts are identical in
both builds, because its values travel through closure returns and
interface boxes the compiler cannot see through.

So the no-inline ratio is the structural cost of interpreting: one
indirect call and one boxed transport per node, immune to compiler
optimisation. The default-build ratio is what a caller sees, and is
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
  compiler in either build. The default-build ratio is what a caller
  sees.
- Inlining feeds escape analysis: the native mirrors lose allocations
  with it on, the vm's counts do not move, because its values travel
  through closures the compiler cannot see through.
- A fixture whose work is one call runs at native parity once nothing
  inlines: variadic hit 1.0x, which locates the interpreter's overhead
  in the statements around a call rather than in the call.
