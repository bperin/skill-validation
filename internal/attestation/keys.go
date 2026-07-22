package attestation

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Scope bounds what a delegated issuer key may attest.
//
// Important trust model: this is an issuer-side permission, not a learner
// permission. A developer receives a credential after evaluation; they never
// receive a root or child issuer private key.
type Scope string

const (
	ScopeGitHubDeployment Scope = "github-deployment"
	ScopeWebPage          Scope = "web-page"
)

type ChildKey struct {
	ParentKeyID   string
	Scope         Scope
	SubjectID     string
	SkillID       string
	PolicyVersion string
	ExpiresAt     time.Time
	// Signer is intentionally only a local POC convenience. It must not be
	// serialized, returned from a production issuer API, put in a credential,
	// or held by the learner. Production replaces this with a non-exportable
	// KMS/HSM key reference owned by the issuer service.
	Signer  *LocalSigner
	revoked bool
}

func (k ChildKey) KeyID() string { return k.Signer.KeyID() }

// DelegationSpec narrows an issuer child key to one person, skill definition,
// and expiry. These fields become the input to a root signed delegation record
// in a production implementation. The local authority stores them in memory so
// callers can exercise the same permission boundary before a KMS adapter is
// introduced.
type DelegationSpec struct {
	Scope         Scope
	SubjectID     string
	SkillID       string
	PolicyVersion string
	ExpiresAt     time.Time
}

// KeyAuthority is the local representation of the issuer's root-to-child key
// tree. The root delegates narrowly scoped child keys to issuer-controlled
// services, for example a GitHub Actions issuer or a hosted evaluation service.
//
// It is not a wallet for the learner and it does not give a learner permission
// to mint a skill. The corresponding Solidity registry records the same
// parent/revocation state for public verification.
//
// POC LIMIT: delegation is in-memory metadata. A production authority must
// persist a root-signed delegation certificate containing the child public key,
// key ID, scope, expiry, and revocation reference.
type KeyAuthority struct {
	mu          sync.RWMutex
	root        *LocalSigner
	rootRevoked bool
	children    map[string]ChildKey
}

func NewKeyAuthority(root *LocalSigner) *KeyAuthority {
	return &KeyAuthority{root: root, children: make(map[string]ChildKey)}
}

func (a *KeyAuthority) Delegate(ctx context.Context, scope Scope) (ChildKey, error) {
	return a.delegate(ctx, DelegationSpec{Scope: scope}, false)
}

// DelegateFor creates an issuer owned child signer restricted to one subject,
// skill, policy version, and expiry. It returns a server side handle only; a
// credential holder must never receive the Signer field or a private key.
func (a *KeyAuthority) DelegateFor(ctx context.Context, specification DelegationSpec) (ChildKey, error) {
	return a.delegate(ctx, specification, true)
}

func (a *KeyAuthority) delegate(ctx context.Context, specification DelegationSpec, requireBoundScope bool) (ChildKey, error) {
	if err := ctx.Err(); err != nil {
		return ChildKey{}, err
	}
	if specification.Scope != ScopeGitHubDeployment && specification.Scope != ScopeWebPage {
		return ChildKey{}, fmt.Errorf("unsupported child-key scope %q", specification.Scope)
	}
	if requireBoundScope && (specification.SubjectID == "" || specification.SkillID == "" || specification.PolicyVersion == "" || specification.ExpiresAt.IsZero()) {
		return ChildKey{}, errors.New("subject ID, skill ID, policy version, and expiry are required for a scoped delegation")
	}
	if requireBoundScope && !specification.ExpiresAt.After(time.Now().UTC()) {
		return ChildKey{}, errors.New("scoped delegation expiry must be in the future")
	}
	a.mu.RLock()
	rootAvailable := a.root != nil && !a.rootRevoked
	rootID := ""
	if a.root != nil {
		rootID = a.root.KeyID()
	}
	a.mu.RUnlock()
	if !rootAvailable {
		return ChildKey{}, errors.New("root issuer key is unavailable")
	}
	childID, err := delegatedKeyID(rootID)
	if err != nil {
		return ChildKey{}, err
	}
	signer, err := NewLocalSigner(childID)
	if err != nil {
		return ChildKey{}, err
	}
	// The issuer creates and keeps this child signer. The returned ChildKey is a
	// server-side handle in this POC, not an object that should be sent to the
	// credential holder.
	child := ChildKey{
		ParentKeyID:   rootID,
		Scope:         specification.Scope,
		SubjectID:     specification.SubjectID,
		SkillID:       specification.SkillID,
		PolicyVersion: specification.PolicyVersion,
		ExpiresAt:     specification.ExpiresAt,
		Signer:        signer,
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.rootRevoked {
		return ChildKey{}, errors.New("root issuer key was revoked during delegation")
	}
	a.children[child.KeyID()] = child
	return child, nil
}

func (a *KeyAuthority) CanIssue(keyID string, scope Scope) bool {
	// Callers should use this gate before a child issuer signs. A verifier does
	// not call this method: it verifies the credential using public key material,
	// the delegation record, and revocation state.
	//
	// Credential issuers should call KeyAuthority.Issue rather than the low level
	// attestation Issue helper so this authorization state is enforced.
	return a.CanIssueFor(keyID, scope, "", "", "", time.Now().UTC())
}

// CanIssueFor reports whether a child is active for the requested person,
// skill, policy version, and time. Legacy unbound children are accepted only
// by CanIssue; new issuance flows should use a DelegateFor child and this
// stricter method.
func (a *KeyAuthority) CanIssueFor(keyID string, scope Scope, subjectID, skillID, policyVersion string, now time.Time) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.canIssueForLocked(keyID, scope, subjectID, skillID, policyVersion, now)
}

func (a *KeyAuthority) canIssueForLocked(keyID string, scope Scope, subjectID, skillID, policyVersion string, now time.Time) bool {
	if a.root == nil || a.rootRevoked {
		return false
	}
	child, ok := a.children[keyID]
	if !ok || child.revoked || child.Scope != scope {
		return false
	}
	if child.SubjectID == "" {
		return subjectID == "" && skillID == "" && policyVersion == ""
	}
	return child.SubjectID == subjectID && child.SkillID == skillID && child.PolicyVersion == policyVersion && now.Before(child.ExpiresAt)
}

// Issue selects an issuer owned child signer and enforces its delegation before
// creating a credential. This is the issuance entry point for a future HTTP or
// workflow adapter. It never accepts a signer or private key from a caller.
func (a *KeyAuthority) Issue(ctx context.Context, keyID string, scope Scope, request IssueRequest) (Credential, error) {
	if err := ctx.Err(); err != nil {
		return Credential{}, err
	}
	a.mu.RLock()
	child, ok := a.children[keyID]
	allowed := a.canIssueForLocked(keyID, scope, request.LearnerID, request.SkillID, request.PolicyVersion, time.Now().UTC())
	a.mu.RUnlock()
	if !ok || !allowed {
		return Credential{}, errors.New("issuer child is not authorized for this credential")
	}
	return Issue(ctx, child.Signer, request)
}

func (a *KeyAuthority) Revoke(keyID string) error {
	// Revoking a child stops that issuer channel without invalidating unrelated
	// child keys. A production implementation also publishes durable revocation
	// state so outside verifiers can reject future or previously issued records
	// according to policy.
	a.mu.Lock()
	defer a.mu.Unlock()
	child, ok := a.children[keyID]
	if !ok {
		return fmt.Errorf("delegated key %q not found", keyID)
	}
	child.revoked = true
	a.children[keyID] = child
	return nil
}

func (a *KeyAuthority) RevokeRoot() {
	// Root revocation is the emergency stop: every child becomes unauthorized.
	a.mu.Lock()
	defer a.mu.Unlock()
	a.rootRevoked = true
}

func delegatedKeyID(rootID string) (string, error) {
	// This random suffix creates a unique POC identifier. It is not  
	// deterministic key derivation and does not prove authorization by itself.
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", fmt.Errorf("generate delegated-key nonce: %w", err)
	}
	return rootID + "/child/" + base64.RawURLEncoding.EncodeToString(nonce[:]), nil
}
