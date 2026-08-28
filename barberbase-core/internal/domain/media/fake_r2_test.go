package media

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"barberbase-core/internal/r2"
)

// fakeR2 is an in-process S3 stand-in that VERIFIES the presigned signature
// rather than accepting anything.
//
// What this proves and what it does not: it catches URL-assembly faults — a
// wrong host, a missing or misspelled X-Amz-* parameter, a key that does not
// match the path, a signature that does not correspond to the request actually
// sent. It does NOT prove the signing algorithm agrees with Amazon, because it
// checks our signer against our signer. That is what the AWS published golden
// vector in internal/r2/sign_test.go is for, and why the manual run against real
// R2 (see Deferred Verification) still has to happen.
//
// CI never touches real R2.
type fakeR2 struct {
	*httptest.Server
	mu      sync.Mutex
	store   *r2.Store
	objects map[string]fakeObject
	// failMode makes every operation fail, for the R2-down cases.
	failMode bool
	// deleteStatus overrides the DELETE response, for the 404-is-success case.
	deleteStatus int
	deletes      []string
}

type fakeObject struct {
	body        []byte
	contentType string
}

func newFakeR2(t *testing.T) (*fakeR2, *r2.Store) {
	t.Helper()
	f := &fakeR2{objects: map[string]fakeObject{}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.Close)

	store := r2.New("acct", "bb-media", "TESTKEY", "testsecret", "https://cdn.test")
	store.Endpoint = f.Server.URL // http://127.0.0.1:PORT
	f.store = store
	return f, store
}

func (f *fakeR2) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	failing := f.failMode
	f.mu.Unlock()
	if failing {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	key := strings.TrimPrefix(r.URL.EscapedPath(), "/bb-media/")
	unescaped, err := url.PathUnescape(key)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodPut:
		if err := f.verifyPresigned(r); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		f.mu.Lock()
		f.objects[unescaped] = fakeObject{body: body, contentType: r.Header.Get("Content-Type")}
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)

	case http.MethodHead:
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		f.mu.Lock()
		o, ok := f.objects[unescaped]
		f.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", o.contentType)
		w.Header().Set("ETag", `"fake-etag-`+unescaped[:8]+`"`)
		w.Header().Set("Content-Length", fmt.Sprint(len(o.body)))
		w.WriteHeader(http.StatusOK)

	case http.MethodDelete:
		f.mu.Lock()
		f.deletes = append(f.deletes, unescaped)
		_, existed := f.objects[unescaped]
		delete(f.objects, unescaped)
		status := f.deleteStatus
		f.mu.Unlock()
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		if !existed {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// verifyPresigned re-derives the signature for the request as received and
// requires an exact match — the whole point of not writing a fake that waves
// everything through.
//
// It uses only r2's public API: recover the key, timestamp and TTL from the
// request, ask the Store to presign that same call, and compare. No test-only
// hook is added to production code for this.
func (f *fakeR2) verifyPresigned(r *http.Request) error {
	q := r.URL.Query()
	got := q.Get("X-Amz-Signature")
	if got == "" {
		return fmt.Errorf("no X-Amz-Signature")
	}
	for _, required := range []string{"X-Amz-Algorithm", "X-Amz-Credential", "X-Amz-Date", "X-Amz-Expires", "X-Amz-SignedHeaders"} {
		if q.Get(required) == "" {
			return fmt.Errorf("missing %s", required)
		}
	}
	key, err := url.PathUnescape(strings.TrimPrefix(r.URL.EscapedPath(), "/bb-media/"))
	if err != nil {
		return err
	}
	when, err := time.Parse("20060102T150405Z", q.Get("X-Amz-Date"))
	if err != nil {
		return fmt.Errorf("bad X-Amz-Date: %w", err)
	}
	secs, err := strconv.Atoi(q.Get("X-Amz-Expires"))
	if err != nil {
		return fmt.Errorf("bad X-Amz-Expires: %w", err)
	}

	rebuilt, err := f.store.PresignPut(key, time.Duration(secs)*time.Second, when)
	if err != nil {
		return err
	}
	u, err := url.Parse(rebuilt)
	if err != nil {
		return err
	}
	if want := u.Query().Get("X-Amz-Signature"); want != got {
		return fmt.Errorf("signature mismatch for key %q: got %s want %s", key, got, want)
	}
	return nil
}

func (f *fakeR2) setFailing(v bool) { f.mu.Lock(); f.failMode = v; f.mu.Unlock() }

func (f *fakeR2) deleteCount() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.deletes) }

func (f *fakeR2) put(key, contentType string, size int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[key] = fakeObject{body: make([]byte, size), contentType: contentType}
}
