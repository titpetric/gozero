//go:build !race

package gozero

// raceDetector reports whether the test binary was built with -race.
// See the -race half of this pair for why an allocation pin reads it.
const raceDetector = false
