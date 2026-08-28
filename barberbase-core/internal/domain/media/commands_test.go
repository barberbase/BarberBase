package media

import (
	"bytes"
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"barberbase-core/internal/domain/queue"
	"barberbase-core/internal/r2"
	"barberbase-core/internal/repository"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

type fixture struct {
	pool       *pgxpool.Pool
	tenantID   uuid.UUID
	locationID uuid.UUID
	variantID  uuid.UUID
	staffID    uuid.UUID
	svc        *Service
	fake       *fakeR2
}

const testHash = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"

func setup(t *testing.T) fixture {
	t.Helper()
	ctx := context.Background()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://bb_user:bb_password@localhost:5432/barberbase?sslmode=disable"
	}
	pool, err := repository.InitPool(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	_, err = pool.Exec(ctx, "TRUNCATE tenants CASCADE")
	require.NoError(t, err)

	f := fixture{pool: pool}
	sfx := uuid.NewString()[:8]
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO tenants (name, slug, owner_phone_number)
		VALUES ('M2', $1, '+919999911111') RETURNING id`, "m2-"+sfx).Scan(&f.tenantID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO locations (tenant_id, slug, name) VALUES ($1, $2, 'M2 Loc') RETURNING id`,
		f.tenantID, "m2loc-"+sfx).Scan(&f.locationID))
	var catID, grpID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO service_categories (tenant_id, location_id, name, gender)
		VALUES ($1,$2,'Hair','men') RETURNING id`, f.tenantID, f.locationID).Scan(&catID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO service_groups (tenant_id, location_id, category_id, name)
		VALUES ($1,$2,$3,'Fade') RETURNING id`, f.tenantID, f.locationID, catID).Scan(&grpID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO service_variants (tenant_id, location_id, group_id, name, duration_minutes, price_paise)
		VALUES ($1,$2,$3,'Mid Fade',30,25000) RETURNING id`,
		f.tenantID, f.locationID, grpID).Scan(&f.variantID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO staff_members (tenant_id, location_id, name, phone_number, role)
		VALUES ($1,$2,'Barber',$3,'barber') RETURNING id`,
		f.tenantID, f.locationID, "+9194"+sfx+"0").Scan(&f.staffID))

	fake, store := newFakeR2(t)
	f.fake = fake
	f.svc = &Service{
		Repo:          &repository.MediaRepository{Pool: pool},
		Store:         store,
		MaxBytes:      300 * 1024,
		MaxPerVariant: 6,
	}
	// Each test gets a fresh limiter: the sync.Map is package-level and 10
	// tokens would otherwise leak across tests.
	presignLimiters = sync.Map{}
	return f
}

func (f fixture) presign(t *testing.T, hash string) PresignOutput {
	t.Helper()
	out, err := f.svc.Presign(context.Background(), PresignInput{
		TenantID: f.tenantID, LocationID: f.locationID, StaffMemberID: f.staffID,
		Purpose: "service_ref", ContentHash: hash, VariantID: &f.variantID,
	}, time.Now())
	require.NoError(t, err)
	return out
}

// uploadVia PUTs through the presigned URL, exactly as a browser would. If the
// signature is wrong the fake returns 403 and this fails.
func uploadVia(t *testing.T, url, contentType string, size int) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(make([]byte, size)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", contentType)
	req.ContentLength = int64(size)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "the presigned PUT must be accepted")
}

// A1 — a presigned URL works and the object is retrievable afterwards.
func TestA1_PresignProducesAWorkingUploadURL(t *testing.T) {
	f := setup(t)
	out := f.presign(t, testHash)

	require.Equal(t,
		fmt.Sprintf("svc/%s/%s/%s.webp", f.locationID, f.variantID, testHash[:16]), out.R2Key)
	require.Contains(t, out.UploadURL, "X-Amz-Signature=")
	require.WithinDuration(t, time.Now().Add(15*time.Minute), out.ExpiresAt, time.Minute)

	uploadVia(t, out.UploadURL, "image/webp", 1024)

	info, err := f.svc.Store.Head(out.R2Key)
	require.NoError(t, err)
	require.EqualValues(t, 1024, info.ContentLength)
	require.Equal(t, "image/webp", info.ContentType)
}

// A2 — commit before upload, after upload, and twice.
func TestA2_CommitOrderingAndIdempotency(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	out := f.presign(t, testHash)

	_, err := f.svc.Commit(ctx, f.tenantID, f.locationID, out.MediaAssetID, nil)
	require.ErrorIs(t, err, ErrNotUploaded, "A2: commit before upload must be refused")
	var status string
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT status FROM media_assets WHERE id=$1`, out.MediaAssetID).Scan(&status))
	require.Equal(t, "pending", status, "A2: a refused commit changes nothing")

	uploadVia(t, out.UploadURL, "image/webp", 2048)
	a, err := f.svc.Commit(ctx, f.tenantID, f.locationID, out.MediaAssetID, nil)
	require.NoError(t, err)
	require.Equal(t, "ready", a.Status)
	require.NotNil(t, a.CommittedAt)
	require.NotNil(t, a.Bytes)
	require.Equal(t, 2048, *a.Bytes)

	again, err := f.svc.Commit(ctx, f.tenantID, f.locationID, out.MediaAssetID, nil)
	require.NoError(t, err, "A2: a second commit is a 200, not an error")
	require.Equal(t, a.CommittedAt.UnixNano(), again.CommittedAt.UnixNano(),
		"A2: committed_at must not move on re-commit")
}

// A3 — over the byte cap: rejected, row stays pending, reaper can collect it.
func TestA3_OversizeObjectRejectedRowStaysPending(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.svc.MaxBytes = 1000
	out := f.presign(t, testHash)
	uploadVia(t, out.UploadURL, "image/webp", 5000)

	_, err := f.svc.Commit(ctx, f.tenantID, f.locationID, out.MediaAssetID, nil)
	require.ErrorIs(t, err, ErrTooLarge)

	var status string
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT status FROM media_assets WHERE id=$1`, out.MediaAssetID).Scan(&status))
	require.Equal(t, "pending", status)

	// Still a reap candidate once it ages out.
	_, err = f.pool.Exec(ctx,
		`UPDATE media_assets SET created_at = NOW() - INTERVAL '2 hours' WHERE id=$1`, out.MediaAssetID)
	require.NoError(t, err)
	cands, err := f.svc.Repo.ReapCandidates(ctx, ReapAge, 10)
	require.NoError(t, err)
	require.Len(t, cands, 1)
	require.Equal(t, out.MediaAssetID, cands[0].ID)
}

// A4 — wrong content-type rejected at commit.
func TestA4_WrongContentTypeRejected(t *testing.T) {
	f := setup(t)
	out := f.presign(t, testHash)
	uploadVia(t, out.UploadURL, "image/png", 512)

	_, err := f.svc.Commit(context.Background(), f.tenantID, f.locationID, out.MediaAssetID, nil)
	require.ErrorIs(t, err, ErrWrongType)

	// A parameterised webp content-type is still webp.
	f2 := setup(t)
	out2 := f2.presign(t, testHash)
	uploadVia(t, out2.UploadURL, "image/webp; charset=binary", 512)
	_, err = f2.svc.Commit(context.Background(), f2.tenantID, f2.locationID, out2.MediaAssetID, nil)
	require.NoError(t, err, "A4: content-type parameters must not cause a false rejection")
}

// A5 — the same bytes presigned twice for one variant collapse to one row.
func TestA5_DuplicatePresignCollapsesToOneRow(t *testing.T) {
	f := setup(t)
	ctx := context.Background()

	first := f.presign(t, testHash)
	second := f.presign(t, testHash)

	require.Equal(t, first.MediaAssetID, second.MediaAssetID, "A5: same bytes, same row")
	require.Equal(t, first.R2Key, second.R2Key)
	require.NotEmpty(t, second.UploadURL, "A5: but a fresh upload URL")

	var n int
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM media_assets WHERE service_variant_id=$1`, f.variantID).Scan(&n))
	require.Equal(t, 1, n, "A5: exactly one row, enforced by idx_media_assets_variant_hash")

	// Abandoned, it is reaped like any other pending row.
	_, err := f.pool.Exec(ctx,
		`UPDATE media_assets SET created_at = NOW() - INTERVAL '2 hours'`)
	require.NoError(t, err)
	cands, err := f.svc.Repo.ReapCandidates(ctx, ReapAge, 10)
	require.NoError(t, err)
	require.Len(t, cands, 1)
}

// A8 — the per-variant cap holds under concurrency. Ten parallel commits for one
// variant must yield exactly six ready rows.
//
// This is what the service_variants FOR UPDATE lock is for: a partial unique
// index can express "at most one primary" but not "at most six rows", so without
// the lock every goroutine counts five and every goroutine inserts a sixth.
func TestA8_PerVariantCapUnderConcurrency(t *testing.T) {
	f := setup(t)
	ctx := context.Background()

	ids := make([]uuid.UUID, 0, 10)
	for i := 0; i < 10; i++ {
		hash := fmt.Sprintf("%016x%048x", i+1, 0)
		out, err := f.svc.Presign(ctx, PresignInput{
			TenantID: f.tenantID, LocationID: f.locationID, StaffMemberID: f.staffID,
			Purpose: "service_ref", ContentHash: hash, VariantID: &f.variantID,
		}, time.Now())
		require.NoError(t, err)
		uploadVia(t, out.UploadURL, "image/webp", 128)
		ids = append(ids, out.MediaAssetID)
	}

	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id uuid.UUID) {
			defer wg.Done()
			_, _ = f.svc.Commit(ctx, f.tenantID, f.locationID, id, nil)
		}(id)
	}
	wg.Wait()

	var ready int
	require.NoError(t, f.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM media_assets
		WHERE service_variant_id=$1 AND status='ready'`, f.variantID).Scan(&ready))
	require.Equal(t, 6, ready, "A8: exactly MaxPerVariant survive, never more")
}

// A9 — Law 11. Another tenant's asset is invisible, and a presign for a variant
// that is not yours cannot attach to it.
func TestA9_TenantIsolation(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	out := f.presign(t, testHash)

	other := uuid.New()
	_, err := f.svc.Commit(ctx, other, f.locationID, out.MediaAssetID, nil)
	require.ErrorIs(t, err, repository.ErrAssetNotFound,
		"A9: a foreign tenant must not be able to commit our asset")

	_, err = f.svc.Repo.GetForCommit(ctx, f.tenantID, uuid.New(), out.MediaAssetID)
	require.ErrorIs(t, err, repository.ErrAssetNotFound,
		"A9: location is scoped too, not just tenant")

	require.ErrorIs(t, f.svc.Archive(ctx, other, f.locationID, out.MediaAssetID),
		repository.ErrAssetNotFound)

	var status string
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT status FROM media_assets WHERE id=$1`, out.MediaAssetID).Scan(&status))
	require.Equal(t, "pending", status, "A9: the foreign calls changed nothing")
}

// A11 — the 11th presign in a minute is refused, with the retry delay attached.
//
// There is no HTTP layer in this unit (openapi.yaml has no media surface), so
// this asserts the typed error rather than a 429. The status-code mapping is in
// the HTTP Handoff.
func TestA11_PresignRateLimit(t *testing.T) {
	f := setup(t)
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		_, err := f.svc.Presign(ctx, PresignInput{
			TenantID: f.tenantID, LocationID: f.locationID, StaffMemberID: f.staffID,
			Purpose: "service_ref", ContentHash: fmt.Sprintf("%016x%048x", i, 0), VariantID: &f.variantID,
		}, time.Now())
		require.NoError(t, err, "presign %d of 10 must be allowed", i+1)
	}

	_, err := f.svc.Presign(ctx, PresignInput{
		TenantID: f.tenantID, LocationID: f.locationID, StaffMemberID: f.staffID,
		Purpose: "service_ref", ContentHash: fmt.Sprintf("%016x%048x", 99, 0), VariantID: &f.variantID,
	}, time.Now())
	require.ErrorIs(t, err, ErrRateLimited, "A11: the 11th is refused")

	var rl RateLimitedError
	require.ErrorAs(t, err, &rl)
	require.Equal(t, 6*time.Second, rl.RetryAfter, "A11: the error carries Retry-After")

	// Per staff member, not global.
	otherStaff := uuid.New()
	_, err = f.svc.Presign(ctx, PresignInput{
		TenantID: f.tenantID, LocationID: f.locationID, StaffMemberID: otherStaff,
		Purpose: "location_logo", ContentHash: testHash,
	}, time.Now())
	require.NoError(t, err, "A11: the limiter is keyed per staff member")
}

// A12 — R2 down at commit is a retryable failure with no data loss, and the
// queue is entirely unaffected.
func TestA12_R2DownDoesNotLoseDataAndDoesNotTouchTheQueue(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	out := f.presign(t, testHash)
	uploadVia(t, out.UploadURL, "image/webp", 900)

	f.fake.setFailing(true)
	_, err := f.svc.Commit(ctx, f.tenantID, f.locationID, out.MediaAssetID, nil)
	require.ErrorIs(t, err, r2.ErrUnavailable)

	var status string
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT status FROM media_assets WHERE id=$1`, out.MediaAssetID).Scan(&status))
	require.Equal(t, "pending", status, "A12: no data loss — the row survives for a retry")

	// The queue does not care that object storage is down. Media is a
	// convenience layer over the queue, exactly as push is (Law 21): with R2
	// unreachable, a customer still joins and a barber still calls them.
	requireDispatchStillWorks(t, f)

	f.fake.setFailing(false)
	a, err := f.svc.Commit(ctx, f.tenantID, f.locationID, out.MediaAssetID, nil)
	require.NoError(t, err, "A12: the retry succeeds once R2 returns")
	require.Equal(t, "ready", a.Status)
}

// A10 — no image bytes ever pass through the droplet.
//
// Asserted mechanically rather than by eye: parse the imports of every file in
// the media pipeline and fail on anything that could decode an image or read an
// object body. On 1GB of RAM with GOMEMLIMIT=250MiB, one decoded 12MP JPEG is
// ~48MB; a few concurrent uploads would be fatal.
func TestA10_NoImageBytesOnTheDroplet(t *testing.T) {
	banned := []string{
		"image", "image/jpeg", "image/png", "image/gif", "mime/multipart",
		"golang.org/x/image", "github.com/disintegration",
	}
	dirs := []string{".", "../../r2", "../../repository/media.go", "../../jobs/media_reaper.go"}

	fset := token.NewFileSet()
	checked := 0
	for _, d := range dirs {
		info, err := os.Stat(d)
		require.NoError(t, err)

		var files []string
		if info.IsDir() {
			entries, err := os.ReadDir(d)
			require.NoError(t, err)
			for _, e := range entries {
				if strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go") {
					files = append(files, d+"/"+e.Name())
				}
			}
		} else {
			files = append(files, d)
		}

		for _, path := range files {
			f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			require.NoError(t, err)
			checked++
			for _, imp := range f.Imports {
				got := strings.Trim(imp.Path.Value, `"`)
				for _, b := range banned {
					require.False(t, got == b || strings.HasPrefix(got, b+"/"),
						"A10: %s imports %q — no image decode may ever run on the droplet", path, got)
				}
			}
		}
	}
	require.Greater(t, checked, 4, "A10: the scan must actually have read the pipeline")

	// io.Copy appears only where a response body is drained to io.Discard.
	body, err := os.ReadFile("../../r2/sign.go")
	require.NoError(t, err)
	for _, line := range strings.Split(string(body), "\n") {
		if strings.Contains(line, "io.Copy") {
			require.Contains(t, line, "io.Discard",
				"A10: io.Copy may only drain a response, never read object bytes")
		}
	}
}

// requireDispatchStillWorks is A12's second half, and the point of the whole
// assertion: with R2 unreachable, a customer still joins the queue and a barber
// still calls them. Nothing in the media pipeline may sit on the dispatch path.
//
// This drives the real repository.CallNextTx, not a stub — if a future change
// ever put an R2 call inside a queue transaction, this is what would catch it.
func requireDispatchStillWorks(t *testing.T, f fixture) {
	t.Helper()
	ctx := context.Background()

	var sessionID, visitID, entryID uuid.UUID
	require.NoError(t, f.pool.QueryRow(ctx, `
		INSERT INTO queue_sessions (tenant_id, location_id, business_date, status)
		VALUES ($1, $2, (NOW() AT TIME ZONE 'Asia/Kolkata')::DATE, 'active') RETURNING id`,
		f.tenantID, f.locationID).Scan(&sessionID))
	require.NoError(t, f.pool.QueryRow(ctx, `
		INSERT INTO visits (tenant_id, location_id, entry_type, status, total_duration_minutes)
		VALUES ($1, $2, 'walk_in', 'active', 30) RETURNING id`,
		f.tenantID, f.locationID).Scan(&visitID))
	require.NoError(t, f.pool.QueryRow(ctx, `
		-- [B9/A3] remote_joined_at is deliberately left NULL. This fixture is what
		-- originally tripped GetEntryStaffView during M2, and it was changed to set
		-- the column so M2 could proceed. B9 fixed the scan, so it is reverted here:
		-- the assertion below now passes because of the fix, not because the fixture
		-- avoids the shape.
		INSERT INTO queue_entries
			(visit_id, queue_session_id, token_number, state, presence_state,
			 is_dispatchable, priority_group, sort_key)
		VALUES ($1, $2, 1, 'waiting', 'arrived', true, 100, 1) RETURNING id`,
		visitID, sessionID).Scan(&entryID))

	// R2 is down for the duration of this call — assert that, so the test cannot
	// silently pass by running against a healthy fake.
	_, err := f.svc.Store.Head("anything")
	require.ErrorIs(t, err, r2.ErrUnavailable, "precondition: R2 must actually be down here")

	out, err := queue.CallNext(ctx, f.pool, queue.CallNextParams{
		TenantID:      f.tenantID,
		LocationID:    f.locationID,
		StaffMemberID: f.staffID,
		HMACSecret:    []byte("test-hmac-secret-012345678901234"),
	})
	require.NoError(t, err, "A12: object storage being down must never block a call-next")
	require.NotNil(t, out.Entry)

	var state string
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT state FROM queue_entries WHERE id=$1`, entryID).Scan(&state))
	require.Equal(t, "called", state, "A12: the queue is untouched by an R2 outage")
}
