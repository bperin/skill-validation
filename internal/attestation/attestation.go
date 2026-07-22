// Package attestation issues and verifies portable COMPANY project credentials.
//
// A credential holder receives the signed credential only. The issuer keeps the
// root and delegated child signing keys; public verifiers receive only public
// key and status material needed to validate the credential.
package attestation

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	credentialContext = "https://www.w3.org/ns/credentials/v2"
	credentialType    = "ProjectCredential"
	proofSuite        = "company-p256-sha256"
)

type Execution string

const (
	ExecutionPass Execution = "PASS"
	ExecutionFail Execution = "FAIL"
)

type IssueRequest struct {
	LearnerID       string
	ProjectName     string
	SkillID         string
	PolicyVersion   string
	EvidenceBinding string
	EvaluationScore int
	IssuedAt        time.Time
	AgentName       string
	Execution       Execution
	Issuer          string
}

type CredentialSubject struct {
	ID              string    `json:"id"`
	LearnerID       string    `json:"learnerId"`
	ProjectName     string    `json:"projectName"`
	SkillID         string    `json:"skillId,omitempty"`
	PolicyVersion   string    `json:"policyVersion,omitempty"`
	EvidenceBinding string    `json:"evidenceBinding,omitempty"`
	EvaluationScore int       `json:"evaluationScore"`
	AgentName       string    `json:"agentName"`
	Execution       Execution `json:"execution"`
}

type Proof struct {
	Type               string `json:"type"`
	Cryptosuite        string `json:"cryptosuite"`
	Created            string `json:"created"`
	VerificationMethod string `json:"verificationMethod"`
	ProofPurpose       string `json:"proofPurpose"`
	ProofValue         string `json:"proofValue"`
}

// Credential is deliberately W3C VC-shaped. Its proof suite is an explicit
// COMPANY MVP suite until RFC 8785/JCS proof canonicalization is added.
type Credential struct {
	Context           []string          `json:"@context"`
	ID                string            `json:"id"`
	Type              []string          `json:"type"`
	Issuer            string            `json:"issuer"`
	ValidFrom         string            `json:"validFrom"`
	CredentialSubject CredentialSubject `json:"credentialSubject"`
	Proof             Proof             `json:"proof"`
}

type LocalSigner struct {
	keyID string
	key   *ecdsa.PrivateKey
}

// DigestSigner signs a fixed SHA 256 digest without exposing the issuer's
// private key to the credential issuer. LocalSigner implements this only for
// tests and local development. Production implementations should delegate the
// operation to a nonexportable KMS or HSM key.
type DigestSigner interface {
	KeyID() string
	SignDigest(ctx context.Context, digest []byte) ([]byte, error)
}

// IssuerKeyStore is the infrastructure boundary for a production issuer key
// service. Its implementation owns key creation, signing, public key lookup,
// and revocation. It must never return private key material to this package or
// to a credential holder.
//
// A cloud KMS or HSM adapter should create a child key for each approved
// DelegationSpec, keep the returned key reference server side, and enforce its
// policy with the provider's IAM and audit controls.
type IssuerKeyStore interface {
	CreateChild(ctx context.Context, delegation DelegationSpec) (keyID string, err error)
	SignDigest(ctx context.Context, keyID string, digest []byte) ([]byte, error)
	PublicKey(ctx context.Context, keyID string) (*ecdsa.PublicKey, error)
	Revoke(ctx context.Context, keyID string) error
}

func NewLocalSigner(keyID string) (*LocalSigner, error) {
	if strings.TrimSpace(keyID) == "" {
		return nil, errors.New("key ID is required")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate P-256 key: %w", err)
	}
	return &LocalSigner{keyID: keyID, key: key}, nil
}

func (s *LocalSigner) KeyID() string { return s.keyID }

func (s *LocalSigner) PublicKey() *ecdsa.PublicKey { return &s.key.PublicKey }

func (s *LocalSigner) SignDigest(_ context.Context, digest []byte) ([]byte, error) {
	if len(digest) != sha256.Size {
		return nil, fmt.Errorf("digest length = %d, want %d", len(digest), sha256.Size)
	}
	signature, err := ecdsa.SignASN1(rand.Reader, s.key, digest)
	if err != nil {
		return nil, fmt.Errorf("sign digest: %w", err)
	}
	return signature, nil
}

// Issue signs a credential with an explicitly supplied issuer signer.
//
// It is deliberately a low-level helper. Callers that issue credentials should
// use KeyAuthority.Issue so the child key, person, skill, policy, and expiry
// checks occur before signing.
func Issue(ctx context.Context, signer DigestSigner, request IssueRequest) (Credential, error) {
	if err := ctx.Err(); err != nil {
		return Credential{}, err
	}
	if signer == nil {
		return Credential{}, errors.New("signer is required")
	}
	if err := validateRequest(request); err != nil {
		return Credential{}, err
	}
	issuedAt := request.IssuedAt.UTC().Format(time.RFC3339)
	credential := Credential{
		Context:   []string{credentialContext},
		ID:        credentialID(request),
		Type:      []string{"VerifiableCredential", credentialType},
		Issuer:    request.Issuer,
		ValidFrom: issuedAt,
		CredentialSubject: CredentialSubject{
			ID:              "urn:company:learner:" + hashID(request.LearnerID),
			LearnerID:       request.LearnerID,
			ProjectName:     request.ProjectName,
			SkillID:         request.SkillID,
			PolicyVersion:   request.PolicyVersion,
			EvidenceBinding: request.EvidenceBinding,
			EvaluationScore: request.EvaluationScore,
			AgentName:       request.AgentName,
			Execution:       request.Execution,
		},
		Proof: Proof{
			Type:               "DataIntegrityProof",
			Cryptosuite:        proofSuite,
			Created:            issuedAt,
			VerificationMethod: request.Issuer + "#" + signer.KeyID(),
			ProofPurpose:       "assertionMethod",
		},
	}
	digest, err := unsignedDigest(credential)
	if err != nil {
		return Credential{}, err
	}
	signature, err := signer.SignDigest(ctx, digest[:])
	if err != nil {
		return Credential{}, fmt.Errorf("sign credential: %w", err)
	}
	credential.Proof.ProofValue = base64.RawURLEncoding.EncodeToString(signature)
	return credential, nil
}

func Verify(credential Credential, publicKey *ecdsa.PublicKey) bool {
	if publicKey == nil || publicKey.Curve != elliptic.P256() || credential.Proof.Cryptosuite != proofSuite {
		return false
	}
	signature, err := base64.RawURLEncoding.DecodeString(credential.Proof.ProofValue)
	if err != nil {
		return false
	}
	digest, err := unsignedDigest(credential)
	if err != nil {
		return false
	}
	return ecdsa.VerifyASN1(publicKey, digest[:], signature)
}

func validateRequest(request IssueRequest) error {
	if strings.TrimSpace(request.LearnerID) == "" || strings.TrimSpace(request.ProjectName) == "" || strings.TrimSpace(request.AgentName) == "" || strings.TrimSpace(request.Issuer) == "" {
		return errors.New("learner ID, project name, agent name, and issuer are required")
	}
	if request.EvaluationScore < 0 || request.EvaluationScore > 100 {
		return fmt.Errorf("evaluation score %d is outside 0-100", request.EvaluationScore)
	}
	if request.IssuedAt.IsZero() {
		return errors.New("issued time is required")
	}
	if request.Execution != ExecutionPass && request.Execution != ExecutionFail {
		return fmt.Errorf("execution %q is invalid", request.Execution)
	}
	return nil
}

func unsignedDigest(credential Credential) ([sha256.Size]byte, error) {
	type unsigned struct {
		Context           []string          `json:"@context"`
		ID                string            `json:"id"`
		Type              []string          `json:"type"`
		Issuer            string            `json:"issuer"`
		ValidFrom         string            `json:"validFrom"`
		CredentialSubject CredentialSubject `json:"credentialSubject"`
		Proof             struct {
			Type               string `json:"type"`
			Cryptosuite        string `json:"cryptosuite"`
			Created            string `json:"created"`
			VerificationMethod string `json:"verificationMethod"`
			ProofPurpose       string `json:"proofPurpose"`
		} `json:"proof"`
	}
	u := unsigned{Context: credential.Context, ID: credential.ID, Type: credential.Type, Issuer: credential.Issuer, ValidFrom: credential.ValidFrom, CredentialSubject: credential.CredentialSubject}
	u.Proof.Type = credential.Proof.Type
	u.Proof.Cryptosuite = credential.Proof.Cryptosuite
	u.Proof.Created = credential.Proof.Created
	u.Proof.VerificationMethod = credential.Proof.VerificationMethod
	u.Proof.ProofPurpose = credential.Proof.ProofPurpose
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(u); err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("canonicalize credential: %w", err)
	}
	return sha256.Sum256(bytes.TrimSuffix(buffer.Bytes(), []byte("\n"))), nil
}

func credentialID(request IssueRequest) string {
	seed := strings.Join([]string{request.LearnerID, request.ProjectName, request.IssuedAt.UTC().Format(time.RFC3339Nano), request.AgentName}, "\x00")
	return "urn:company:credential:" + hashID(seed)
}

func hashID(value string) string {
	digest := sha256.Sum256([]byte(value))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}
