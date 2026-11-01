package http

import (
	"strings"
	"testing"
	"time"

	"github.com/Lephiziel/findrail/internal/evidencebundle"
)

func TestEvidenceBundleClaimBindsDigestAndExpires(t *testing.T) {
	b := evidencebundle.Bundle{Title: "synthetic claim", Sources: []evidencebundle.Source{}, Entries: []evidencebundle.Entry{}}
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	secret := []byte(strings.Repeat("k", 32))
	mappings := map[string]string{}
	token, err := signEvidenceBundleClaim(secret, b, mappings, "registration-binding", now.Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	claim, err := verifyEvidenceBundleClaim(secret, token, b, now)
	if err != nil || claim.Binding != "registration-binding" {
		t.Fatalf("valid claim=%+v err=%v", claim, err)
	}
	if _, err = verifyEvidenceBundleClaim(secret, token, b, now.Add(5*time.Minute+time.Second)); err == nil {
		t.Fatal("expired claim accepted")
	}
	if _, err = verifyEvidenceBundleClaim(secret, token, b, now.Add(5*time.Minute)); err == nil {
		t.Fatal("claim accepted at its exact expiry")
	}
	tampered := "A" + token[1:]
	if token[0] == 'A' {
		tampered = "B" + token[1:]
	}
	if _, err = verifyEvidenceBundleClaim(secret, tampered, b, now); err == nil {
		t.Fatal("tampered claim accepted")
	}
	other := b
	other.Title = "another bundle"
	if _, err = verifyEvidenceBundleClaim(secret, token, other, now); err == nil {
		t.Fatal("cross-bundle claim accepted")
	}
}

func TestEvidenceBundleClaimLimitsBeforeAcceptingOrIssuing(t *testing.T) {
	b := evidencebundle.Bundle{Title: "Synthetic bounded metadata"}
	secret := []byte(strings.Repeat("k", 32))
	if _, err := signEvidenceBundleClaim(secret, b, map[string]string{"s1": strings.Repeat("x", 32<<10)}, "binding", time.Now().Add(time.Minute)); err == nil {
		t.Fatal("issued a claim larger than the import limit")
	}
	if _, err := verifyEvidenceBundleClaim(secret, strings.Repeat(".", 4<<20), b, time.Now()); err == nil {
		t.Fatal("oversized raw claim accepted")
	}
}
