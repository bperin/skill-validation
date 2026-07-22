package attestation

import (
	"context"
	"time"
)

// HolderScope is a capability granted to a developer-controlled holder key.
// It is deliberately separate from Scope, which grants an issuer service the
// ability to issue a credential.
type HolderScope string

const (
	// HolderScopePresentCredential lets a holder answer a verifier's fresh
	// challenge when presenting their credential.
	HolderScopePresentCredential HolderScope = "present-credential"
	// HolderScopeBindProjectEvidence lets a holder bind a repository commit or
	// deployment to the credential it was granted for.
	HolderScopeBindProjectEvidence HolderScope = "bind-project-evidence"
	// HolderScopeIssueCredential is intentionally not a valid holder grant. It
	// exists so authorization checks can reject a request to mint a skill.
	HolderScopeIssueCredential HolderScope = "issue-credential"
)

// HolderBinding is an issuer-signed grant that binds a developer public key to
// one credential. The developer generates and retains their private key (for
// example in a passkey or wallet); the issuer only signs the public binding.
//
// This is the safe meaning of a "child key" after a skill is earned. It is a
// scoped child capability, not a child issuer private key. It may prove control
// of one credential or bind project evidence, but it cannot issue, update, or
// revoke a skill. Production code must verify IssuerSignature against the
// current issuer delegation before accepting the binding.
type HolderBinding struct {
	CredentialID    string
	HolderKeyID     string
	HolderPublicKey []byte
	Scopes          []HolderScope
	ExpiresAt       time.Time
	Issuer          string
	IssuerSignature []byte
}

// Allows reports whether this holder binding permits the requested action for
// its credential at the supplied time. Issuance is never a holder permission,
// even if an invalid serialized binding contains that scope.
func (binding HolderBinding) Allows(scope HolderScope, credentialID string, now time.Time) bool {
	if credentialID == "" || binding.CredentialID != credentialID || binding.HolderKeyID == "" || !binding.ExpiresAt.After(now) {
		return false
	}
	if scope == HolderScopeIssueCredential {
		return false
	}
	for _, granted := range binding.Scopes {
		if granted == scope {
			return true
		}
	}
	return false
}

// HolderProof binds a presentation of a public credential to a developer
// controlled key. It is intentionally separate from issuer authority: a holder
// can prove control of a credential but cannot issue skills.
type HolderProof struct {
	CredentialID string
	HolderKeyID  string
	Nonce        []byte
	Signature    []byte
}

// HolderProofVerifier is the boundary for wallet, passkey, or DID based holder
// verification. It must first verify the issuer-signed HolderBinding and then
// bind a fresh nonce and credential ID to the holder signature to prevent
// replay. No holder private key belongs in an issuer service or credential
// payload.
type HolderProofVerifier interface {
	VerifyHolderProof(ctx context.Context, binding HolderBinding, proof HolderProof) error
}
