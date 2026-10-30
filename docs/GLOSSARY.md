---
title: Glossary
date: "2026-09-10T16:00:00+02:00"
---

Definitions for the virtual machine and JIT terms this project's
documentation uses. Each entry defines one term on its own; entries
reference each other by name. Where a term carries one meaning in
the literature and another here, the entry has both, the general
meaning first.

## allocation

One request for heap memory. Benchmarks report allocations per
operation as `allocs/op` and bytes per operation as `B/op`. An
allocation costs time when the program makes it and costs time again
when the garbage collector later scans and frees it.

## aliasing

Passing a pointer to existing storage in place of a copy of the
value. In gozero, an interface argument can point directly at a
frame slot and skip the copied cell, which saves the copy's
allocation. The alias is only safe while the storage outlives every
holder of the pointer, so the frame pool cannot take an aliased frame
and no statement can rewrite an aliased slot.

## back edge

The control transfer from the end of a loop body back to the loop
header, and the reason a loop runs more than once. A loop with a
static trip count unrolls into straight-line code and keeps no back
edge. A loop with a runtime trip count keeps one, so a scheme that
removes every jump has to emit one copy of the body per trip and
cannot bound the code it emits.

## bailout

The transfer of control out of specialised code when a guard fails,
into a general implementation that covers the case the
specialisation left out. A trace JIT calls the exit a side exit and
HotSpot calls the guard site an uncommon trap. The word names the
event; deoptimisation names the state reconstruction behind it.

## basic block versioning

Compiling more than one version of a basic block, each specialised
to the types that hold along the path reaching it, and picking the
version as control arrives. The lazy form compiles a version on
first use and caps the count with a version limit. It removes type
tests with no type-analysis pass, at the code size of the extra
versions.

## binding

A Go function the host registers in a Runtime under a name, for
example `rt.Bind("url.Parse", url.Parse)`. The set of bindings is
the complete surface a program can call: gozero programs have no
standard library of their own.

## boxed value

A value copied into a heap cell so that an interface can point at
it. Go's `any` and every other interface is two words: a type or
itab word, and a data word that holds a pointer. A pointer-sized
value stores its pointer in the data word directly. A wider value,
for example a string (two words) or a struct, does not fit, so the
runtime allocates a cell, copies the value into the cell, and points
the data word at the cell. That allocation plus copy is boxing.

```
s := "GET"        // a string: two words (pointer, length), no heap cell
var v any = s     // boxing: cell := new(string); *cell = s
                  // v = {type: string, data: cell}

p := &Request{}   // a pointer: one word
var w any = p     // no boxing: w = {type: *Request, data: p}
```

Boxing appears in benchmark output as one allocation per boxed
value. gozero avoids boxing where it can: constants box once at
compile time, small integers point into a static table and need no
fresh cell, and an argument aliases an eligible frame slot and skips the copy.

## borrowed argument

The binding contract: a call's arguments are valid for the duration
of the call, and a binding that keeps one copies it first. The
runtime relies on the contract to recycle the memory behind
arguments - the pack slice, the boxed values, the literal blocks -
once the call returns. A type assertion copies a value out of its
box; a plain interface assignment copies only the box's address and
is not a copy in this sense. String and scalar parameters carry
their values directly and need no copy.

## branch predictor

The hardware that predicts the direction and target of a branch
before the condition resolves, so the pipeline keeps fetching. A
correct prediction costs nothing. A wrong one costs a misprediction,
which is the per-event cost a branch-removing transformation sets
out to recover.

## builtin

One of the eighteen functions Go's spec defines with no package
behind them: `append`, `cap`, `clear`, `close`, `complex`, `copy`,
`delete`, `imag`, `len`, `make`, `max`, `min`, `new`, `panic`,
`print`, `println`, `real` and `recover`. A builtin is generic over
its argument's type, which no single Go signature expresses; some
produce a constant. In gozero the compiler recognises none of them:
`append` and `len` resolve through the binding lookup like any other
name. A host binds one at a single signature, so the bound form
covers one argument type. An `any` signature covers every type and
checks none.

## computed goto

The C extension that takes the address of a label and jumps through
it, `goto *ptr`. An interpreter ends every handler with its own
indirect jump to the next handler, in place of one shared dispatch
switch. The copies spread the dispatch across the handlers, so the
branch predictor keeps a separate history per opcode.

## deoptimisation

Reconstructing the state a general implementation needs from the
state optimised code holds, so execution leaves the optimised code
and resumes in the general one at the same program point. The
optimised code may hold a value in a register, in a different frame
layout, or nowhere at all. HotSpot names the trigger an uncommon
trap. A bailout is the event; deoptimisation is the state
reconstruction behind it. gozero does none, because every
specialisation it makes happens at compile time against a static
type.

## direct-call tier

The fastest of the three execution tiers. A bound function whose
signature matches an entry in the shape table runs as a plain
Go call, with no reflection. The other two tiers are the reflect
bridge and the reflect evaluator. The design documents shorten the
name to the direct tier.

## discovery

The runtime walk that makes a type nameable with no host naming it.
Every `Bind` walks the type graph reachable from the signature -
parameters, results, pointees, elements, exported struct fields,
interface methods - five levels deep and registers every type it
finds. Binding `url.Parse` makes `var u url.URL` compile with no
registration of `url.URL` anywhere. `BindType` covers the exception,
a type no binding mentions.

## escape

A pointer escapes when it outlives the scope that created it, which
forces the pointed-to memory onto the heap and blocks reuse. The
frame pool depends on an escape proof: the compiler marks every node
that passes a frame pointer to a callee, and the pool recycles only
a frame with no such node.

## fixture

A gozero program stored as a file under `testdata/`, for example
`testdata/http.txt`. The test suite compiles and runs every fixture;
each fixture makes its own assertions through a bound assert
function.

## frame

One per-run block of memory holding a program's named values. The
compiler lays the frame out as a Go struct with one field per
surviving slot, using `reflect.StructOf`, so the garbage collector
scans it correctly. A program with no multi-use names has no frame.

## frame pool

A `sync.Pool` that recycles a program's frames between runs, saving
the per-run frame allocation. Only a program whose frame provably
never escapes gets a pool; the pool clears a reused frame back to
zero before the next run.

## Futamura projection

One of three results that follow from partial evaluation. The first:
specialising an interpreter to a fixed source program produces that
program compiled. The second: specialising the specialiser to a
fixed interpreter produces a compiler for the language that
interpreter reads. The third: specialising the specialiser to itself
produces a compiler generator. A design that specialises an
interpreter for one fixed program describes compiled output. The
machinery that does it for any program is a compiler.

## growslice

The Go runtime function behind `append` when the slice has no spare
capacity: it allocates a larger backing array, copies the elements
and returns the new header. A direct-tier `append` would have to
reproduce it, which is an allocation the closure tree cannot
express, so `append` stays a binding.

## guard

A runtime test inside specialised code that checks the condition the
specialisation depends on and bails out when the check fails. A
trace JIT puts one at every condition it recorded. The count of
guards is the price of recording a straight line through branching
code.

In gozero, `guard` in `runtime.go` wraps what `Compile` returns and
turns a panic inside a binding into a `*PanicError` on every tier.
The design documents also use the word for a one-armed condition
written as a call: a binding that returns a non-nil `error` ends the
program there, which is the only conditional the language has.

## hyperblock

A region of predicated basic blocks with one entry and more than one
exit. Tail duplication removes the side entries and if-conversion
folds the branches inside the region into predicates, so a
hyperblock is a superblock with its internal control flow turned
into data flow. It needs an instruction set that carries predicates.

## if-conversion

The compiler transformation that replaces a branch with a
computation of both arms and a selection of one result, converting a
control dependence into a data dependence. It merges paths where
tail duplication splits them. It pays when both arms are a few
instructions, because the work of the untaken arm costs less than a
misprediction. With no predication in the instruction set it lowers
to a select or a conditional move.

## inline cache

A per-call-site cache of the target a dynamically typed call last
resolved to, with a check that the receiver's type still matches. It
removes a method lookup per call and costs the check and the cache
storage. Quickening is one way to carry one in an interpreter: the
quickened instruction encodes the cached target, so the fast path
reads no cache slot and makes no indirect call. gozero needs none,
because the subset is statically typed and the compiler resolves
every method against a static type.

## IPC

Instructions per cycle: the rate at which a processor retires
instructions, and the throughput number a microarchitectural change
moves. A misprediction lowers IPC by stalling the pipeline for a
refill. Removing mispredictions raises IPC by the share of cycles
those refills hold.

## itab

The runtime table that backs a non-empty interface value: it pairs a
concrete type with an interface type and lists the method addresses.
The Go runtime builds itabs on demand during type assertions, and
gozero obtains them the same way at compile time, so no itab work
happens per call.

## JIT

Just-in-time compilation: translating a program into a faster
executable form at run time. gozero's JIT produces a tree of typed
Go closures, no machine code; the speed comes from replacing
per-call reflection with direct calls through the shape table.

## join point

A program point more than one path reaches. The condition before it
splits control and the join merges control back, at one assignment
to the program counter per path. Tail duplication removes a join by
copying everything after it once per incoming path. If-conversion
removes the split and keeps the join.

## layout class

The ABI category of a type: pointer-shaped (one word), string (two
words), interface (two words), slice (three words), or one scalar
class per width. Two types in the same layout class pass through
registers identically, and that lets one shape-table entry
serve every function with the same class signature. `bool` is a
scalar class, so a binding returning one reaches the direct tier as
a one-byte load. `layout` is a closed enum. Every scalar switch in
the tree covers each of its classes, so a type that fits no existing
class - a complex number is sixteen bytes and fits neither `uint64`
nor `float64` - costs two new classes, new node closure types, new
shape keys and an arm in each of those switches.

## mirror

The handwritten Go function that performs the same work as one
fixture, used as the native baseline in benchmarks. Every fixture
has one mirror in `fixture_bench_test.go`; `BenchmarkFixtures` runs
the compiled fixture and its mirror under the same measurement and
reports both.

## misprediction

A branch the predictor called wrong. The processor discards the
instructions it fetched past the branch and refetches from the
correct target, at the cost of the pipeline's depth in cycles.
Mispredictions are what a branch-removing transformation sets out to
save. MPKI is the rate they arrive at.

## monomorphisation

Compiling one copy of polymorphic code per concrete instantiation,
so each copy runs with no runtime type dispatch. The cost is code
size in proportion to the number of instantiations. In gozero the
copies are per layout class and not per type: one shape-table entry
covers every bound function whose parameters and results fall in the
same classes, which makes the direct tier monomorphic in the ABI and
polymorphic in the Go type. The choice happens at compile time
against a static type, so it needs no guard and no deoptimisation.

## MPKI

Mispredictions per kilo-instruction: the branch mispredictions a
program incurs for every thousand instructions it retires. The rate
compares across programs of different lengths where a raw
misprediction count does not. A dispatch measurement pairs it with
IPC, the first naming how often the event happens and the second
naming what it costs.

## partial evaluation

Specialising a program to part of its input, producing a residual
program over the rest. The literature names the specialiser mix, the
part it has static and the rest dynamic. The Futamura projections
are what follows when the program a specialiser takes is an
interpreter. A residual program has nothing to do with a residual
loop, which comes out of unrolling.

## path explosion

The growth in the number of distinct control-flow paths through a
region: two to the power of the number of conditions, times the trip
count for a loop. Any transformation that emits code per path pays
it. A version limit or a trace budget caps the duplication so the
code size stays bounded.

## predication

The instruction set mechanism if-conversion targets: an instruction
carries a predicate operand and the processor nullifies the
instruction when the predicate reads false. Predication is hardware;
if-conversion is the compiler transformation that uses it. An
instruction set with no predication still admits if-conversion
through a select or a conditional move, over a narrower set of arms.

## program counter

The index of the instruction a machine runs next. A branch is an
assignment to it. gozero's dispatch loop ranges over the statement
slice and never assigns to the index, so the compiled form holds no
program counter and no branch op.

## quickening

Rewriting a bytecode instruction in place at run time with a faster
variant of the same instruction, chosen from information only the
run reveals: an operand's type, or that an operand holds a constant.
The rewrite drops the checks the general instruction repeats, at the
cost of a larger instruction set and self-modifying bytecode.
Quickening is the mechanism; specialisation is the policy it
carries. An inline cache is one such policy, with the cached target
encoded in the quickened opcode.

## reflect bridge

The middle execution tier. A call whose signature has no shape-table
entry compiles to one `reflect.Value.Call`, while the calls around
it stay on the direct-call tier. The bridge costs reflection for
that one call only.

## reflect evaluator

The slowest and most general execution tier, and the reference
implementation the JIT tests check against. Every value travels as a
`reflect.Value`, and every call runs through `reflect.Value.Call`.
A program falls back to this tier as a whole when the JIT cannot
compile it.

## representability

Whether a literal fits the parameter it fills. The compiler checks
it against the parameter's `reflect.Type`, so `takesU32(-1)` and
`takesI8(300)` are compile errors and never execution ones. Go's
rule for an untyped constant is the same one.

## residual loop

The loop that runs the iterations unrolling could not cover.
Unrolling to a static bound needs one, because the trip count need
not divide by the unroll factor. The residual loop keeps the back
edge the unrolling set out to remove.

## selector link

The dotted path after a name, resolved against the name's static
type at compile time. `req.Header` reads a field and `req.Cookies()`
calls a method, both against `req`'s static type and with no binding
of their own. The link covers Go's method sets in full - value and
pointer receivers, methods promoted from embedded types - under Go's
addressability rule. An unknown selector is a compile error and
never an execution one.

## shape table

The table of function signatures, keyed by layout class, that the
direct-call tier supports. One entry covers every bound function
with the same class signature: one entry calls every function
taking two strings and returning a pointer and an error. A signature
outside the table sends that call to the reflect bridge.

## slot

The storage for one named value in a program, held as a field of the
frame. A name that one statement produces and one statement consumes
travels as a Go return value and needs no slot; the planner removes
such slots before the compiler lays out the frame.

## splice

The planner optimization that removes a slot: when the next statement
reads a statement's single result exactly once, the planner moves
the producing call into the consuming call's argument tree, and
the value travels as a closure return value.

## stack map

The `map[string]any` a host passes to `Exec`. Names a program never
binds resolve against the stack map at execution time; the runtime checks
their types on read, because the map's values carry no static
type. The stack map has no relation to the call stack.

## statement node

A node in the compiled closure tree that runs for effect and returns
no value, carrying the layout class `lNone`. A call whose results a
program discards compiles to one. A builtin shaped as a statement,
`clear` or `close`, needs a node of its own. The language defines
few of them, so `planInline` names its refusal for every shape it
has no node for: a statement-shaped node missing from the plan falls
through the `s.call == nil` check and the statement vanishes with no
error.

## straight-line code

A sequence of instructions with no branch into or out of the middle:
control enters at the top and leaves at the bottom. A gozero program
is straight-line by construction, because the dispatch loop walks
the statement slice in order and `planInline` refuses a return
before the last statement.

## superblock

A trace with one entry point and more than one exit. Trace selection
picks the blocks along the likely path and tail duplication copies
the tail from the first side entry onward, so no path enters the
middle. The single entry drops the bookkeeping code trace scheduling
needs, at the code size of the duplicated tail.

## supercompilation

Driving the tree of a program's possible computation histories and
building a residual program out of it, with a generalisation step to
keep the tree finite. Without the generalisation step the tree
enumerates every path, which for a loop with a runtime trip count
never ends.

## superinstruction

One interpreter instruction that does the work of a fixed sequence
of two or more instructions. A static superinstruction covers a
sequence a peephole pass finds in the program text. The dynamic
form, selective inlining, concatenates the handlers' native code at
run time. It saves the dispatch between the instructions and costs
one handler per sequence in the instruction set.

## tail duplication

Copying the code after a join once per path that reaches the join,
so each path runs its own copy and no path enters a copy's middle.
Tail duplication is the duplication half of superblock formation;
trace selection is the other half. The copies remove the join and
leave the control transfer in place: a condition that picks between
two paths still assigns to the program counter, now from a table of
copies. The copies are the cost, in code size.

## tail-call dispatch

An interpreter shape where each opcode handler is a function and
ends with a tail call to the next handler, so the compiler emits one
jump per handler and the handlers share no dispatch block. The shape
replaces a computed goto when a compiler stops duplicating the
dispatch tail back into each handler.

## threaded code

An interpreter representation where the program is a sequence of
handler addresses and not of opcodes a switch decodes. Each handler
jumps to the address the next slot holds, so dispatch costs an
indirect jump and no decode. Superinstruction and replication
measurements take it as the baseline.

## tier

One of the three execution paths a compiled call lands on: the
direct-call tier, the reflect bridge, or the reflect evaluator.
`Runtime.Supports` reports which calls of a program leave the
direct-call tier and why. Two of the three run on reflect, so a bare
reflect tier names neither of them.

## trace JIT

A JIT that records the instructions one execution ran as a linear
trace across branches and calls, compiles the trace as straight-line
code with a guard at every point the original control flow could
have left it, and bails out to the interpreter when a guard fails.
The trace carries only the path taken. The guards are the price. The
bailout keeps the result correct when the recorded path stops being
the path a run takes.

## trace scheduling

Selecting a linear path through the control-flow graph and
scheduling it as one region, with fix-up code at every entry into
and exit out of the middle to keep the off-trace paths correct. A
trace keeps more than one entry, which the fix-up code pays for.
Superblock formation removes the entries with tail duplication and
drops the fix-up code.

## type parameter

A name standing for a type, filled at the call site, which lets one
function body cover more than one type. Go has them. A gozero
program has none. The gap is what keeps a builtin out of the binding
form: a binding carries one `reflect.Type` signature and a builtin
is generic over its argument's type.

## unrolling

Replacing a loop with N copies of its body, so the body runs N times
per back edge. It trades N-1 back edges for N copies of the body in
code size. A trip count the compiler cannot pin needs a residual
loop after the copies, so unrolling alone never removes the jump.

## untyped constant

Go's literal with no type attached, taking its type from the context
that uses it and computing at arbitrary precision until it lands
somewhere typed. Go's `len` and `cap` of an array produce one, so
those two cases have no runtime function behind them. A gozero
literal takes its type from the first binding parameter it reaches,
falling back to its own width. The grammar carries no arithmetic
over constants.

## version limit

The cap on how many specialised versions of a block a compiler
compiles before it falls back to one generic version. The cap is
what keeps basic block versioning bounded. The lazy form at a small
cap removes most of the type tests at a code-size cost near zero.
The eager form with no cap more than doubles the code and runs
slower than the baseline it replaces.

## zero value

The value every Go type holds before assignment: `0`, `""`, `nil`,
or a struct of zero fields. gozero fills omitted trailing arguments
with the parameter's zero value, and the pool clears a pooled frame back
to zero before reuse so declarations start from it.
