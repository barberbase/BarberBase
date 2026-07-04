package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"barberbase-core/internal/auth"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func newAdminRequest(method, url string, body []byte, tenantID, locationID, staffID uuid.UUID, role string) *http.Request {
	req := httptest.NewRequest(method, url, bytes.NewReader(body))
	ctx := req.Context()
	ctx = context.WithValue(ctx, auth.CtxTenantID, tenantID.String())
	ctx = context.WithValue(ctx, auth.CtxLocationID, locationID.String())
	ctx = context.WithValue(ctx, auth.CtxStaffMemberID, staffID.String())
	ctx = context.WithValue(ctx, auth.CtxRole, role)
	return req.WithContext(ctx)
}

func hoursBody(t *testing.T, days []LocationHoursDay) []byte {
	b, err := json.Marshal(map[string]any{"days": days})
	require.NoError(t, err)
	return b
}

func fullWeek(open string, close string) []LocationHoursDay {
	days := make([]LocationHoursDay, 7)
	for i := range days {
		o, c := open, close
		days[i] = LocationHoursDay{DayOfWeek: i, IsOpen: true, OpensAt: &o, ClosesAt: &c}
	}
	// Sunday closed
	days[0] = LocationHoursDay{DayOfWeek: 0, IsOpen: false}
	return days
}

func TestLocationHours_PutThenGetRoundTrip(t *testing.T) {
	s, _, tenantID, locationID, staffID, _ := setupCallNextTestServer(t)

	// barber role is rejected
	rec := httptest.NewRecorder()
	s.SetLocationHours(rec, newAdminRequest(http.MethodPut, "/v1/admin/locations/x/hours",
		hoursBody(t, fullWeek("09:00", "21:00")), tenantID, locationID, staffID, "barber"), UUIDv7(locationID))
	require.Equal(t, http.StatusForbidden, rec.Code)

	// owner can write
	rec = httptest.NewRecorder()
	s.SetLocationHours(rec, newAdminRequest(http.MethodPut, "/v1/admin/locations/x/hours",
		hoursBody(t, fullWeek("09:00", "21:00")), tenantID, locationID, staffID, "owner"), UUIDv7(locationID))
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())

	// upsert path: write again with different times (ON CONFLICT DO UPDATE)
	rec = httptest.NewRecorder()
	s.SetLocationHours(rec, newAdminRequest(http.MethodPut, "/v1/admin/locations/x/hours",
		hoursBody(t, fullWeek("10:30", "20:00")), tenantID, locationID, staffID, "owner"), UUIDv7(locationID))
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())

	// read back
	rec = httptest.NewRecorder()
	s.GetLocationHours(rec, newAdminRequest(http.MethodGet, "/v1/admin/locations/x/hours",
		nil, tenantID, locationID, staffID, "owner"), UUIDv7(locationID))
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Days []LocationHoursDay `json:"days"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Days, 7)
	require.False(t, resp.Days[0].IsOpen)
	require.Nil(t, resp.Days[0].OpensAt)
	for i := 1; i <= 6; i++ {
		require.True(t, resp.Days[i].IsOpen)
		require.Equal(t, "10:30", *resp.Days[i].OpensAt)
		require.Equal(t, "20:00", *resp.Days[i].ClosesAt)
	}

	// validation: opens_at >= closes_at rejected
	bad := fullWeek("21:00", "09:00")
	rec = httptest.NewRecorder()
	s.SetLocationHours(rec, newAdminRequest(http.MethodPut, "/v1/admin/locations/x/hours",
		hoursBody(t, bad), tenantID, locationID, staffID, "owner"), UUIDv7(locationID))
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)

	// validation: wrong tenant gets 404 (Law 11)
	rec = httptest.NewRecorder()
	s.SetLocationHours(rec, newAdminRequest(http.MethodPut, "/v1/admin/locations/x/hours",
		hoursBody(t, fullWeek("09:00", "21:00")), uuid.New(), locationID, staffID, "owner"), UUIDv7(locationID))
	require.Equal(t, http.StatusNotFound, rec.Code)
}
