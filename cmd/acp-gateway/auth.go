package main

import (
	"net/http"
	"strings"
)

// authMiddleware requires a Bearer token matching `expected`. Routes listed
// in `exempt` (full path match, e.g. "/healthz") skip the check.
func authMiddleware(expected string, exempt map[string]bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if expected == "" || exempt[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		got := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(got, prefix) || got[len(prefix):] != expected {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
