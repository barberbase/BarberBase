package notification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"barberbase-core/internal/r2"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ObjectStore is the slice of r2.Store this handler needs. An interface rather
// than the concrete type so tests can drive the failure paths without an R2
// endpoint.
type ObjectStore interface {
	Delete(key string) error
}

// MediaPurgeHandler removes the R2 object behind an archived asset, once the
// grace window in process_after has elapsed. Exported struct with public fields,
// matching PushHandler, so a test constructs it directly and outbox.NewWorker's
// signature never changes.
type MediaPurgeHandler struct {
	Pool  *pgxpool.Pool
	Store ObjectStore
}

type mediaPurgePayload struct {
	MediaAssetID string `json:"media_asset_id"`
	R2Key        string `json:"r2_key"`
}

// Handle deletes the object, then the row.
//
// That order is not arbitrary: the row is the only record that the object
// exists, so deleting it first would strand the object in R2 forever. Crashing
// between the two leaves a row whose object is already gone, and r2.Delete
// treats a 404 as success, so the retry converges instead of wedging.
//
// A malformed payload is terminal — retrying it can only fail the same way.
// An unreachable R2 is retryable, and the worker's backoff handles the rest.
func (h *MediaPurgeHandler) Handle(ctx context.Context, pool *pgxpool.Pool, event *OutboxEvent) error {
	var p mediaPurgePayload
	if err := json.Unmarshal(event.Payload, &p); err != nil {
		return newTerminalError("media.purge: malformed payload: %v", err)
	}
	if p.R2Key == "" || p.MediaAssetID == "" {
		return newTerminalError("media.purge: payload missing r2_key or media_asset_id")
	}
	assetID, err := uuid.Parse(p.MediaAssetID)
	if err != nil {
		return newTerminalError("media.purge: bad media_asset_id %q", p.MediaAssetID)
	}

	if err := h.Store.Delete(p.R2Key); err != nil {
		if errors.Is(err, r2.ErrNotConfigured) {
			// No credentials: the object cannot be reached at all. Terminal —
			// retrying every backoff step forever would say nothing new.
			return newTerminalError("media.purge: %v", err)
		}
		return fmt.Errorf("media.purge: delete %s: %w", p.R2Key, err) // retryable
	}

	db := h.Pool
	if db == nil {
		db = pool
	}
	_, err = db.Exec(ctx,
		`DELETE FROM media_assets WHERE id = $1 AND status = 'archived'`, assetID)
	return err
}
