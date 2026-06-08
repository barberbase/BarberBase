package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
)

// RejectBodyTenantID is a middleware that rejects requests containing a top-level "tenant_id" key
// in the JSON request body, returning a 400 Bad Request error.
func RejectBodyTenantID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only check if there is a request body and the method is mutating (POST, PUT, PATCH, DELETE)
		if r.Body == nil || r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}

		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			// If we fail to read the body, pass to the next handler to handle standard error reporting
			next.ServeHTTP(w, r)
			return
		}

		// Re-wrap body immediately so it is always available
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

		if len(bodyBytes) > 0 {
			var obj map[string]interface{}
			// Try to unmarshal. If it fails, let the actual endpoint handler deal with invalid JSON formatting.
			if err := json.Unmarshal(bodyBytes, &obj); err == nil {
				if _, exists := obj["tenant_id"]; exists {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"code":"INVALID_REQUEST","message":"tenant_id must not be provided in request body"}`))
					return
				}
			}
		}

		next.ServeHTTP(w, r)
	})
}
