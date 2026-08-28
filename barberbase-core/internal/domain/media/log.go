package media

import (
	"log"

	"github.com/google/uuid"
)

// LogCommit records the ETag R2 reported at commit time. Deliberately a log line
// rather than a column: see Service.Commit for why no migration is opened for it.
// Overridable in tests.
var LogCommit = func(assetID uuid.UUID, key, etag string, bytes int64) {
	log.Printf("[Media] committed asset=%s key=%s bytes=%d r2_etag=%s", assetID, key, bytes, etag)
}
