package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"barberbase-core/internal/auth"

	"github.com/google/uuid"
)

// Role gates fire before any DB access, so a zero-value Server is enough.
func settingsRequest(t *testing.T, role, body string) *httptest.ResponseRecorder {
	t.Helper()
	s := &Server{}
	req := httptest.NewRequest(http.MethodPatch, "/v1/admin/locations/x/settings", strings.NewReader(body))
	ctx := context.WithValue(req.Context(), auth.CtxRole, role)
	ctx = context.WithValue(ctx, auth.CtxTenantID, uuid.Nil.String())
	rec := httptest.NewRecorder()
	s.UpdateLocationSettings(rec, req.WithContext(ctx), UUIDv7(uuid.Nil))
	return rec
}

func TestUpdateLocationSettingsRoleGates(t *testing.T) {
	cases := []struct {
		name     string
		role     string
		body     string
		wantCode int
	}{
		{"barber rejected entirely", "barber", `{"geolocation_assist":true}`, http.StatusForbidden},
		{"manager cannot set routing mode", "manager", `{"queue_routing_mode":"hybrid"}`, http.StatusForbidden},
		{"manager mixed body rejected whole", "manager", `{"queue_routing_mode":"hybrid","arrival_radius_metres":50}`, http.StatusForbidden},
		{"radius below 20 rejected", "owner", `{"arrival_radius_metres":10}`, http.StatusBadRequest},
		{"radius above 500 rejected", "owner", `{"arrival_radius_metres":900}`, http.StatusBadRequest},
		{"lone latitude rejected", "owner", `{"gps_latitude":12.97}`, http.StatusBadRequest},
		{"bad routing mode rejected", "owner", `{"queue_routing_mode":"round_robin"}`, http.StatusBadRequest},
		{"out-of-range coordinates rejected", "owner", `{"gps_latitude":99.0,"gps_longitude":77.6}`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := settingsRequest(t, tc.role, tc.body)
			if rec.Code != tc.wantCode {
				t.Fatalf("role=%s body=%s: got %d, want %d (body: %s)", tc.role, tc.body, rec.Code, tc.wantCode, rec.Body.String())
			}
			var resp map[string]string
			_ = json.Unmarshal(rec.Body.Bytes(), &resp)
			if tc.wantCode == http.StatusForbidden && resp["code"] != "INSUFFICIENT_ROLE" {
				t.Fatalf("expected INSUFFICIENT_ROLE, got %q", resp["code"])
			}
		})
	}
}

// Barber must not even read shop-internal config — GET is gated like PATCH.
func TestGetLocationSettingsBarberForbidden(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/locations/x/settings", nil)
	ctx := context.WithValue(req.Context(), auth.CtxRole, "barber")
	ctx = context.WithValue(ctx, auth.CtxTenantID, uuid.Nil.String())
	rec := httptest.NewRecorder()
	s.GetLocationSettings(rec, req.WithContext(ctx), UUIDv7(uuid.Nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("barber GET settings: got %d, want 403", rec.Code)
	}
	var resp map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["code"] != "INSUFFICIENT_ROLE" {
		t.Fatalf("expected INSUFFICIENT_ROLE, got %q", resp["code"])
	}
}

// DB-backed: owner writes routing mode, manager writes geofence, defaults verified.
func TestLocationSettingsReadWrite(t *testing.T) {
	s, pool, tenantID, locationID, _, _ := setupTestServer(t)
	defer pool.Close()
	ctx := context.Background()

	do := func(role, method, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/v1/admin/locations/x/settings", strings.NewReader(body))
		c := context.WithValue(req.Context(), auth.CtxRole, role)
		c = context.WithValue(c, auth.CtxTenantID, tenantID.String())
		rec := httptest.NewRecorder()
		if method == http.MethodGet {
			s.GetLocationSettings(rec, req.WithContext(c), UUIDv7(locationID))
		} else {
			s.UpdateLocationSettings(rec, req.WithContext(c), UUIDv7(locationID))
		}
		return rec
	}

	// Deliverable 4: new location defaults to pooled with no settings visit.
	rec := do("owner", http.MethodGet, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET settings: got %d (%s)", rec.Code, rec.Body.String())
	}
	var got LocationSettings
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.QueueRoutingMode != "pooled" {
		t.Fatalf("default routing mode: got %q, want pooled", got.QueueRoutingMode)
	}
	if got.ArrivalRadiusMetres != 100 || !got.GeolocationAssist {
		t.Fatalf("geofence defaults: radius=%d assist=%v", got.ArrivalRadiusMetres, got.GeolocationAssist)
	}

	// Owner can change routing mode.
	rec = do("owner", http.MethodPatch, `{"queue_routing_mode":"hybrid"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner PATCH routing: got %d (%s)", rec.Code, rec.Body.String())
	}
	var mode string
	if err := pool.QueryRow(ctx, `SELECT queue_routing_mode FROM locations WHERE id=$1`, locationID).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "hybrid" {
		t.Fatalf("routing mode not persisted: %q", mode)
	}

	// Manager CAN change geofence fields.
	rec = do("manager", http.MethodPatch, `{"gps_latitude":12.9716,"gps_longitude":77.5946,"arrival_radius_metres":150,"geolocation_assist":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("manager PATCH geofence: got %d (%s)", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.GpsLatitude == nil || *got.GpsLatitude != 12.9716 || got.ArrivalRadiusMetres != 150 || got.GeolocationAssist {
		t.Fatalf("geofence not applied: %+v", got)
	}
	// Routing mode untouched by the geofence-only PATCH.
	if got.QueueRoutingMode != "hybrid" {
		t.Fatalf("routing mode clobbered by geofence PATCH: %q", got.QueueRoutingMode)
	}
}
