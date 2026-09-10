---
title: Inlining and the vm/native gap
date: "2026-09-07T10:02:54+02:00"
---

`BenchmarkFixtures` compared across two builds of the same tree: the
default build, and `-gcflags=all=-l`, which disables inlining in every
package including the standard library. Pinned core (`taskset -c 3`),
Intel N150, go1.27; each figure is the best of three fixed-count
runs (`-benchtime 100000x -count 3`), which holds steady against
other load on the shared machine. The tables were remeasured on
2026-09-10, after argument pooling landed
([changelog](changelog.md)).

## With inlining (default build)

Table 1, the fixture suite in a default build; other chapters
reference these measurements by table number rather than carrying
copies:

| fixture  | vm                        | native            | ratio |
|----------|---------------------------|-------------------|-------|
| channels | 2001ns, 608 B, 13 allocs  | 1293ns, 400 B, 8  | 1.5x  |
| fmt      | 792ns, 16 B, 2 allocs     | 607ns, 48 B, 4    | 1.3x  |
| http     | 1796ns, 576 B, 7 allocs   | 1590ns, 592 B, 8  | 1.1x  |
| json     | 2376ns, 952 B, 14 allocs  | 1579ns, 728 B, 14 | 1.5x  |
| structs  | 4947ns, 1400 B, 18 allocs | 3394ns, 680 B, 19 | 1.5x  |
| types    | 2928ns, 56 B, 8 allocs    | 2272ns, 120 B, 11 | 1.3x  |
| url      | 2791ns, 736 B, 11 allocs  | 2337ns, 384 B, 12 | 1.2x  |
| variadic | 439ns, 53 B, 2 allocs     | 347ns, 69 B, 3    | 1.3x  |

## Without inlining (-gcflags=all=-l)

Table 2, the same suite with inlining disabled:

| fixture  | vm                        | native            | ratio |
|----------|---------------------------|-------------------|-------|
| channels | 4355ns, 608 B, 13 allocs  | 2045ns, 400 B, 8  | 2.1x  |
| fmt      | 1232ns, 16 B, 2 allocs    | 972ns, 48 B, 4    | 1.3x  |
| http     | 3185ns, 576 B, 7 allocs   | 2510ns, 592 B, 8  | 1.3x  |
| json     | 4573ns, 952 B, 14 allocs  | 3789ns, 984 B, 16 | 1.2x  |
| structs  | 8800ns, 1400 B, 18 allocs | 5830ns, 680 B, 19 | 1.5x  |
| types    | 4446ns, 56 B, 8 allocs    | 3449ns, 120 B, 11 | 1.3x  |
| url      | 4968ns, 736 B, 11 allocs  | 4184ns, 784 B, 14 | 1.2x  |
| variadic | 596ns, 53 B, 2 allocs     | 525ns, 69 B, 3    | 1.1x  |

Two pools shape the allocation columns. The frame pool recycles the
per-run frame of every program whose frame the compiler proves does
not escape. Argument pooling recycles the pack slices, string boxes
and literal blocks behind every call, under the binding contract
that arguments are borrowed. Together they put seven of the eight fixtures at or below their mirrors'
allocation counts: fmt runs at 2 allocations against its mirror's
4, types at 8 against 11, and json at parity in the default build
and two under at `-l`. channels is the exception at 13 against 8:
its constructor packs pool, but its receive and send box one element
each inside reflect, and its asserts read from write-once string
slots, which alias the frame instead of boxing.

## What the difference says

Disabling inlining roughly doubles both columns: most of every
fixture's time is shared work in fmt and net/*, and that work
inflates equally on both sides. The ratios still move, in two
directions.

The compiled program is a graph of closures called through function
pointers. The compiler can never inline across those calls, in either
build, so the vm column changes only by what the standard library
loses. The native column additionally loses the inlining of its own
statements. channels moves furthest, 1.5x to 2.1x: its per-operation
cost is the reflect receive and the element box, both immune to
inlining, while the mirror's channel operations are runtime calls
whose surroundings inline away. fmt holds 1.3x in both builds since
argument pooling: two allocations of remaining vm work leave little
for either build to fold away. variadic sits between 1.1x and 1.3x:
its work is one spread call, and the vm and the mirror make the same
calls once nothing inlines.

Inlining also feeds escape analysis, which shows in the alloc columns.
With inlining, json/native drops from 16 allocs to 14 and url/native
from 14 to 12: inlined callees let the compiler prove pointers do not
escape and keep values on the stack. The vm's counts are identical in
both builds, because its values travel through closure returns and
interface boxes the compiler cannot see through.

The no-inline ratio is the structural cost of interpreting: one
indirect call and one boxed transport per node, immune to compiler
optimisation. The default-build ratio is the one a caller sees, and is
lower because the interpreter's fixed cost is diluted by library work
that inlining has already made faster on both sides.

## Reproducing

```
taskset -c 3 go test -run '^$' -bench 'BenchmarkFixtures/' -benchmem -benchtime 100000x -count 3
taskset -c 3 go test -run '^$' -bench 'BenchmarkFixtures/' -benchmem -benchtime 100000x -count 3 -gcflags=all=-l
```

Take the best run per benchmark: the minimum is the run least
disturbed by other load, and the fixed iteration count keeps the
three runs comparable.

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
- A fixture whose work is one call runs near native parity once
  nothing inlines: variadic reads 1.1x-1.3x across sweeps, which
  locates the interpreter's overhead in the statements around a call
  rather than in the call.
