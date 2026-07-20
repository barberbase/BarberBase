package bhejna

import "testing"

// S3 fix: Meta rejects bare "en" — every producer hardcodes it, so the client
// normalizes at the single serialization choke point.
func TestNormalizeLanguage(t *testing.T) {
	for in, want := range map[string]string{
		"":      "en_US",
		"en":    "en_US",
		"en_US": "en_US",
		"hi":    "hi",
	} {
		if got := normalizeLanguage(in); got != want {
			t.Errorf("normalizeLanguage(%q) = %q, want %q", in, got, want)
		}
	}
}
