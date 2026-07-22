package attestation

import (
	"context"
	"testing"
	"time"
)

func TestIssueAndVerifyCredential(t *testing.T) {
	t.Parallel()

	signer, err := NewLocalSigner("company-dev-key-1")
	if err != nil {
		t.Fatalf("NewLocalSigner() error = %v", err)
	}
	issuedAt := time.Date(2026, time.July, 21, 18, 0, 0, 0, time.UTC)
	credential, err := Issue(context.Background(), signer, IssueRequest{
		LearnerID:       "learner-123",
		ProjectName:     "portfolio-agent",
		EvaluationScore: 96,
		IssuedAt:        issuedAt,
		AgentName:       "Claude 3.5 Agent",
		Execution:       ExecutionPass,
		Issuer:          "https://company.example/issuers/primary",
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	if credential.Proof.Cryptosuite != "company-p256-sha256" {
		t.Fatalf("cryptosuite = %q, want company-p256-sha256", credential.Proof.Cryptosuite)
	}
	if credential.CredentialSubject.EvaluationScore != 96 {
		t.Fatalf("score = %d, want 96", credential.CredentialSubject.EvaluationScore)
	}
	if !Verify(credential, signer.PublicKey()) {
		t.Fatal("Verify() = false, want true")
	}
}

func TestVerifyRejectsTamperedCredential(t *testing.T) {
	t.Parallel()

	signer, err := NewLocalSigner("company-dev-key-1")
	if err != nil {
		t.Fatalf("NewLocalSigner() error = %v", err)
	}
	credential, err := Issue(context.Background(), signer, IssueRequest{
		LearnerID:       "learner-123",
		ProjectName:     "portfolio-agent",
		EvaluationScore: 96,
		IssuedAt:        time.Date(2026, time.July, 21, 18, 0, 0, 0, time.UTC),
		AgentName:       "Claude 3.5 Agent",
		Execution:       ExecutionPass,
		Issuer:          "https://company.example/issuers/primary",
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	credential.CredentialSubject.EvaluationScore = 100
	if Verify(credential, signer.PublicKey()) {
		t.Fatal("Verify() = true after score tampering, want false")
	}
}

func TestIssueRejectsInvalidScore(t *testing.T) {
	t.Parallel()

	signer, err := NewLocalSigner("company-dev-key-1")
	if err != nil {
		t.Fatalf("NewLocalSigner() error = %v", err)
	}
	_, err = Issue(context.Background(), signer, IssueRequest{
		LearnerID:       "learner-123",
		ProjectName:     "portfolio-agent",
		EvaluationScore: 101,
		IssuedAt:        time.Now().UTC(),
		AgentName:       "Claude 3.5 Agent",
		Execution:       ExecutionPass,
		Issuer:          "https://company.example/issuers/primary",
	})
	if err == nil {
		t.Fatal("Issue() error = nil, want invalid score error")
	}
}
