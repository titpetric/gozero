// Middleware wraps any host handler, rejecting requests without a
// bearer token before they reach it.
package auth

import (
	"net/http"
	"hostlog"
)

func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		_, err := ParseHeader(header)
		if err != nil {
			http.Error(w, err.Error(), 401)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func init() {
	hostlog.Loaded("auth/middleware")
}
