// Package chain holds the domain types used to publish a proof-backed
// attestation. Constructing a receipt or calldata never sends a transaction.
package chain

import (
	"context"
	"fmt"
)

// DefaultNetwork returns a copy of the intended Arbitrum target without
// contacting it. Network configuration and the optional read-only dialing
// boundary are defined in publisher.go.
func DefaultNetwork() Network { return ArbitrumOne }

// Attestation is the publishable, privacy-preserving registry statement. Raw
// learner identifiers, scores, projects, and execution logs are intentionally
// absent from this type.
type Attestation struct {
	SubjectCommitment    string
	CredentialCommitment string
	EvidenceBinding      string
	ProgramID            string
	SkillIDs             []string
	Proof                []byte
	PublicSignals        []string
}

// Receipt is a confirmed-chain result shape. It stores no private witness data.
type Receipt struct {
	ChainID              int64
	TransactionHash      string
	SubjectCommitment    string
	CredentialCommitment string
	EvidenceBinding      string
	ProgramID            string
	SkillIDs             []string
}

// Validate rejects incomplete or non-privacy-preserving receipt data.
func (r Receipt) Validate() error {
	if r.ChainID <= 0 {
		return fmt.Errorf("chain ID is required")
	}
	if r.TransactionHash == "" || r.SubjectCommitment == "" || r.CredentialCommitment == "" || r.EvidenceBinding == "" || r.ProgramID == "" {
		return fmt.Errorf("transaction hash and commitments are required")
	}
	if len(r.SkillIDs) == 0 {
		return fmt.Errorf("at least one skill ID is required")
	}
	return nil
}

// Publisher is the transaction-publishing seam. No implementation in this
// package sends a transaction; publisher.go provides only an opt-in RPC dial.
type Publisher interface {
	Publish(ctx context.Context, attestation Attestation) (Receipt, error)
}

// SkillRegistryABI is the minimal ABI surface needed by an EVM adapter for the
// proof-backed registry contract.
const SkillRegistryABI = `[{"type":"function","name":"delegateIssuer","stateMutability":"nonpayable","inputs":[{"name":"child","type":"address"},{"name":"scopeMask","type":"uint8"}],"outputs":[]},{"type":"function","name":"revokeIssuer","stateMutability":"nonpayable","inputs":[{"name":"child","type":"address"}],"outputs":[]},{"type":"function","name":"attestWithProof","stateMutability":"nonpayable","inputs":[{"name":"subjectCommitment","type":"bytes32"},{"name":"credentialCommitment","type":"bytes32"},{"name":"evidenceBinding","type":"bytes32"},{"name":"programId","type":"bytes32"},{"name":"scope","type":"uint8"},{"name":"skillIds","type":"bytes32[]"},{"name":"proof","type":"uint256[8]"},{"name":"input","type":"uint256[2]"}],"outputs":[]},{"type":"function","name":"supersedeWithProof","stateMutability":"nonpayable","inputs":[{"name":"predecessor","type":"bytes32"},{"name":"credentialCommitment","type":"bytes32"},{"name":"evidenceBinding","type":"bytes32"},{"name":"programId","type":"bytes32"},{"name":"proof","type":"uint256[8]"},{"name":"input","type":"uint256[2]"}],"outputs":[]},{"type":"function","name":"deriveAttestationId","stateMutability":"pure","inputs":[{"name":"subjectCommitment","type":"bytes32"},{"name":"credentialCommitment","type":"bytes32"}],"outputs":[{"name":"","type":"bytes32"}]},{"type":"function","name":"hasSkill","stateMutability":"view","inputs":[{"name":"subjectCommitment","type":"bytes32"},{"name":"skillId","type":"bytes32"}],"outputs":[{"name":"","type":"bool"}]},{"type":"function","name":"isCurrent","stateMutability":"view","inputs":[{"name":"attestationId","type":"bytes32"}],"outputs":[{"name":"","type":"bool"}]},{"type":"function","name":"successorOf","stateMutability":"view","inputs":[{"name":"attestationId","type":"bytes32"}],"outputs":[{"name":"","type":"bytes32"}]},{"type":"function","name":"predecessorOf","stateMutability":"view","inputs":[{"name":"attestationId","type":"bytes32"}],"outputs":[{"name":"","type":"bytes32"}]},{"type":"function","name":"verifyAttestation","stateMutability":"view","inputs":[{"name":"attestationId","type":"bytes32"},{"name":"evidenceBinding","type":"bytes32"}],"outputs":[{"name":"","type":"bool"}]}]`
