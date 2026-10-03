package middleware

import "net/http"

// maxBodyBytes caps request bodies. Every JSON payload the API accepts is a
// few hundred bytes, so 1 MB is generous while keeping handlers that call
// io.ReadAll from buffering arbitrarily large uploads into memory.
const maxBodyBytes = 1 << 20

func (m *Middleware) LimitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		next.ServeHTTP(w, r)
	})
}
