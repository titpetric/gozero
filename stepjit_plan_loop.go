package gozero

import (
	"fmt"
	"reflect"
)

// Loop planning: the bodies of range, condition and three-clause
// loops plan 1:1 (no splicing crosses a loop boundary in either
// direction), their reads count like any other reads, and every slot
// a loop writes counts conservatively at two, which is what turns
// the write-once aliasing rule off for loop-carried names.

// planBody plans a loop's body 1:1: every body statement keeps its
// slot, because a value produced in one iteration and read in the
// next has to live somewhere the iterations share.
func planBody(body []vmStmt) ([]plannedStmt, error) {
	planned := make([]plannedStmt, 0, len(body))
	for i := range body {
		s := &body[i]
		out := -1
		if len(s.out) > 0 {
			out = s.out[0]
		}
		switch {
		case s.brk || s.cont:
			planned = append(planned, plannedStmt{brk: s.brk, cont: s.cont, out: -1})
		case s.rng != nil:
			sub, err := planRange(s.rng)
			if err != nil {
				return nil, err
			}
			planned = append(planned, plannedStmt{rng: sub, out: -1})
		case s.fors != nil:
			sub, err := planFor(s.fors)
			if err != nil {
				return nil, err
			}
			planned = append(planned, plannedStmt{fors: sub, out: -1})
		case s.assign != nil:
			planned = append(planned, plannedStmt{assign: s.assign, out: out})
		case s.fieldSet != nil:
			planned = append(planned, plannedStmt{fieldSet: s.fieldSet, out: -1})
		case s.recv != nil:
			planned = append(planned, plannedStmt{recv: s.recv, out: out})
		case s.send != nil:
			planned = append(planned, plannedStmt{send: s.send, out: -1})
		case s.lit.IsValid():
			planned = append(planned, plannedStmt{lit: s.lit, out: out})
		case s.ret || s.retArg != nil || s.call == nil:
			return nil, fmt.Errorf("a statement inside a loop body is not in the table")
		default:
			planned = append(planned, plannedStmt{call: s.call, out: out})
		}
	}
	return planned, nil
}

// planRange pairs a range loop with its planned body.
func planRange(r *vmRange) (*plannedRange, error) {
	body, err := planBody(r.body)
	if err != nil {
		return nil, err
	}
	return &plannedRange{src: r, body: body}, nil
}

// planFor pairs a condition or three-clause loop with its planned
// body.
func planFor(f *vmFor) (*plannedFor, error) {
	body, err := planBody(f.body)
	if err != nil {
		return nil, err
	}
	return &plannedFor{src: f, body: body}, nil
}

// countBodyReads tallies every read a planned loop body makes.
func countBodyReads(reads map[int]int, body []plannedStmt) {
	for _, s := range body {
		if s.call != nil {
			countReads(reads, s.call)
		}
		if s.assign != nil {
			countArgReads(reads, s.assign)
		}
		if s.fieldSet != nil {
			reads[s.fieldSet.base]++
			countArgReads(reads, s.fieldSet.val)
		}
		if s.recv != nil {
			countArgReads(reads, s.recv.ch)
		}
		if s.send != nil {
			countArgReads(reads, s.send.ch)
			countArgReads(reads, s.send.val)
		}
		if s.rng != nil {
			countRangeReads(reads, s.rng)
		}
		if s.fors != nil {
			countForReads(reads, s.fors)
		}
	}
}

// countRangeReads tallies the reads a range loop makes: the ranged
// expression once, and every read the body statements make.
func countRangeReads(reads map[int]int, r *plannedRange) {
	countArgReads(reads, r.src.over)
	countBodyReads(reads, r.body)
}

// countForReads tallies the reads a condition or three-clause loop
// makes: the header's values, and every read the body makes.
func countForReads(reads map[int]int, f *plannedFor) {
	if f.src.cond != nil {
		countArgReads(reads, f.src.cond)
	}
	if f.src.initVal != nil {
		countArgReads(reads, f.src.initVal)
	}
	if f.src.cmp != nil {
		countArgReads(reads, f.src.cmp.x)
		countArgReads(reads, f.src.cmp.y)
	}
	countBodyReads(reads, f.body)
}

// accountBody marks a loop body's written slots live and counts every
// write conservatively at two: a slot assigned each iteration is
// written more than once by definition, so the write-once rule that
// lets an interface argument alias the frame turns off for it, and
// the argument copies instead, which is what the Go compiler does at
// the same call site.
func accountBody(body []plannedStmt, live map[int]bool, writes map[int]int, p *vmProgram) {
	for _, s := range body {
		if s.rng != nil {
			accountRange(s.rng, live, writes, p)
			continue
		}
		if s.fors != nil {
			accountFor(s.fors, live, writes, p)
			continue
		}
		if s.fieldSet != nil {
			live[s.fieldSet.base] = true
			if t := p.slotTypes[s.fieldSet.base]; t != nil && t.Kind() != reflect.Pointer {
				writes[s.fieldSet.base] += 2
			}
			continue
		}
		if s.out >= 0 {
			live[s.out] = true
			writes[s.out] += 2
		}
	}
}

// accountRange marks a range loop's slots live and its writes
// conservative: the key and value are assigned per iteration.
func accountRange(r *plannedRange, live map[int]bool, writes map[int]int, p *vmProgram) {
	if k := r.src.keySlot; k >= 0 {
		live[k] = true
		writes[k] += 2
	}
	if v := r.src.valSlot; v >= 0 {
		live[v] = true
		writes[v] += 2
	}
	accountBody(r.body, live, writes, p)
}

// accountFor does the same for a condition or three-clause loop: the
// loop variable is written by the init once and by the post clause
// per iteration, so it counts at two like every loop-carried slot.
func accountFor(f *plannedFor, live map[int]bool, writes map[int]int, p *vmProgram) {
	if s := f.src.initSlot; s >= 0 {
		live[s] = true
		writes[s] += 2
	}
	accountBody(f.body, live, writes, p)
}
