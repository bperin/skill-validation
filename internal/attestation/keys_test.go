package attestation

import (
	"context"
	"testing"
	"time"
)

func TestRootKeyDelegatesAndRevokesChildKey(t *testing.T) {
	t.Parallel()

	root, err := NewLocalSigner("company-root-2026")
	if err != nil {
		t.Fatalf("NewLocalSigner() error = %v", err)
	}
	authority := NewKeyAuthority(root)
	child, err := authority.Delegate(context.Background(), ScopeGitHubDeployment)
	if err != nil {
		t.Fatalf("Delegate() error = %v", err)
	}
	if !authority.CanIssue(child.KeyID(), ScopeGitHubDeployment) {
		t.Fatal("CanIssue() = false, want true for delegated GitHub deployment key")
	}
	if authority.CanIssue(child.KeyID(), ScopeWebPage) {
		t.Fatal("CanIssue() = true, want false for an unscoped web-page attestation")
	}
	if err := authority.Revoke(child.KeyID()); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	if authority.CanIssue(child.KeyID(), ScopeGitHubDeployment) {
		t.Fatal("CanIssue() = true after child revocation, want false")
	}
}

func TestRootRevocationDisablesEveryChild(t *testing.T) {
	t.Parallel()

	root, err := NewLocalSigner("company-root-2026")
	if err != nil {
		t.Fatalf("NewLocalSigner() error = %v", err)
	}
	authority := NewKeyAuthority(root)
	child, err := authority.Delegate(context.Background(), ScopeWebPage)
	if err != nil {
		t.Fatalf("Delegate() error = %v", err)
	}
	authority.RevokeRoot()
	if authority.CanIssue(child.KeyID(), ScopeWebPage) {
		t.Fatal("CanIssue() = true after root revocation, want false")
	}
}

func TestAuthorityIssueRequiresMatchingPersonSkillPolicyAndExpiry(t *testing.T) {
	t.Parallel()

	root, err := NewLocalSigner("company-root-2026")
	if err != nil {
		t.Fatalf("NewLocalSigner() error = %v", err)
	}
	authority := NewKeyAuthority(root)
	issuedAt := time.Date(2026, time.July, 21, 18, 0, 0, 0, time.UTC)
	expiresAt := time.Now().UTC().Add(time.Hour)
	child, err := authority.DelegateFor(context.Background(), DelegationSpec{
		Scope:         ScopeGitHubDeployment,
		SubjectID:     "learner-123",
		SkillID:       "postgres",
		PolicyVersion: "postgres-v2",
		ExpiresAt:     expiresAt,
	})
	if err != nil {
		t.Fatalf("DelegateFor() error = %v", err)
	}

	request := IssueRequest{
		LearnerID:       "learner-123",
		ProjectName:     "database-migration",
		SkillID:         "postgres",
		PolicyVersion:   "postgres-v2",
		EvidenceBinding: "repository:commit:build",
		EvaluationScore: 96,
		IssuedAt:        issuedAt,
		AgentName:       "evaluation-agent",
		Execution:       ExecutionPass,
		Issuer:          "https://issuer.example/primary",
	}
	credential, err := authority.Issue(context.Background(), child.KeyID(), ScopeGitHubDeployment, request)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if credential.CredentialSubject.SkillID != request.SkillID {
		t.Fatalf("credential skill ID = %q, want %q", credential.CredentialSubject.SkillID, request.SkillID)
	}

	request.SkillID = "redis"
	if _, err := authority.Issue(context.Background(), child.KeyID(), ScopeGitHubDeployment, request); err == nil {
		t.Fatal("Issue() error = nil for a skill outside the delegated scope")
	}
	if authority.CanIssueFor(child.KeyID(), ScopeGitHubDeployment, "learner-123", "postgres", "postgres-v2", expiresAt.Add(time.Hour)) {
		t.Fatal("CanIssueFor() = true after delegation expiry")
	}
}

func TestDelegateForRejectsExpiredDelegation(t *testing.T) {
	t.Parallel()

	root, err := NewLocalSigner("company-root-2026")
	if err != nil {
		t.Fatalf("NewLocalSigner() error = %v", err)
	}
	_, err = NewKeyAuthority(root).DelegateFor(context.Background(), DelegationSpec{
		Scope:         ScopeWebPage,
		SubjectID:     "learner-123",
		SkillID:       "postgres",
		PolicyVersion: "postgres-v2",
		ExpiresAt:     time.Now().UTC().Add(-time.Minute),
	})
	if err == nil {
		t.Fatal("DelegateFor() error = nil for an expired delegation")
	}
}
