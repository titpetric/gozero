package gozero

import (
	"errors"
)

// Control flow travels the error return every statement already has,
// on both tiers. A statement that must leave a nested list raises a
// sentinel, the construct that owns the exit consumes it, and nothing
// else sees it; the nil-error hot path is untouched, because the
// comparison only runs when a statement returns non-nil.
//
// The channel is one per construct. errProgramReturn is the first:
// a return inside an if arm raises it, and the top of the program
// consumes it. Both tiers need this, because the reflect evaluator
// runs a nested statement list through the same runStmts the program
// does and the direct tier's nodes return nothing but an error.
//
// A binding cannot forge a signal: the values are unexported, so a
// bound func has no way to name one, and a signal that somehow left
// a program would surface as its message rather than as silence.
var errProgramReturn = errors.New("return outside a program")
