// Package greet is the smallest plugin shape: a type, a method, a
// constructor, an exported entry point and an init hook.
package greet

import "hostlog"

type Greeter struct {
	Prefix string
}

func (g Greeter) Greet(name string) string {
	return g.Prefix + name
}

func NewGreeter(prefix string) Greeter {
	return Greeter{Prefix: prefix}
}

func Hello(name string) string {
	g := NewGreeter("hello ")
	return g.Greet(name)
}

func init() {
	hostlog.Loaded("greet")
}
