package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

type testContextKey string
const testScopeKey testContextKey = "test.StaffJWT.Scopes"

func TestRequireStaffJWT(t *testing.T) {
	secret := []byte("test-jwt-secret-key-that-is-long-enough")
	// Generate a valid token for test
	validToken, _, err := GenerateAccessAndRefreshTokens(secret, "tenant-123", "location-456", "staff-789", "owner")
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}

	streamToken, err := GenerateStreamToken(secret, "tenant-123", "location-456", "staff-789", "owner")
	if err != nil {
		t.Fatalf("failed to generate stream token: %v", err)
	}

	tests := []struct {
		name           string
		withMarker     bool
		authHeader     string
		expectedStatus int
		expectContext  bool
	}{
		{
			name:           "Request WITHOUT marker and WITHOUT Authorization header",
			withMarker:     false,
			authHeader:     "",
			expectedStatus: http.StatusOK,
			expectContext:  false,
		},
		{
			name:           "Request WITH marker but no Authorization header",
			withMarker:     true,
			authHeader:     "",
			expectedStatus: http.StatusUnauthorized,
			expectContext:  false,
		},
		{
			name:           "Request WITH marker and valid token",
			withMarker:     true,
			authHeader:     "Bearer " + validToken,
			expectedStatus: http.StatusOK,
			expectContext:  true,
		},
		{
			name:           "Request WITH marker and valid stream-scoped token",
			withMarker:     true,
			authHeader:     "Bearer " + streamToken,
			expectedStatus: http.StatusUnauthorized,
			expectContext:  false,
		},
		{
			name:           "Request WITH marker and invalid token",
			withMarker:     true,
			authHeader:     "Bearer garbage-token-12345",
			expectedStatus: http.StatusUnauthorized,
			expectContext:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Create a sentinel handler to verify "next" execution and context population
			sentinelCalled := false
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sentinelCalled = true
				if tc.expectContext {
					role := RoleFromCtx(r.Context())
					tenantID := TenantIDFromCtx(r.Context())
					if role != "owner" {
						t.Errorf("expected role 'owner', got '%s'", role)
					}
					if tenantID != "tenant-123" {
						t.Errorf("expected tenantID 'tenant-123', got '%s'", tenantID)
					}
				}
				w.WriteHeader(http.StatusOK)
			})

			middleware := RequireStaffJWT(secret, testScopeKey)
			handler := middleware(next)

			req := httptest.NewRequest("GET", "/test", nil)
			if tc.withMarker {
				ctx := context.WithValue(req.Context(), testScopeKey, []string{})
				req = req.WithContext(ctx)
			}
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tc.expectedStatus {
				t.Errorf("expected status %d, got %d", tc.expectedStatus, rec.Code)
			}

			if tc.expectedStatus == http.StatusOK && !sentinelCalled {
				t.Error("expected next handler to be called, but it was not")
			}
			if tc.expectedStatus != http.StatusOK && sentinelCalled {
				t.Error("expected next handler NOT to be called, but it was")
			}
		})
	}
}
