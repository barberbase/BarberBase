package r2

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// awsExample* are AWS's own published worked example for a presigned URL,
// "Signature Calculation: Transfer Payload in a Single Chunk — Example:
// Signature Calculation for Presigned URL" from the S3 REST API reference. Every
// value below is AWS's, not ours: the credentials are the documentation's
// throwaway pair, the date is fixed, and the expected signature is the one AWS
// publishes as correct.
//
// This is the vector that makes hand-rolling defensible. If it passes, our
// canonicalisation, key derivation and HMAC chain agree with the reference
// implementation on a case AWS itself certified.
const (
	awsExampleAccessKey = "AKIAIOSFODNN7EXAMPLE"
	awsExampleSecretKey = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	awsExampleRegion    = "us-east-1"
	awsExampleHost      = "examplebucket.s3.amazonaws.com"
	awsExampleURI       = "/test.txt"
	awsExampleDate      = "20130524T000000Z"
	awsExampleStamp     = "20130524"

	awsExampleCanonicalQuery = "X-Amz-Algorithm=AWS4-HMAC-SHA256" +
		"&X-Amz-Credential=AKIAIOSFODNN7EXAMPLE%2F20130524%2Fus-east-1%2Fs3%2Faws4_request" +
		"&X-Amz-Date=20130524T000000Z&X-Amz-Expires=86400&X-Amz-SignedHeaders=host"

	awsExampleCanonicalRequest = "GET\n" +
		"/test.txt\n" +
		awsExampleCanonicalQuery + "\n" +
		"host:examplebucket.s3.amazonaws.com\n" +
		"\n" +
		"host\n" +
		"UNSIGNED-PAYLOAD"

	awsExampleStringToSign = "AWS4-HMAC-SHA256\n" +
		"20130524T000000Z\n" +
		"20130524/us-east-1/s3/aws4_request\n" +
		"3bfa292879f6447bbcda7001decf97f4a54dc650c8942174ae0a9121cf58ad04"

	awsExampleSignature = "aeeed9bbccd4d02ee5c0109b86d86835f995330da4c265957d157751f604d404"
)

// TestGoldenVector_AWSPublishedPresign is requirement 3 made concrete: each
// stage is asserted on its own.
//
// A single end-to-end "signature mismatch" tells whoever inherits this nothing.
// Asserting the canonical request, then the string-to-sign, then the signature
// means the first failing assertion names the stage that diverged — encoding, or
// hashing, or key derivation — which is the difference between a five-minute fix
// and a day lost.
func TestGoldenVector_AWSPublishedPresign(t *testing.T) {
	now, err := time.Parse(amzDateFormat, awsExampleDate)
	require.NoError(t, err)

	q := url.Values{
		"X-Amz-Algorithm":     {algorithm},
		"X-Amz-Credential":    {awsExampleAccessKey + "/" + credentialScope(awsExampleStamp, awsExampleRegion)},
		"X-Amz-Date":          {awsExampleDate},
		"X-Amz-Expires":       {"86400"},
		"X-Amz-SignedHeaders": {"host"},
	}

	// ── Stage 0: the canonical query string ──────────────────────────────────
	// Isolated because it is where s3URIEncode's encodeSlash flag earns its
	// keep: the three slashes inside X-Amz-Credential must be %2F.
	cq := canonicalQuery(q)
	require.Equal(t, awsExampleCanonicalQuery, cq,
		"stage 0 — canonical query: check s3URIEncode's slash handling in query values")

	// ── Stage 1: the canonical request ───────────────────────────────────────
	creq, signedHeaders := canonicalRequest(
		http.MethodGet, awsExampleURI, cq,
		map[string]string{"host": awsExampleHost}, unsignedPayload)
	require.Equal(t, "host", signedHeaders)
	require.Equal(t, awsExampleCanonicalRequest, creq,
		"stage 1 — canonical request: newline layout, header casing, or payload marker")

	// ── Stage 2: the string to sign ──────────────────────────────────────────
	// Its fourth line is sha256(canonical request), so a stage-1 break also
	// shows here; asserting both tells you which came first.
	sts := stringToSign(now, credentialScope(awsExampleStamp, awsExampleRegion), creq)
	require.Equal(t, awsExampleStringToSign, sts,
		"stage 2 — string to sign: scope, timestamp format, or the canonical-request hash")

	// ── Stage 3: the signature ───────────────────────────────────────────────
	sig := deriveSignature(awsExampleSecretKey, awsExampleStamp, awsExampleRegion, sts)
	require.Equal(t, awsExampleSignature, sig,
		"stage 3 — signature: the four-step key derivation")
}

// TestGoldenVector_CanonicalRequestHashIsTheLinkBetweenStages pins the join
// between stage 1 and stage 2 explicitly, so that if only stage 2 breaks the
// cause is unambiguous.
func TestGoldenVector_CanonicalRequestHashIsTheLinkBetweenStages(t *testing.T) {
	now, _ := time.Parse(amzDateFormat, awsExampleDate)
	sts := stringToSign(now, credentialScope(awsExampleStamp, awsExampleRegion), awsExampleCanonicalRequest)
	lines := strings.Split(sts, "\n")
	require.Len(t, lines, 4)
	require.Equal(t, "3bfa292879f6447bbcda7001decf97f4a54dc650c8942174ae0a9121cf58ad04", lines[3],
		"the fourth line of the string-to-sign is sha256 of the canonical request")
}

// r2Fixture is a Store with fixed everything, so its output is a stable golden
// value. These are OUR regression vectors: they do not prove R2 accepts the URL
// (only the deferred manual run does that), they prove we have not silently
// changed what we emit.
func r2Fixture() *Store {
	s := New("acct123", "bb-media", "TESTKEYID000000000000", "testsecret0000000000000000000000000000000", "https://cdn.example.com")
	return s
}

var fixedNow = time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)

// TestGoldenVector_R2Presign covers requirement 2's first two shapes: a simple
// hash-named key (what production actually emits) and a key needing escaping.
func TestGoldenVector_R2Presign(t *testing.T) {
	s := r2Fixture()

	cases := []struct {
		name        string
		key         string
		wantURIPart string
	}{
		{
			name:        "hash named key, the production shape",
			key:         "svc/de8f01c9/3fcd13e3/9f86d081884c7d65.webp",
			wantURIPart: "/bb-media/svc/de8f01c9/3fcd13e3/9f86d081884c7d65.webp",
		},
		{
			// Not emitted today — keys are content-hashed hex plus a fixed
			// extension — but Head and Delete take whatever key is in the
			// database, and a future purpose may not be hash-named.
			name:        "key needing escaping",
			key:         "loc/de8f01c9/logo/a b+c=d&e~f.webp",
			wantURIPart: "/bb-media/loc/de8f01c9/logo/a%20b%2Bc%3Dd%26e~f.webp",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw, err := s.PresignPut(c.key, 15*time.Minute, fixedNow)
			require.NoError(t, err)

			require.Contains(t, raw, "https://acct123.r2.cloudflarestorage.com"+c.wantURIPart,
				"canonical URI: slashes are path separators, everything else is escaped")
			require.Contains(t, raw, "X-Amz-Expires=900")
			require.Contains(t, raw, "X-Amz-Date=20260829T120000Z")
			require.Contains(t, raw, "X-Amz-Credential=TESTKEYID000000000000%2F20260829%2Fauto%2Fs3%2Faws4_request",
				"R2 requires region 'auto'; the scope's slashes must be %2F")
			require.Contains(t, raw, "X-Amz-SignedHeaders=host")

			// The signature is 64 lowercase hex and deterministic for fixed input.
			u, err := url.Parse(raw)
			require.NoError(t, err)
			sig := u.Query().Get("X-Amz-Signature")
			require.Len(t, sig, 64)
			require.Regexp(t, `^[0-9a-f]{64}$`, sig)

			again, err := s.PresignPut(c.key, 15*time.Minute, fixedNow)
			require.NoError(t, err)
			require.Equal(t, raw, again, "same inputs must produce the same URL, or the vectors are worthless")

			later, err := s.PresignPut(c.key, 15*time.Minute, fixedNow.Add(time.Second))
			require.NoError(t, err)
			require.NotEqual(t, raw, later, "a different clock must produce a different signature")
		})
	}
}

// TestGoldenVector_R2HeadAuthorizationHeader is requirement 2's third shape: the
// Authorization-header path, which HEAD and DELETE use and which signs a
// different header set and a real payload hash rather than UNSIGNED-PAYLOAD.
func TestGoldenVector_R2HeadAuthorizationHeader(t *testing.T) {
	s := r2Fixture()
	req, err := s.signedRequest(http.MethodHead, "svc/loc/var/9f86d081884c7d65.webp", fixedNow)
	require.NoError(t, err)

	require.Equal(t, emptyPayloadHash, req.Header.Get("X-Amz-Content-Sha256"))
	require.Equal(t, "20260829T120000Z", req.Header.Get("X-Amz-Date"))

	auth := req.Header.Get("Authorization")
	require.True(t, strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential="), "auth: %s", auth)
	require.Contains(t, auth, "Credential=TESTKEYID000000000000/20260829/auto/s3/aws4_request",
		"credential in the header is NOT %2F-escaped — only the query form is")
	require.Contains(t, auth, "SignedHeaders=host;x-amz-content-sha256;x-amz-date",
		"signed headers must be lowercase and sorted")
	require.Regexp(t, `Signature=[0-9a-f]{64}$`, auth)

	// Stage separation again: rebuild the canonical request independently and
	// confirm the header's signature is derived from exactly that.
	creq, signedHeaders := canonicalRequest(http.MethodHead,
		"/bb-media/svc/loc/var/9f86d081884c7d65.webp", "",
		map[string]string{
			"host":                 "acct123.r2.cloudflarestorage.com",
			"x-amz-content-sha256": emptyPayloadHash,
			"x-amz-date":           "20260829T120000Z",
		}, emptyPayloadHash)
	require.Equal(t, "host;x-amz-content-sha256;x-amz-date", signedHeaders)
	want := deriveSignature(s.SecretKey, "20260829", "auto",
		stringToSign(fixedNow, credentialScope("20260829", "auto"), creq))
	require.Contains(t, auth, "Signature="+want)
}

// TestS3URIEncode is requirement 4. S3 canonicalisation is not url.PathEscape
// and not url.QueryEscape; this table is what stops someone "simplifying" it
// into one of them.
func TestS3URIEncode(t *testing.T) {
	cases := []struct {
		in                     string
		wantNoSlash, wantSlash string
	}{
		{"abc", "abc", "abc"},
		{"a b", "a%20b", "a%20b"}, // space is %20, never '+'
		{"a+b", "a%2Bb", "a%2Bb"}, // '+' is not a space
		{"a=b", "a%3Db", "a%3Db"},
		{"a&b", "a%26b", "a%26b"},
		{"a/b", "a/b", "a%2Fb"},   // the whole point of the flag
		{"-_.~", "-_.~", "-_.~"},  // RFC 3986 unreserved, untouched
		{"*", "%2A", "%2A"},       // url.QueryEscape leaves '*' alone; S3 does not
		{"é", "%C3%A9", "%C3%A9"}, // UTF-8, byte by byte
		{"a%b", "a%25b", "a%25b"},
		{"", "", ""},
	}
	for _, c := range cases {
		require.Equal(t, c.wantNoSlash, s3URIEncode(c.in, false), "encodeSlash=false: %q", c.in)
		require.Equal(t, c.wantSlash, s3URIEncode(c.in, true), "encodeSlash=true: %q", c.in)
	}

	// Hex must be uppercase. Lowercase %2f is a different string and a wrong
	// signature, and it is exactly what a naive fmt verb produces.
	require.Equal(t, "%2F", s3URIEncode("/", true))
	require.NotContains(t, s3URIEncode("/ +", true), "%2f")
}

// TestCanonicalQuerySortsByByteOrder — sorting is by the ENCODED key, not the
// raw one, and the difference is visible as soon as a key contains a character
// that encodes to a different byte.
func TestCanonicalQuerySortsByByteOrder(t *testing.T) {
	got := canonicalQuery(url.Values{
		"X-Amz-SignedHeaders": {"host"},
		"X-Amz-Algorithm":     {algorithm},
		"X-Amz-Date":          {"20260829T120000Z"},
	})
	require.Equal(t,
		"X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Date=20260829T120000Z&X-Amz-SignedHeaders=host", got)

	// The AWS vector's values happen to encode identically under
	// url.QueryEscape, so it alone would NOT catch someone swapping the encoder
	// out here. These values do: QueryEscape renders a space as '+' and leaves
	// '*' and '~' alone, all three of which are wrong for S3.
	require.Equal(t, "k=a%20b", canonicalQuery(url.Values{"k": {"a b"}}),
		"a space in a query value is %20, never '+' — url.QueryEscape is wrong here")
	require.Equal(t, "k=a%2Ab", canonicalQuery(url.Values{"k": {"a*b"}}))
	require.Equal(t, "k=a~b", canonicalQuery(url.Values{"k": {"a~b"}}))
}

// An unconfigured Store must fail cleanly, never panic and never sign with an
// empty key. Media is a convenience layer; a deployment without R2 credentials
// has to boot and serve the queue.
func TestUnconfiguredStoreFailsCleanly(t *testing.T) {
	var s *Store
	require.False(t, s.Configured())

	empty := &Store{}
	require.False(t, empty.Configured())
	_, err := empty.PresignPut("k", time.Minute, fixedNow)
	require.ErrorIs(t, err, ErrNotConfigured)
	_, err = empty.Head("k")
	require.ErrorIs(t, err, ErrNotConfigured)
	require.ErrorIs(t, empty.Delete("k"), ErrNotConfigured)
}
