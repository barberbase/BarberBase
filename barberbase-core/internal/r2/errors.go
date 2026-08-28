package r2

import "errors"

var (
	// ErrNotConfigured means no R2 credentials are set. Callers map this to 503:
	// the deployment simply has no media storage, which must never be fatal.
	ErrNotConfigured = errors.New("r2: media storage is not configured")
	// ErrNotFound means the object is absent — for a commit, that the browser
	// never finished uploading.
	ErrNotFound = errors.New("r2: object not found")
	// ErrUnavailable is any transport or 5xx failure. Always retryable.
	ErrUnavailable = errors.New("r2: storage unavailable")
)
