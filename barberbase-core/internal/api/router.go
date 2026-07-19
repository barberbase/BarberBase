package api

import (
	"context"
	"net/http"

	"barberbase-core/internal/auth"

	"github.com/go-chi/chi/v5"
)

// NewRouter mounts every /v1 route — generated handlers plus manual routes —
// exactly as production serves them. main.go and HTTP tests share this, so a
// route that is dead in tests is dead in prod and vice versa (the Phase 1.5
// check-in route shipped unreachable precisely because tests bypassed HTTP).
func NewRouter(s *Server, jwtSecret []byte) chi.Router {
	r := chi.NewRouter()

	apiHandler := HandlerWithOptions(s, ChiServerOptions{
		Middlewares: []MiddlewareFunc{
			auth.RequireStaffJWT(jwtSecret, StaffJWTScopes),
		},
	})
	r.Route("/v1", func(r chi.Router) {
		r.With(s.PlatformAdminKeyMiddleware).Post("/admin/setup", s.ProvisionTenant)
		r.Mount("/", apiHandler)
	})
	s.RegisterManualRoutes(r, jwtSecret)
	return r
}

// markStaffJWT stamps the StaffJWT scope key into the context. The generated
// wrapper does this per-operation for spec routes; manual chi routes must do
// it themselves because RequireStaffJWT only enforces when the key is present
// — without it the route would be reachable unauthenticated.
func markStaffJWT(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := context.WithValue(req.Context(), StaffJWTScopes, []string{})
		next.ServeHTTP(w, req.WithContext(ctx))
	})
}
