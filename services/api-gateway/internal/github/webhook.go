package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io"
	"strings"
)

// VerifySignature validates the X-Hub-Signature-256 header value against
// the raw request body using GitHub's HMAC-SHA256 scheme.
//
// Returns false on any of: empty header, missing prefix, wrong length,
// or mismatch. The constant-time comparison prevents timing attacks.
//
// If `secret` is empty the function returns true unconditionally — useful
// for local development where webhooks are sent without a secret. In prod
// the API gateway refuses to start if GITHUB_WEBHOOK_SECRET is empty.
func VerifySignature(secret string, body []byte, signatureHeader string) bool {
	if secret == "" {
		return true
	}
	if !strings.HasPrefix(signatureHeader, "sha256=") {
		return false
	}
	want, err := hex.DecodeString(strings.TrimPrefix(signatureHeader, "sha256="))
	if err != nil {
		return false
	}
	var mac hash.Hash = hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	got := mac.Sum(nil)
	return hmac.Equal(got, want)
}

// DrainBody is a tiny helper for handlers that need to both verify the
// signature (which needs the bytes) and decode into a struct.
func DrainBody(r io.Reader, maxBytes int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, maxBytes))
}
