package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"testing"

	"barberbase-core/internal/auth"
	"barberbase-core/internal/domain/media"
	"barberbase-core/internal/r2"
	"barberbase-core/internal/repository"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// O5 A2-A7. The four media routes over the production router.
//
// Nothing here talks to real R2: fakeStore is an httptest server standing in for
// the bucket, and tests "upload" by putting an entry in its map. That is enough,
// because the only thing these routes do with R2 is presign a URL and HEAD an
// object — the bytes never pass through the server, by design.

type fakeObject struct {
	size        int64
	contentType string
	status      int // when non-zero, HEAD returns this instead of the object
}

type fakeStore struct {
	*httptest.Server
	mu      sync.Mutex
	objects map[string]fakeObject
}

func newFakeStore(t *testing.T) *fakeStore {
	t.Helper()
	f := &fakeStore{objects: map[string]fakeObject{}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		obj, ok := f.objects[r.URL.Path]
		f.mu.Unlock()
		switch {
		case !ok:
			w.WriteHeader(http.StatusNotFound)
		case obj.status != 0:
			w.WriteHeader(obj.status)
		default:
			w.Header().Set("Content-Type", obj.contentType)
			w.Header().Set("Content-Length", strconv.FormatInt(obj.size, 10))
			w.Header().Set("ETag", `"fake-etag"`)
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

// put makes an object exist, as a real browser upload would have.
func (f *fakeStore) put(bucket, key string, obj fakeObject) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects["/"+bucket+"/"+key] = obj
}

func (f *fakeStore) store() *r2.Store {
	return &r2.Store{
		AccessKey: "test-access-key",
		SecretKey: "test-secret-key",
		Region:    "auto",
		Endpoint:  f.URL,
		Bucket:    "test-bucket",
		HTTP:      f.Client(),
	}
}

// mediaTestServer wires a Server whose media service points at the fake bucket.
func mediaTestServer(t *testing.T, maxBytes, maxPerVariant int) (*Server, *fakeStore, *httptest.Server, uuid.UUID, uuid.UUID, string) {
	t.Helper()
	cleanDatabase(t, os.Getenv("DATABASE_URL"))
	t.Cleanup(func() { cleanDatabase(t, os.Getenv("DATABASE_URL")) })
	s, pool, tenantID, locationID, staffID, _ := setupTestServer(t)
	t.Cleanup(pool.Close)

	fake := newFakeStore(t)
	s.Media = &media.Service{
		Repo:          &repository.MediaRepository{Pool: pool},
		Store:         fake.store(),
		MaxBytes:      maxBytes,
		MaxPerVariant: maxPerVariant,
	}
	srv := httptest.NewServer(NewRouter(s, []byte(s.Config.JWTSecret)))
	t.Cleanup(srv.Close)

	// Role comes from the token, and every media route demands owner or manager.
	jwt, _, err := auth.GenerateAccessAndRefreshTokens(
		[]byte(s.Config.JWTSecret), tenantID.String(), locationID.String(), staffID.String(), "owner")
	require.NoError(t, err)
	return s, fake, srv, tenantID, locationID, jwt
}

func do(t *testing.T, method, url, jwt string, body any) *http.Response {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		rdr = bytes.NewReader(raw)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, url, rdr)
	require.NoError(t, err)
	if jwt != "" {
		req.Header.Set("Authorization", "Bearer "+jwt)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { res.Body.Close() })
	return res
}

// hash builds a distinct 64-hex content hash. The N MUST vary in the first 16
// characters: buildKey derives the R2 key from contentHash[:16], so hashes that
// agree on that prefix collide onto ONE asset row via CreatePending's
// ON CONFLICT (r2_key) — which silently turns "two images" into "one".
func hash(n int) string { return fmt.Sprintf("%016x%048x", n, n) }

// presign issues one presign and returns the decoded response.
func presign(t *testing.T, srv *httptest.Server, jwt string, body map[string]any) (*http.Response, MediaPresignResponse) {
	t.Helper()
	res := do(t, http.MethodPost, srv.URL+"/v1/admin/media/presign", jwt, body)
	var out MediaPresignResponse
	if res.StatusCode == http.StatusOK {
		require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	}
	return res, out
}

// A3 + A4: every row of the error table, each from a real condition.
func TestMediaRoutes_ErrorMapping(t *testing.T) {
	s, fake, srv, tenantID, locationID, jwt := mediaTestServer(t, 300*1024, 1)
	pool := s.Pool
	ctx := context.Background()
	variantID := seedServiceVariant(t, pool, tenantID, locationID, "Haircut", 30, 30000, true)

	t.Run("ErrBadPurpose 400 — unknown purpose", func(t *testing.T) {
		res, _ := presign(t, srv, jwt, map[string]any{"purpose": "not_a_purpose", "content_hash": hash(1)})
		require.Equal(t, http.StatusBadRequest, res.StatusCode)
	})

	t.Run("ErrBadPurpose 400 — service_ref without a variant", func(t *testing.T) {
		res, _ := presign(t, srv, jwt, map[string]any{"purpose": "service_ref", "content_hash": hash(2)})
		require.Equal(t, http.StatusBadRequest, res.StatusCode)
	})

	t.Run("ErrBadContentHash 400", func(t *testing.T) {
		res, _ := presign(t, srv, jwt, map[string]any{"purpose": "location_logo", "content_hash": "NOTHEX"})
		require.Equal(t, http.StatusBadRequest, res.StatusCode)
	})

	t.Run("ErrAssetNotFound 404 — commit an asset that does not exist", func(t *testing.T) {
		res := do(t, http.MethodPost, srv.URL+"/v1/admin/media/"+uuid.NewString()+"/commit", jwt, nil)
		require.Equal(t, http.StatusNotFound, res.StatusCode)
	})

	t.Run("ErrNotUploaded 409 — commit before the browser uploaded", func(t *testing.T) {
		res, out := presign(t, srv, jwt, map[string]any{"purpose": "location_logo", "content_hash": hash(3)})
		require.Equal(t, http.StatusOK, res.StatusCode)
		res = do(t, http.MethodPost, srv.URL+"/v1/admin/media/"+out.MediaAssetId.String()+"/commit", jwt, nil)
		require.Equal(t, http.StatusConflict, res.StatusCode)
	})

	t.Run("ErrTooLarge 413", func(t *testing.T) {
		res, out := presign(t, srv, jwt, map[string]any{"purpose": "location_cover", "content_hash": hash(4)})
		require.Equal(t, http.StatusOK, res.StatusCode)
		fake.put("test-bucket", out.R2Key, fakeObject{size: 900 * 1024, contentType: "image/webp"})
		res = do(t, http.MethodPost, srv.URL+"/v1/admin/media/"+out.MediaAssetId.String()+"/commit", jwt, nil)
		require.Equal(t, http.StatusRequestEntityTooLarge, res.StatusCode)
	})

	t.Run("ErrWrongType 415", func(t *testing.T) {
		res, out := presign(t, srv, jwt, map[string]any{"purpose": "location_cover", "content_hash": hash(5)})
		require.Equal(t, http.StatusOK, res.StatusCode)
		fake.put("test-bucket", out.R2Key, fakeObject{size: 1024, contentType: "image/png"})
		res = do(t, http.MethodPost, srv.URL+"/v1/admin/media/"+out.MediaAssetId.String()+"/commit", jwt, nil)
		require.Equal(t, http.StatusUnsupportedMediaType, res.StatusCode)
	})

	t.Run("r2.ErrUnavailable 503 — R2 answers 500", func(t *testing.T) {
		res, out := presign(t, srv, jwt, map[string]any{"purpose": "location_cover", "content_hash": hash(6)})
		require.Equal(t, http.StatusOK, res.StatusCode)
		fake.put("test-bucket", out.R2Key, fakeObject{status: http.StatusInternalServerError})
		res = do(t, http.MethodPost, srv.URL+"/v1/admin/media/"+out.MediaAssetId.String()+"/commit", jwt, nil)
		require.Equal(t, http.StatusServiceUnavailable, res.StatusCode)
	})

	// MaxPerVariant is 1 for this server, so the second commit on one variant is
	// the real cap being enforced, not a stub.
	t.Run("ErrVariantFull 409", func(t *testing.T) {
		for i, want := range []int{http.StatusOK, http.StatusConflict} {
			res, out := presign(t, srv, jwt, map[string]any{
				"purpose": "service_ref", "content_hash": hash(10 + i),
				"service_variant_id": variantID.String(),
			})
			require.Equal(t, http.StatusOK, res.StatusCode)
			fake.put("test-bucket", out.R2Key, fakeObject{size: 2048, contentType: "image/webp"})
			res = do(t, http.MethodPost, srv.URL+"/v1/admin/media/"+out.MediaAssetId.String()+"/commit", jwt, nil)
			require.Equal(t, want, res.StatusCode, "commit %d", i+1)
		}
	})

	// A4. Burst is 10 per staff member, so the 11th presign is refused and must
	// carry the delay the domain layer chose.
	t.Run("ErrRateLimited 429 with Retry-After", func(t *testing.T) {
		staffID := uuid.New()
		_, err := pool.Exec(ctx, `INSERT INTO staff_members (id, tenant_id, location_id, name, phone_number, role, is_active)
			VALUES ($1, $2, $3, 'Burst Owner', $4, 'owner', true)`,
			staffID, tenantID, locationID, "+9197"+uuid.NewString()[:9])
		require.NoError(t, err)
		burstJWT, _, err := auth.GenerateAccessAndRefreshTokens(
			[]byte(s.Config.JWTSecret), tenantID.String(), locationID.String(), staffID.String(), "owner")
		require.NoError(t, err)

		var last *http.Response
		for i := 0; i < 11; i++ {
			last, _ = presign(t, srv, burstJWT, map[string]any{
				"purpose": "location_cover", "content_hash": hash(100 + i)})
		}
		require.Equal(t, http.StatusTooManyRequests, last.StatusCode)
		require.NotEmpty(t, last.Header.Get("Retry-After"), "A4: 429 must carry Retry-After")
		secs, err := strconv.Atoi(last.Header.Get("Retry-After"))
		require.NoError(t, err)
		require.Greater(t, secs, 0, "A4: Retry-After must come from RateLimitedError, not be 0")
	})
}

// A5: Law 11. Neither ids in a body nor ids in a path may reach across tenants.
func TestMediaRoutes_TenantIsolation(t *testing.T) {
	s, fake, srv, _, _, jwt := mediaTestServer(t, 300*1024, 6)
	pool := s.Pool
	ctx := context.Background()

	// A second shop, with its own variant and its own committed asset.
	otherTenant, otherLocation := uuid.New(), uuid.New()
	_, err := pool.Exec(ctx, `INSERT INTO tenants (id, name, slug, owner_phone_number)
		VALUES ($1, 'Other Tenant', 'other-tenant', '+919000000001')`, otherTenant)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO locations (id, tenant_id, name, slug)
		VALUES ($1, $2, 'Other Location', 'other-location')`, otherLocation, otherTenant)
	require.NoError(t, err)
	otherVariant := seedServiceVariant(t, pool, otherTenant, otherLocation, "Their Cut", 30, 30000, true)

	// The repository CANNOT catch this: it inserts our tenant_id beside whatever
	// variant id the body names, and the foreign key only proves the variant
	// exists somewhere. The handler check is what refuses it.
	t.Run("presign naming another tenant's variant is refused", func(t *testing.T) {
		res, _ := presign(t, srv, jwt, map[string]any{
			"purpose": "service_ref", "content_hash": hash(20),
			"service_variant_id": otherVariant.String(),
		})
		require.Equal(t, http.StatusNotFound, res.StatusCode, "A5: cross-tenant variant must not be presignable")

		var leaked int
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM media_assets WHERE service_variant_id = $1`, otherVariant).Scan(&leaked))
		require.Equal(t, 0, leaked, "A5: no row may be created against another tenant's variant")
	})

	t.Run("commit and archive on another tenant's asset are 404", func(t *testing.T) {
		var theirAsset uuid.UUID
		require.NoError(t, pool.QueryRow(ctx, `INSERT INTO media_assets
			(tenant_id, location_id, purpose, r2_key, content_hash, status)
			VALUES ($1, $2, 'location_logo', $3, $4, 'pending') RETURNING id`,
			otherTenant, otherLocation, "loc/"+otherLocation.String()+"/logo/deadbeef.webp", hash(21)).Scan(&theirAsset))
		fake.put("test-bucket", "loc/"+otherLocation.String()+"/logo/deadbeef.webp",
			fakeObject{size: 1024, contentType: "image/webp"})

		res := do(t, http.MethodPost, srv.URL+"/v1/admin/media/"+theirAsset.String()+"/commit", jwt, nil)
		require.Equal(t, http.StatusNotFound, res.StatusCode, "A5: 404, not 403 — GetForCommit scopes by tenant+location")

		res = do(t, http.MethodDelete, srv.URL+"/v1/admin/media/"+theirAsset.String(), jwt, nil)
		require.Equal(t, http.StatusNotFound, res.StatusCode)

		var status string
		require.NoError(t, pool.QueryRow(ctx, `SELECT status FROM media_assets WHERE id = $1`, theirAsset).Scan(&status))
		require.Equal(t, "pending", status, "A5: their asset must be untouched")
	})
}

// A2 + A6: list filters, scoping, the archived default, and the generated type.
func TestMediaRoutes_List(t *testing.T) {
	s, fake, srv, tenantID, locationID, jwt := mediaTestServer(t, 300*1024, 6)
	pool := s.Pool
	ctx := context.Background()
	variantID := seedServiceVariant(t, pool, tenantID, locationID, "Haircut", 30, 30000, true)

	// One committed service image, one pending logo, one archived cover.
	res, svc := presign(t, srv, jwt, map[string]any{
		"purpose": "service_ref", "content_hash": hash(30), "service_variant_id": variantID.String()})
	require.Equal(t, http.StatusOK, res.StatusCode)
	fake.put("test-bucket", svc.R2Key, fakeObject{size: 4096, contentType: "image/webp"})
	res = do(t, http.MethodPost, srv.URL+"/v1/admin/media/"+svc.MediaAssetId.String()+"/commit", jwt,
		map[string]any{"alt_text": "a fade"})
	require.Equal(t, http.StatusOK, res.StatusCode)

	var committed MediaAsset // A2: the response decodes into the GENERATED type
	require.NoError(t, json.NewDecoder(res.Body).Decode(&committed))
	require.Equal(t, MediaAssetStatus("ready"), committed.Status)
	require.NotNil(t, committed.Bytes)
	require.Equal(t, 4096, *committed.Bytes)
	require.NotNil(t, committed.AltText)
	require.Equal(t, "a fade", *committed.AltText)
	require.NotNil(t, committed.ServiceVariantId)

	_, logo := presign(t, srv, jwt, map[string]any{"purpose": "location_logo", "content_hash": hash(31)})
	_, cover := presign(t, srv, jwt, map[string]any{"purpose": "location_cover", "content_hash": hash(32)})
	res = do(t, http.MethodDelete, srv.URL+"/v1/admin/media/"+cover.MediaAssetId.String(), jwt, nil)
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	list := func(t *testing.T, query string) []MediaAsset {
		t.Helper()
		res := do(t, http.MethodGet, srv.URL+"/v1/admin/media"+query, jwt, nil)
		require.Equal(t, http.StatusOK, res.StatusCode)
		var out []MediaAsset
		require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
		return out
	}

	t.Run("archived is excluded by default", func(t *testing.T) {
		got := list(t, "")
		ids := map[uuid.UUID]bool{}
		for _, a := range got {
			ids[a.Id] = true
		}
		require.True(t, ids[svc.MediaAssetId], "ready asset must be listed")
		require.True(t, ids[logo.MediaAssetId], "pending asset must be listed")
		require.False(t, ids[cover.MediaAssetId], "A6: archived must be excluded by default")
	})

	t.Run("include_archived=true adds it back", func(t *testing.T) {
		got := list(t, "?include_archived=true")
		found := false
		for _, a := range got {
			if a.Id == cover.MediaAssetId {
				found = true
				require.Equal(t, MediaAssetStatus("archived"), a.Status)
			}
		}
		require.True(t, found, "A6: include_archived must surface archived rows")
	})

	t.Run("filters by purpose and by variant", func(t *testing.T) {
		byPurpose := list(t, "?purpose=location_logo")
		require.Len(t, byPurpose, 1)
		require.Equal(t, logo.MediaAssetId, byPurpose[0].Id)

		byVariant := list(t, "?service_variant_id="+variantID.String())
		require.Len(t, byVariant, 1)
		require.Equal(t, svc.MediaAssetId, byVariant[0].Id)

		require.Empty(t, list(t, "?service_variant_id="+uuid.NewString()), "unknown variant returns nothing")
	})

	t.Run("scoped to the caller's tenant and location", func(t *testing.T) {
		otherTenant, otherLocation := uuid.New(), uuid.New()
		_, err := pool.Exec(ctx, `INSERT INTO tenants (id, name, slug, owner_phone_number)
			VALUES ($1, 'List Other', 'list-other', '+919000000002')`, otherTenant)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `INSERT INTO locations (id, tenant_id, name, slug)
			VALUES ($1, $2, 'List Other Loc', 'list-other-loc')`, otherLocation, otherTenant)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `INSERT INTO media_assets (tenant_id, location_id, purpose, r2_key, content_hash, status)
			VALUES ($1, $2, 'location_logo', $3, $4, 'ready')`,
			otherTenant, otherLocation, "loc/"+otherLocation.String()+"/logo/cafe.webp", hash(33))
		require.NoError(t, err)

		for _, a := range list(t, "?include_archived=true") {
			require.NotEqual(t, "loc/"+otherLocation.String()+"/logo/cafe.webp", a.R2Key,
				"A6: another tenant's asset must never appear")
		}
	})
}

// A7: prod runs with media disabled today. Every route must say 503 and nothing
// may panic — including Archive, whose domain method never checks the store.
func TestMediaRoutes_DisabledReturns503(t *testing.T) {
	cleanDatabase(t, os.Getenv("DATABASE_URL"))
	t.Cleanup(func() { cleanDatabase(t, os.Getenv("DATABASE_URL")) })
	s, pool, tenantID, locationID, staffID, _ := setupTestServer(t)
	defer pool.Close()

	// Exactly how main.go builds it when the R2 env vars are absent.
	s.Media = &media.Service{
		Repo:  &repository.MediaRepository{Pool: pool},
		Store: r2.New("", "", "", "", ""),
	}
	require.False(t, s.Media.Store.Configured())

	srv := httptest.NewServer(NewRouter(s, []byte(s.Config.JWTSecret)))
	defer srv.Close()
	jwt, _, err := auth.GenerateAccessAndRefreshTokens(
		[]byte(s.Config.JWTSecret), tenantID.String(), locationID.String(), staffID.String(), "owner")
	require.NoError(t, err)

	id := uuid.NewString()
	for _, c := range []struct{ name, method, path string }{
		{"presign", http.MethodPost, "/v1/admin/media/presign"},
		{"commit", http.MethodPost, "/v1/admin/media/" + id + "/commit"},
		{"archive", http.MethodDelete, "/v1/admin/media/" + id},
		{"list", http.MethodGet, "/v1/admin/media"},
	} {
		t.Run(c.name, func(t *testing.T) {
			res := do(t, c.method, srv.URL+c.path, jwt, map[string]any{
				"purpose": "location_logo", "content_hash": hash(40)})
			require.Equal(t, http.StatusServiceUnavailable, res.StatusCode)
		})
	}

	// The role gate still runs first: a barber gets 403, not a hint about media.
	t.Run("role gate precedes the media gate", func(t *testing.T) {
		barberJWT, _, err := auth.GenerateAccessAndRefreshTokens(
			[]byte(s.Config.JWTSecret), tenantID.String(), locationID.String(), staffID.String(), "barber")
		require.NoError(t, err)
		res := do(t, http.MethodGet, srv.URL+"/v1/admin/media", barberJWT, nil)
		require.Equal(t, http.StatusForbidden, res.StatusCode)
	})

	t.Run("no JWT is 401", func(t *testing.T) {
		res := do(t, http.MethodGet, srv.URL+"/v1/admin/media", "", nil)
		require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	})
}
