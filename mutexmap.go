package gozero

import (
	"sync"
)

// MutexMap is a map[string]int behind a mutex: shared state offered
// to programs as a binding, the host-type alternative to moving
// values through a channel. Its methods land on the direct-call
// tier; docs/concurrency.md holds the measured comparison.
type MutexMap struct {
	mu sync.Mutex
	m  map[string]int
}

// NewMutexMap returns an empty MutexMap.
func NewMutexMap() *MutexMap {
	return &MutexMap{m: map[string]int{}}
}

// Set writes a key under the lock.
func (m *MutexMap) Set(k string, v int) {
	m.mu.Lock()
	m.m[k] = v
	m.mu.Unlock()
}

// Get reads a key under the lock; a missing key is zero.
func (m *MutexMap) Get(k string) int {
	m.mu.Lock()
	v := m.m[k]
	m.mu.Unlock()
	return v
}
