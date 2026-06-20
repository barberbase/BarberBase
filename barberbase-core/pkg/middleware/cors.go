package middleware

import (
	"net/http"
	"os"
)

// CORS is a middleware that handles Cross-Origin Resource Sharing (CORS).
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		// Allowed origins check
		isAllowed := false
		if origin != "" {
			if origin == "https://barberbase.in" || origin == "https://www.barberbase.in" {
				isAllowed = true
			} else if os.Getenv("ENVIRONMENT") == "development" {
				if origin == "http://localhost:5173" || origin == "http://localhost:4173" {
					isAllowed = true
				}
			}
		}

		if isAllowed {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Session-Token, X-Push-Action-Token, X-Bhejna-Key")
			w.Header().Set("Access-Control-Expose-Headers", "X-Queue-Version")
			w.Header().Set("Vary", "Origin")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent) // 204
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}
