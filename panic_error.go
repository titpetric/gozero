package gozero

import (
	"fmt"
)

// PanicError is what a panic raised inside a bound function becomes. A
// binding is host code and a panic crossing back into a compiled
// program would unwind through the JIT's raw frame stores. The boundary
// turns it into an ordinary error at the point Compile returns the
// compiled program.
//
// Error reports the recovered value, and the stack of the goroutine that
// panicked stays on the struct for a caller that wants it.
type PanicError struct {
	Value any
	Stack []byte
}

// Error reports the recovered value.
func (e *PanicError) Error() string {
	return fmt.Sprintf("exec: binding panicked: %v", e.Value)
}
