package auth

import (
	"context"
	"net/http"
	"strings"
)

// RequireStaffJWT returns a middleware that validates the Bearer token in the Authorization header.
func RequireStaffJWT(secret []byte, scopeKey any) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Context().Value(scopeKey) == nil {
				next.ServeHTTP(w, r)
				return
			}

			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				respondUnauthorized(w)
				return
			}

			parts := strings.Split(authHeader, " ")
			if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
				respondUnauthorized(w)
				return
			}

			tokenStr := parts[1]
			claims, err := ParseAndVerifyToken(tokenStr, secret)
			if err != nil {
				respondUnauthorized(w)
				return
			}

			if claims.Scope == "stream" {
				respondUnauthorized(w)
				return
			}

			// Inject values into the request context using the defined contextKeys
			ctx := r.Context()
			ctx = context.WithValue(ctx, CtxTenantID, claims.TenantID)
			ctx = context.WithValue(ctx, CtxLocationID, claims.LocationID)
			ctx = context.WithValue(ctx, CtxStaffMemberID, claims.StaffMemberID)
			ctx = context.WithValue(ctx, CtxRole, claims.Role)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func respondUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"code":"UNAUTHORIZED","message":"unauthorized"}`))
}

// TenantIDFromCtx retrieves CtxTenantID from context.
func TenantIDFromCtx(ctx context.Context) string {
	val, _ := ctx.Value(CtxTenantID).(string)
	return val
}

// LocationIDFromCtx retrieves CtxLocationID from context.
func LocationIDFromCtx(ctx context.Context) string {
	val, _ := ctx.Value(CtxLocationID).(string)
	return val
}

// StaffMemberIDFromCtx retrieves CtxStaffMemberID from context.
func StaffMemberIDFromCtx(ctx context.Context) string {
	val, _ := ctx.Value(CtxStaffMemberID).(string)
	return val
}

// RoleFromCtx retrieves CtxRole from context.
func RoleFromCtx(ctx context.Context) string {
	val, _ := ctx.Value(CtxRole).(string)
	return val
}
