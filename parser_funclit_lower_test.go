package gozero

import (
	"testing"
)

// TestFuncLitLowering pins the shape the ast lowers into: the
// structures are the byte-level parser's own, so the compiler cannot
// tell which front end produced them.
func TestFuncLitLowering(t *testing.T) {
	prog, err := (&Parser{}).Parse(`f(func(a, b) { g(a, 0x10, 'x'); c <- b; return h() });`)
	if err != nil {
		t.Fatal(err)
	}
	fl := prog.stmts[0].call.args[0].fn
	if fl == nil {
		t.Fatal("the argument did not lower to a func literal")
	}
	if len(fl.params) != 2 || fl.params[0] != "a" || fl.params[1] != "b" {
		t.Fatalf("params lowered to %v", fl.params)
	}
	if len(fl.body.stmts) != 3 {
		t.Fatalf("body lowered to %d statements", len(fl.body.stmts))
	}
	call := fl.body.stmts[0].call
	if call == nil || call.path[0] != "g" || len(call.args) != 3 {
		t.Fatalf("statement 0 lowered to %+v", fl.body.stmts[0])
	}
	if a := call.args[1]; a.kind != argInt || a.i != 16 {
		t.Fatalf("0x10 lowered to %+v", a)
	}
	if a := call.args[2]; a.kind != argString || a.str != "x" {
		t.Fatalf("'x' lowered to %+v", a)
	}
	if send := fl.body.stmts[1]; send.sendCh == nil || send.sendCh[0] != "c" || send.sendVal.kind != argVar {
		t.Fatalf("statement 1 lowered to %+v", send)
	}
	if ret := fl.body.stmts[2]; !ret.ret || ret.call == nil || ret.call.path[0] != "h" {
		t.Fatalf("statement 2 lowered to %+v", ret)
	}
}
