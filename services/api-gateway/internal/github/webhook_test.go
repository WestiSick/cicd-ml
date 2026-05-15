package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestVerifySignature(t *testing.T) {
	body := []byte(`{"action":"requested"}`)
	secret := "topsecret"

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	good := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if !VerifySignature(secret, body, good) {
		t.Fatal("valid signature rejected")
	}
	if VerifySignature(secret, body, "sha256=deadbeef") {
		t.Fatal("invalid signature accepted")
	}
	if VerifySignature(secret, body, "") {
		t.Fatal("empty header accepted")
	}
	if VerifySignature(secret, body, "wrong=prefix") {
		t.Fatal("wrong prefix accepted")
	}
	// Empty secret bypass — for local dev.
	if !VerifySignature("", body, "sha256=deadbeef") {
		t.Fatal("empty secret should pass-through")
	}
}
