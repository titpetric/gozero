package gozero

// BindOption configures one binding at Bind or BindScope.
type BindOption func(*binding)

// NonRetaining is the host's promise that the bound function neither
// stores its arguments beyond the call nor returns them. The JIT then
// recycles the memory behind the call's arguments - variadic pack
// slices, boxed values, composite literal blocks - through pools
// instead of allocating fresh per run. The compiler cannot check the
// promise: a binding that breaks it will later observe its retained
// argument overwritten by an unrelated run. fmt.Sprintf, an encoder's
// Encode, and an assert helper are non-retaining; anything that
// appends an argument to a captured slice or returns one is not.
func NonRetaining() BindOption {
	return func(b *binding) { b.nonRetaining = true }
}
