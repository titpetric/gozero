//go:build race

package gozero

// raceEnabled skips exact allocation assertions under the race
// detector, whose runtime instruments calls with allocations of its
// own; behaviour assertions still run.
const raceEnabled = true
