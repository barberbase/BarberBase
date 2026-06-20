package middleware

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestCORSAllowedOrigin(t *testing.T) {
	reachedNext := false
	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reachedNext = true
		w.WriteHeader(http.StatusOK)
	})

	handler := CORS(nextHandler)

	req := httptest.NewRequest(http.MethodOptions, "/v1/test", nil)
	req.Header.Set("Origin", "https://barberbase.in")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("Expected status code %d, got %d", http.StatusNoContent, rec.Code)
	}

	allowOrigin := rec.Header().Get("Access-Control-Allow-Origin")
	if allowOrigin != "https://barberbase.in" {
		t.Errorf("Expected Access-Control-Allow-Origin to be 'https://barberbase.in', got '%s'", allowOrigin)
	}

	if reachedNext {
		t.Error("Expected short-circuit on OPTIONS request, but next handler was called")
	}
}

func TestCORSBlocksUnknownOrigin(t *testing.T) {
	reachedNext := false
	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reachedNext = true
		w.WriteHeader(http.StatusOK)
	})

	handler := CORS(nextHandler)

	req := httptest.NewRequest(http.MethodOptions, "/v1/test", nil)
	req.Header.Set("Origin", "https://evil.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	allowOrigin := rec.Header().Get("Access-Control-Allow-Origin")
	if allowOrigin != "" {
		t.Errorf("Expected no Access-Control-Allow-Origin header, got '%s'", allowOrigin)
	}

	if !reachedNext {
		t.Error("Expected next handler to be called for unknown origin")
	}
}

func TestCORSLocalhostOnlyInDev(t *testing.T) {
	runTest := func(env string, origin string, expectedAllowed bool) {
		oldEnv := os.Getenv("ENVIRONMENT")
		os.Setenv("ENVIRONMENT", env)
		defer os.Setenv("ENVIRONMENT", oldEnv)

		reachedNext := false
		nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reachedNext = true
			w.WriteHeader(http.StatusOK)
		})

		handler := CORS(nextHandler)

		req := httptest.NewRequest(http.MethodOptions, "/v1/test", nil)
		req.Header.Set("Origin", origin)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		allowOrigin := rec.Header().Get("Access-Control-Allow-Origin")
		if expectedAllowed {
			if rec.Code != http.StatusNoContent {
				t.Errorf("[%s - %s] Expected status code %d, got %d", env, origin, http.StatusNoContent, rec.Code)
			}
			if allowOrigin != origin {
				t.Errorf("[%s - %s] Expected Access-Control-Allow-Origin to be '%s', got '%s'", env, origin, origin, allowOrigin)
			}
			if reachedNext {
				t.Errorf("[%s - %s] Expected short-circuit, but next handler was called", env, origin)
			}
		} else {
			if allowOrigin != "" {
				t.Errorf("[%s - %s] Expected no Access-Control-Allow-Origin header, got '%s'", env, origin, allowOrigin)
			}
			if !reachedNext {
				t.Errorf("[%s - %s] Expected next handler to be called", env, origin)
			}
		}
	}

	runTest("development", "http://localhost:5173", true)
	runTest("development", "http://localhost:4173", true)
	runTest("production", "http://localhost:5173", false)
	runTest("production", "http://localhost:4173", false)
}

func TestCORSPassesNonBrowserRequests(t *testing.T) {
	reachedNext := false
	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reachedNext = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	handler := CORS(nextHandler)

	req := httptest.NewRequest(http.MethodGet, "/v1/webhook", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, rec.Code)
	}

	if !reachedNext {
		t.Error("Expected request with no Origin to reach next handler")
	}

	allowOrigin := rec.Header().Get("Access-Control-Allow-Origin")
	if allowOrigin != "" {
		t.Errorf("Expected no Access-Control-Allow-Origin header for request without Origin, got '%s'", allowOrigin)
	}
}
