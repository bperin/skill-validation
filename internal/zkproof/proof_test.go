package zkproof

import (
	"math/big"
	"testing"
)

func TestSystemProvesAndVerifiesThresholdClaim(t *testing.T) {
	t.Parallel()

	system, err := NewSystem()
	if err != nil {
		t.Fatalf("NewSystem() error = %v", err)
	}

	claim := Claim{
		Score:     big.NewInt(96),
		Salt:      big.NewInt(424242),
		Threshold: big.NewInt(80),
	}
	proof, public, err := system.Prove(claim)
	if err != nil {
		t.Fatalf("Prove() error = %v", err)
	}
	if public.Commitment.Sign() == 0 {
		t.Fatal("public commitment must be non-zero")
	}
	if err := system.Verify(proof, public); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
}

func TestSystemRejectsClaimBelowThreshold(t *testing.T) {
	t.Parallel()

	system, err := NewSystem()
	if err != nil {
		t.Fatalf("NewSystem() error = %v", err)
	}

	_, _, err = system.Prove(Claim{
		Score:     big.NewInt(79),
		Salt:      big.NewInt(424242),
		Threshold: big.NewInt(80),
	})
	if err == nil {
		t.Fatal("Prove() error = nil for a score below the threshold")
	}
}

func TestSystemRejectsAlteredPublicCommitment(t *testing.T) {
	t.Parallel()

	system, err := NewSystem()
	if err != nil {
		t.Fatalf("NewSystem() error = %v", err)
	}
	proof, public, err := system.Prove(Claim{
		Score:     big.NewInt(96),
		Salt:      big.NewInt(424242),
		Threshold: big.NewInt(80),
	})
	if err != nil {
		t.Fatalf("Prove() error = %v", err)
	}
	public.Commitment = new(big.Int).Add(public.Commitment, big.NewInt(1))
	if err := system.Verify(proof, public); err == nil {
		t.Fatal("Verify() error = nil for altered commitment")
	}
}
