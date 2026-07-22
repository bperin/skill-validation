package main

import (
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestValidateRequest(t *testing.T) {
	validRequest := issueRequest{
		LearnerID:     "learner-123",
		SkillID:       "rag-ai",
		Evidence:      "project=owner/repo;commit=abc",
		Progress:      100,
		Milestone:     "completed",
		HolderAddress: "0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC",
	}

	if err := validateRequest(validRequest); err != nil {
		t.Fatalf("validateRequest() error = %v, want nil", err)
	}

	// Missing learner ID
	invalidLearner := validRequest
	invalidLearner.LearnerID = ""
	if err := validateRequest(invalidLearner); err == nil || !strings.Contains(err.Error(), "learner ID") {
		t.Fatalf("validateRequest() error = %v, want learner ID error", err)
	}

	// Progress under bounds
	invalidProgressLow := validRequest
	invalidProgressLow.Progress = -1
	if err := validateRequest(invalidProgressLow); err == nil || !strings.Contains(err.Error(), "progress") {
		t.Fatalf("validateRequest() error = %v, want progress low error", err)
	}

	// Progress over bounds
	invalidProgressHigh := validRequest
	invalidProgressHigh.Progress = 101
	if err := validateRequest(invalidProgressHigh); err == nil || !strings.Contains(err.Error(), "progress") {
		t.Fatalf("validateRequest() error = %v, want progress high error", err)
	}
}

func TestValidateProjectProofRequest(t *testing.T) {
	validProofRequest := projectProofRequest{
		Registry:    "0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC",
		Attestation: "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
		Evidence:    "project=owner/repo;commit=abc",
	}

	if err := validateProjectProofRequest(validProofRequest); err != nil {
		t.Fatalf("validateProjectProofRequest() error = %v, want nil", err)
	}

	// Invalid registry address
	invalidRegistry := validProofRequest
	invalidRegistry.Registry = "invalid"
	if err := validateProjectProofRequest(invalidRegistry); err == nil {
		t.Fatal("validateProjectProofRequest() error = nil, want registry address error")
	}
}

func TestCommitmentIsDomainSeparated(t *testing.T) {
	if commitment("subject", "learner-123") == commitment("skill", "learner-123") {
		t.Fatal("different commitment domains must not collide")
	}
	if commitment("skill", "rag-ai") != commitment("skill", "rag-ai") {
		t.Fatal("commitment must be deterministic")
	}
}

// TestProjectProofSecurityBoundaries simulates all EIP-191 and on-chain signature edge cases
func TestProjectProofSecurityBoundaries(t *testing.T) {
	// 1. Generate local developer holder keys
	holderKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("failed to generate holder key: %v", err)
	}
	holderAddr := crypto.PubkeyToAddress(holderKey.PublicKey)

	// 2. Generate a separate attacker key
	attackerKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("failed to generate attacker key: %v", err)
	}

	attestationId := crypto.Keccak256Hash([]byte("test-attestation-id"))
	evidenceString := "repo=github.com/brian/my-awesome-rag;commit=abc999"

	// 3. Compute correct evidence commitment
	correctEvidenceHash := holderEvidenceCommitment(evidenceString)

	// 4. Calculate domain-bound EIP-191 digest (simulating the smart contract's holderProjectDigest)
	// keccak256(abi.encode(chainId, contractAddress, attestationId, evidenceHash))
	contractAddr := common.HexToAddress("0x0165878a594ca255338adfa4d48449f69242eb8f")
	chainId := big.NewInt(31337)
	
	digestHash := localProjectDigest(chainId, contractAddr, attestationId, correctEvidenceHash)

	// Sign the digest with the valid holder key
	correctSig, err := crypto.Sign(digestHash.Bytes(), holderKey)
	if err != nil {
		t.Fatalf("failed to sign digest: %v", err)
	}
	// EIP-191 requires v to be 27 or 28, go-ethereum's crypto.Sign returns 0 or 1
	correctSig[64] += 27

	// Edge Case 1: Valid Verification
	recoveredAddr, err := recoverSigner(digestHash, correctSig)
	if err != nil {
		t.Fatalf("failed to recover signer: %v", err)
	}
	if recoveredAddr != holderAddr {
		t.Fatalf("recovered signer = %s, want %s", recoveredAddr.Hex(), holderAddr.Hex())
	}

	// Edge Case 2: Tampered Evidence String
	tamperedEvidenceHash := holderEvidenceCommitment("repo=github.com/brian/my-awesome-rag;commit=tampered")
	tamperedDigest := localProjectDigest(chainId, contractAddr, attestationId, tamperedEvidenceHash)
	recoveredTamperedAddr, _ := recoverSigner(tamperedDigest, correctSig)
	if recoveredTamperedAddr == holderAddr {
		t.Fatal("tampered evidence must NOT recover the valid holder address")
	}

	// Edge Case 4: Attacker Signs (Mismatched Key)
	attackerSig, err := crypto.Sign(digestHash.Bytes(), attackerKey)
	if err != nil {
		t.Fatalf("failed to sign: %v", err)
	}
	attackerSig[64] += 27
	recoveredAttackerAddr, _ := recoverSigner(digestHash, attackerSig)
	if recoveredAttackerAddr == holderAddr {
		t.Fatal("attacker signature must NOT recover the valid holder address")
	}

	// Edge Case 5: Malformed/Corrupted Signature
	corruptedSig := make([]byte, len(correctSig))
	copy(corruptedSig, correctSig)
	corruptedSig[10] ^= 0xFF // Flip a byte
	recoveredCorruptedAddr, err := recoverSigner(digestHash, corruptedSig)
	// It should either fail or recover an incorrect address, but never the valid holder
	if err == nil && recoveredCorruptedAddr == holderAddr {
		t.Fatal("corrupted signature must NOT successfully verify as the valid holder")
	}

	// Edge Case 6: Mismatched/Forged Attestation ID
	forgedAttestationId := crypto.Keccak256Hash([]byte("forged-attestation-id"))
	forgedAttestationDigest := localProjectDigest(chainId, contractAddr, forgedAttestationId, correctEvidenceHash)
	recoveredForgedAttestationAddr, _ := recoverSigner(forgedAttestationDigest, correctSig)
	if recoveredForgedAttestationAddr == holderAddr {
		t.Fatal("forged attestation ID must NOT recover the valid holder address")
	}
}

func TestHTMLWebpageGenerationAndSignatureValidation(t *testing.T) {
	// 1. Generate local developer holder keys
	holderKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("failed to generate holder key: %v", err)
	}
	holderAddr := crypto.PubkeyToAddress(holderKey.PublicKey)

	// 2. Simulate index.html generation with "Brian Perin knows RAG."
	evidenceString := "Brian Perin knows RAG. Verified content inside index.html"
	evidenceHash := crypto.Keccak256Hash([]byte(evidenceString))

	attestationId := crypto.Keccak256Hash([]byte("brian-rag-attestation"))
	contractAddr := common.HexToAddress("0x0165878a594ca255338adfa4d48449f69242eb8f")
	chainId := big.NewInt(31337)

	// 3. Generate signature
	digestHash := localProjectDigest(chainId, contractAddr, attestationId, evidenceHash)
	signature, err := crypto.Sign(digestHash.Bytes(), holderKey)
	if err != nil {
		t.Fatalf("failed to sign digest: %v", err)
	}
	signature[64] += 27 // EIP-191 offset

	// 4. Verify the webpage signature
	recoveredAddr, err := recoverSigner(digestHash, signature)
	if err != nil {
		t.Fatalf("failed to recover signer: %v", err)
	}
	if recoveredAddr != holderAddr {
		t.Fatalf("recovered signer = %s, want %s", recoveredAddr.Hex(), holderAddr.Hex())
	}

	// 5. Simulate tampering (attacker tries to change name on the webpage)
	forgedEvidenceString := "Attacker knows RAG. Verified content inside index.html"
	forgedEvidenceHash := crypto.Keccak256Hash([]byte(forgedEvidenceString))
	forgedDigest := localProjectDigest(chainId, contractAddr, attestationId, forgedEvidenceHash)

	recoveredForgedAddr, _ := recoverSigner(forgedDigest, signature)
	if recoveredForgedAddr == holderAddr {
		t.Fatal("tampered webpage evidence must NOT recover the valid holder address")
	}
}

// Local helper to reconstruct the ABI-packed EIP-191 digest Hash used on-chain
func localProjectDigest(chainId *big.Int, contract common.Address, attestationId common.Hash, evidenceHash common.Hash) common.Hash {
	var payload []byte
	payload = append(payload, common.LeftPadBytes(chainId.Bytes(), 32)...)
	payload = append(payload, common.LeftPadBytes(contract.Bytes(), 32)...)
	payload = append(payload, attestationId.Bytes()...)
	payload = append(payload, evidenceHash.Bytes()...)
	
	hash := crypto.Keccak256Hash(payload)
	prefix := []byte("\x19Ethereum Signed Message:\n32")
	return crypto.Keccak256Hash(append(prefix, hash.Bytes()...))
}

// Local helper simulating the ecrecover EVM opcode behavior
func recoverSigner(digest common.Hash, signature []byte) (common.Address, error) {
	if len(signature) != 65 {
		return common.Address{}, fmt.Errorf("invalid signature length: %d", len(signature))
	}
	sigCopy := make([]byte, 65)
	copy(sigCopy, signature)
	sigCopy[64] -= 27

	pubKey, err := crypto.Ecrecover(digest.Bytes(), sigCopy)
	if err != nil {
		return common.Address{}, err
	}
	
	pub, err := crypto.UnmarshalPubkey(pubKey)
	if err != nil {
		return common.Address{}, err
	}
	return crypto.PubkeyToAddress(*pub), nil
}

func TestEvidenceCommitmentSupportsRawHash(t *testing.T) {
	rawHashStr := "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"
	expectedHash := common.HexToHash(rawHashStr)
	
	resultHash := evidenceCommitment(rawHashStr, 100, "completed")
	if resultHash != expectedHash {
		t.Fatalf("expected evidenceCommitment to return %s directly, got %s", expectedHash.Hex(), resultHash.Hex())
	}

	// Make sure invalid hex formats or non-hex string fall back to JSON hashing
	invalidHexStr := "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdeG" // invalid hex char G
	resultFallbackHash := evidenceCommitment(invalidHexStr, 100, "completed")
	if resultFallbackHash == expectedHash {
		t.Fatalf("invalid hex string should not match direct hash")
	}
}

func TestTelemetrySigningAndVerification(t *testing.T) {
	// 1. Generate keys
	holderKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("failed to generate holder key: %v", err)
	}
	holderAddr := crypto.PubkeyToAddress(holderKey.PublicKey)

	// 2. Setup mock parameters
	chainId := big.NewInt(1337)
	contractAddr := common.HexToAddress("0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC")
	attestationId := crypto.Keccak256Hash([]byte("mock-attestation"))
	metricName := "api_requests"
	metricValue := 125000.00
	timestamp := int64(1774886400) // Mock future timestamp

	// 3. Compute telemetry evidence binding hash
	evidenceHash := telemetryEvidenceBinding(metricName, metricValue, timestamp)
	expectedEvidenceHash := crypto.Keccak256Hash([]byte(fmt.Sprintf("telemetry:%s:%f:%d", metricName, metricValue, timestamp)))
	if evidenceHash != expectedEvidenceHash {
		t.Fatalf("mismatched telemetry evidence hash: got %s, want %s", evidenceHash.Hex(), expectedEvidenceHash.Hex())
	}

	// 4. Generate local EIP-191 domain-bound digest
	digestHash := localProjectDigest(chainId, contractAddr, attestationId, evidenceHash)
	
	// 5. Sign the digest as the developer/holder
	signature, err := crypto.Sign(digestHash.Bytes(), holderKey)
	if err != nil {
		t.Fatalf("failed to sign telemetry digest: %v", err)
	}
	signature[64] += 27 // Ethereum EIP-191 offset

	// 6. Verify the recovered signer matches the holder address
	recoveredAddr, err := recoverSigner(digestHash, signature)
	if err != nil {
		t.Fatalf("failed to recover signer: %v", err)
	}
	if recoveredAddr != holderAddr {
		t.Fatalf("recovered signer = %s, want %s", recoveredAddr.Hex(), holderAddr.Hex())
	}

	// 7. Test tampering detection
	tamperedValue := 125001.00 // Changed value slightly
	tamperedEvidenceHash := telemetryEvidenceBinding(metricName, tamperedValue, timestamp)
	tamperedDigest := localProjectDigest(chainId, contractAddr, attestationId, tamperedEvidenceHash)
	
	recoveredTamperedAddr, _ := recoverSigner(tamperedDigest, signature)
	if recoveredTamperedAddr == holderAddr {
		t.Fatal("tampered telemetry data must NOT recover the correct holder address")
	}
}
