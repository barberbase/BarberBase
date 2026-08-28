// Package r2 speaks the three S3 operations this product needs — presign a PUT,
// HEAD an object, DELETE an object — and nothing else.
//
// It is hand-rolled rather than aws-sdk-go-v2 on a ratio argument: the SDK's
// presigner lives inside service/s3 and cannot be pulled alone, so three
// operations would cost roughly eighteen modules on a tree of nine direct
// requires. SigV4 is a deterministic string-building exercise over an HMAC
// chain, not a protocol negotiation — it either matches byte-for-byte or it does
// not, which is the property that makes hand-rolling defensible here. The
// failure mode is a 403, loud and immediate, never a silent partial success.
//
// If a fourth or fifth operation ever appears — multipart, lifecycle, bucket
// ops — that is the trigger to reconsider the SDK. Three operations justify this
// file; ten would not.
//
// The safety argument rests entirely on sign_test.go's golden vectors. Do not
// weaken them.
package r2

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	algorithm = "AWS4-HMAC-SHA256"
	service   = "s3"
	// emptyPayloadHash is sha256("") — the payload hash for a body-less signed
	// request (HEAD, DELETE).
	emptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	// unsignedPayload is what a presigned URL uses: the bytes are uploaded by a
	// browser we never see, so their hash cannot be part of the signature.
	unsignedPayload = "UNSIGNED-PAYLOAD"

	amzDateFormat   = "20060102T150405Z"
	dateStampFormat = "20060102"
)

// s3URIEncode implements the encoding S3 canonicalisation requires, which is NOT
// url.QueryEscape and NOT url.PathEscape. The unreserved set of RFC 3986 passes
// through; everything else becomes %XX with UPPERCASE hex. A space is %20, never
// '+'.
//
// encodeSlash is the whole reason this takes a flag: in the canonical URI a '/'
// is a path separator and must survive, while inside a query parameter value —
// X-Amz-Credential contains three of them — it must become %2F. Getting this
// backwards produces a signature that is wrong in exactly one character.
func s3URIEncode(s string, encodeSlash bool) string {
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		case c == '/':
			if encodeSlash {
				b.WriteString("%2F")
			} else {
				b.WriteByte('/')
			}
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// canonicalQuery renders parameters sorted by byte order of the encoded key,
// each key and value fully encoded. Multi-valued keys sort by value too.
func canonicalQuery(q url.Values) string {
	pairs := make([]string, 0, len(q))
	for k, vs := range q {
		ek := s3URIEncode(k, true)
		for _, v := range vs {
			pairs = append(pairs, ek+"="+s3URIEncode(v, true))
		}
	}
	sort.Strings(pairs)
	return strings.Join(pairs, "&")
}

// canonicalRequest is stage one of SigV4. It is returned as its own string, and
// asserted on its own in the tests, so that a future break names the stage that
// diverged instead of reporting an opaque signature mismatch.
func canonicalRequest(method, canonicalURI, query string, headers map[string]string, payloadHash string) (req, signedHeaders string) {
	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, strings.ToLower(k))
	}
	sort.Strings(names)

	var canonicalHeaders strings.Builder
	for _, n := range names {
		canonicalHeaders.WriteString(n)
		canonicalHeaders.WriteByte(':')
		canonicalHeaders.WriteString(strings.TrimSpace(headers[n]))
		canonicalHeaders.WriteByte('\n')
	}
	signedHeaders = strings.Join(names, ";")

	return strings.Join([]string{
		method, canonicalURI, query, canonicalHeaders.String(), signedHeaders, payloadHash,
	}, "\n"), signedHeaders
}

// stringToSign is stage two. Also returned separately, for the same reason.
func stringToSign(now time.Time, scope, canonicalReq string) string {
	sum := sha256.Sum256([]byte(canonicalReq))
	return strings.Join([]string{
		algorithm, now.UTC().Format(amzDateFormat), scope, hex.EncodeToString(sum[:]),
	}, "\n")
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

// deriveSignature is stage three: the four-step key derivation, then one HMAC.
func deriveSignature(secret, dateStamp, region, sts string) string {
	k := hmacSHA256([]byte("AWS4"+secret), dateStamp)
	k = hmacSHA256(k, region)
	k = hmacSHA256(k, service)
	k = hmacSHA256(k, "aws4_request")
	return hex.EncodeToString(hmacSHA256(k, sts))
}

func credentialScope(dateStamp, region string) string {
	return dateStamp + "/" + region + "/" + service + "/aws4_request"
}

// Store is a single R2 bucket. Zero value is unusable; use New.
type Store struct {
	AccessKey  string
	SecretKey  string
	Region     string // "auto" for R2
	Endpoint   string // scheme://host, no trailing slash
	Bucket     string
	PublicBase string // public read origin; not used for signing
	HTTP       *http.Client
}

// New builds a Store for a Cloudflare R2 bucket. accountID selects the endpoint;
// R2 ignores region but SigV4 requires one, and R2 expects the literal "auto".
func New(accountID, bucket, accessKey, secretKey, publicBase string) *Store {
	return &Store{
		AccessKey:  accessKey,
		SecretKey:  secretKey,
		Region:     "auto",
		Endpoint:   "https://" + accountID + ".r2.cloudflarestorage.com",
		Bucket:     bucket,
		PublicBase: strings.TrimSuffix(publicBase, "/"),
		HTTP:       &http.Client{Timeout: 10 * time.Second},
	}
}

// Configured reports whether this Store can talk to R2 at all. Media is a
// convenience layer (Law 21's reasoning): an unconfigured deployment must boot
// and run every queue workflow, with media calls failing cleanly instead.
func (s *Store) Configured() bool {
	return s != nil && s.AccessKey != "" && s.SecretKey != "" && s.Bucket != "" && s.Endpoint != ""
}

// host returns the endpoint's host, which is what the canonical request signs.
func (s *Store) host() string {
	return strings.TrimPrefix(strings.TrimPrefix(s.Endpoint, "https://"), "http://")
}

// canonicalURI is path-style: /{bucket}/{key}. Slashes inside the key are path
// separators and are preserved.
func (s *Store) canonicalURI(key string) string {
	return "/" + s3URIEncode(s.Bucket, false) + "/" + s3URIEncode(key, false)
}

// PresignPut returns a URL a browser can PUT the object bytes to directly. The
// droplet never sees those bytes — that is the entire point of this package.
//
// now is an explicit parameter rather than time.Now() so the golden vectors are
// writable at all; a signer that reads the clock internally cannot be tested
// against a fixed expected signature.
func (s *Store) PresignPut(key string, ttl time.Duration, now time.Time) (string, error) {
	if !s.Configured() {
		return "", ErrNotConfigured
	}
	return s.presign(http.MethodPut, key, ttl, now), nil
}

func (s *Store) presign(method, key string, ttl time.Duration, now time.Time) string {
	now = now.UTC()
	dateStamp := now.Format(dateStampFormat)
	scope := credentialScope(dateStamp, s.Region)

	q := url.Values{
		"X-Amz-Algorithm":     {algorithm},
		"X-Amz-Credential":    {s.AccessKey + "/" + scope},
		"X-Amz-Date":          {now.Format(amzDateFormat)},
		"X-Amz-Expires":       {strconv.Itoa(int(ttl.Seconds()))},
		"X-Amz-SignedHeaders": {"host"},
	}
	cq := canonicalQuery(q)
	uri := s.canonicalURI(key)

	creq, _ := canonicalRequest(method, uri, cq, map[string]string{"host": s.host()}, unsignedPayload)
	sig := deriveSignature(s.SecretKey, dateStamp, s.Region, stringToSign(now, scope, creq))

	return s.Endpoint + uri + "?" + cq + "&X-Amz-Signature=" + sig
}

// signedRequest builds a header-signed (not query-signed) request, which is what
// HEAD and DELETE use.
func (s *Store) signedRequest(method, key string, now time.Time) (*http.Request, error) {
	now = now.UTC()
	dateStamp := now.Format(dateStampFormat)
	scope := credentialScope(dateStamp, s.Region)
	uri := s.canonicalURI(key)

	headers := map[string]string{
		"host":                 s.host(),
		"x-amz-content-sha256": emptyPayloadHash,
		"x-amz-date":           now.Format(amzDateFormat),
	}
	creq, signedHeaders := canonicalRequest(method, uri, "", headers, emptyPayloadHash)
	sig := deriveSignature(s.SecretKey, dateStamp, s.Region, stringToSign(now, scope, creq))

	req, err := http.NewRequest(method, s.Endpoint+uri, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Amz-Content-Sha256", emptyPayloadHash)
	req.Header.Set("X-Amz-Date", now.Format(amzDateFormat))
	req.Header.Set("Authorization", fmt.Sprintf(
		"%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		algorithm, s.AccessKey, scope, signedHeaders, sig))
	return req, nil
}

// ObjectInfo is what a HEAD tells us. ETag is logged at commit but deliberately
// not stored: media_assets has no column for it and 003 is frozen.
type ObjectInfo struct {
	ContentLength int64
	ContentType   string
	ETag          string
}

// Head reports on an object. ErrNotFound means the browser never completed its
// upload, which is an ordinary outcome, not a failure of this package.
func (s *Store) Head(key string) (ObjectInfo, error) {
	if !s.Configured() {
		return ObjectInfo{}, ErrNotConfigured
	}
	req, err := s.signedRequest(http.MethodHead, key, time.Now())
	if err != nil {
		return ObjectInfo{}, err
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return ObjectInfo{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body) // HEAD has no body; drain to reuse the conn

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return ObjectInfo{}, ErrNotFound
	case resp.StatusCode >= 400:
		return ObjectInfo{}, fmt.Errorf("%w: HEAD %d", ErrUnavailable, resp.StatusCode)
	}
	return ObjectInfo{
		ContentLength: resp.ContentLength,
		ContentType:   resp.Header.Get("Content-Type"),
		ETag:          strings.Trim(resp.Header.Get("ETag"), `"`),
	}, nil
}

// Delete removes an object. A 404 is success: the reaper deletes from R2 before
// deleting the row, so a crash between the two leaves a row whose object is
// already gone, and the retry must converge rather than wedge.
func (s *Store) Delete(key string) error {
	if !s.Configured() {
		return ErrNotConfigured
	}
	req, err := s.signedRequest(http.MethodDelete, key, time.Now())
	if err != nil {
		return err
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 400 && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("%w: DELETE %d", ErrUnavailable, resp.StatusCode)
	}
	return nil
}

// PublicURL is where a committed object is readable. Keys are content-hashed, so
// objects are immutable and cacheable forever.
func (s *Store) PublicURL(key string) string { return s.PublicBase + "/" + key }
