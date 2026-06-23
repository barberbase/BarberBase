package auth

import (
	"testing"
	"time"
)

func TestGenerateStreamToken(t *testing.T) {
	secret := []byte("test-jwt-secret-key-that-is-long-enough")
	tenantID := "tenant-123"
	locationID := "location-456"
	staffID := "staff-789"
	role := "barber"

	tokenStr, err := GenerateStreamToken(secret, tenantID, locationID, staffID, role)
	if err != nil {
		t.Fatalf("failed to generate stream token: %v", err)
	}

	claims, err := ParseAndVerifyToken(tokenStr, secret)
	if err != nil {
		t.Fatalf("failed to parse and verify stream token: %v", err)
	}

	if claims.TenantID != tenantID {
		t.Errorf("expected tenantID %q, got %q", tenantID, claims.TenantID)
	}
	if claims.LocationID != locationID {
		t.Errorf("expected locationID %q, got %q", locationID, claims.LocationID)
	}
	if claims.StaffMemberID != staffID {
		t.Errorf("expected staffMemberID %q, got %q", staffID, claims.StaffMemberID)
	}
	if claims.Role != role {
		t.Errorf("expected role %q, got %q", role, claims.Role)
	}
	if claims.Scope != "stream" {
		t.Errorf("expected scope %q, got %q", "stream", claims.Scope)
	}

	// Verify expiry (approx. 12 hours)
	expiresAt := claims.ExpiresAt.Time
	timeDiff := expiresAt.Sub(time.Now())
	if timeDiff < 11*time.Hour || timeDiff > 13*time.Hour {
		t.Errorf("expected token expiry around 12h, got time difference: %v", timeDiff)
	}
}
