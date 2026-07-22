package chain

import (
	"testing"
)

func TestArbitrumDefaults(t *testing.T) {
	t.Parallel()

	defaults := DefaultNetwork()
	if defaults.ChainID != 42161 {
		t.Fatalf("ChainID = %d, want 42161", defaults.ChainID)
	}
	if defaults.Name != "arbitrum-one" {
		t.Fatalf("Name = %q, want arbitrum-one", defaults.Name)
	}
}

func TestReceiptDoesNotExposePrivateInputs(t *testing.T) {
	t.Parallel()

	receipt := Receipt{
		ChainID:              84532,
		TransactionHash:      "0xabc",
		SubjectCommitment:    "0xsubject",
		CredentialCommitment: "0xcredential",
		EvidenceBinding:      "0xevidence",
		ProgramID:            "0xprogram",
		SkillIDs:             []string{"0xskill"},
	}
	if err := receipt.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}
