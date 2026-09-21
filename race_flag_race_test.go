//go:build race

package gozero

// raceDetector reports whether the test binary was built with -race.
// The detector's sync.Pool drops one Put in four at random, so any
// pin that counts a pooled allocation measures the detector in that
// build rather than the tier.
const raceDetector = true
