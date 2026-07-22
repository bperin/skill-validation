package attestation

import (
	"testing"
	"time"
)

func TestHolderBindingAllowsOnlyGrantedCredentialScopeBeforeExpiry(t *testing.T) {
	now := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	binding := HolderBinding{
		CredentialID: "credential-123",
		HolderKeyID:  "holder-key-abc",
		Scopes: []HolderScope{
			HolderScopePresentCredential,
			HolderScopeBindProjectEvidence,
		},
		ExpiresAt: now.Add(time.Hour),
	}

	if !binding.Allows(HolderScopeBindProjectEvidence, "credential-123", now) {
		t.Fatal("holder binding should allow its granted project evidence scope")
	}
	if binding.Allows(HolderScopeIssueCredential, "credential-123", now) {
		t.Fatal("holder binding must never grant credential issuance")
	}
	if binding.Allows(HolderScopePresentCredential, "other-credential", now) {
		t.Fatal("holder binding must not apply to another credential")
	}
	if binding.Allows(HolderScopePresentCredential, "credential-123", now.Add(time.Hour)) {
		t.Fatal("holder binding must expire")
	}
}
