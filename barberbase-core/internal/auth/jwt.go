package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type StaffClaims struct {
	jwt.RegisteredClaims
	TenantID      string `json:"tenant_id"`
	LocationID    string `json:"location_id"`
	StaffMemberID string `json:"staff_member_id"`
	Role          string `json:"role"`
	Scope         string `json:"scope,omitempty"`
}

type RefreshClaims struct {
	jwt.RegisteredClaims
	// Subject = staff_member_id
}

type contextKey string

const (
	CtxTenantID      contextKey = "tenant_id"
	CtxLocationID    contextKey = "location_id"
	CtxStaffMemberID contextKey = "staff_member_id"
	CtxRole          contextKey = "role"
)

// GenerateAccessAndRefreshTokens signs and generates Access and Refresh tokens.
func GenerateAccessAndRefreshTokens(secret []byte, tenantID, locationID, staffMemberID, role string) (accessToken string, refreshToken string, err error) {
	now := time.Now()

	accessClaims := StaffClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        uuid.New().String(),
		},
		TenantID:      tenantID,
		LocationID:    locationID,
		StaffMemberID: staffMemberID,
		Role:          role,
		Scope:         "",
	}

	refreshClaims := RefreshClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(30 * 24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(now),
			Subject:   staffMemberID,
		},
	}

	accessToken, err = jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims).SignedString(secret)
	if err != nil {
		return "", "", fmt.Errorf("failed to sign access token: %w", err)
	}

	refreshToken, err = jwt.NewWithClaims(jwt.SigningMethodHS256, refreshClaims).SignedString(secret)
	if err != nil {
		return "", "", fmt.Errorf("failed to sign refresh token: %w", err)
	}

	return accessToken, refreshToken, nil
}

// GenerateStreamToken signs and generates a long-lived StaffClaims token for SSE stream.
func GenerateStreamToken(secret []byte, tenantID, locationID, staffMemberID, role string) (string, error) {
	now := time.Now()

	claims := StaffClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(12 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        uuid.New().String(),
		},
		TenantID:      tenantID,
		LocationID:    locationID,
		StaffMemberID: staffMemberID,
		Role:          role,
		Scope:         "stream",
	}

	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
	if err != nil {
		return "", fmt.Errorf("failed to sign stream token: %w", err)
	}

	return token, nil
}

// ParseAndVerifyToken validates an Access JWT using HS256 and the provided secret.
func ParseAndVerifyToken(tokenStr string, secret []byte) (*StaffClaims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &StaffClaims{}, func(token *jwt.Token) (interface{}, error) {
		// Verify signature method is HMAC (HS256)
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		// Explicitly validate alg is HS256 to reject alg: none or asymmetric algs
		if alg, ok := token.Header["alg"].(string); !ok || alg != "HS256" {
			return nil, fmt.Errorf("unexpected signing algorithm: %v", token.Header["alg"])
		}
		return secret, nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*StaffClaims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid or expired token claims")
	}

	return claims, nil
}

// ParseAndVerifyRefreshToken validates a Refresh JWT using HS256 and the provided secret.
func ParseAndVerifyRefreshToken(tokenStr string, secret []byte) (*RefreshClaims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &RefreshClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		if alg, ok := token.Header["alg"].(string); !ok || alg != "HS256" {
			return nil, fmt.Errorf("unexpected signing algorithm: %v", token.Header["alg"])
		}
		return secret, nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*RefreshClaims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid or expired refresh token claims")
	}

	return claims, nil
}
