package gozero

import (
	"fmt"
	"reflect"
)

// The structural plan for programs with control flow, defers or
// error-binding calls: statements keep their nesting and nothing
// splices. The straight-line plan stays in stepjit_plan.go.

// hasDeferOrBindErr reports the statement forms the structural plan
// also owns: a defer, or a call binding its trailing error.
func hasDeferOrBindErr(stmts []vmStmt) bool {
	for i := range stmts {
		s := &stmts[i]
		if s.deferCall != nil {
			return true
		}
		if s.call != nil && s.call.bindErr {
			return true
		}
	}
	return false
}

// hasFlow reports control flow anywhere in a statement list.
func hasFlow(stmts []vmStmt) bool {
	for i := range stmts {
		s := &stmts[i]
		if s.ifs != nil || s.loop != nil || s.rng != nil || s.brk || s.cont || s.init != nil {
			return true
		}
	}
	return false
}

// planFlow is the structural plan: statements keep their nesting,
// nothing splices, and every slot written inside a loop counts as
// rewritten so the write-once aliasing never fires for it.
func planFlow(p *vmProgram) (*jitPlan, error) {
	plan := &jitPlan{
		live:    map[int]bool{},
		writes:  map[int]int{},
		splices: map[*vmArg]*vmCall{},
		retSlot: -1,
	}
	stmts := make([]plannedStmt, 0, len(p.stmts))
	for i := range p.stmts {
		ps, err := planStmt(&p.stmts[i])
		if err != nil {
			return nil, err
		}
		stmts = append(stmts, ps)
	}
	plan.stmts = stmts
	for _, in := range p.inits {
		plan.live[in.slot] = true
		plan.writes[in.slot]++
	}
	reads := map[int]int{}
	if err := flowCounters(p, p.stmts, false, reads, plan); err != nil {
		return nil, err
	}
	// A trailing "return name" keeps the typed slot path even with
	// flow present: the box costs an allocation the common shape
	// need not pay. Everything else returns through the boxed
	// channel.
	body := p.stmts
	if n := len(stmts); n > 0 {
		if f := stmts[n-1].flow; f != nil && f.retArg != nil && f.retArg.kind == vaSlot {
			plan.retSlot = f.retArg.slot
			plan.live[plan.retSlot] = true
			plan.stmts = stmts[:n-1]
			body = p.stmts[:len(p.stmts)-1]
		}
	}
	plan.needsRetAny = flowReturns(body)
	plan.needsDefers = flowDefers(p.stmts)
	return plan, nil
}

// flowDefers reports a deferred call anywhere in the tree.
func flowDefers(stmts []vmStmt) bool {
	for i := range stmts {
		s := &stmts[i]
		if s.deferCall != nil {
			return true
		}
		if s.ifs != nil {
			if flowDefers(s.ifs.then.stmts) {
				return true
			}
			if s.ifs.els != nil && flowDefers(s.ifs.els.stmts) {
				return true
			}
		}
		if s.loop != nil && flowDefers(s.loop.body.stmts) {
			return true
		}
		if s.rng != nil && flowDefers(s.rng.body.stmts) {
			return true
		}
	}
	return false
}

// planStmt converts one statement without splicing. Flow statements
// and returns travel whole for flowNode.
func planStmt(s *vmStmt) (plannedStmt, error) {
	if s.deferCall != nil {
		if s.deferCall.script != nil {
			// A deferred script call re-enters through runAll, which
			// carries it whole.
			return plannedStmt{flow: s, out: -1}, nil
		}
		return plannedStmt{flow: s, out: -1}, nil
	}
	if s.retList != nil {
		return plannedStmt{flow: s, out: -1}, nil
	}
	if s.ifs != nil || s.loop != nil || s.rng != nil || s.brk || s.cont || s.init != nil ||
		s.retArg != nil || (s.ret && s.call == nil && !s.lit.IsValid() && s.assign == nil) {
		return plannedStmt{flow: s, out: -1}, nil
	}
	out := -1
	if len(s.out) > 0 {
		out = s.out[0]
	}
	switch {
	case s.assign != nil:
		return plannedStmt{assign: s.assign, out: out}, nil
	case s.fieldSet != nil:
		return plannedStmt{fieldSet: s.fieldSet, out: -1}, nil
	case s.recv != nil:
		return plannedStmt{recv: s.recv, out: out}, nil
	case s.send != nil:
		return plannedStmt{send: s.send, out: -1}, nil
	case s.lit.IsValid():
		return plannedStmt{lit: s.lit, out: out}, nil
	case s.call != nil:
		if s.call.bindErr {
			return plannedStmt{flow: s, out: -1}, nil
		}
		if s.ret && s.call.nres > 0 && out < 0 {
			return plannedStmt{}, fmt.Errorf("a returned call inside control flow is not in the table yet")
		}
		return plannedStmt{call: s.call, out: out, ret: s.ret}, nil
	}
	return plannedStmt{}, fmt.Errorf("this statement is not in the table")
}

// flowCounters walks the whole tree filling reads, writes and live.
// Inside a loop every write counts twice, which is what turns off
// the single-write interface aliasing for loop-carried names.
func flowCounters(p *vmProgram, stmts []vmStmt, inLoop bool, reads map[int]int, plan *jitPlan) error {
	bump := 1
	if inLoop {
		bump = 2
	}
	mark := func(slot int) {
		if slot >= 0 {
			plan.writes[slot] += bump
			plan.live[slot] = true
		}
	}
	for i := range stmts {
		s := &stmts[i]
		for _, o := range s.out {
			mark(o)
		}
		if s.init != nil {
			mark(s.init.slot)
		}
		if s.assign != nil {
			countArgReads(reads, s.assign)
		}
		if s.retArg != nil {
			countArgReads(reads, s.retArg)
		}
		for _, ra := range s.retList {
			countArgReads(reads, ra)
		}
		if s.call != nil {
			countReads(reads, s.call)
		}
		if s.fieldSet != nil {
			reads[s.fieldSet.base]++
			plan.live[s.fieldSet.base] = true
			if t := p.slotTypes[s.fieldSet.base]; t != nil && t.Kind() != reflect.Pointer {
				plan.writes[s.fieldSet.base] += bump
			}
			countArgReads(reads, s.fieldSet.val)
		}
		if s.recv != nil {
			countArgReads(reads, s.recv.ch)
		}
		if s.send != nil {
			countArgReads(reads, s.send.ch)
			countArgReads(reads, s.send.val)
		}
		if s.deferCall != nil {
			countReads(reads, s.deferCall)
		}
		if s.ifs != nil {
			countArgReads(reads, s.ifs.cond)
			if err := flowCounters(p, s.ifs.then.stmts, inLoop, reads, plan); err != nil {
				return err
			}
			if s.ifs.els != nil {
				if err := flowCounters(p, s.ifs.els.stmts, inLoop, reads, plan); err != nil {
					return err
				}
			}
		}
		if s.loop != nil {
			if err := flowCounters(p, s.loop.init, inLoop, reads, plan); err != nil {
				return err
			}
			if s.loop.cond != nil {
				countArgReads(reads, s.loop.cond)
			}
			if err := flowCounters(p, s.loop.post, true, reads, plan); err != nil {
				return err
			}
			if err := flowCounters(p, s.loop.body.stmts, true, reads, plan); err != nil {
				return err
			}
		}
		if s.rng != nil {
			countArgReads(reads, s.rng.over)
			if s.rng.keySlot >= 0 {
				plan.writes[s.rng.keySlot] += 2
				plan.live[s.rng.keySlot] = true
			}
			if s.rng.valSlot >= 0 {
				plan.writes[s.rng.valSlot] += 2
				plan.live[s.rng.valSlot] = true
			}
			if err := flowCounters(p, s.rng.body.stmts, true, reads, plan); err != nil {
				return err
			}
		}
	}
	return nil
}

// flowReturns reports a return anywhere in the tree, the trailing
// straight-line form included: with flow present, every return goes
// through the boxed channel so they all agree on the path out.
func flowReturns(stmts []vmStmt) bool {
	for i := range stmts {
		s := &stmts[i]
		if (s.ret || s.retArg != nil) && s.retList == nil {
			return true
		}
		if s.ifs != nil {
			if flowReturns(s.ifs.then.stmts) {
				return true
			}
			if s.ifs.els != nil && flowReturns(s.ifs.els.stmts) {
				return true
			}
		}
		if s.loop != nil {
			if flowReturns(s.loop.body.stmts) {
				return true
			}
		}
		if s.rng != nil && flowReturns(s.rng.body.stmts) {
			return true
		}
	}
	return false
}
