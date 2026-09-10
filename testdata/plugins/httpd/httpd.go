// Package main is one plugin source with two loaders: the standard
// library's, after go build -buildmode=plugin, and gozero's, hot
// from this file. Both look up Handler and serve requests through
// the http.Handler it returns.
package main

import (
	"fmt"
	"net/http"
)

// Handler returns the plugin's http.Handler.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Plugin", "httpd")
		w.WriteHeader(200)
		fmt.Fprintf(w, "hello %s", r.URL.Path)
	})
}

func main() {}
