package main

import "net/http"

// cabeceras adds the headers every response carries.
func cabeceras(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// This is a private instance: search engines must not index it.
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		h.ServeHTTP(w, r)
	})
}

// robots blocks every crawler, so none of them spends requests to CeX.
func robots(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("User-agent: *\nDisallow: /\n"))
}
